package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type ProjectApp struct {
	ID               string `json:"id"`
	ProjectID        string `json:"projectId"`
	ExperimentID     string `json:"experimentId,omitempty"`
	Name             string `json:"name"`
	Kind             string `json:"kind"`
	WorkingDirectory string `json:"workingDirectory"`
	StartCommand     string `json:"startCommand"`
	StopCommand      string `json:"stopCommand"`
	TestCommand      string `json:"testCommand"`
	Executable       string `json:"executable"`
	ArgumentsJSON    string `json:"argumentsJson"`
	PreviewURL       string `json:"previewUrl"`
	HealthcheckURL   string `json:"healthcheckUrl"`
	Status           string `json:"status"`
	IsPrimary        bool   `json:"isPrimary"`
	CreatedAt        string `json:"createdAt"`
	UpdatedAt        string `json:"updatedAt"`
}

type AppInput struct {
	ProjectID        string `json:"projectId"`
	Name             string `json:"name"`
	Kind             string `json:"kind"`
	WorkingDirectory string `json:"workingDirectory"`
	StartCommand     string `json:"startCommand"`
	StopCommand      string `json:"stopCommand"`
	TestCommand      string `json:"testCommand"`
	Executable       string `json:"executable"`
	ArgumentsJSON    string `json:"argumentsJson"`
	PreviewURL       string `json:"previewUrl"`
	HealthcheckURL   string `json:"healthcheckUrl"`
}

type AppRun struct {
	ID                string  `json:"id"`
	ProjectID         string  `json:"projectId"`
	AppID             string  `json:"appId"`
	ExperimentID      *string `json:"experimentId"`
	Target            string  `json:"target"`
	RuntimeProvider   string  `json:"runtimeProvider"`
	RuntimeReference  string  `json:"runtimeReference"`
	Status            string  `json:"status"`
	PreviewURL        string  `json:"previewUrl"`
	StartedAt         string  `json:"startedAt"`
	StoppedAt         *string `json:"stoppedAt"`
	ExitCode          *int    `json:"exitCode"`
	ErrorMessage      string  `json:"errorMessage"`
	TerminalSessionID string  `json:"terminalSessionId"`
	Ownership         string  `json:"ownership"`
	DiscoverySource   string  `json:"discoverySource"`
	DetectedPort      *int    `json:"detectedPort"`
	LastVerifiedAt    *string `json:"lastVerifiedAt"`
}

type Server struct {
	ID               string  `json:"id"`
	ProjectID        string  `json:"projectId"`
	AppID            string  `json:"appId"`
	ExperimentID     *string `json:"experimentId"`
	BaseServerID     *string `json:"baseServerId"`
	Name             string  `json:"name"`
	Provider         string  `json:"provider"`
	Distro           string  `json:"distro"`
	RuntimeReference string  `json:"runtimeReference"`
	Status           string  `json:"status"`
	CPULimit         float64 `json:"cpuLimit"`
	MemoryMB         int     `json:"memoryMb"`
	DiskGB           int     `json:"diskGb"`
	KeepAlive        bool    `json:"keepAlive"`
	CreatedAt        string  `json:"createdAt"`
	UpdatedAt        string  `json:"updatedAt"`
}

type ServerInput struct {
	ProjectID    string  `json:"projectId"`
	AppID        string  `json:"appId"`
	ExperimentID string  `json:"experimentId"`
	BaseServerID string  `json:"baseServerId"`
	Name         string  `json:"name"`
	Provider     string  `json:"provider"`
	Distro       string  `json:"distro"`
	CPULimit     float64 `json:"cpuLimit"`
	MemoryMB     int     `json:"memoryMb"`
	DiskGB       int     `json:"diskGb"`
	KeepAlive    bool    `json:"keepAlive"`
}

type ServerService struct {
	ID             string `json:"id"`
	ServerID       string `json:"serverId"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	Host           string `json:"host"`
	Port           *int   `json:"port"`
	Protocol       string `json:"protocol"`
	HealthcheckURL string `json:"healthcheckUrl"`
	Status         string `json:"status"`
	Source         string `json:"source"`
	MetadataJSON   string `json:"metadataJson"`
	PositionJSON   string `json:"positionJson"`
}

type ServerConnection struct {
	ID              string  `json:"id"`
	ServerID        string  `json:"serverId"`
	SourceServiceID *string `json:"sourceServiceId"`
	TargetServiceID *string `json:"targetServiceId"`
	Protocol        string  `json:"protocol"`
	Port            *int    `json:"port"`
	Status          string  `json:"status"`
	Source          string  `json:"source"`
	TrafficRate     float64 `json:"trafficRate"`
	ErrorRate       float64 `json:"errorRate"`
	MetadataJSON    string  `json:"metadataJson"`
}

const appColumns = `id, project_id, name, kind, working_directory, start_command,
stop_command, test_command, executable, arguments_json, preview_url, healthcheck_url,
status, is_primary, created_at, updated_at`

const serverColumns = `id, project_id, app_id, experiment_id, base_server_id, name, provider,
distro, runtime_reference, status, cpu_limit, memory_mb, disk_gb, keep_alive, created_at, updated_at`

func (a *App) ListApps(projectID string) ([]ProjectApp, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, errors.New("project is required")
	}
	ctx := a.context()
	db, err := a.database.Pool(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT `+appColumns+` FROM apps WHERE project_id = ? ORDER BY is_primary DESC, updated_at DESC, LOWER(name)`, projectID)
	if err != nil {
		return nil, fmt.Errorf("could not load the Apps: %w", err)
	}
	defer rows.Close()
	apps := make([]ProjectApp, 0)
	for rows.Next() {
		item, scanErr := scanProjectApp(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("could not read an App: %w", scanErr)
		}
		apps = append(apps, item)
	}
	return apps, rows.Err()
}

func (a *App) CreateApp(input AppInput) (ProjectApp, error) {
	ctx := a.context()
	db, err := a.database.Pool(ctx)
	if err != nil {
		return ProjectApp{}, err
	}
	input, err = normalizeAppInput(ctx, db, input)
	if err != nil {
		return ProjectApp{}, err
	}
	id, err := newUUID()
	if err != nil {
		return ProjectApp{}, fmt.Errorf("could not create the App identifier: %w", err)
	}
	item, err := scanProjectApp(db.QueryRowContext(ctx, `INSERT INTO apps (
id, project_id, name, kind, working_directory, start_command, stop_command,
test_command, executable, arguments_json, preview_url, healthcheck_url, status, is_primary,
created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'stopped',
CASE WHEN EXISTS (SELECT 1 FROM apps WHERE project_id = ?) THEN 0 ELSE 1 END, `+projectNow+`, `+projectNow+`)
RETURNING `+appColumns, id, input.ProjectID, input.Name, input.Kind, input.WorkingDirectory,
		input.StartCommand, input.StopCommand, input.TestCommand, input.Executable,
		input.ArgumentsJSON, input.PreviewURL, input.HealthcheckURL, input.ProjectID))
	if err != nil {
		return ProjectApp{}, fmt.Errorf("could not save the App: %w", err)
	}
	a.emitAgentEvent("app.created", item)
	return item, nil
}

func (a *App) UpdateApp(id string, input AppInput) (ProjectApp, error) {
	if strings.TrimSpace(id) == "" {
		return ProjectApp{}, errors.New("the App is required")
	}
	ctx := a.context()
	db, err := a.database.Pool(ctx)
	if err != nil {
		return ProjectApp{}, err
	}
	input, err = normalizeAppInput(ctx, db, input)
	if err != nil {
		return ProjectApp{}, err
	}
	item, err := scanProjectApp(db.QueryRowContext(ctx, `UPDATE apps SET
name = ?, kind = ?, working_directory = ?, start_command = ?, stop_command = ?,
test_command = ?, executable = ?, arguments_json = ?, preview_url = ?,
healthcheck_url = ?, status = CASE WHEN status = 'unconfigured' THEN 'stopped' ELSE status END,
updated_at = `+projectNow+`
WHERE id = ? AND project_id = ? AND status IN ('unconfigured', 'stopped', 'failed')
RETURNING `+appColumns, input.Name, input.Kind, input.WorkingDirectory, input.StartCommand,
		input.StopCommand, input.TestCommand, input.Executable, input.ArgumentsJSON,
		input.PreviewURL, input.HealthcheckURL, id, input.ProjectID))
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectApp{}, errors.New("the App was not found or is running")
	}
	if err != nil {
		return ProjectApp{}, fmt.Errorf("could not update the App: %w", err)
	}
	a.emitAgentEvent("app.updated", item)
	return item, nil
}

func (a *App) DeleteApp(id string) error {
	ctx := a.context()
	db, err := a.database.Pool(ctx)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var projectID string
	var wasPrimary bool
	if err = tx.QueryRowContext(ctx, `SELECT project_id, is_primary FROM apps WHERE id = ?`, id).Scan(&projectID, &wasPrimary); err != nil {
		return errors.New("the App was not found")
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM apps
WHERE id = ? AND status IN ('unconfigured', 'stopped', 'failed')
AND NOT EXISTS (SELECT 1 FROM servers WHERE servers.app_id = apps.id)`, id)
	if err != nil {
		return fmt.Errorf("could not delete the App: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("the App was not found, is running, or still has linked servers")
	}
	if wasPrimary {
		if _, err = tx.ExecContext(ctx, `UPDATE apps SET is_primary = 1 WHERE id = (
SELECT id FROM apps WHERE project_id = ? ORDER BY created_at, id LIMIT 1)`, projectID); err != nil {
			return fmt.Errorf("could not promote the next primary App: %w", err)
		}
	}
	return tx.Commit()
}

func (a *App) SetPrimaryApp(projectID, appID string) (ProjectApp, error) {
	projectID, appID = strings.TrimSpace(projectID), strings.TrimSpace(appID)
	if projectID == "" || appID == "" {
		return ProjectApp{}, errors.New("project and App are required")
	}
	db, err := a.database.Pool(a.context())
	if err != nil {
		return ProjectApp{}, err
	}
	tx, err := db.BeginTx(a.context(), nil)
	if err != nil {
		return ProjectApp{}, err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRow(`SELECT 1 FROM apps WHERE id = ? AND project_id = ?`, appID, projectID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProjectApp{}, errors.New("the App does not belong to the project")
		}
		return ProjectApp{}, err
	}
	if _, err = tx.Exec(`UPDATE apps SET is_primary = 0 WHERE project_id = ?`, projectID); err == nil {
		_, err = tx.Exec(`UPDATE apps SET is_primary = 1, updated_at = `+projectNow+` WHERE id = ? AND project_id = ?`, appID, projectID)
	}
	if err != nil {
		return ProjectApp{}, fmt.Errorf("could not select the primary App: %w", err)
	}
	item, err := scanProjectApp(tx.QueryRow(`SELECT `+appColumns+` FROM apps WHERE id = ?`, appID))
	if err == nil {
		err = tx.Commit()
	}
	if err == nil {
		a.emitAgentEvent("app.primary.updated", item)
	}
	return item, err
}

func (a *App) ListServers(projectID string) ([]Server, error) {
	return a.listServers(projectID, "", false)
}

func (a *App) ListServersContext(projectID, experimentID string) ([]Server, error) {
	return a.listServers(projectID, strings.TrimSpace(experimentID), true)
}

func (a *App) listServers(projectID, experimentID string, filterContext bool) ([]Server, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, errors.New("project is required")
	}
	ctx := a.context()
	db, err := a.database.Pool(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT `+serverColumns+` FROM servers
WHERE project_id = ? AND (? = 0 OR COALESCE(experiment_id, '') = ?)
ORDER BY updated_at DESC, LOWER(name)`, projectID, filterContext, experimentID)
	if err != nil {
		return nil, fmt.Errorf("could not load the servers: %w", err)
	}
	defer rows.Close()
	servers := make([]Server, 0)
	for rows.Next() {
		server, scanErr := scanServer(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("could not read a server: %w", scanErr)
		}
		servers = append(servers, server)
	}
	return servers, rows.Err()
}

func (a *App) CreateServerDraft(input ServerInput) (Server, error) {
	if err := validateServerInput(input); err != nil {
		return Server{}, err
	}
	ctx := a.context()
	db, err := a.database.Pool(ctx)
	if err != nil {
		return Server{}, err
	}
	var linked int
	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM apps WHERE id = ? AND project_id = ?`, input.AppID, input.ProjectID).Scan(&linked)
	if err != nil {
		return Server{}, fmt.Errorf("could not check the App: %w", err)
	}
	if linked != 1 {
		return Server{}, errors.New("the server must be linked to an App in the project")
	}
	input.ExperimentID, input.BaseServerID = strings.TrimSpace(input.ExperimentID), strings.TrimSpace(input.BaseServerID)
	if input.ExperimentID == "" && input.BaseServerID != "" {
		return Server{}, errors.New("a primary server cannot have a base server")
	}
	if input.ExperimentID != "" {
		if input.BaseServerID == "" {
			return Server{}, errors.New("the experimental server requires a base server")
		}
		err = db.QueryRowContext(ctx, `SELECT 1 FROM experiments
JOIN servers AS base ON base.id = ? AND base.project_id = experiments.project_id
    AND base.app_id = experiments.app_id AND base.experiment_id IS NULL
WHERE experiments.id = ? AND experiments.project_id = ? AND experiments.app_id = ?`,
			input.BaseServerID, input.ExperimentID, input.ProjectID, input.AppID).Scan(&linked)
		if errors.Is(err, sql.ErrNoRows) {
			return Server{}, errors.New("the experiment and the base server must belong to the same App")
		}
		if err != nil {
			return Server{}, err
		}
	}
	id, err := newUUID()
	if err != nil {
		return Server{}, fmt.Errorf("could not create the server identifier: %w", err)
	}
	if strings.TrimSpace(input.Distro) == "" {
		input.Distro = "Debian 12"
	}
	server, err := scanServer(db.QueryRowContext(ctx, `INSERT INTO servers (
id, project_id, app_id, experiment_id, base_server_id, name, provider, distro, runtime_reference, status,
cpu_limit, memory_mb, disk_gb, keep_alive, created_at, updated_at)
VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, '', 'draft', ?, ?, ?, FALSE, `+projectNow+`, `+projectNow+`)
RETURNING `+serverColumns, id, input.ProjectID, input.AppID, input.ExperimentID, input.BaseServerID,
		strings.TrimSpace(input.Name), input.Provider, input.Distro, input.CPULimit, input.MemoryMB, input.DiskGB))
	if err != nil {
		return Server{}, fmt.Errorf("could not create the server draft: %w", err)
	}
	return server, nil
}

func (a *App) StartMockServer(id string) (Server, error) {
	ctx := a.context()
	db, err := a.database.Pool(ctx)
	if err != nil {
		return Server{}, err
	}
	server, err := getServer(ctx, db, id)
	if err != nil {
		return Server{}, err
	}
	if server.Provider != "mock" {
		return Server{}, errors.New("this server does not use the mock provider")
	}
	if server.Status == "running" {
		return server, nil
	}
	if server.Status != "draft" && server.Status != "stopped" && server.Status != "failed" {
		return Server{}, fmt.Errorf("cannot start a server in state %s", server.Status)
	}

	intermediate := "starting"
	if server.RuntimeReference == "" {
		intermediate = "provisioning"
	}
	if server, err = updateServerState(ctx, db, server.ID, intermediate, server.RuntimeReference); err != nil {
		return Server{}, err
	}
	if server.RuntimeReference == "" {
		server.RuntimeReference, err = defaultMockServerProvider.Create(ctx, server)
		if err == nil {
			err = defaultMockServerProvider.Start(ctx, server)
		}
	} else {
		err = defaultMockServerProvider.Start(ctx, server)
	}
	if err != nil {
		_, _ = updateServerState(ctx, db, server.ID, "failed", server.RuntimeReference)
		return Server{}, fmt.Errorf("could not start the mock server: %w", err)
	}
	return updateServerState(ctx, db, server.ID, "running", server.RuntimeReference)
}

func (a *App) StopMockServer(id string) (Server, error) {
	ctx := a.context()
	db, err := a.database.Pool(ctx)
	if err != nil {
		return Server{}, err
	}
	server, err := getServer(ctx, db, id)
	if err != nil {
		return Server{}, err
	}
	if server.Provider != "mock" {
		return Server{}, errors.New("this server does not use the mock provider")
	}
	if server.Status == "stopped" || server.Status == "draft" {
		return server, nil
	}
	if server.Status != "running" && server.Status != "degraded" {
		return Server{}, fmt.Errorf("cannot stop a server in state %s", server.Status)
	}
	if server, err = updateServerState(ctx, db, server.ID, "stopping", server.RuntimeReference); err != nil {
		return Server{}, err
	}
	if err = defaultMockServerProvider.Stop(ctx, server); err != nil {
		_, _ = updateServerState(ctx, db, server.ID, "failed", server.RuntimeReference)
		return Server{}, fmt.Errorf("could not stop the mock server: %w", err)
	}
	return updateServerState(ctx, db, server.ID, "stopped", server.RuntimeReference)
}

func (a *App) DeleteServer(id string) error {
	return a.DestroyServer(id)
}

func normalizeAppInput(ctx context.Context, db *sql.DB, input AppInput) (AppInput, error) {
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	input.Name = strings.TrimSpace(input.Name)
	input.Kind = strings.TrimSpace(input.Kind)
	input.StartCommand = strings.TrimSpace(input.StartCommand)
	input.StopCommand = strings.TrimSpace(input.StopCommand)
	input.TestCommand = strings.TrimSpace(input.TestCommand)
	input.Executable = strings.TrimSpace(input.Executable)
	input.PreviewURL = strings.TrimSpace(input.PreviewURL)
	input.HealthcheckURL = strings.TrimSpace(input.HealthcheckURL)
	if input.ProjectID == "" || input.Name == "" {
		return input, errors.New("project and App name are required")
	}
	if input.Kind != "web" && input.Kind != "desktop" {
		return input, errors.New("App kind must be web or desktop")
	}
	if input.Kind == "web" && input.StartCommand == "" {
		return input, errors.New("a web App needs a start command")
	}
	if input.Kind == "desktop" && input.Executable == "" && input.StartCommand == "" {
		return input, errors.New("a desktop App needs an executable or start command")
	}
	if input.ArgumentsJSON == "" {
		input.ArgumentsJSON = "[]"
	}
	var arguments []string
	if err := json.Unmarshal([]byte(input.ArgumentsJSON), &arguments); err != nil {
		return input, errors.New("arguments must be a JSON array of strings")
	}
	for _, candidate := range []struct {
		name  string
		value string
	}{{"preview", input.PreviewURL}, {"healthcheck", input.HealthcheckURL}} {
		if err := validateLocalAppURL(candidate.value); err != nil {
			return input, fmt.Errorf("%s URL is not valid: %w", candidate.name, err)
		}
	}

	var projectPath string
	if err := db.QueryRowContext(ctx, `SELECT path FROM projects WHERE id = ?`, input.ProjectID).Scan(&projectPath); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return input, errors.New("the project was not found")
		}
		return input, fmt.Errorf("could not check the project: %w", err)
	}
	workingDirectory := strings.TrimSpace(input.WorkingDirectory)
	if workingDirectory == "" {
		workingDirectory = projectPath
	} else if !filepath.IsAbs(workingDirectory) {
		workingDirectory = filepath.Join(projectPath, workingDirectory)
	}
	resolvedProject, err := existingDirectory(projectPath)
	if err != nil {
		return input, err
	}
	resolvedWorking, err := existingDirectory(workingDirectory)
	if err != nil {
		return input, fmt.Errorf("the working directory is not valid: %w", err)
	}
	relative, err := filepath.Rel(resolvedProject, resolvedWorking)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return input, errors.New("the working directory must be inside the project")
	}
	if input.Executable != "" {
		executable := input.Executable
		if strings.ContainsRune(executable, 0) {
			return input, errors.New("the executable is not valid")
		}
		if !filepath.IsAbs(executable) {
			executable = filepath.Join(resolvedWorking, executable)
		}
		executable, err = filepath.Abs(executable)
		if err != nil {
			return input, errors.New("the executable is not valid")
		}
		if metadata, statErr := os.Stat(executable); statErr == nil {
			if metadata.IsDir() {
				return input, errors.New("the executable must point to a file")
			}
			executable, err = canonicalPath(executable)
			if err != nil {
				return input, errors.New("the executable is not accessible")
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return input, errors.New("the executable is not accessible")
		}
		relative, err = filepath.Rel(resolvedProject, executable)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return input, errors.New("the executable must be inside the project")
		}
	}
	input.WorkingDirectory = displayPath(resolvedWorking)
	return input, nil
}

func validateLocalAppURL(raw string) error {
	if raw == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return errors.New("only full HTTP or HTTPS URLs are allowed")
	}
	if parsed.User != nil {
		return errors.New("credentials are not allowed in the URL")
	}
	hostname := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if hostname == "wails.localhost" || strings.HasSuffix(hostname, ".wails.localhost") {
		return errors.New("wails.localhost is reserved")
	}
	return nil
}

func validateServerInput(input ServerInput) error {
	if strings.TrimSpace(input.ProjectID) == "" || strings.TrimSpace(input.AppID) == "" {
		return errors.New("project and App are required")
	}
	if strings.TrimSpace(input.Name) == "" {
		return errors.New("the server name is required")
	}
	if input.Provider != "mock" && input.Provider != "wsl" && input.Provider != "incus" {
		return errors.New("the server provider is not valid")
	}
	if input.Provider == "wsl" && !strings.EqualFold(strings.TrimSpace(input.Distro), "Debian 12") {
		return errors.New("the WSL provider only supports Debian 12")
	}
	if input.CPULimit <= 0 || input.MemoryMB <= 0 || input.DiskGB <= 0 {
		return errors.New("CPU, RAM, and disk must be greater than zero")
	}
	if input.KeepAlive {
		return errors.New("keep_alive remains disabled until Seizen has a visible tray process")
	}
	return nil
}

func getServer(ctx context.Context, db *sql.DB, id string) (Server, error) {
	server, err := scanServer(db.QueryRowContext(ctx, `SELECT `+serverColumns+` FROM servers WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, errors.New("the server was not found")
	}
	return server, err
}

func updateServerState(ctx context.Context, db *sql.DB, id, status, runtimeReference string) (Server, error) {
	server, err := scanServer(db.QueryRowContext(ctx, `UPDATE servers SET status = ?, runtime_reference = ?, updated_at = `+projectNow+`
WHERE id = ? RETURNING `+serverColumns, status, runtimeReference, id))
	if err != nil {
		return Server{}, fmt.Errorf("could not update the server: %w", err)
	}
	return server, nil
}

func scanProjectApp(row rowScanner) (ProjectApp, error) {
	var item ProjectApp
	err := row.Scan(&item.ID, &item.ProjectID, &item.Name, &item.Kind, &item.WorkingDirectory,
		&item.StartCommand, &item.StopCommand, &item.TestCommand, &item.Executable,
		&item.ArgumentsJSON, &item.PreviewURL, &item.HealthcheckURL, &item.Status,
		&item.IsPrimary, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func scanServer(row rowScanner) (Server, error) {
	var item Server
	err := row.Scan(&item.ID, &item.ProjectID, &item.AppID, &item.ExperimentID, &item.BaseServerID,
		&item.Name, &item.Provider, &item.Distro, &item.RuntimeReference, &item.Status, &item.CPULimit, &item.MemoryMB,
		&item.DiskGB, &item.KeepAlive, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}
