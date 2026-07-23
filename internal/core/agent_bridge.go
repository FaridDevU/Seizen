package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	agentBridgeRequestLimit = 256 * 1024
	agentApprovalLifetime   = 5 * time.Minute
)

type AgentAuditEvent struct {
	ID            string `json:"id"`
	SessionID     string `json:"sessionId"`
	ProjectID     string `json:"projectId,omitempty"`
	ExperimentID  string `json:"experimentId,omitempty"`
	AppID         string `json:"appId,omitempty"`
	ToolName      string `json:"toolName"`
	ArgumentsJSON string `json:"argumentsJson"`
	Success       bool   `json:"success"`
	ErrorMessage  string `json:"errorMessage,omitempty"`
	ApprovalID    string `json:"approvalId,omitempty"`
	CreatedAt     string `json:"createdAt"`
}

type AgentApproval struct {
	ID           string  `json:"id"`
	SessionID    string  `json:"sessionId"`
	ProjectID    string  `json:"projectId"`
	ExperimentID string  `json:"experimentId,omitempty"`
	AppID        string  `json:"appId,omitempty"`
	Action       string  `json:"action"`
	ResourceID   string  `json:"resourceId"`
	RequestJSON  string  `json:"requestJson"`
	Status       string  `json:"status"`
	ExpiresAt    string  `json:"expiresAt"`
	DecidedAt    *string `json:"decidedAt"`
	ConsumedAt   *string `json:"consumedAt"`
	CreatedAt    string  `json:"createdAt"`
}

type agentRPCRequest struct {
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

type agentRPCResponse struct {
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

type AgentBridge struct {
	app       *App
	tokens    *agentTokenStore
	mu        sync.Mutex
	listener  net.Listener
	server    *http.Server
	url       string
	closeOnce sync.Once
}

func newAgentBridge(app *App, tokens *agentTokenStore) *AgentBridge {
	return &AgentBridge{app: app, tokens: tokens}
}

func (bridge *AgentBridge) Start() (string, error) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if bridge.listener != nil {
		return bridge.url, nil
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("could not start the local agent bridge: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /agent/tool", bridge.handleTool)
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	bridge.listener = listener
	bridge.server = server
	bridge.url = "http://" + listener.Addr().String()
	go func() { _ = server.Serve(listener) }()
	return bridge.url, nil
}

func (bridge *AgentBridge) Close() {
	bridge.closeOnce.Do(func() {
		bridge.tokens.RevokeAll()
		bridge.mu.Lock()
		server := bridge.server
		bridge.mu.Unlock()
		if server != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = server.Shutdown(ctx)
			cancel()
		}
	})
}

func (bridge *AgentBridge) handleTool(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	request.Body = http.MaxBytesReader(response, request.Body, agentBridgeRequestLimit)
	defer request.Body.Close()
	var input agentRPCRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeAgentRPCResponse(response, nil, errors.New("invalid agent request"))
		return
	}
	if input.Arguments == nil {
		input.Arguments = json.RawMessage(`{}`)
	}
	token := strings.TrimSpace(strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "))
	result, err := bridge.callTool(request.Context(), token, input.Tool, input.Arguments)
	writeAgentRPCResponse(response, result, err)
}

func writeAgentRPCResponse(response http.ResponseWriter, result any, err error) {
	payload := agentRPCResponse{Result: result}
	if err != nil {
		payload.Error = err.Error()
	}
	_ = json.NewEncoder(response).Encode(payload)
}

func (bridge *AgentBridge) callTool(ctx context.Context, token, tool string, arguments json.RawMessage) (result any, callErr error) {
	scope, err := bridge.tokens.Authorize(token, tool)
	if err != nil {
		bridge.app.recordAgentAudit(AgentTokenScope{SessionID: "unauthenticated"}, tool, arguments, err)
		return nil, err
	}
	defer func() { bridge.app.recordAgentAudit(scope, tool, arguments, callErr) }()

	switch tool {
	case "seizen_project_context":
		return bridge.projectContext(ctx, scope)
	case "seizen_app_list":
		return bridge.listApps(scope)
	case "seizen_app_discover":
		return bridge.app.DiscoverApps(scope.ProjectID)
	case "seizen_app_create":
		var input agentAppCreateInput
		if callErr = decodeAgentArguments(arguments, &input); callErr != nil {
			return nil, callErr
		}
		if scope.AppID != "" {
			return nil, errors.New("the token is limited to an existing App")
		}
		created, err := bridge.createAppDraft(ctx, scope.ProjectID, input)
		if err != nil {
			return nil, err
		}
		return created, nil
	case "seizen_app_configure":
		var input agentAppConfigureInput
		if callErr = decodeAgentArguments(arguments, &input); callErr != nil {
			return nil, callErr
		}
		if callErr = bridge.ensureOwnedApp(ctx, scope, input.AppID); callErr != nil {
			return nil, callErr
		}
		return bridge.app.ConfigureAppContext(input.AppID, scope.ExperimentID, input.appInput(scope.ProjectID))
	case "seizen_app_run", "seizen_app_stop", "seizen_app_restart", "seizen_app_status",
		"seizen_app_run_tests", "seizen_app_get_logs", "seizen_app_capture_preview",
		"seizen_app_get_console_errors", "seizen_app_smoke_test":
		var input agentAppIDInput
		if callErr = decodeAgentArguments(arguments, &input); callErr != nil {
			return nil, callErr
		}
		if callErr = bridge.ensureOwnedApp(ctx, scope, input.AppID); callErr != nil {
			return nil, callErr
		}
		switch tool {
		case "seizen_app_run":
			return bridge.app.StartAppContext(input.AppID, scope.ExperimentID)
		case "seizen_app_stop":
			return bridge.app.StopAppContext(input.AppID, scope.ExperimentID)
		case "seizen_app_restart":
			return bridge.app.RestartAppContext(input.AppID, scope.ExperimentID)
		case "seizen_app_status":
			return bridge.app.GetAppStatusContext(input.AppID, scope.ExperimentID)
		case "seizen_app_run_tests":
			return bridge.app.RunAppTestsContext(input.AppID, scope.ExperimentID)
		case "seizen_app_get_logs":
			logs, err := bridge.app.GetAppLogsContext(input.AppID, scope.ExperimentID)
			return map[string]string{"logs": logs}, err
		case "seizen_app_capture_preview":
			return bridge.app.CaptureAppPreview(input.AppID)
		case "seizen_app_smoke_test":
			return bridge.app.SmokeTestApp(input.AppID)
		default:
			return bridge.app.GetAppConsoleErrors(input.AppID)
		}
	case "seizen_app_test_route":
		var input agentAppTestRouteInput
		if callErr = decodeAgentArguments(arguments, &input); callErr != nil {
			return nil, callErr
		}
		if callErr = bridge.ensureOwnedApp(ctx, scope, input.AppID); callErr != nil {
			return nil, callErr
		}
		return bridge.app.TestAppRoute(input.AppID, input.Route)
	case "seizen_app_set_preview":
		var input agentSetPreviewInput
		if callErr = decodeAgentArguments(arguments, &input); callErr != nil {
			return nil, callErr
		}
		if callErr = bridge.ensureOwnedApp(ctx, scope, input.AppID); callErr != nil {
			return nil, callErr
		}
		return bridge.app.SetPreviewURLContext(input.AppID, scope.ExperimentID, input.PreviewURL)
	case "seizen_app_mount":
		var input agentAppMountInput
		if callErr = decodeAgentArguments(arguments, &input); callErr != nil {
			return nil, callErr
		}
		return bridge.mountApp(ctx, token, scope, input)
	case "seizen_app_wait_ready":
		var input agentAppWaitReadyInput
		if callErr = decodeAgentArguments(arguments, &input); callErr != nil {
			return nil, callErr
		}
		return bridge.waitAppReady(ctx, scope, input)
	case "seizen_app_attach_running":
		var input agentAppAttachInput
		if callErr = decodeAgentArguments(arguments, &input); callErr != nil {
			return nil, callErr
		}
		return bridge.attachRunningApp(ctx, scope, input)
	case "seizen_app_get_runtime_diagnostics":
		var input agentAppIDInput
		if callErr = decodeAgentArguments(arguments, &input); callErr != nil {
			return nil, callErr
		}
		return bridge.appRuntimeDiagnostics(ctx, scope, input.AppID)
	default:
		if strings.HasPrefix(tool, "seizen_experiment_") {
			return bridge.callExperimentTool(ctx, scope, tool, arguments)
		}
		if strings.HasPrefix(tool, "seizen_server_") {
			return bridge.callServerTool(ctx, token, scope, tool, arguments)
		}
		if strings.HasPrefix(tool, "seizen_desk_") || strings.HasPrefix(tool, "seizen_files_") {
			return bridge.callDeskTool(ctx, scope, tool, arguments)
		}
		return nil, errors.New("unrecognized agent tool")
	}
}

func decodeAgentArguments(arguments json.RawMessage, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(arguments)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("invalid tool arguments")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("invalid tool arguments")
	}
	return nil
}

func (bridge *AgentBridge) projectContext(ctx context.Context, scope AgentTokenScope) (map[string]any, error) {
	db, err := bridge.app.database.Pool(ctx)
	if err != nil {
		return nil, err
	}
	project, err := scanProject(db.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM projects WHERE id = ?`, scope.ProjectID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("the agent's project was not found")
	}
	if err != nil {
		return nil, err
	}
	path, err := projectPathForExperiment(ctx, db, scope.ProjectID, scope.ExperimentID)
	if err != nil {
		return nil, err
	}
	project.Path = path
	var experiment any
	if scope.ExperimentID != "" {
		item, loadErr := scanExperiment(db.QueryRowContext(ctx, `SELECT `+experimentColumns+` FROM experiments WHERE id = ? AND project_id = ?`, scope.ExperimentID, scope.ProjectID))
		if loadErr != nil {
			return nil, errors.New("the agent's experiment is no longer available")
		}
		project.Branch = &item.BranchName
		experiment = item
	}
	return map[string]any{
		"project": project, "experiment": experiment, "experimentId": scope.ExperimentID,
		"contextPath": path, "spaceId": scope.SpaceID, "selectedAppId": scope.AppID,
	}, nil
}

func (bridge *AgentBridge) listApps(scope AgentTokenScope) ([]ProjectApp, error) {
	apps, err := bridge.app.ListApps(scope.ProjectID)
	if err != nil || scope.AppID == "" {
		return apps, err
	}
	for _, app := range apps {
		if app.ID == scope.AppID {
			if scope.ExperimentID != "" {
				db, poolErr := bridge.app.database.Pool(context.Background())
				if poolErr != nil {
					return nil, poolErr
				}
				app, poolErr = contextualProjectApp(context.Background(), db, app.ID, scope.ExperimentID)
				if poolErr != nil {
					return nil, poolErr
				}
			}
			return []ProjectApp{app}, nil
		}
	}
	return nil, errors.New("the selected App was not found")
}

func (bridge *AgentBridge) ensureOwnedApp(ctx context.Context, scope AgentTokenScope, appID string) error {
	if strings.TrimSpace(appID) == "" {
		return errors.New("the App is required")
	}
	if scope.AppID != "" && scope.AppID != appID {
		return errors.New("the token does not allow access to that App")
	}
	db, err := bridge.app.database.Pool(ctx)
	if err != nil {
		return err
	}
	var exists int
	if scope.ExperimentID == "" {
		err = db.QueryRowContext(ctx, `SELECT 1 FROM apps WHERE id = ? AND project_id = ?`, appID, scope.ProjectID).Scan(&exists)
	} else {
		err = db.QueryRowContext(ctx, `SELECT 1 FROM experiments WHERE id = ? AND project_id = ? AND app_id = ?
AND status NOT IN ('discarded', 'archived')`, scope.ExperimentID, scope.ProjectID, appID).Scan(&exists)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("the App does not belong to the authorized project")
	}
	return err
}

func (bridge *AgentBridge) createAppDraft(ctx context.Context, projectID string, input agentAppCreateInput) (ProjectApp, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Kind = strings.TrimSpace(input.Kind)
	if input.Name == "" {
		return ProjectApp{}, errors.New("the App name is required")
	}
	if input.Kind != "web" && input.Kind != "desktop" {
		return ProjectApp{}, errors.New("the App type must be web or desktop")
	}
	db, err := bridge.app.database.Pool(ctx)
	if err != nil {
		return ProjectApp{}, err
	}
	var projectPath string
	if err = db.QueryRowContext(ctx, `SELECT path FROM projects WHERE id = ?`, projectID).Scan(&projectPath); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProjectApp{}, errors.New("the project was not found")
		}
		return ProjectApp{}, err
	}
	projectPath, err = existingDirectory(projectPath)
	if err != nil {
		return ProjectApp{}, err
	}
	id, err := newUUID()
	if err != nil {
		return ProjectApp{}, err
	}
	item, err := scanProjectApp(db.QueryRowContext(ctx, `INSERT INTO apps (
	id, project_id, name, kind, working_directory, status, is_primary, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, 'unconfigured',
CASE WHEN EXISTS (SELECT 1 FROM apps WHERE project_id = ?) THEN 0 ELSE 1 END,
`+projectNow+`, `+projectNow+`) RETURNING `+appColumns,
		id, projectID, input.Name, input.Kind, displayPath(projectPath), projectID))
	if err != nil {
		return ProjectApp{}, fmt.Errorf("could not create the App draft: %w", err)
	}
	bridge.app.emitAgentEvent("app.created", item)
	return item, nil
}

func (a *App) recordAgentAudit(scope AgentTokenScope, tool string, arguments json.RawMessage, callErr error) {
	if strings.TrimSpace(tool) == "" {
		tool = "unknown"
	}
	id, err := newUUID()
	if err != nil {
		return
	}
	if len(arguments) == 0 || !json.Valid(arguments) {
		arguments = json.RawMessage(`{}`)
	}
	approvalID := agentApprovalID(arguments)
	arguments = sanitizeAgentArguments(arguments)
	errorMessage := ""
	if callErr != nil {
		errorMessage = callErr.Error()
		if len(errorMessage) > 2048 {
			errorMessage = errorMessage[:2048]
		}
	}
	db, err := a.database.Pool(context.Background())
	if err != nil {
		return
	}
	projectID, experimentID, appID := nullableAgentID(scope.ProjectID), nullableAgentID(scope.ExperimentID), nullableAgentID(scope.AppID)
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = db.ExecContext(context.Background(), `INSERT INTO agent_audit_events (
id, session_id, project_id, experiment_id, app_id, tool_name, arguments_json, success, error_message, approval_id, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, scope.SessionID, projectID, experimentID, appID, tool,
		string(arguments), callErr == nil, errorMessage, nullableAgentID(approvalID), createdAt)
	if err == nil {
		a.emitAgentEvent("agent.audit", AgentAuditEvent{
			ID: id, SessionID: scope.SessionID, ProjectID: scope.ProjectID, ExperimentID: scope.ExperimentID, AppID: scope.AppID,
			ToolName: tool, ArgumentsJSON: string(arguments), Success: callErr == nil,
			ErrorMessage: errorMessage, ApprovalID: approvalID, CreatedAt: createdAt,
		})
	}
}

func agentApprovalID(arguments json.RawMessage) string {
	var input struct {
		ApprovalID string `json:"approvalId"`
	}
	if json.Unmarshal(arguments, &input) != nil {
		return ""
	}
	return strings.TrimSpace(input.ApprovalID)
}

func sanitizeAgentArguments(arguments json.RawMessage) json.RawMessage {
	decoder := json.NewDecoder(strings.NewReader(string(arguments)))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return json.RawMessage(`{}`)
	}
	redactAgentSecrets(value)
	sanitized, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return sanitized
}

func redactAgentSecrets(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "secret") || strings.Contains(lower, "token") ||
				strings.Contains(lower, "password") || strings.Contains(lower, "api_key") ||
				strings.Contains(lower, "apikey") || strings.Contains(lower, "private_key") {
				typed[key] = "[REDACTED]"
				continue
			}
			redactAgentSecrets(child)
		}
	case []any:
		for _, child := range typed {
			redactAgentSecrets(child)
		}
	}
}

func nullableAgentID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (a *App) emitAgentEvent(name string, payload any) {
	a.mu.RLock()
	emit, ctx := a.emitEvent, a.ctx
	a.mu.RUnlock()
	if emit != nil && ctx != nil {
		emit(ctx, name, payload)
	}
}

func (a *App) requestAgentApproval(scope AgentTokenScope, action, resourceID string, request any) (AgentApproval, error) {
	if strings.TrimSpace(action) == "" {
		return AgentApproval{}, errors.New("the sensitive action is required")
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return AgentApproval{}, errors.New("the approval request is not valid")
	}
	id, err := newUUID()
	if err != nil {
		return AgentApproval{}, err
	}
	now := time.Now().UTC()
	approval := AgentApproval{
		ID: id, SessionID: scope.SessionID, ProjectID: scope.ProjectID, ExperimentID: scope.ExperimentID, AppID: scope.AppID,
		Action: action, ResourceID: resourceID, RequestJSON: string(payload), Status: "pending",
		ExpiresAt: now.Add(agentApprovalLifetime).Format(time.RFC3339Nano), CreatedAt: now.Format(time.RFC3339Nano),
	}
	db, err := a.database.Pool(context.Background())
	if err != nil {
		return AgentApproval{}, err
	}
	_, err = db.Exec(`INSERT INTO agent_approvals (
id, session_id, project_id, experiment_id, app_id, action, resource_id, request_json, status, expires_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?)`, approval.ID, approval.SessionID, approval.ProjectID,
		nullableAgentID(approval.ExperimentID), nullableAgentID(approval.AppID), approval.Action, approval.ResourceID, approval.RequestJSON,
		approval.ExpiresAt, approval.CreatedAt)
	if err != nil {
		return AgentApproval{}, fmt.Errorf("could not request approval: %w", err)
	}
	a.emitAgentEvent("agent.approval.requested", approval)
	return approval, nil
}

// ResolveAgentApproval is called only by Seizen's trusted UI after explicit user input.
func (a *App) ResolveAgentApproval(id string, approved bool) (AgentApproval, error) {
	status := "denied"
	if approved {
		status = "approved"
	}
	db, err := a.database.Pool(a.context())
	if err != nil {
		return AgentApproval{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	item, err := scanAgentApproval(db.QueryRowContext(a.context(), `UPDATE agent_approvals
SET status = ?, decided_at = ? WHERE id = ? AND status = 'pending' AND expires_at > ?
RETURNING id, session_id, project_id, COALESCE(experiment_id, ''), COALESCE(app_id, ''), action, resource_id,
request_json, status, expires_at, decided_at, consumed_at, created_at`, status, now, id, now))
	if errors.Is(err, sql.ErrNoRows) {
		_, _ = db.Exec(`UPDATE agent_approvals SET status = 'expired' WHERE id = ? AND status = 'pending'`, id)
		return AgentApproval{}, errors.New("the approval does not exist, expired, or was already resolved")
	}
	if err != nil {
		return AgentApproval{}, err
	}
	a.emitAgentEvent("agent.approval.resolved", item)
	if item.Action == "experiment.create" {
		decision, event := "rejected", "experiment.rejected"
		if approved {
			decision, event = "approved", "experiment.approved"
		}
		_, _ = db.ExecContext(a.context(), `UPDATE experiment_change_requests SET decision = ?, updated_at = `+projectNow+` WHERE approval_id = ?`, decision, item.ID)
		a.emitAgentEvent(event, item)
	}
	return item, nil
}

func (a *App) ListPendingAgentApprovals(projectID string) ([]AgentApproval, error) {
	db, err := a.database.Pool(a.context())
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = db.ExecContext(a.context(), `UPDATE agent_approvals SET status = 'expired'
WHERE status = 'pending' AND expires_at <= ?`, now)
	rows, err := db.QueryContext(a.context(), `SELECT id, session_id, project_id, COALESCE(experiment_id, ''), COALESCE(app_id, ''),
action, resource_id, request_json, status, expires_at, decided_at, consumed_at, created_at
FROM agent_approvals WHERE project_id = ? AND status = 'pending' ORDER BY created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]AgentApproval, 0)
	for rows.Next() {
		item, scanErr := scanAgentApproval(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (a *App) consumeAgentApproval(scope AgentTokenScope, approvalID, action, resourceID string) error {
	db, err := a.database.Pool(context.Background())
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := db.Exec(`UPDATE agent_approvals SET status = 'consumed', consumed_at = ?
WHERE id = ? AND session_id = ? AND project_id = ? AND COALESCE(experiment_id, '') = ? AND COALESCE(app_id, '') = ?
AND action = ? AND resource_id = ? AND status = 'approved' AND expires_at > ?`, now, approvalID,
		scope.SessionID, scope.ProjectID, scope.ExperimentID, scope.AppID, action, resourceID, now)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return errors.New("the operation needs a current, unused approval")
	}
	return nil
}

func scanAgentApproval(row rowScanner) (AgentApproval, error) {
	var item AgentApproval
	err := row.Scan(&item.ID, &item.SessionID, &item.ProjectID, &item.ExperimentID, &item.AppID, &item.Action,
		&item.ResourceID, &item.RequestJSON, &item.Status, &item.ExpiresAt,
		&item.DecidedAt, &item.ConsumedAt, &item.CreatedAt)
	return item, err
}
