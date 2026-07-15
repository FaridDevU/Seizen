package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type agentServerFixture struct {
	app        *App
	project    Project
	projectApp ProjectApp
	bridge     *AgentBridge
	token      string
}

func newAgentServerFixture(t *testing.T) agentServerFixture {
	t.Helper()
	app, project := newAppServerTestApp(t)
	projectApp, err := app.CreateApp(testAppInput(project))
	if err != nil {
		t.Fatal(err)
	}
	app.agentTokens = newAgentTokenStore()
	app.servers = newServerManager(app.database, nil, map[string]ServerProvider{"mock": NewMockServerProvider()})
	token, err := app.agentTokens.Issue(AgentTokenScope{
		SessionID: "server-agent", ProjectID: project.ID, AppID: projectApp.ID,
		Permissions: appAgentPermissions,
	}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return agentServerFixture{
		app: app, project: project, projectApp: projectApp,
		bridge: newAgentBridge(app, app.agentTokens), token: token,
	}
}

func agentServerDraftInput(fixture agentServerFixture) agentServerCreateDraftInput {
	return agentServerCreateDraftInput{
		AppID: fixture.projectApp.ID, Name: "Debian Agent", Provider: "mock",
		Distro: "Debian 12", CPULimit: 2, MemoryMB: 1024, DiskGB: 10,
	}
}

func approveAgentRequest(t *testing.T, app *App, result any) string {
	t.Helper()
	pending, ok := result.(agentApprovalRequired)
	if !ok || !pending.ApprovalRequired || pending.Approval.Status != "pending" {
		t.Fatalf("expected structured approval request, got %#v", result)
	}
	resolved, err := app.ResolveAgentApproval(pending.Approval.ID, true)
	if err != nil || resolved.Status != "approved" {
		t.Fatalf("approve request: %+v, %v", resolved, err)
	}
	return pending.Approval.ID
}

func TestAgentServerDraftStartAndExecApprovalsAreSingleUse(t *testing.T) {
	fixture := newAgentServerFixture(t)
	input := agentServerDraftInput(fixture)

	pending, err := fixture.bridge.callTool(context.Background(), fixture.token, "seizen_server_create_draft", mustAgentJSON(input))
	if err != nil {
		t.Fatal(err)
	}
	input.ApprovalID = approveAgentRequest(t, fixture.app, pending)
	createdValue, err := fixture.bridge.callTool(context.Background(), fixture.token, "seizen_server_create_draft", mustAgentJSON(input))
	if err != nil {
		t.Fatal(err)
	}
	server := createdValue.(Server)
	if server.Status != "draft" || server.AppID != fixture.projectApp.ID {
		t.Fatalf("unexpected approved draft: %+v", server)
	}
	if _, err = fixture.bridge.callTool(context.Background(), fixture.token, "seizen_server_create_draft", mustAgentJSON(input)); err == nil {
		t.Fatal("consumed draft approval was reused")
	}

	startInput := agentServerLifecycleInput{ServerID: server.ID}
	pending, err = fixture.bridge.callTool(context.Background(), fixture.token, "seizen_server_start", mustAgentJSON(startInput))
	if err != nil {
		t.Fatal(err)
	}
	startInput.ApprovalID = approveAgentRequest(t, fixture.app, pending)
	runningValue, err := fixture.bridge.callTool(context.Background(), fixture.token, "seizen_server_start", mustAgentJSON(startInput))
	if err != nil || runningValue.(Server).Status != "running" {
		t.Fatalf("start approved server: %+v, %v", runningValue, err)
	}
	restartTest, err := fixture.bridge.callTool(context.Background(), fixture.token, "seizen_server_restart_test", mustAgentJSON(agentServerLifecycleInput{ServerID: server.ID}))
	if err != nil || !restartTest.(map[string]any)["status"].(agentServerStatusResult).Health.Healthy {
		t.Fatalf("restart test did not perform a real healthcheck: %+v, %v", restartTest, err)
	}

	safe, err := fixture.bridge.callTool(context.Background(), fixture.token, "seizen_server_exec", mustAgentJSON(agentServerExecInput{
		ServerID: server.ID, Command: "uptime",
	}))
	if err != nil || !strings.Contains(safe.(ServerExecResult).Output, "uptime") {
		t.Fatalf("safe exec: %+v, %v", safe, err)
	}
	execInput := agentServerExecInput{ServerID: server.ID, Command: "apt-get update"}
	pending, err = fixture.bridge.callTool(context.Background(), fixture.token, "seizen_server_exec", mustAgentJSON(execInput))
	if err != nil {
		t.Fatal(err)
	}
	execInput.ApprovalID = approveAgentRequest(t, fixture.app, pending)
	approved, err := fixture.bridge.callTool(context.Background(), fixture.token, "seizen_server_exec", mustAgentJSON(execInput))
	if err != nil || !strings.Contains(approved.(ServerExecResult).Output, "apt-get update") {
		t.Fatalf("approved exec: %+v, %v", approved, err)
	}
	if _, err = fixture.bridge.callTool(context.Background(), fixture.token, "seizen_server_exec", mustAgentJSON(agentServerExecInput{
		ServerID: server.ID, Command: "rm -rf /; whoami",
	})); err == nil {
		t.Fatal("shell metacharacters were accepted")
	}

	db, _ := fixture.app.database.Pool(context.Background())
	var auditedApproval string
	if err = db.QueryRow(`SELECT COALESCE(approval_id, '') FROM agent_audit_events
WHERE tool_name = 'seizen_server_exec' AND approval_id <> '' ORDER BY created_at DESC LIMIT 1`).Scan(&auditedApproval); err != nil || auditedApproval != execInput.ApprovalID {
		t.Fatalf("approval was not linked to audit: %q, %v", auditedApproval, err)
	}
}

func TestAgentServerScopeBlocksOtherApps(t *testing.T) {
	fixture := newAgentServerFixture(t)
	otherInput := testAppInput(fixture.project)
	otherInput.Name = "Other App"
	otherApp, err := fixture.app.CreateApp(otherInput)
	if err != nil {
		t.Fatal(err)
	}
	owned, err := fixture.app.CreateServerDraft(ServerInput{
		ProjectID: fixture.project.ID, AppID: fixture.projectApp.ID, Name: "Owned", Provider: "mock",
		Distro: "Debian 12", CPULimit: 1, MemoryMB: 512, DiskGB: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := fixture.app.CreateServerDraft(ServerInput{
		ProjectID: fixture.project.ID, AppID: otherApp.ID, Name: "Other", Provider: "mock",
		Distro: "Debian 12", CPULimit: 1, MemoryMB: 512, DiskGB: 5,
	})
	if err != nil {
		t.Fatal(err)
	}

	listed, err := fixture.bridge.callTool(context.Background(), fixture.token, "seizen_server_list", mustAgentJSON(agentEmptyInput{}))
	servers := listed.([]Server)
	if err != nil || len(servers) != 1 || servers[0].ID != owned.ID {
		t.Fatalf("server list escaped App scope: %+v, %v", servers, err)
	}
	if _, err = fixture.bridge.callTool(context.Background(), fixture.token, "seizen_server_status", mustAgentJSON(agentServerIDInput{ServerID: other.ID})); err == nil {
		t.Fatal("cross-App server access was accepted")
	}
	if _, err = fixture.bridge.callTool(context.Background(), fixture.token, "seizen_server_register_service", mustAgentJSON(agentServerRegisterServiceInput{
		ServerID: other.ID, Service: agentServerServiceDeclaration{Name: "Bad", Kind: "backend"},
	})); err == nil {
		t.Fatal("cross-App topology write was accepted")
	}

	db, _ := fixture.app.database.Pool(context.Background())
	otherProjectPath := filepath.Join(t.TempDir(), "other-project")
	if _, err = ensureProjectRoot(otherProjectPath); err != nil {
		t.Fatal(err)
	}
	otherProject, err := upsertProject(context.Background(), db, FSProjectInfo{Name: "Other Project", Path: otherProjectPath}, ProjectCreated)
	if err != nil {
		t.Fatal(err)
	}
	crossInput := testAppInput(otherProject)
	crossInput.WorkingDirectory = otherProject.Path
	crossApp, err := fixture.app.CreateApp(crossInput)
	if err != nil {
		t.Fatal(err)
	}
	crossServer, err := fixture.app.CreateServerDraft(ServerInput{
		ProjectID: otherProject.ID, AppID: crossApp.ID, Name: "Cross project", Provider: "mock",
		Distro: "Debian 12", CPULimit: 1, MemoryMB: 512, DiskGB: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.bridge.callTool(context.Background(), fixture.token, "seizen_server_status", mustAgentJSON(agentServerIDInput{ServerID: crossServer.ID})); err == nil {
		t.Fatal("cross-project server access was accepted")
	}
}

func TestAgentServerTopologyStartsDeclaredThenRequiresRealVerification(t *testing.T) {
	fixture := newAgentServerFixture(t)
	server, err := fixture.app.CreateServerDraft(ServerInput{
		ProjectID: fixture.project.ID, AppID: fixture.projectApp.ID, Name: "Topology", Provider: "mock",
		Distro: "Debian 12", CPULimit: 1, MemoryMB: 512, DiskGB: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.app.StartServer(server.ID); err != nil {
		t.Fatal(err)
	}
	healthEndpoint := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(healthEndpoint.Close)

	registeredValue, err := fixture.bridge.callTool(context.Background(), fixture.token, "seizen_server_register_service", mustAgentJSON(agentServerRegisterServiceInput{
		ServerID: server.ID,
		Service: agentServerServiceDeclaration{
			Name: "API", Kind: "backend", Protocol: "http", HealthcheckURL: healthEndpoint.URL,
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	registered := registeredValue.(ServerService)
	if registered.Source != "declared" || registered.Status != "unknown" {
		t.Fatalf("agent declaration claimed runtime truth: %+v", registered)
	}
	verifiedValue, err := fixture.bridge.callTool(context.Background(), fixture.token, "seizen_server_healthcheck", mustAgentJSON(agentServerHealthcheckInput{
		ServerID: server.ID, ServiceID: registered.ID,
	}))
	if err != nil {
		t.Fatal(err)
	}
	verified := verifiedValue.(ServerService)
	if verified.Source != "verified" || verified.Status != "healthy" {
		t.Fatalf("real healthcheck did not verify declaration: %+v", verified)
	}
	statusValue, err := fixture.bridge.callTool(context.Background(), fixture.token, "seizen_server_status", mustAgentJSON(agentServerIDInput{ServerID: server.ID}))
	status := statusValue.(agentServerStatusResult)
	if err != nil || !status.ProcessChecked || !status.Health.Healthy {
		t.Fatalf("server status was not provider-checked: %+v, %v", status, err)
	}
}

type agentCountingServerProvider struct {
	ServerProvider
	healthCalls int
	statsCalls  int
}

func (provider *agentCountingServerProvider) CheckHealth(context.Context, Server) (ServerHealth, error) {
	provider.healthCalls++
	return ServerHealth{Healthy: true, Message: "unexpected"}, nil
}

func (provider *agentCountingServerProvider) Stats(context.Context, Server) (ServerStats, error) {
	provider.statsCalls++
	return ServerStats{}, nil
}

func TestAgentStoppedServerStatusAndReportDoNotStartProvider(t *testing.T) {
	fixture := newAgentServerFixture(t)
	provider := &agentCountingServerProvider{ServerProvider: NewMockServerProvider()}
	fixture.app.servers = newServerManager(fixture.app.database, nil, map[string]ServerProvider{"mock": provider})
	server, err := fixture.app.CreateServerDraft(ServerInput{
		ProjectID: fixture.project.ID, AppID: fixture.projectApp.ID, Name: "Stopped", Provider: "mock",
		Distro: "Debian 12", CPULimit: 2, MemoryMB: 1536, DiskGB: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	db, _ := fixture.app.database.Pool(context.Background())
	server, err = updateServerState(context.Background(), db, server.ID, "stopped", "mock://"+server.ID)
	if err != nil {
		t.Fatal(err)
	}
	statusValue, err := fixture.bridge.callTool(context.Background(), fixture.token, "seizen_server_status", mustAgentJSON(agentServerIDInput{ServerID: server.ID}))
	if err != nil || statusValue.(agentServerStatusResult).ProcessChecked {
		t.Fatalf("stopped status unexpectedly checked provider: %+v, %v", statusValue, err)
	}
	reportValue, err := fixture.bridge.callTool(context.Background(), fixture.token, "seizen_server_publish_report", mustAgentJSON(agentServerIDInput{ServerID: server.ID}))
	if err != nil {
		t.Fatal(err)
	}
	report := reportValue.(map[string]any)
	stats := report["stats"].(ServerStats)
	if stats.MemoryLimitMB != server.MemoryMB || provider.healthCalls != 0 || provider.statsCalls != 0 {
		t.Fatalf("stopped report started provider or lost requested resources: %+v, health=%d stats=%d", stats, provider.healthCalls, provider.statsCalls)
	}
}
