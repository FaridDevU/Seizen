package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

func (a *App) ConfigureAppContext(id, experimentID string, input AppInput) (ProjectApp, error) {
	experimentID = strings.TrimSpace(experimentID)
	if experimentID == "" {
		return a.ConfigureApp(id, input)
	}
	ctx := a.context()
	db, err := a.database.Pool(ctx)
	if err != nil {
		return ProjectApp{}, err
	}
	experiment, err := scanExperiment(db.QueryRowContext(ctx, `SELECT `+experimentColumns+` FROM experiments
WHERE id = ? AND app_id = ? AND status NOT IN ('discarded', 'archived', 'integrated')`, experimentID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectApp{}, errors.New("the App does not belong to the active experiment")
	}
	if err != nil {
		return ProjectApp{}, err
	}
	input.ProjectID = experiment.ProjectID
	mainRoot, err := projectPathForExperiment(ctx, db, experiment.ProjectID, "")
	if err != nil {
		return ProjectApp{}, err
	}
	experimentRoot, err := projectPathForExperiment(ctx, db, experiment.ProjectID, experimentID)
	if err != nil {
		return ProjectApp{}, err
	}
	if input.WorkingDirectory == "" {
		input.WorkingDirectory = mainRoot
	} else if working, resolveErr := existingDirectory(input.WorkingDirectory); resolveErr == nil {
		if relative, relErr := filepath.Rel(experimentRoot, working); relErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			input.WorkingDirectory = filepath.Join(mainRoot, relative)
		}
	}
	input, err = normalizeAppInput(ctx, db, input)
	if err != nil {
		return ProjectApp{}, err
	}
	var configuration experimentConfiguration
	if err = json.Unmarshal([]byte(experiment.ConfigurationJSON), &configuration); err != nil {
		return ProjectApp{}, err
	}
	configuration.App = &input
	encoded, _ := json.Marshal(configuration)
	_, err = db.ExecContext(ctx, `UPDATE experiments SET configuration_json = ?, updated_at = `+projectNow+` WHERE id = ?`, encoded, experimentID)
	if err != nil {
		return ProjectApp{}, err
	}
	return contextualProjectApp(ctx, db, id, experimentID)
}

type experimentConfiguration struct {
	App               *AppInput          `json:"app,omitempty"`
	Server            *ServerInput       `json:"server,omitempty"`
	Services          []ServerService    `json:"services,omitempty"`
	Connections       []ServerConnection `json:"connections,omitempty"`
	Reproducible      bool               `json:"reproducible"`
	ReproducibleFiles []string           `json:"reproducibleFiles,omitempty"`
}

func (a *App) defaultExperimentConfiguration(ctx context.Context, db *sql.DB, input ExperimentCreateInput) (string, error) {
	if input.ConfigurationJSON != "" {
		var candidate experimentConfiguration
		if err := json.Unmarshal([]byte(input.ConfigurationJSON), &candidate); err != nil {
			return "", fmt.Errorf("the experiment configuration is not valid: %w", err)
		}
		return input.ConfigurationJSON, nil
	}
	app, err := getProjectApp(ctx, db, input.AppID)
	if err != nil || app.ProjectID != input.ProjectID {
		return "", fmt.Errorf("could not copy the App configuration: %w", err)
	}
	configuration := experimentConfiguration{App: &AppInput{
		ProjectID: app.ProjectID, Name: app.Name, Kind: app.Kind,
		WorkingDirectory: app.WorkingDirectory, StartCommand: app.StartCommand,
		StopCommand: app.StopCommand, TestCommand: app.TestCommand,
		Executable: app.Executable, ArgumentsJSON: app.ArgumentsJSON,
		PreviewURL: app.PreviewURL, HealthcheckURL: app.HealthcheckURL,
	}}
	if input.Kind == "server" {
		base, loadErr := getServer(ctx, db, input.BaseServerID)
		if loadErr != nil || base.ProjectID != input.ProjectID || base.AppID != input.AppID || base.ExperimentID != nil {
			return "", fmt.Errorf("could not copy the base server configuration: %w", loadErr)
		}
		configuration.Server = &ServerInput{
			ProjectID: base.ProjectID, AppID: base.AppID, Name: base.Name,
			Provider: base.Provider, Distro: base.Distro, CPULimit: base.CPULimit,
			MemoryMB: base.MemoryMB, DiskGB: base.DiskGB, KeepAlive: false,
		}
		configuration.Services, err = a.ListServerServices(input.ProjectID, input.BaseServerID)
		if err == nil {
			configuration.Connections, err = a.ListServerConnections(input.ProjectID, input.BaseServerID)
		}
		if err != nil {
			return "", err
		}
	}
	encoded, err := json.Marshal(configuration)
	return string(encoded), err
}

func (a *App) cloneExperimentServer(experiment Experiment) error {
	if experiment.Kind != "server" || experiment.BaseServerID == nil {
		return nil
	}
	var configuration experimentConfiguration
	if err := json.Unmarshal([]byte(experiment.ConfigurationJSON), &configuration); err != nil || configuration.Server == nil {
		return fmt.Errorf("could not read the experimental server configuration: %w", err)
	}
	input := *configuration.Server
	input.ExperimentID, input.BaseServerID = experiment.ID, *experiment.BaseServerID
	input.Name = experiment.Name
	server, err := a.CreateServerDraft(input)
	if err != nil {
		return err
	}
	serviceIDs := make(map[string]string, len(configuration.Services))
	for _, service := range configuration.Services {
		created, createErr := a.RegisterServerService(ServerServiceInput{
			ProjectID: experiment.ProjectID, ServerID: server.ID, Name: service.Name,
			Kind: service.Kind, Host: service.Host, Port: service.Port, Protocol: service.Protocol,
			HealthcheckURL: service.HealthcheckURL, MetadataJSON: service.MetadataJSON,
			PositionJSON: service.PositionJSON,
		})
		if createErr != nil {
			return createErr
		}
		serviceIDs[service.ID] = created.ID
	}
	for _, connection := range configuration.Connections {
		source, target := "", ""
		if connection.SourceServiceID != nil {
			source = serviceIDs[*connection.SourceServiceID]
		}
		if connection.TargetServiceID != nil {
			target = serviceIDs[*connection.TargetServiceID]
		}
		if _, err = a.RegisterServerConnection(ServerConnectionInput{
			ProjectID: experiment.ProjectID, ServerID: server.ID,
			SourceServiceID: source, TargetServiceID: target,
			Protocol: connection.Protocol, Port: connection.Port, MetadataJSON: connection.MetadataJSON,
		}); err != nil {
			return err
		}
	}
	return nil
}
