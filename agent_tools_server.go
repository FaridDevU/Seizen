package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type agentApprovalRequired struct {
	ApprovalRequired bool          `json:"approvalRequired"`
	Approval         AgentApproval `json:"approval"`
}

type agentServerIDInput struct {
	ServerID string `json:"serverId" jsonschema:"Server identifier in the authorized project and App."`
}

type agentServerLifecycleInput struct {
	ServerID   string `json:"serverId" jsonschema:"Server identifier in the authorized project and App."`
	ApprovalID string `json:"approvalId,omitempty" jsonschema:"Single-use approval returned by Seizen when provisioning needs confirmation."`
}

type agentServerCreateDraftInput struct {
	AppID      string  `json:"appId" jsonschema:"App identifier in the authorized project."`
	Name       string  `json:"name" jsonschema:"Human-readable server name."`
	Provider   string  `json:"provider" jsonschema:"Server provider: mock or wsl. Incus is currently unavailable."`
	Distro     string  `json:"distro,omitempty" jsonschema:"Distribution label; WSL uses Debian 12."`
	CPULimit   float64 `json:"cpuLimit" jsonschema:"Requested CPU limit greater than zero."`
	MemoryMB   int     `json:"memoryMb" jsonschema:"Requested memory in MiB greater than zero."`
	DiskGB     int     `json:"diskGb" jsonschema:"Requested disk size in GiB greater than zero."`
	ApprovalID string  `json:"approvalId,omitempty" jsonschema:"Single-use approval returned by the first request."`
}

type agentServerExecInput struct {
	ServerID   string `json:"serverId" jsonschema:"Running server identifier in the authorized project and App."`
	Command    string `json:"command" jsonschema:"Command without shell metacharacters. Known read-only commands run directly; all others require approval."`
	ApprovalID string `json:"approvalId,omitempty" jsonschema:"Single-use approval required for commands outside the read-only allowlist."`
}

type agentServerServiceDeclaration struct {
	Name           string         `json:"name" jsonschema:"Human-readable service name."`
	Kind           string         `json:"kind" jsonschema:"Topology kind such as frontend, backend, database, cache, queue, worker, storage, proxy, internet, or external."`
	Host           string         `json:"host,omitempty" jsonschema:"Hostname or IP observed from inside the server."`
	Port           *int           `json:"port,omitempty" jsonschema:"TCP or UDP port from 1 through 65535."`
	Protocol       string         `json:"protocol,omitempty" jsonschema:"Declared protocol such as http, https, ws, tcp, postgres, redis, or internal."`
	HealthcheckURL string         `json:"healthcheckUrl,omitempty" jsonschema:"Validated HTTP or HTTPS healthcheck URL."`
	Metadata       map[string]any `json:"metadata,omitempty" jsonschema:"Structured declaration metadata."`
	Position       *agentPosition `json:"position,omitempty" jsonschema:"Optional diagram position."`
}

type agentPosition struct {
	X float64 `json:"x" jsonschema:"Horizontal diagram coordinate."`
	Y float64 `json:"y" jsonschema:"Vertical diagram coordinate."`
}

type agentServerRegisterServiceInput struct {
	ServerID string                        `json:"serverId" jsonschema:"Server identifier in the authorized project and App."`
	Service  agentServerServiceDeclaration `json:"service" jsonschema:"Service declaration. Seizen always stores it as declared until verified."`
}

type agentServerUpdateServiceInput struct {
	ServerID  string                        `json:"serverId" jsonschema:"Server identifier in the authorized project and App."`
	ServiceID string                        `json:"serviceId" jsonschema:"Existing service identifier."`
	Service   agentServerServiceDeclaration `json:"service" jsonschema:"Complete replacement declaration. Seizen resets its source to declared."`
}

type agentServerConnectionDeclaration struct {
	SourceServiceID string         `json:"sourceServiceId" jsonschema:"Origin service identifier in this server."`
	TargetServiceID string         `json:"targetServiceId" jsonschema:"Destination service identifier in this server."`
	Protocol        string         `json:"protocol" jsonschema:"Declared connection protocol."`
	Port            *int           `json:"port,omitempty" jsonschema:"Optional connection port from 1 through 65535."`
	Metadata        map[string]any `json:"metadata,omitempty" jsonschema:"Structured declaration metadata."`
}

type agentServerRegisterConnectionInput struct {
	ServerID   string                           `json:"serverId" jsonschema:"Server identifier in the authorized project and App."`
	Connection agentServerConnectionDeclaration `json:"connection" jsonschema:"Connection declaration. Seizen always stores it as declared until verified."`
}

type agentServerUpdateConnectionInput struct {
	ServerID     string                           `json:"serverId" jsonschema:"Server identifier in the authorized project and App."`
	ConnectionID string                           `json:"connectionId" jsonschema:"Existing connection identifier."`
	Connection   agentServerConnectionDeclaration `json:"connection" jsonschema:"Complete replacement declaration. Seizen resets its source to declared."`
}

type agentServerHealthcheckInput struct {
	ServerID     string `json:"serverId" jsonschema:"Server identifier in the authorized project and App."`
	ServiceID    string `json:"serviceId,omitempty" jsonschema:"Optional service to verify."`
	ConnectionID string `json:"connectionId,omitempty" jsonschema:"Optional connection to verify. Cannot be combined with serviceId."`
}

type agentServerExportReproducibleInput struct {
	Files      []string `json:"files" jsonschema:"Declarative configuration files relative to the experiment worktree."`
	ApprovalID string   `json:"approvalId,omitempty" jsonschema:"Single-use approval returned by the first request."`
}

type agentServerStatusResult struct {
	Server         Server       `json:"server"`
	Health         ServerHealth `json:"health"`
	ProcessChecked bool         `json:"processChecked"`
}

func addAgentServerTools(server *mcp.Server, client *agentRPCClient) {
	addAgentTool(server, client, "seizen_server_list", "List servers visible in the current project and App scope.", agentEmptyInput{})
	addAgentTool(server, client, "seizen_server_create_draft", "Request approval, then create a server draft linked to an authorized App.", agentServerCreateDraftInput{})
	addAgentTool(server, client, "seizen_server_start", "Start a managed server; first provisioning requires explicit Seizen approval.", agentServerLifecycleInput{})
	addAgentTool(server, client, "seizen_server_stop", "Stop a managed server and disconnect its terminals safely.", agentServerIDInput{})
	addAgentTool(server, client, "seizen_server_restart", "Restart a managed server; provisioning requires explicit Seizen approval.", agentServerLifecycleInput{})
	addAgentTool(server, client, "seizen_server_status", "Read server status verified against its provider when provisioned.", agentServerIDInput{})
	addAgentTool(server, client, "seizen_server_exec", "Execute an allowlisted read-only command, or request explicit approval for another command.", agentServerExecInput{})
	addAgentTool(server, client, "seizen_server_register_service", "Declare a service in the selected server topology.", agentServerRegisterServiceInput{})
	addAgentTool(server, client, "seizen_server_update_service", "Replace a service declaration and reset it to declared until verified.", agentServerUpdateServiceInput{})
	addAgentTool(server, client, "seizen_server_register_connection", "Declare a connection between two services in the selected server.", agentServerRegisterConnectionInput{})
	addAgentTool(server, client, "seizen_server_update_connection", "Replace a connection declaration and reset it to declared until verified.", agentServerUpdateConnectionInput{})
	addAgentTool(server, client, "seizen_server_healthcheck", "Verify the server process, a service, or a connection using Seizen checks.", agentServerHealthcheckInput{})
	addAgentTool(server, client, "seizen_server_restart_test", "Restart the selected server and verify its real provider health.", agentServerLifecycleInput{})
	addAgentTool(server, client, "seizen_server_get_logs", "Read bounded provisioning, health and action logs for a server.", agentServerIDInput{})
	addAgentTool(server, client, "seizen_server_get_stats", "Read verified CPU, memory and disk usage for a provisioned server.", agentServerIDInput{})
	addAgentTool(server, client, "seizen_server_list_topology", "List the declared services and connections of a server.", agentServerIDInput{})
	addAgentTool(server, client, "seizen_server_publish_report", "Return a checked server, health, topology, resources, and bounded logs report.", agentServerIDInput{})
	addAgentTool(server, client, "seizen_server_export_reproducible_config", "Request approval, checkpoint declarative files, and verify a clean server rebuild before marking the experiment reproducible.", agentServerExportReproducibleInput{})
}

func (bridge *AgentBridge) callServerTool(ctx context.Context, token string, scope AgentTokenScope, tool string, arguments json.RawMessage) (any, error) {
	switch tool {
	case "seizen_server_list":
		return bridge.listServers(scope)
	case "seizen_server_create_draft":
		var input agentServerCreateDraftInput
		if err := decodeAgentArguments(arguments, &input); err != nil {
			return nil, err
		}
		return bridge.createServerDraft(ctx, token, scope, input)
	case "seizen_server_start", "seizen_server_restart", "seizen_server_restart_test":
		var input agentServerLifecycleInput
		if err := decodeAgentArguments(arguments, &input); err != nil {
			return nil, err
		}
		server, err := bridge.ensureOwnedServer(ctx, scope, input.ServerID)
		if err != nil {
			return nil, err
		}
		if server.Status == "draft" || server.Status == "provisioning" || server.RuntimeReference == "" {
			pending, approved, approvalErr := bridge.requireApproval(scope, input.ApprovalID, "server.start", server.ID, input)
			if approvalErr != nil || !approved {
				return pending, approvalErr
			}
		}
		if tool == "seizen_server_start" {
			return bridge.app.StartServer(server.ID)
		}
		restarted, err := bridge.app.RestartServer(server.ID)
		if err != nil || tool == "seizen_server_restart" {
			return restarted, err
		}
		status, err := bridge.checkedServerStatus(ctx, scope, restarted.ID)
		return map[string]any{"restart": restarted, "status": status}, err
	case "seizen_server_stop":
		var input agentServerIDInput
		if err := decodeAgentArguments(arguments, &input); err != nil {
			return nil, err
		}
		server, err := bridge.ensureOwnedServer(ctx, scope, input.ServerID)
		if err != nil {
			return nil, err
		}
		return bridge.app.StopServer(server.ID)
	case "seizen_server_get_logs":
		var input agentServerIDInput
		if err := decodeAgentArguments(arguments, &input); err != nil {
			return nil, err
		}
		server, err := bridge.ensureOwnedServer(ctx, scope, input.ServerID)
		if err != nil {
			return nil, err
		}
		logs, err := bridge.app.GetServerLogs(server.ID)
		return map[string]string{"logs": logs}, err
	case "seizen_server_get_stats":
		var input agentServerIDInput
		if err := decodeAgentArguments(arguments, &input); err != nil {
			return nil, err
		}
		server, err := bridge.ensureOwnedServer(ctx, scope, input.ServerID)
		if err != nil {
			return nil, err
		}
		return bridge.app.GetServerStats(server.ID)
	case "seizen_server_list_topology":
		var input agentServerIDInput
		if err := decodeAgentArguments(arguments, &input); err != nil {
			return nil, err
		}
		server, err := bridge.ensureOwnedServer(ctx, scope, input.ServerID)
		if err != nil {
			return nil, err
		}
		services, err := bridge.app.ListServerServices(scope.ProjectID, server.ID)
		if err != nil {
			return nil, err
		}
		connections, err := bridge.app.ListServerConnections(scope.ProjectID, server.ID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"services": services, "connections": connections}, nil
	case "seizen_server_status":
		var input agentServerIDInput
		if err := decodeAgentArguments(arguments, &input); err != nil {
			return nil, err
		}
		return bridge.checkedServerStatus(ctx, scope, input.ServerID)
	case "seizen_server_exec":
		var input agentServerExecInput
		if err := decodeAgentArguments(arguments, &input); err != nil {
			return nil, err
		}
		return bridge.execServer(ctx, scope, input)
	case "seizen_server_register_service", "seizen_server_update_service":
		return bridge.callServerServiceTool(ctx, scope, tool, arguments)
	case "seizen_server_register_connection", "seizen_server_update_connection":
		return bridge.callServerConnectionTool(ctx, scope, tool, arguments)
	case "seizen_server_healthcheck":
		var input agentServerHealthcheckInput
		if err := decodeAgentArguments(arguments, &input); err != nil {
			return nil, err
		}
		if _, err := bridge.ensureOwnedServer(ctx, scope, input.ServerID); err != nil {
			return nil, err
		}
		if input.ServiceID != "" && input.ConnectionID != "" {
			return nil, errors.New("choose a service or a connection for the healthcheck")
		}
		if input.ServiceID != "" {
			return bridge.app.VerifyServerService(scope.ProjectID, input.ServerID, input.ServiceID)
		}
		if input.ConnectionID != "" {
			return bridge.app.VerifyServerConnection(scope.ProjectID, input.ServerID, input.ConnectionID)
		}
		return bridge.checkedServerStatus(ctx, scope, input.ServerID)
	case "seizen_server_publish_report":
		var input agentServerIDInput
		if err := decodeAgentArguments(arguments, &input); err != nil {
			return nil, err
		}
		return bridge.publishServerReport(ctx, scope, input.ServerID)
	case "seizen_server_export_reproducible_config":
		var input agentServerExportReproducibleInput
		if err := decodeAgentArguments(arguments, &input); err != nil {
			return nil, err
		}
		if scope.ExperimentID == "" {
			return nil, errors.New("reproducible export is only available inside a server experiment")
		}
		experiment, _, err := bridge.app.loadExperiment(scope.ExperimentID)
		if err != nil || experiment.ProjectID != scope.ProjectID || experiment.AppID != scope.AppID || experiment.Kind != "server" {
			return nil, errors.New("the token does not belong to an authorized server experiment")
		}
		resourceID := approvalResource("server-reproducible:"+experiment.ID, input.Files)
		pending, approved, approvalErr := bridge.requireApproval(scope, input.ApprovalID, "server.export_reproducible_config", resourceID, input.Files)
		if approvalErr != nil || !approved {
			return pending, approvalErr
		}
		return bridge.app.ExportServerReproducibleConfig(experiment.ID, input.Files, true)
	default:
		return nil, errors.New("unrecognized agent Server tool")
	}
}

func (bridge *AgentBridge) listServers(scope AgentTokenScope) ([]Server, error) {
	servers, err := bridge.app.ListServersContext(scope.ProjectID, scope.ExperimentID)
	if err != nil || scope.AppID == "" {
		return servers, err
	}
	visible := make([]Server, 0, len(servers))
	for _, server := range servers {
		if server.AppID == scope.AppID {
			visible = append(visible, server)
		}
	}
	return visible, nil
}

func (bridge *AgentBridge) createServerDraft(ctx context.Context, token string, scope AgentTokenScope, input agentServerCreateDraftInput) (any, error) {
	if err := bridge.ensureOwnedApp(ctx, scope, input.AppID); err != nil {
		return nil, err
	}
	serverInput := ServerInput{
		ProjectID: scope.ProjectID, AppID: strings.TrimSpace(input.AppID), Name: strings.TrimSpace(input.Name),
		Provider: strings.TrimSpace(input.Provider), Distro: strings.TrimSpace(input.Distro),
		CPULimit: input.CPULimit, MemoryMB: input.MemoryMB, DiskGB: input.DiskGB,
	}
	if scope.ExperimentID != "" {
		serverInput.ExperimentID = scope.ExperimentID
		db, err := bridge.app.database.Pool(ctx)
		if err != nil {
			return nil, err
		}
		if err = db.QueryRowContext(ctx, `SELECT base_server_id FROM experiments
WHERE id = ? AND project_id = ? AND app_id = ? AND kind = 'server'`,
			scope.ExperimentID, scope.ProjectID, serverInput.AppID).Scan(&serverInput.BaseServerID); err != nil {
			return nil, errors.New("the experiment does not authorize creating servers")
		}
	}
	resourceID := approvalResource("server-draft", serverInput)
	pending, approved, err := bridge.requireApproval(scope, input.ApprovalID, "server.create_draft", resourceID, serverInput)
	if err != nil || !approved {
		return pending, err
	}
	server, err := bridge.app.CreateServerDraft(serverInput)
	if err == nil && scope.AppID == "" {
		err = bridge.tokens.BindApp(token, server.AppID)
	}
	return server, err
}

func (bridge *AgentBridge) requireApproval(scope AgentTokenScope, approvalID, action, resourceID string, request any) (any, bool, error) {
	if strings.TrimSpace(approvalID) == "" {
		approval, err := bridge.app.requestAgentApproval(scope, action, resourceID, request)
		if err != nil {
			return nil, false, err
		}
		return agentApprovalRequired{ApprovalRequired: true, Approval: approval}, false, nil
	}
	if err := bridge.app.consumeAgentApproval(scope, approvalID, action, resourceID); err != nil {
		return nil, false, err
	}
	return nil, true, nil
}

func approvalResource(prefix string, request any) string {
	digest := sha256.Sum256(mustAgentJSON(request))
	return fmt.Sprintf("%s:%x", prefix, digest[:12])
}

func (bridge *AgentBridge) ensureOwnedServer(ctx context.Context, scope AgentTokenScope, serverID string) (Server, error) {
	serverID = strings.TrimSpace(serverID)
	if serverID == "" {
		return Server{}, errors.New("the server is required")
	}
	db, err := bridge.app.database.Pool(ctx)
	if err != nil {
		return Server{}, err
	}
	server, err := scanServer(db.QueryRowContext(ctx, `SELECT `+serverColumns+`
FROM servers WHERE id = ? AND project_id = ? AND COALESCE(experiment_id, '') = ?`, serverID, scope.ProjectID, scope.ExperimentID))
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, errors.New("the server does not belong to the authorized project")
	}
	if err != nil {
		return Server{}, err
	}
	if scope.AppID != "" && server.AppID != scope.AppID {
		return Server{}, errors.New("the token does not allow access to another App's server")
	}
	return server, nil
}

func (bridge *AgentBridge) checkedServerStatus(ctx context.Context, scope AgentTokenScope, serverID string) (agentServerStatusResult, error) {
	server, err := bridge.ensureOwnedServer(ctx, scope, serverID)
	if err != nil {
		return agentServerStatusResult{}, err
	}
	result := agentServerStatusResult{
		Server: server,
		Health: ServerHealth{Healthy: false, Message: "server not provisioned"},
	}
	if server.Status != "running" && server.Status != "degraded" && server.Status != "starting" {
		if server.Status == "stopped" || server.Status == "failed" {
			result.Health.Message = "server stopped; provider not started to check it"
		}
		return result, nil
	}
	if server.RuntimeReference == "" {
		return result, errors.New("the running server has no runtime reference")
	}
	result.Health, err = bridge.app.CheckServerHealth(server.ID)
	if err != nil {
		return result, err
	}
	result.ProcessChecked = true
	result.Server, err = bridge.ensureOwnedServer(ctx, scope, serverID)
	return result, err
}

var readOnlyServerCommands = map[string]bool{
	"pwd": true, "whoami": true, "id": true, "uname": true, "uname -a": true,
	"uptime": true, "df -h": true, "free -m": true, "ps aux": true,
	"ss -lnt": true, "cat /etc/os-release": true,
	"systemctl --no-pager --type=service --state=running": true,
}

func (bridge *AgentBridge) execServer(ctx context.Context, scope AgentTokenScope, input agentServerExecInput) (any, error) {
	server, err := bridge.ensureOwnedServer(ctx, scope, input.ServerID)
	if err != nil {
		return nil, err
	}
	command := strings.Join(strings.Fields(input.Command), " ")
	if command == "" {
		return nil, errors.New("the command is empty")
	}
	if strings.ContainsAny(command, "&|;<>\r\n`$(){}[]!*?~") {
		return nil, errors.New("the command contains disallowed metacharacters")
	}
	if !readOnlyServerCommands[command] {
		resourceID := approvalResource("server-exec:"+server.ID, map[string]string{"command": command})
		pending, approved, approvalErr := bridge.requireApproval(scope, input.ApprovalID, "server.exec", resourceID, map[string]string{
			"serverId": server.ID, "command": command,
		})
		if approvalErr != nil || !approved {
			return pending, approvalErr
		}
	}
	return bridge.app.projectServerManager().Exec(ctx, server.ID, command)
}

func (bridge *AgentBridge) callServerServiceTool(ctx context.Context, scope AgentTokenScope, tool string, arguments json.RawMessage) (any, error) {
	if tool == "seizen_server_register_service" {
		var input agentServerRegisterServiceInput
		if err := decodeAgentArguments(arguments, &input); err != nil {
			return nil, err
		}
		if _, err := bridge.ensureOwnedServer(ctx, scope, input.ServerID); err != nil {
			return nil, err
		}
		serviceInput, err := input.Service.storeInput(scope.ProjectID, input.ServerID, "")
		if err != nil {
			return nil, err
		}
		return bridge.app.RegisterServerService(serviceInput)
	}
	var input agentServerUpdateServiceInput
	if err := decodeAgentArguments(arguments, &input); err != nil {
		return nil, err
	}
	if _, err := bridge.ensureOwnedServer(ctx, scope, input.ServerID); err != nil {
		return nil, err
	}
	serviceInput, err := input.Service.storeInput(scope.ProjectID, input.ServerID, input.ServiceID)
	if err != nil {
		return nil, err
	}
	return bridge.app.UpdateServerService(serviceInput)
}

func (declaration agentServerServiceDeclaration) storeInput(projectID, serverID, serviceID string) (ServerServiceInput, error) {
	metadata, err := json.Marshal(declaration.Metadata)
	if err != nil {
		return ServerServiceInput{}, errors.New("invalid service metadata")
	}
	if declaration.Metadata == nil {
		metadata = []byte(`{}`)
	}
	position := []byte(`{}`)
	if declaration.Position != nil {
		position, err = json.Marshal(declaration.Position)
		if err != nil {
			return ServerServiceInput{}, errors.New("invalid service position")
		}
	}
	return ServerServiceInput{
		ID: serviceID, ProjectID: projectID, ServerID: serverID, Name: declaration.Name,
		Kind: declaration.Kind, Host: declaration.Host, Port: declaration.Port,
		Protocol: declaration.Protocol, HealthcheckURL: declaration.HealthcheckURL,
		MetadataJSON: string(metadata), PositionJSON: string(position),
	}, nil
}

func (bridge *AgentBridge) callServerConnectionTool(ctx context.Context, scope AgentTokenScope, tool string, arguments json.RawMessage) (any, error) {
	if tool == "seizen_server_register_connection" {
		var input agentServerRegisterConnectionInput
		if err := decodeAgentArguments(arguments, &input); err != nil {
			return nil, err
		}
		if _, err := bridge.ensureOwnedServer(ctx, scope, input.ServerID); err != nil {
			return nil, err
		}
		connectionInput, err := input.Connection.storeInput(scope.ProjectID, input.ServerID, "")
		if err != nil {
			return nil, err
		}
		return bridge.app.RegisterServerConnection(connectionInput)
	}
	var input agentServerUpdateConnectionInput
	if err := decodeAgentArguments(arguments, &input); err != nil {
		return nil, err
	}
	if _, err := bridge.ensureOwnedServer(ctx, scope, input.ServerID); err != nil {
		return nil, err
	}
	connectionInput, err := input.Connection.storeInput(scope.ProjectID, input.ServerID, input.ConnectionID)
	if err != nil {
		return nil, err
	}
	return bridge.app.UpdateServerConnection(connectionInput)
}

func (declaration agentServerConnectionDeclaration) storeInput(projectID, serverID, connectionID string) (ServerConnectionInput, error) {
	metadata, err := json.Marshal(declaration.Metadata)
	if err != nil {
		return ServerConnectionInput{}, errors.New("invalid connection metadata")
	}
	if declaration.Metadata == nil {
		metadata = []byte(`{}`)
	}
	return ServerConnectionInput{
		ID: connectionID, ProjectID: projectID, ServerID: serverID,
		SourceServiceID: declaration.SourceServiceID, TargetServiceID: declaration.TargetServiceID,
		Protocol: declaration.Protocol, Port: declaration.Port, MetadataJSON: string(metadata),
	}, nil
}

func (bridge *AgentBridge) publishServerReport(ctx context.Context, scope AgentTokenScope, serverID string) (map[string]any, error) {
	status, err := bridge.checkedServerStatus(ctx, scope, serverID)
	if err != nil {
		return nil, err
	}
	stats := ServerStats{
		MemoryLimitMB:    status.Server.MemoryMB,
		LimitsEnforced:   false,
		LimitDescription: "Requested resources; the stopped provider was not started to measure them.",
	}
	if status.ProcessChecked {
		stats, err = bridge.app.GetServerStats(serverID)
		if err != nil {
			return nil, err
		}
	}
	services, err := bridge.app.ListServerServices(scope.ProjectID, serverID)
	if err != nil {
		return nil, err
	}
	connections, err := bridge.app.ListServerConnections(scope.ProjectID, serverID)
	if err != nil {
		return nil, err
	}
	logs, err := bridge.app.GetServerLogs(serverID)
	if err != nil {
		return nil, err
	}
	report := map[string]any{
		"status": status, "stats": stats, "services": services,
		"connections": connections, "logs": logs,
	}
	bridge.app.emitAgentEvent("server.report.published", report)
	return report, nil
}
