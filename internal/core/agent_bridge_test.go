package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAgentTokensAreHashedScopedExpiringAndRevocable(t *testing.T) {
	store := newAgentTokenStore()
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	token, err := store.Issue(AgentTokenScope{
		SessionID: "session-one", ProjectID: "project-one", AppID: "app-one",
		Permissions: []string{"read", "write"},
	}, time.Minute)
	if err != nil || token == "" {
		t.Fatalf("issue token: %q, %v", token, err)
	}
	hash := sha256.Sum256([]byte(token))
	if store.tokens[hash] == nil {
		t.Fatal("the token store does not contain the expected hash")
	}
	stored, _ := json.Marshal(store.tokens[hash])
	if strings.Contains(string(stored), token) {
		t.Fatal("raw token was stored in its record")
	}
	scope, err := store.Authorize(token, "read")
	if err != nil || scope.ProjectID != "project-one" || scope.AppID != "app-one" {
		t.Fatalf("unexpected scope: %+v, %v", scope, err)
	}
	if _, err = store.Authorize(token, "admin"); err == nil {
		t.Fatal("permission escalation was accepted")
	}
	if err = store.BindApp(token, "app-two"); err == nil {
		t.Fatal("token was rebound to another App")
	}
	now = now.Add(2 * time.Minute)
	if _, err = store.Authorize(token, "read"); err == nil {
		t.Fatal("expired token was accepted")
	}

	token, _ = store.Issue(AgentTokenScope{SessionID: "session-two", ProjectID: "project-one", Permissions: []string{"read"}}, time.Minute)
	store.RevokeSession("session-two")
	if _, err = store.Authorize(token, "read"); err == nil {
		t.Fatal("revoked session token was accepted")
	}
	token, _ = store.Issue(AgentTokenScope{SessionID: "session-three", ProjectID: "project-one", Permissions: []string{"read"}}, time.Minute)
	store.RevokeProject("project-one")
	if _, err = store.Authorize(token, "read"); err == nil {
		t.Fatal("revoked project token was accepted")
	}
}

func TestAgentBridgeOwnershipAuditRedactionAndDraftFlow(t *testing.T) {
	app, project := newAppServerTestApp(t)
	app.agentTokens = newAgentTokenStore()
	bridge := newAgentBridge(app, app.agentTokens)
	token, err := app.agentTokens.Issue(AgentTokenScope{
		SessionID: "agent-session", ProjectID: project.ID, Permissions: appAgentPermissions,
	}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	createdValue, err := bridge.callTool(context.Background(), token, "seizen_app_create", mustAgentJSON(agentAppCreateInput{Name: "Web", Kind: "web"}))
	if err != nil {
		t.Fatal(err)
	}
	created := createdValue.(ProjectApp)
	if created.Status != "unconfigured" {
		t.Fatalf("draft status = %q", created.Status)
	}
	scope, err := app.agentTokens.Authorize(token, "seizen_app_status")
	if err != nil || scope.AppID != "" {
		t.Fatalf("project-scoped token was unexpectedly bound to one draft: %+v, %v", scope, err)
	}
	configuredValue, err := bridge.callTool(context.Background(), token, "seizen_app_configure", mustAgentJSON(agentAppConfigureInput{
		AppID: created.ID,
		Configuration: agentAppConfiguration{
			Name: "Web", Kind: "web", WorkingDirectory: project.Path,
			StartCommand: "go run .", PreviewURL: "http://127.0.0.1:8080",
		},
	}))
	if err != nil || configuredValue.(ProjectApp).Status != "stopped" {
		t.Fatalf("configure draft: %+v, %v", configuredValue, err)
	}

	secondPath := filepath.Join(t.TempDir(), "other")
	if _, err = ensureProjectRoot(secondPath); err != nil {
		t.Fatal(err)
	}
	db, _ := app.database.Pool(context.Background())
	secondProject, err := upsertProject(context.Background(), db, FSProjectInfo{Name: "Other", Path: secondPath}, ProjectCreated)
	if err != nil {
		t.Fatal(err)
	}
	secondApp, err := app.CreateApp(AppInput{ProjectID: secondProject.ID, Name: "Other", Kind: "web", WorkingDirectory: secondPath, StartCommand: "go run .", ArgumentsJSON: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = bridge.callTool(context.Background(), token, "seizen_app_status", mustAgentJSON(agentAppIDInput{AppID: secondApp.ID})); err == nil {
		t.Fatal("cross-project App access was accepted")
	}

	app.recordAgentAudit(scope, "secret_test", json.RawMessage(`{"password":"p","nested":{"api_key":"k"},"safe":"ok"}`), errors.New("denied"))
	var arguments string
	if err = db.QueryRow(`SELECT arguments_json FROM agent_audit_events WHERE tool_name = 'secret_test'`).Scan(&arguments); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(arguments, `"p"`) || strings.Contains(arguments, `"k"`) || !strings.Contains(arguments, "[REDACTED]") {
		t.Fatalf("audit arguments were not redacted: %s", arguments)
	}
	var auditCount int
	if err = db.QueryRow(`SELECT COUNT(*) FROM agent_audit_events WHERE session_id = 'agent-session'`).Scan(&auditCount); err != nil || auditCount < 4 {
		t.Fatalf("expected every tool call to be audited, got %d, %v", auditCount, err)
	}
}

func TestAgentApprovalsExpireAndAreSingleUse(t *testing.T) {
	app, project := newAppServerTestApp(t)
	scope := AgentTokenScope{SessionID: "approval-session", ProjectID: project.ID}
	approval, err := app.requestAgentApproval(scope, "server.destroy", "server-one", map[string]any{"reason": "test"})
	if err != nil || approval.Status != "pending" {
		t.Fatalf("request approval: %+v, %v", approval, err)
	}
	resolved, err := app.ResolveAgentApproval(approval.ID, true)
	if err != nil || resolved.Status != "approved" {
		t.Fatalf("resolve approval: %+v, %v", resolved, err)
	}
	if err = app.consumeAgentApproval(scope, approval.ID, "server.destroy", "server-one"); err != nil {
		t.Fatal(err)
	}
	if err = app.consumeAgentApproval(scope, approval.ID, "server.destroy", "server-one"); err == nil {
		t.Fatal("approval was consumed twice")
	}

	expired, err := app.requestAgentApproval(scope, "server.destroy", "server-two", nil)
	if err != nil {
		t.Fatal(err)
	}
	db, _ := app.database.Pool(context.Background())
	_, _ = db.Exec(`UPDATE agent_approvals SET expires_at = ? WHERE id = ?`, time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano), expired.ID)
	if _, err = app.ResolveAgentApproval(expired.ID, true); err == nil {
		t.Fatal("expired approval was resolved")
	}
}

func TestAgentMCPTranscriptUsesOfficialSDK(t *testing.T) {
	app, project := newAppServerTestApp(t)
	app.agentTokens = newAgentTokenStore()
	bridge := newAgentBridge(app, app.agentTokens)
	url, err := bridge.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Close)
	token, err := app.agentTokens.Issue(AgentTokenScope{
		SessionID: "mcp-session", ProjectID: project.ID, Permissions: appAgentPermissions,
	}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	rpcClient := &agentRPCClient{endpoint: url + "/agent/tool", token: token, client: &http.Client{}}
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := newAgentMCPServer(rpcClient).Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-agent", Version: "1"}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = clientSession.Close()
		_ = serverSession.Close()
	})
	tools, err := clientSession.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != len(appAgentPermissions) {
		t.Fatalf("tool list: %d, %v", len(tools.Tools), err)
	}
	toolNames := make(map[string]bool, len(tools.Tools))
	for _, tool := range tools.Tools {
		toolNames[tool.Name] = true
	}
	for _, expected := range appAgentPermissions {
		if !toolNames[expected] {
			t.Fatalf("MCP tool list is missing %s", expected)
		}
	}
	result, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{Name: "seizen_project_context", Arguments: map[string]any{}})
	if err != nil || result.IsError || result.StructuredContent == nil {
		t.Fatalf("project context MCP result: %+v, %v", result, err)
	}
	structured, _ := json.Marshal(result.StructuredContent)
	if !strings.Contains(string(structured), project.ID) {
		t.Fatalf("project context missing project scope: %s", structured)
	}
	result, err = clientSession.CallTool(context.Background(), &mcp.CallToolParams{Name: "seizen_server_list", Arguments: map[string]any{}})
	if err != nil || result.IsError || result.StructuredContent == nil {
		t.Fatalf("server list MCP result: %+v, %v", result, err)
	}
}
