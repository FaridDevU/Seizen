package core

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const maxReproducibleConfigFileSize = 1024 * 1024

var safeReproduciblePath = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

type ServerReproducibleExport struct {
	Experiment  Experiment         `json:"experiment"`
	Server      Server             `json:"server"`
	Files       []string           `json:"files"`
	Health      ServerHealth       `json:"health"`
	Services    []ServerService    `json:"services"`
	Connections []ServerConnection `json:"connections"`
	Rebuilt     bool               `json:"rebuilt"`
}

// ExportServerReproducibleConfig records only declarative files and accepts
// them after a brand-new server can be provisioned and checked from the copy.
// Runtime state, volumes, logs, sockets and secrets never enter the export.
func (a *App) ExportServerReproducibleConfig(experimentID string, files []string, confirmed bool) (result ServerReproducibleExport, resultErr error) {
	if !confirmed {
		return result, errors.New("confirm the server export and rebuild")
	}
	experiment, db, err := a.loadExperiment(experimentID)
	if err != nil {
		return result, err
	}
	if experiment.Kind != "server" || experiment.BaseServerID == nil {
		return result, errors.New("the reproducible export requires a server experiment")
	}
	if experiment.Status == "integrated" || experiment.Status == "discarded" || experiment.Status == "archived" {
		return result, errors.New("the experiment no longer accepts reproducible changes")
	}
	files, err = validateReproducibleConfigFiles(experiment.WorktreePath, files)
	if err != nil {
		return result, err
	}
	server, err := experimentServer(a.context(), db, experiment)
	if err != nil {
		return result, err
	}
	services, err := a.ListServerServices(experiment.ProjectID, server.ID)
	if err != nil {
		return result, err
	}
	connections, err := a.ListServerConnections(experiment.ProjectID, server.ID)
	if err != nil {
		return result, err
	}
	configuration, err := decodeExperimentConfiguration(experiment)
	if err != nil {
		return result, err
	}
	configuration.Server = &ServerInput{
		ProjectID: experiment.ProjectID, AppID: experiment.AppID, Name: server.Name,
		Provider: server.Provider, Distro: server.Distro, CPULimit: server.CPULimit,
		MemoryMB: server.MemoryMB, DiskGB: server.DiskGB, KeepAlive: false,
	}
	configuration.Services = services
	configuration.Connections = connections
	configuration.Reproducible = false
	configuration.ReproducibleFiles = files

	a.emitAgentEvent("experiment.server.rebuild.started", map[string]any{
		"experiment": experiment, "files": files,
	})
	defer func() {
		if resultErr != nil {
			a.emitAgentEvent("experiment.failed", map[string]any{
				"experiment": experiment, "operation": "server.rebuild", "error": resultErr.Error(),
			})
		}
	}()
	if _, err = a.CreateExperimentCheckpoint(experiment.ID); err != nil {
		return result, err
	}
	health, rebuiltServices, rebuiltConnections, err := a.rebuildServerConfiguration(experiment, configuration)
	if err != nil {
		return result, fmt.Errorf("could not rebuild the server from scratch: %w", err)
	}
	configuration.Reproducible = true
	encoded, err := json.Marshal(configuration)
	if err != nil {
		return result, err
	}
	experiment, err = scanExperiment(db.QueryRow(`UPDATE experiments SET configuration_json = ?, updated_at = `+projectNow+`
WHERE id = ? RETURNING `+experimentColumns, encoded, experiment.ID))
	if err != nil {
		return result, err
	}
	_, _ = db.Exec(`UPDATE experiment_reviews SET reproducible_verified = 1, updated_at = `+projectNow+` WHERE experiment_id = ?`, experiment.ID)
	result = ServerReproducibleExport{
		Experiment: experiment, Server: server, Files: files, Health: health,
		Services: rebuiltServices, Connections: rebuiltConnections, Rebuilt: true,
	}
	a.emitAgentEvent("experiment.server.rebuild.verified", result)
	return result, nil
}

func decodeExperimentConfiguration(experiment Experiment) (experimentConfiguration, error) {
	var configuration experimentConfiguration
	if err := json.Unmarshal([]byte(experiment.ConfigurationJSON), &configuration); err != nil {
		return configuration, errors.New("the experiment configuration is not valid")
	}
	return configuration, nil
}

func experimentServer(ctx context.Context, db *sql.DB, experiment Experiment) (Server, error) {
	server, err := scanServer(db.QueryRowContext(ctx, `SELECT `+serverColumns+` FROM servers
WHERE project_id = ? AND experiment_id = ? ORDER BY created_at, id LIMIT 1`, experiment.ProjectID, experiment.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, errors.New("the experiment does not have an isolated server")
	}
	return server, err
}

func validateReproducibleConfigFiles(worktree string, files []string) ([]string, error) {
	root, err := existingDirectory(worktree)
	if err != nil {
		return nil, errors.New("the experiment's worktree is not available")
	}
	if len(files) == 0 {
		return nil, errors.New("specify at least one reproducible configuration file")
	}
	seen := make(map[string]bool, len(files))
	result := make([]string, 0, len(files))
	for _, candidate := range files {
		candidate = filepath.Clean(strings.TrimSpace(candidate))
		if candidate == "." || filepath.IsAbs(candidate) || candidate == ".." || strings.HasPrefix(candidate, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("the reproducible path must be relative to the worktree: %s", candidate)
		}
		if !isDeclarativeServerFile(candidate) {
			return nil, fmt.Errorf("the file is not an allowed declarative configuration: %s", candidate)
		}
		if !safeReproduciblePath.MatchString(filepath.ToSlash(candidate)) {
			return nil, fmt.Errorf("the reproducible path contains unsafe characters: %s", candidate)
		}
		path, err := filepath.EvalSymlinks(filepath.Join(root, candidate))
		if err != nil {
			return nil, fmt.Errorf("the reproducible file %s was not found: %w", candidate, err)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("the reproducible file is outside the worktree: %s", candidate)
		}
		metadata, err := os.Stat(path)
		if err != nil || !metadata.Mode().IsRegular() {
			return nil, fmt.Errorf("the reproducible configuration must be a regular file: %s", candidate)
		}
		if metadata.Size() > maxReproducibleConfigFileSize {
			return nil, fmt.Errorf("the reproducible file exceeds 1 MiB: %s", candidate)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		for _, pattern := range experimentSecretPatterns {
			if pattern.Match(contents) {
				return nil, fmt.Errorf("the reproducible file contains a possible secret: %s", candidate)
			}
		}
		normalized := filepath.ToSlash(relative)
		if !seen[normalized] {
			seen[normalized] = true
			result = append(result, normalized)
		}
	}
	sort.Strings(result)
	return result, nil
}

func isDeclarativeServerFile(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	base := filepath.Base(lower)
	if base == ".env" || strings.HasPrefix(base, ".env.") || strings.HasSuffix(base, ".key") || strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".pfx") {
		return false
	}
	if base == "dockerfile" || strings.HasPrefix(base, "dockerfile.") || base == "compose.yaml" || base == "compose.yml" || base == "docker-compose.yaml" || base == "docker-compose.yml" {
		return true
	}
	switch strings.ToLower(filepath.Ext(base)) {
	case ".yaml", ".yml", ".json", ".toml", ".conf", ".ini", ".sh", ".ps1", ".md":
		return true
	default:
		return false
	}
}

func (a *App) rebuildServerConfiguration(experiment Experiment, configuration experimentConfiguration) (health ServerHealth, services []ServerService, connections []ServerConnection, resultErr error) {
	if configuration.Server == nil || experiment.BaseServerID == nil {
		return health, nil, nil, errors.New("the server's declarative configuration is missing")
	}
	input := *configuration.Server
	input.ProjectID, input.AppID = experiment.ProjectID, experiment.AppID
	input.ExperimentID, input.BaseServerID = experiment.ID, *experiment.BaseServerID
	input.Name = "Reproducible verification · " + experiment.Name
	input.KeepAlive = false
	verification, err := a.CreateServerDraft(input)
	if err != nil {
		return health, nil, nil, err
	}
	defer func() {
		db, _ := a.database.Pool(a.context())
		if db != nil {
			if current, loadErr := getServer(a.context(), db, verification.ID); loadErr == nil {
				if current.Status == "running" || current.Status == "degraded" || current.Status == "starting" {
					_, _ = a.StopServer(current.ID)
				}
				if destroyErr := a.DestroyServer(current.ID); destroyErr != nil {
					resultErr = errors.Join(resultErr, destroyErr)
				}
			}
		}
	}()
	if err = a.copyServerTopology(experiment.ProjectID, configuration.Services, configuration.Connections, verification.ID); err != nil {
		return health, nil, nil, err
	}
	if _, err = a.StartServer(verification.ID); err != nil {
		return health, nil, nil, err
	}
	if err = a.applyServerReproducibleFiles(verification, experiment.WorktreePath, configuration.ReproducibleFiles); err != nil {
		return health, nil, nil, err
	}
	health, err = a.CheckServerHealth(verification.ID)
	if err != nil {
		return health, nil, nil, err
	}
	if !health.Healthy {
		return health, nil, nil, errors.New("the rebuilt server did not pass the healthcheck")
	}
	services, err = a.ListServerServices(experiment.ProjectID, verification.ID)
	if err != nil {
		return health, nil, nil, err
	}
	connections, err = a.ListServerConnections(experiment.ProjectID, verification.ID)
	if err != nil {
		return health, nil, nil, err
	}
	if len(services) != len(configuration.Services) || len(connections) != len(configuration.Connections) {
		return health, nil, nil, errors.New("the rebuilt topology does not match the configuration")
	}
	for _, service := range services {
		if service.HealthcheckURL == "" && (service.Port == nil || service.Protocol == "udp") {
			continue
		}
		if _, err = a.VerifyServerService(experiment.ProjectID, verification.ID, service.ID); err != nil {
			return health, nil, nil, fmt.Errorf("the rebuilt service %s did not pass its healthcheck: %w", service.Name, err)
		}
	}
	return health, services, connections, nil
}

func (a *App) applyServerReproducibleFiles(server Server, worktree string, files []string) error {
	if server.Provider != "wsl" {
		return nil
	}
	script := ""
	for _, file := range files {
		if strings.EqualFold(filepath.Base(file), "seizen-rebuild.sh") {
			script = filepath.ToSlash(file)
			break
		}
	}
	if script == "" {
		return errors.New("the WSL rebuild requires including a declarative seizen-rebuild.sh")
	}
	manager := a.projectServerManager()
	if result, err := manager.Exec(a.context(), server.ID, "rm -rf /root/seizen-rebuild && mkdir -p /root/seizen-rebuild"); err != nil {
		return fmt.Errorf("could not prepare the rebuild directory: %w", err)
	} else if result.ExitCode != 0 {
		return fmt.Errorf("could not prepare the rebuild directory: code %d", result.ExitCode)
	}
	for _, file := range files {
		contents, err := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(file)))
		if err != nil {
			return err
		}
		target := "/root/seizen-rebuild/" + filepath.ToSlash(file)
		directory := target[:strings.LastIndex(target, "/")]
		encoded := base64.StdEncoding.EncodeToString(contents)
		command := fmt.Sprintf("mkdir -p '%s' && printf '%%s' '%s' | base64 -d > '%s'", directory, encoded, target)
		result, execErr := manager.Exec(a.context(), server.ID, command)
		if execErr != nil {
			return fmt.Errorf("could not copy %s to the rebuilt server: %w", file, execErr)
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("could not copy %s to the rebuilt server: code %d", file, result.ExitCode)
		}
	}
	command := "cd /root/seizen-rebuild && /bin/sh '" + script + "'"
	result, err := manager.Exec(a.context(), server.ID, command)
	if err != nil {
		return fmt.Errorf("seizen-rebuild.sh failed on the clean server: %s: %w", tailText(result.Output, 4096), err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("seizen-rebuild.sh failed on the clean server with code %d: %s", result.ExitCode, tailText(result.Output, 4096))
	}
	return nil
}

func (a *App) copyServerTopology(projectID string, sourceServices []ServerService, sourceConnections []ServerConnection, targetServerID string) error {
	serviceIDs := make(map[string]string, len(sourceServices))
	for _, service := range sourceServices {
		created, err := a.RegisterServerService(ServerServiceInput{
			ProjectID: projectID, ServerID: targetServerID, Name: service.Name,
			Kind: service.Kind, Host: service.Host, Port: service.Port, Protocol: service.Protocol,
			HealthcheckURL: service.HealthcheckURL, MetadataJSON: service.MetadataJSON,
			PositionJSON: service.PositionJSON,
		})
		if err != nil {
			return err
		}
		serviceIDs[service.ID] = created.ID
	}
	for _, connection := range sourceConnections {
		source, target := "", ""
		if connection.SourceServiceID != nil {
			source = serviceIDs[*connection.SourceServiceID]
		}
		if connection.TargetServiceID != nil {
			target = serviceIDs[*connection.TargetServiceID]
		}
		if _, err := a.RegisterServerConnection(ServerConnectionInput{
			ProjectID: projectID, ServerID: targetServerID, SourceServiceID: source,
			TargetServiceID: target, Protocol: connection.Protocol, Port: connection.Port,
			MetadataJSON: connection.MetadataJSON,
		}); err != nil {
			return err
		}
	}
	return nil
}
