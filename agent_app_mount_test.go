package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgentMountTimeoutStopsRuntimeAndPersistsSource(t *testing.T) {
	app, project := newAppServerTestApp(t)
	manager := newAppRuntimeManager(app.database, nil)
	process := newFakeManagedProcess(701)
	manager.starter = func(managedProcessSpec, io.Writer) (managedProcess, error) { return process, nil }
	app.appRuntimes = manager
	app.agentTokens = newAgentTokenStore()
	bridge := newAgentBridge(app, app.agentTokens)
	token, err := app.agentTokens.Issue(AgentTokenScope{
		SessionID: "mount-agent", ProjectID: project.ID, SpaceID: currentProjectSpaceID, Permissions: appAgentPermissions,
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if discovered, discoverErr := bridge.callTool(context.Background(), token, "seizen_app_discover", mustAgentJSON(agentEmptyInput{})); discoverErr != nil || discovered == nil {
		t.Fatalf("MCP discovery failed: %v", discoverErr)
	}
	_, err = bridge.callTool(context.Background(), token, "seizen_app_mount", mustAgentJSON(agentAppMountInput{
		Configuration: agentAppConfiguration{Name: "Web", Kind: "web", WorkingDirectory: project.Path, StartCommand: "serve"},
		ExpectedPorts: []int{1}, TimeoutSeconds: 1, SetPrimary: true,
	}))
	if err == nil || process.stops.Load() == 0 {
		t.Fatalf("mount timeout did not stop its runtime: err=%v stops=%d", err, process.stops.Load())
	}
	db, _ := app.database.Pool(context.Background())
	var status, source string
	if err = db.QueryRow(`SELECT status, discovery_source FROM app_runs ORDER BY started_at DESC LIMIT 1`).Scan(&status, &source); err != nil {
		t.Fatal(err)
	}
	if status != "stopped" || source != "agent" {
		t.Fatalf("run metadata = status %q source %q", status, source)
	}
}

func TestAttachedAppRequiresManagedPortAndFollowsTerminalExit(t *testing.T) {
	app, project := newAppServerTestApp(t)
	created, err := app.CreateApp(testAppInput(project))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	backend := newTerminalTestBackend(false)
	manager := newTerminalManager(func(name string, payload any) {
		if name == terminalExitEvent {
			app.handleAttachedTerminalExit(payload.(TerminalExitEvent).SessionID, payload.(TerminalExitEvent).Error)
		}
	})
	if err = manager.startWithScopeProfile("owned", project.ID, "", currentProjectSpaceID, "cmd", "", func() (terminalBackend, error) { return backend, nil }); err != nil {
		t.Fatal(err)
	}
	app.terminals = manager
	t.Cleanup(manager.stopAll)
	app.agentTokens = newAgentTokenStore()
	bridge := newAgentBridge(app, app.agentTokens)
	token, err := app.agentTokens.Issue(AgentTokenScope{SessionID: "attach-agent", ProjectID: project.ID,
		SpaceID: currentProjectSpaceID, Permissions: appAgentPermissions}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	originalLookup := terminalPortLookup
	t.Cleanup(func() { terminalPortLookup = originalLookup })
	terminalPortLookup = func(*terminalSession) ([]DetectedTerminalEndpoint, error) { return nil, nil }
	input := agentAppAttachInput{AppID: created.ID, TerminalSessionID: "owned",
		PreviewURL: "http://" + listener.Addr().String(), DetectedPort: port, Confirmed: true}
	if _, err = bridge.callTool(context.Background(), token, "seizen_app_attach_running", mustAgentJSON(input)); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("external port was accepted: %v", err)
	}
	terminalPortLookup = func(*terminalSession) ([]DetectedTerminalEndpoint, error) {
		return []DetectedTerminalEndpoint{{PID: 77, Port: port, Managed: true}}, nil
	}
	if _, err = bridge.callTool(context.Background(), token, "seizen_app_attach_running", mustAgentJSON(input)); err != nil {
		t.Fatal(err)
	}
	backend.finish(nil)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, _ := app.GetAppStatus(created.ID)
		if current.App.Status == "stopped" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("attached App stayed running after its terminal exited")
}

func TestAgentTaskPTYInputIsScopedAuditedAndCancellable(t *testing.T) {
	app, project := newAppServerTestApp(t)
	backend := newTerminalTestBackend(false)
	manager := newTerminalManager(nil)
	if err := manager.startWithScopeProfile("agent-one", project.ID, "", currentProjectSpaceID, "codex", "codex", func() (terminalBackend, error) { return backend, nil }); err != nil {
		t.Fatal(err)
	}
	app.terminals = manager
	t.Cleanup(manager.stopAll)
	if err := app.SendAgentTask(project.ID, currentProjectSpaceID, "agent-one", "analyze the project", true); err != nil {
		t.Fatal(err)
	}
	if err := app.CancelAgentTask(project.ID, currentProjectSpaceID, "agent-one"); err != nil {
		t.Fatal(err)
	}
	input := backend.inputBytes()
	if !strings.Contains(string(input), "\x1b[200~analyze the project\x1b[201~\r") || input[len(input)-1] != 3 {
		t.Fatalf("unexpected PTY input %q", input)
	}
	if err := app.SendAgentTask(project.ID, currentProjectSpaceID, "agent-one", "bad\x1b[201~prompt", true); err == nil {
		t.Fatal("terminal control sequence was accepted")
	}
	db, _ := app.database.Pool(context.Background())
	rows, err := db.Query(`SELECT tool_name, arguments_json FROM agent_audit_events WHERE session_id = 'agent-one' ORDER BY created_at`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var tool, arguments string
		if err = rows.Scan(&tool, &arguments); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(arguments, "analyze") || !json.Valid([]byte(arguments)) {
			t.Fatalf("audit leaked task text: %s", arguments)
		}
		count++
	}
	if count != 3 {
		t.Fatalf("audited task calls = %d", count)
	}
}

func TestMCPRejectsExecutableOutsideProjectAndPrimarySurvivesMigration(t *testing.T) {
	app, project := newAppServerTestApp(t)
	outside := filepath.Join(t.TempDir(), "outside.exe")
	if err := os.WriteFile(outside, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	app.agentTokens = newAgentTokenStore()
	bridge := newAgentBridge(app, app.agentTokens)
	token, _ := app.agentTokens.Issue(AgentTokenScope{SessionID: "scope", ProjectID: project.ID, SpaceID: currentProjectSpaceID, Permissions: appAgentPermissions}, time.Minute)
	createdValue, err := bridge.callTool(context.Background(), token, "seizen_app_create", mustAgentJSON(agentAppCreateInput{Name: "Desktop", Kind: "desktop"}))
	if err != nil {
		t.Fatal(err)
	}
	draft := createdValue.(ProjectApp)
	_, err = bridge.callTool(context.Background(), token, "seizen_app_configure", mustAgentJSON(agentAppConfigureInput{AppID: draft.ID,
		Configuration: agentAppConfiguration{Name: "Desktop", Kind: "desktop", WorkingDirectory: project.Path, Executable: outside}}))
	if err == nil || !strings.Contains(err.Error(), "inside the project") {
		t.Fatalf("outside executable was accepted: %v", err)
	}
	second, err := app.CreateApp(testAppInput(project))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.SetPrimaryApp(project.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = app.SetPrimaryApp(project.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	db, _ := app.database.Pool(context.Background())
	if _, err = db.Exec(`DROP INDEX apps_primary_project_idx; UPDATE apps SET is_primary = 1 WHERE project_id = ?`, project.ID); err != nil {
		t.Fatal(err)
	}
	app.database.Close()
	if err = app.database.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	db, _ = app.database.Pool(context.Background())
	var primaries int
	if err = db.QueryRow(`SELECT COUNT(*) FROM apps WHERE project_id = ? AND is_primary = 1`, project.ID).Scan(&primaries); err != nil || primaries != 1 {
		t.Fatalf("primary backfill count=%d err=%v", primaries, err)
	}
	var primaryID string
	if err = db.QueryRow(`SELECT id FROM apps WHERE project_id = ? AND is_primary = 1`, project.ID).Scan(&primaryID); err != nil {
		t.Fatal(err)
	}
	if err = app.DeleteApp(primaryID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM apps WHERE project_id = ? AND is_primary = 1`, project.ID).Scan(&primaries); err != nil || primaries != 1 {
		t.Fatalf("primary promotion count=%d err=%v", primaries, err)
	}
}
