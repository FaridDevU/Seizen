package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAppReadyTimeout = 45 * time.Second
	maxAppReadyTimeout     = 2 * time.Minute
)

type agentAppMountInput struct {
	AppID          string                `json:"appId,omitempty" jsonschema:"Existing App identifier; omit to create an App in the project scope."`
	Configuration  agentAppConfiguration `json:"configuration" jsonschema:"Validated App configuration."`
	ExpectedPorts  []int                 `json:"expectedPorts,omitempty" jsonschema:"Expected local listening ports."`
	TimeoutSeconds int                   `json:"timeoutSeconds,omitempty" jsonschema:"Readiness timeout, from 1 to 120 seconds."`
	SetPrimary     bool                  `json:"setPrimary,omitempty" jsonschema:"Select this App as the project's primary App."`
}

type agentAppWaitReadyInput struct {
	AppID          string `json:"appId" jsonschema:"App identifier in the authorized project."`
	ExpectedPorts  []int  `json:"expectedPorts,omitempty" jsonschema:"Expected local listening ports."`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty" jsonschema:"Readiness timeout, from 1 to 120 seconds."`
}

type AttachRunningAppInput struct {
	ProjectID         string `json:"projectId"`
	SpaceID           string `json:"spaceId"`
	AppID             string `json:"appId"`
	TerminalSessionID string `json:"terminalSessionId"`
	PreviewURL        string `json:"previewUrl"`
	DetectedPort      int    `json:"detectedPort"`
	DiscoverySource   string `json:"discoverySource"`
	Confirmed         bool   `json:"confirmed"`
	Name              string `json:"name"`
	Kind              string `json:"kind"`
	WorkingDirectory  string `json:"workingDirectory"`
}

type agentAppAttachInput struct {
	AppID             string `json:"appId" jsonschema:"Existing App identifier."`
	TerminalSessionID string `json:"terminalSessionId" jsonschema:"A managed terminal session in the authorized project and space."`
	PreviewURL        string `json:"previewUrl,omitempty" jsonschema:"Verified HTTP or HTTPS preview URL."`
	DetectedPort      int    `json:"detectedPort,omitempty" jsonschema:"Verified local listening port."`
	Confirmed         bool   `json:"confirmed" jsonschema:"True only after the user confirmed attaching this managed terminal."`
}

type AppReadyResult struct {
	Ready             bool       `json:"ready"`
	App               ProjectApp `json:"app"`
	RuntimeReference  string     `json:"runtimeReference"`
	PID               int        `json:"pid"`
	ProcessAlive      bool       `json:"processAlive"`
	HealthcheckPassed bool       `json:"healthcheckPassed"`
	DetectedPort      int        `json:"detectedPort,omitempty"`
	PreviewURL        string     `json:"previewUrl,omitempty"`
	Diagnostic        string     `json:"diagnostic"`
}

type AgentAppMountResult struct {
	App       ProjectApp     `json:"app"`
	Readiness AppReadyResult `json:"readiness"`
}

type AppRuntimeDiagnostics struct {
	Status              AppRuntimeStatus        `json:"status"`
	Run                 *AppRun                 `json:"run,omitempty"`
	Logs                string                  `json:"logs"`
	Automation          BrowserAutomationStatus `json:"automation"`
	ConsoleErrors       []string                `json:"consoleErrors"`
	ConsoleErrorMessage string                  `json:"consoleErrorMessage,omitempty"`
}

func validatedReadyTimeout(seconds int) (time.Duration, error) {
	if seconds == 0 {
		return defaultAppReadyTimeout, nil
	}
	if seconds < 1 || seconds > int(maxAppReadyTimeout/time.Second) {
		return 0, errors.New("the timeout must be between 1 and 120 seconds")
	}
	return time.Duration(seconds) * time.Second, nil
}

func validateExpectedPorts(ports []int) error {
	if len(ports) > 16 {
		return errors.New("too many expected ports were provided")
	}
	for _, port := range ports {
		if port < 1 || port > 65535 {
			return errors.New("ports must be between 1 and 65535")
		}
	}
	return nil
}

func (bridge *AgentBridge) mountApp(ctx context.Context, token string, scope AgentTokenScope, input agentAppMountInput) (result AgentAppMountResult, resultErr error) {
	if err := validateExpectedPorts(input.ExpectedPorts); err != nil {
		return result, err
	}
	if _, err := validatedReadyTimeout(input.TimeoutSeconds); err != nil {
		return result, err
	}
	configuration := input.Configuration
	if configuration.PreviewURL == "" && configuration.Kind == "web" && len(input.ExpectedPorts) > 0 {
		configuration.PreviewURL = fmt.Sprintf("http://127.0.0.1:%d", input.ExpectedPorts[0])
	}
	if configuration.HealthcheckURL == "" && configuration.Kind == "web" && configuration.PreviewURL != "" {
		configuration.HealthcheckURL = configuration.PreviewURL
	}
	bridge.app.emitAgentEvent("app.configuration.started", map[string]any{"projectId": scope.ProjectID, "appId": input.AppID})
	var app ProjectApp
	var err error
	if strings.TrimSpace(input.AppID) == "" {
		if scope.ExperimentID != "" {
			return result, errors.New("an experiment can only use its linked App")
		}
		if scope.AppID != "" {
			return result, errors.New("the token is limited to one App; provide its appId")
		}
		app, err = bridge.app.CreateApp(configuration.appInput(scope.ProjectID))
	} else {
		if err = bridge.ensureOwnedApp(ctx, scope, input.AppID); err == nil {
			app, err = bridge.app.ConfigureAppContext(input.AppID, scope.ExperimentID, configuration.appInput(scope.ProjectID))
		}
	}
	if err != nil {
		bridge.emitAppMountFailure(scope, input.AppID, "configuring", err, input.ExpectedPorts)
		return result, err
	}
	bridge.app.emitAgentEvent("app.configuration.completed", app)
	if input.SetPrimary && scope.ExperimentID == "" {
		if app, err = bridge.app.SetPrimaryApp(scope.ProjectID, app.ID); err != nil {
			return result, err
		}
	}
	bridge.app.emitAgentEvent("app.mount.started", app)
	if _, err = bridge.app.StartAppContext(app.ID, scope.ExperimentID); err != nil {
		bridge.emitAppMountFailure(scope, app.ID, "starting", err, input.ExpectedPorts)
		return result, err
	}
	defer func() {
		if resultErr != nil {
			_, _ = bridge.app.StopAppContext(app.ID, scope.ExperimentID)
		}
	}()
	if db, dbErr := bridge.app.database.Pool(ctx); dbErr == nil {
		_, _ = db.ExecContext(ctx, `UPDATE app_runs SET discovery_source = 'agent'
WHERE app_id = ? AND COALESCE(experiment_id, '') = ? AND stopped_at IS NULL`, app.ID, scope.ExperimentID)
	}
	readiness, err := bridge.waitAppReady(ctx, scope, agentAppWaitReadyInput{AppID: app.ID, ExpectedPorts: input.ExpectedPorts, TimeoutSeconds: input.TimeoutSeconds})
	if err != nil {
		_, _ = bridge.app.StopAppContext(app.ID, scope.ExperimentID)
		bridge.emitAppMountFailure(scope, app.ID, "waiting_ready", err, input.ExpectedPorts)
		return AgentAppMountResult{App: readiness.App, Readiness: readiness}, err
	}
	if readiness.PreviewURL != "" && readiness.PreviewURL != readiness.App.PreviewURL {
		readiness.App, err = bridge.app.SetPreviewURLContext(app.ID, scope.ExperimentID, readiness.PreviewURL)
		if err != nil {
			return AgentAppMountResult{App: app, Readiness: readiness}, err
		}
	}
	if db, dbErr := bridge.app.database.Pool(ctx); dbErr == nil {
		_, _ = db.ExecContext(ctx, `UPDATE app_runs SET discovery_source = 'agent', detected_port = ?,
last_verified_at = `+projectNow+`, preview_url = ? WHERE app_id = ? AND COALESCE(experiment_id, '') = ? AND stopped_at IS NULL`,
			nullablePort(readiness.DetectedPort), readiness.PreviewURL, app.ID, scope.ExperimentID)
	}
	bridge.app.emitAgentEvent("app.preview.ready", readiness)
	return AgentAppMountResult{App: readiness.App, Readiness: readiness}, nil
}

func nullablePort(port int) any {
	if port == 0 {
		return nil
	}
	return port
}

func (bridge *AgentBridge) emitAppMountFailure(scope AgentTokenScope, appID, step string, failure error, expectedPorts []int) {
	payload := map[string]any{"projectId": scope.ProjectID, "experimentId": scope.ExperimentID, "appId": appID, "step": step, "error": failure.Error()}
	if len(expectedPorts) > 0 {
		payload["expectedPort"] = expectedPorts[0]
	}
	if appID != "" {
		logs, _ := bridge.app.GetAppLogsContext(appID, scope.ExperimentID)
		payload["logs"] = tailText(logs, 8192)
		if db, err := bridge.app.database.Pool(context.Background()); err == nil {
			var exitCode sql.NullInt64
			if db.QueryRow(`SELECT exit_code FROM app_runs WHERE app_id = ? AND COALESCE(experiment_id, '') = ? ORDER BY started_at DESC LIMIT 1`, appID, scope.ExperimentID).Scan(&exitCode) == nil && exitCode.Valid {
				payload["exitCode"] = exitCode.Int64
			}
		}
	}
	bridge.app.emitAgentEvent("app.mount.failed", payload)
}

func (bridge *AgentBridge) waitAppReady(ctx context.Context, scope AgentTokenScope, input agentAppWaitReadyInput) (AppReadyResult, error) {
	if err := bridge.ensureOwnedApp(ctx, scope, input.AppID); err != nil {
		return AppReadyResult{}, err
	}
	if err := validateExpectedPorts(input.ExpectedPorts); err != nil {
		return AppReadyResult{}, err
	}
	timeout, err := validatedReadyTimeout(input.TimeoutSeconds)
	if err != nil {
		return AppReadyResult{}, err
	}
	waitContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(appHealthPoll)
	defer ticker.Stop()
	processEventSent := false
	last := AppReadyResult{}
	for {
		status, statusErr := bridge.app.GetAppStatusContext(input.AppID, scope.ExperimentID)
		if statusErr != nil {
			return last, statusErr
		}
		last = AppReadyResult{App: status.App, RuntimeReference: status.RuntimeReference, PID: status.PID,
			ProcessAlive: status.ProcessAlive, HealthcheckPassed: status.HealthcheckPassed, PreviewURL: status.App.PreviewURL}
		if status.App.Status == "failed" || status.App.Status == "stopped" {
			logs, _ := bridge.app.GetAppLogsContext(input.AppID, scope.ExperimentID)
			last.Diagnostic = tailText(logs, 8192)
			return last, fmt.Errorf("the App exited before it was ready: %s", last.Diagnostic)
		}
		if status.ProcessAlive && !processEventSent {
			bridge.app.emitAgentEvent("app.process.started", status)
			processEventSent = true
		}
		if status.ProcessAlive {
			if status.App.Kind == "desktop" {
				last.Ready = true
				last.Diagnostic = "desktop process verified"
				return last, nil
			}
			port, preview, ready := readyWebEndpoint(status.App, input.ExpectedPorts)
			endpointReady := ready || status.HealthcheckPassed
			if endpointReady && (port == 0 || !bridge.app.projectAppRuntimeManager().ownsPortContext(input.AppID, scope.ExperimentID, port)) {
				endpointReady = false
			}
			if endpointReady {
				last.Ready, last.DetectedPort = true, port
				if preview != "" {
					last.PreviewURL = preview
				}
				last.Diagnostic = "process and web endpoint verified"
				if port > 0 {
					bridge.app.emitAgentEvent("app.port.detected", map[string]any{"appId": input.AppID, "port": port, "previewUrl": last.PreviewURL})
				}
				return last, nil
			}
		}
		select {
		case <-waitContext.Done():
			logs, _ := bridge.app.GetAppLogsContext(input.AppID, scope.ExperimentID)
			last.Diagnostic = tailText(logs, 8192)
			return last, fmt.Errorf("the App was not ready within %s; process=%t, preview=%q, logs=%s", timeout, last.ProcessAlive, last.PreviewURL, last.Diagnostic)
		case <-ticker.C:
		}
	}
}

func readyWebEndpoint(app ProjectApp, expectedPorts []int) (int, string, bool) {
	urls := []string{app.HealthcheckURL, app.PreviewURL}
	for _, rawURL := range urls {
		if rawURL == "" || validateLocalAppURL(rawURL) != nil {
			continue
		}
		parsed, _ := url.Parse(rawURL)
		port := parsed.Port()
		if port == "" {
			if parsed.Scheme == "https" {
				port = "443"
			} else {
				port = "80"
			}
		}
		portNumber, _ := strconv.Atoi(port)
		if canConnectLocalPort(parsed.Hostname(), portNumber) {
			return portNumber, rawURL, true
		}
	}
	for _, port := range expectedPorts {
		if canConnectLocalPort("127.0.0.1", port) {
			return port, fmt.Sprintf("http://127.0.0.1:%d", port), true
		}
	}
	return 0, app.PreviewURL, false
}

func canConnectLocalPort(host string, port int) bool {
	if port < 1 || port > 65535 {
		return false
	}
	connection, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 250*time.Millisecond)
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}

func tailText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[len(value)-limit:]
}

func (a *App) AttachRunningApp(input AttachRunningAppInput) (AppRuntimeStatus, error) {
	spaceID, err := normalizeProjectSpaceID(input.SpaceID)
	if err != nil {
		return AppRuntimeStatus{}, err
	}
	if input.DiscoverySource == "" {
		input.DiscoverySource = "manual"
	}
	if input.DiscoverySource != "manual" && input.DiscoverySource != "detected" && input.DiscoverySource != "agent" {
		return AppRuntimeStatus{}, errors.New("the discovery source is not valid")
	}
	if input.DiscoverySource == "agent" && !input.Confirmed {
		return AppRuntimeStatus{}, errors.New("confirm the link before the agent attaches the terminal")
	}
	manager := a.currentTerminalManager()
	if manager == nil {
		return AppRuntimeStatus{}, errors.New("the managed terminal was not found")
	}
	session := manager.session(input.TerminalSessionID)
	if session == nil || session.projectID != input.ProjectID || session.spaceID != spaceID || session.serverID != "" || session.stopping.Load() {
		return AppRuntimeStatus{}, errors.New("the terminal does not belong to the given project and space")
	}
	db, err := a.database.Pool(a.context())
	if err != nil {
		return AppRuntimeStatus{}, err
	}
	var app ProjectApp
	if strings.TrimSpace(input.AppID) != "" {
		app, err = getProjectApp(a.context(), db, input.AppID)
		if err != nil || app.ProjectID != input.ProjectID {
			return AppRuntimeStatus{}, errors.New("the App does not belong to the project")
		}
	} else {
		input.Name, input.Kind = strings.TrimSpace(input.Name), strings.TrimSpace(input.Kind)
		if input.Name == "" || input.Kind != "web" {
			return AppRuntimeStatus{}, errors.New("provide a name and web type to create the linked App")
		}
		input.WorkingDirectory, err = attachedWorkingDirectory(a.context(), db, input.ProjectID, input.WorkingDirectory)
		if err != nil {
			return AppRuntimeStatus{}, err
		}
		app = ProjectApp{ProjectID: input.ProjectID, Name: input.Name, Kind: input.Kind, WorkingDirectory: input.WorkingDirectory, Status: "stopped"}
	}
	if app.Kind != "web" {
		return AppRuntimeStatus{}, errors.New("only verified web Apps can be attached from a terminal; start desktop Apps with AppRuntimeManager")
	}
	if app.ID != "" && app.Status != "stopped" && app.Status != "failed" {
		return AppRuntimeStatus{}, errors.New("the App must be stopped to attach a terminal")
	}
	previewURL := strings.TrimSpace(input.PreviewURL)
	if previewURL == "" && input.DetectedPort > 0 {
		previewURL = fmt.Sprintf("http://127.0.0.1:%d", input.DetectedPort)
	}
	if err = validateLocalAppURL(previewURL); err != nil || previewURL == "" {
		return AppRuntimeStatus{}, errors.New("provide a secure HTTP or HTTPS URL to verify the App")
	}
	parsed, _ := url.Parse(previewURL)
	port := input.DetectedPort
	if port == 0 {
		port, _ = strconv.Atoi(parsed.Port())
		if port == 0 && parsed.Scheme == "http" {
			port = 80
		} else if port == 0 {
			port = 443
		}
	}
	urlPort, _ := strconv.Atoi(parsed.Port())
	if urlPort == 0 && parsed.Scheme == "http" {
		urlPort = 80
	} else if urlPort == 0 {
		urlPort = 443
	}
	if input.DetectedPort > 0 && input.DetectedPort != urlPort {
		return AppRuntimeStatus{}, errors.New("the detected port does not match the preview URL")
	}
	if !canConnectLocalPort(parsed.Hostname(), port) {
		return AppRuntimeStatus{}, errors.New("the URL is not responding; start the App in the terminal before linking it")
	}
	if input.DiscoverySource != "manual" && !a.terminalOwnsPort(input.TerminalSessionID, port) {
		return AppRuntimeStatus{}, errors.New("the port does not belong to that terminal's managed process tree")
	}
	runID, err := newUUID()
	if err != nil {
		return AppRuntimeStatus{}, err
	}
	tx, err := db.BeginTx(a.context(), nil)
	if err != nil {
		return AppRuntimeStatus{}, err
	}
	defer tx.Rollback()
	if app.ID == "" {
		app.ID, err = newUUID()
		if err == nil {
			app, err = scanProjectApp(tx.QueryRow(`INSERT INTO apps (id, project_id, name, kind, working_directory,
preview_url, status, is_primary, created_at, updated_at) VALUES (?, ?, ?, 'web', ?, ?, 'running',
CASE WHEN EXISTS (SELECT 1 FROM apps WHERE project_id = ?) THEN 0 ELSE 1 END, `+projectNow+`, `+projectNow+`)
RETURNING `+appColumns, app.ID, app.ProjectID, app.Name, app.WorkingDirectory, previewURL, app.ProjectID))
		}
	} else {
		app, err = scanProjectApp(tx.QueryRow(`UPDATE apps SET status = 'running', preview_url = ?, updated_at = `+projectNow+`
WHERE id = ? AND project_id = ? AND status IN ('stopped', 'failed') RETURNING `+appColumns, previewURL, input.AppID, input.ProjectID))
	}
	if err != nil {
		return AppRuntimeStatus{}, errors.New("the App is no longer available to link")
	}
	_, err = tx.Exec(`INSERT INTO app_runs (id, project_id, app_id, target, runtime_provider, runtime_reference, status, preview_url,
started_at, terminal_session_id, ownership, discovery_source, detected_port, last_verified_at)
VALUES (?, ?, ?, 'development', 'terminal', ?, 'running', ?, `+projectNow+`, ?, 'attached', ?, ?, `+projectNow+`)`,
		runID, app.ProjectID, app.ID, input.TerminalSessionID, previewURL, input.TerminalSessionID, input.DiscoverySource, port)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return AppRuntimeStatus{}, fmt.Errorf("could not link the App to the terminal: %w", err)
	}
	status := AppRuntimeStatus{App: app, RuntimeReference: input.TerminalSessionID, ProcessAlive: true, HealthcheckPassed: true}
	a.emitAgentEvent("app.process.started", status)
	a.emitAgentEvent("app.port.detected", map[string]any{"appId": app.ID, "port": port, "terminalSessionId": input.TerminalSessionID})
	a.emitAgentEvent("app.preview.ready", status)
	a.emitAgentEvent("app.running", app)
	return status, nil
}

func attachedWorkingDirectory(ctx context.Context, db *sql.DB, projectID, requested string) (string, error) {
	var projectPath string
	if err := db.QueryRowContext(ctx, `SELECT path FROM projects WHERE id = ?`, projectID).Scan(&projectPath); err != nil {
		return "", errors.New("the project was not found")
	}
	if strings.TrimSpace(requested) == "" {
		requested = projectPath
	} else if !filepath.IsAbs(requested) {
		requested = filepath.Join(projectPath, requested)
	}
	projectPath, err := existingDirectory(projectPath)
	if err != nil {
		return "", err
	}
	requested, err = existingDirectory(requested)
	if err != nil {
		return "", errors.New("the working directory does not exist")
	}
	relative, err := filepath.Rel(projectPath, requested)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("the working directory must be inside the project")
	}
	return displayPath(requested), nil
}

func (bridge *AgentBridge) attachRunningApp(ctx context.Context, scope AgentTokenScope, input agentAppAttachInput) (AppRuntimeStatus, error) {
	if err := bridge.ensureOwnedApp(ctx, scope, input.AppID); err != nil {
		return AppRuntimeStatus{}, err
	}
	return bridge.app.AttachRunningApp(AttachRunningAppInput{
		ProjectID: scope.ProjectID, SpaceID: scope.SpaceID, AppID: input.AppID,
		TerminalSessionID: input.TerminalSessionID, PreviewURL: input.PreviewURL,
		DetectedPort: input.DetectedPort, DiscoverySource: "agent", Confirmed: input.Confirmed,
	})
}

func (bridge *AgentBridge) appRuntimeDiagnostics(ctx context.Context, scope AgentTokenScope, appID string) (AppRuntimeDiagnostics, error) {
	if err := bridge.ensureOwnedApp(ctx, scope, appID); err != nil {
		return AppRuntimeDiagnostics{}, err
	}
	status, err := bridge.app.GetAppStatus(appID)
	if err != nil {
		return AppRuntimeDiagnostics{}, err
	}
	logs, _ := bridge.app.GetAppLogs(appID)
	result := AppRuntimeDiagnostics{Status: status, Logs: tailText(logs, 16384)}
	db, err := bridge.app.database.Pool(ctx)
	if err != nil {
		return result, err
	}
	var run AppRun
	err = db.QueryRowContext(ctx, `SELECT id, project_id, app_id, experiment_id, target, runtime_provider, runtime_reference, status, preview_url,
started_at, stopped_at, exit_code, error_message, terminal_session_id, ownership, discovery_source, detected_port, last_verified_at
FROM app_runs WHERE app_id = ? ORDER BY started_at DESC LIMIT 1`, appID).Scan(
		&run.ID, &run.ProjectID, &run.AppID, &run.ExperimentID, &run.Target, &run.RuntimeProvider, &run.RuntimeReference, &run.Status, &run.PreviewURL,
		&run.StartedAt, &run.StoppedAt, &run.ExitCode, &run.ErrorMessage, &run.TerminalSessionID, &run.Ownership,
		&run.DiscoverySource, &run.DetectedPort, &run.LastVerifiedAt)
	if err == nil {
		result.Run = &run
	} else if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	if status.App.Kind == "web" && status.App.PreviewURL != "" {
		result.Automation = NewBrowserAutomationProvider(status.App.WorkingDirectory).Status()
		if result.Automation.BrowserFeatures {
			console := NewBrowserAutomationProvider(status.App.WorkingDirectory).GetConsoleErrors(ctx, status.App.PreviewURL)
			result.ConsoleErrors, result.ConsoleErrorMessage = console.ConsoleErrors, console.ErrorMessage
		}
	}
	return result, nil
}
