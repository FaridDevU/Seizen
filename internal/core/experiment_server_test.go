package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type recordingWSLRebuildProvider struct {
	ServerProvider
	commands []string
}

func newRecordingWSLRebuildProvider() *recordingWSLRebuildProvider {
	return &recordingWSLRebuildProvider{ServerProvider: NewMockServerProvider()}
}

func mockWSLServer(server Server) Server {
	server.Provider = "mock"
	return server
}

func (provider *recordingWSLRebuildProvider) Create(ctx context.Context, server Server) (string, error) {
	return provider.ServerProvider.Create(ctx, mockWSLServer(server))
}

func (provider *recordingWSLRebuildProvider) Start(ctx context.Context, server Server) error {
	return provider.ServerProvider.Start(ctx, mockWSLServer(server))
}

func (provider *recordingWSLRebuildProvider) Stop(ctx context.Context, server Server) error {
	return provider.ServerProvider.Stop(ctx, mockWSLServer(server))
}

func (provider *recordingWSLRebuildProvider) Destroy(ctx context.Context, server Server) error {
	return provider.ServerProvider.Destroy(ctx, mockWSLServer(server))
}

func (provider *recordingWSLRebuildProvider) CheckHealth(ctx context.Context, server Server) (ServerHealth, error) {
	return provider.ServerProvider.CheckHealth(ctx, mockWSLServer(server))
}

func (provider *recordingWSLRebuildProvider) Exec(_ context.Context, _ Server, command string) (ServerExecResult, error) {
	provider.commands = append(provider.commands, command)
	return ServerExecResult{Output: "ok", ExitCode: 0}, nil
}

func newServerExperiment(t *testing.T) (*App, Project, ProjectApp, Experiment, Server) {
	t.Helper()
	app, project, projectApp, _, manager, _ := newLifecycleExperiment(t)
	provider := NewMockServerProvider()
	app.servers = newServerManager(app.database, nil, map[string]ServerProvider{"mock": provider})
	base, err := app.CreateServerDraft(ServerInput{
		ProjectID: project.ID, AppID: projectApp.ID, Name: "Base",
		Provider: "mock", Distro: "Debian 13", CPULimit: 1, MemoryMB: 512, DiskGB: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	experiment, err := app.CreateExperiment(ExperimentCreateInput{
		ProjectID: project.ID, Kind: "server", AppID: projectApp.ID, BaseServerID: base.ID,
		Name: "Reproducible server", Objective: "test rebuild", CreatedBy: "user", Confirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	servers, err := app.ListServersContext(project.ID, experiment.ID)
	if err != nil || len(servers) != 1 {
		t.Fatalf("experimental servers = %#v, %v", servers, err)
	}
	t.Cleanup(func() {
		_, _ = manager.StopAppContext(app.context(), projectApp.ID, experiment.ID)
	})
	return app, project, projectApp, experiment, servers[0]
}

func TestServerExperimentAgentScopeAndReproducibleExportApproval(t *testing.T) {
	app, project, projectApp, experiment, server := newServerExperiment(t)
	if err := os.WriteFile(filepath.Join(experiment.WorktreePath, "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app.agentTokens = newAgentTokenStore()
	token, err := app.agentTokens.Issue(AgentTokenScope{
		SessionID: "server-experiment-agent", ProjectID: project.ID, ExperimentID: experiment.ID,
		AppID: projectApp.ID, Permissions: appAgentPermissions,
	}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	bridge := newAgentBridge(app, app.agentTokens)
	listed, err := bridge.callTool(context.Background(), token, "seizen_server_list", mustAgentJSON(agentEmptyInput{}))
	if err != nil || len(listed.([]Server)) != 1 || listed.([]Server)[0].ID != server.ID {
		t.Fatalf("experiment-scoped servers = %#v, %v", listed, err)
	}
	if experiment.BaseServerID == nil {
		t.Fatal("missing base server")
	}
	if _, err = bridge.callTool(context.Background(), token, "seizen_server_status", mustAgentJSON(agentServerIDInput{ServerID: *experiment.BaseServerID})); err == nil {
		t.Fatal("experiment token accessed the principal server")
	}
	input := agentServerExportReproducibleInput{Files: []string{"Dockerfile"}}
	pending, err := bridge.callTool(context.Background(), token, "seizen_server_export_reproducible_config", mustAgentJSON(input))
	if err != nil {
		t.Fatal(err)
	}
	input.ApprovalID = approveAgentRequest(t, app, pending)
	exported, err := bridge.callTool(context.Background(), token, "seizen_server_export_reproducible_config", mustAgentJSON(input))
	if err != nil || !exported.(ServerReproducibleExport).Rebuilt {
		t.Fatalf("approved reproducible export = %#v, %v", exported, err)
	}
}

func TestServerExperimentExportsAndRebuildsDeclarativeConfiguration(t *testing.T) {
	app, project, _, experiment, server := newServerExperiment(t)
	if err := os.WriteFile(filepath.Join(experiment.WorktreePath, "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.RegisterServerService(ServerServiceInput{
		ProjectID: project.ID, ServerID: server.ID, Name: "API", Kind: "backend",
		Protocol: "http", MetadataJSON: "{}", PositionJSON: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	result, err := app.ExportServerReproducibleConfig(experiment.ID, []string{"Dockerfile"}, true)
	if err != nil || !result.Rebuilt || !result.Health.Healthy || len(result.Services) != 1 {
		t.Fatalf("export = %#v, %v", result, err)
	}
	configuration, err := decodeExperimentConfiguration(result.Experiment)
	if err != nil || !configuration.Reproducible || len(configuration.ReproducibleFiles) != 1 || configuration.ReproducibleFiles[0] != "Dockerfile" {
		t.Fatalf("configuration = %#v, %v", configuration, err)
	}
	servers, err := app.ListServersContext(project.ID, experiment.ID)
	if err != nil || len(servers) != 1 || servers[0].ID != server.ID {
		t.Fatalf("temporary rebuild was not cleaned: %#v, %v", servers, err)
	}
}

func TestServerExperimentBlocksNonReproducibleIntegration(t *testing.T) {
	app, _, _, experiment, _ := newServerExperiment(t)
	if err := os.WriteFile(filepath.Join(experiment.WorktreePath, "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	review, err := app.PrepareExperimentIntegration(experiment.ID, true)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "non-reproducible") || review.ReproducibleVerified {
		t.Fatalf("non-reproducible review = %#v, %v", review, err)
	}
	if _, err = app.ExportServerReproducibleConfig(experiment.ID, []string{"Dockerfile"}, true); err != nil {
		t.Fatal(err)
	}
	review, err = app.PrepareExperimentIntegration(experiment.ID, true)
	if err != nil || !review.ReproducibleVerified || review.Experiment.Status != "review_ready" {
		t.Fatalf("reproducible review = %#v, %v", review, err)
	}
}

func TestServerReproducibleExportRejectsEscapesAndSecrets(t *testing.T) {
	app, _, _, experiment, _ := newServerExperiment(t)
	if _, err := app.ExportServerReproducibleConfig(experiment.ID, []string{"../outside.yaml"}, true); err == nil {
		t.Fatal("path outside the worktree was accepted")
	}
	if err := os.WriteFile(filepath.Join(experiment.WorktreePath, "compose.yaml"), []byte("password: \"super-secret-value\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ExportServerReproducibleConfig(experiment.ID, []string{"compose.yaml"}, true); err == nil {
		t.Fatal("secret-bearing declarative file was accepted")
	}
}

func TestArchivedServerExperimentRestoresDeclarativeServer(t *testing.T) {
	app, project, _, experiment, server := newServerExperiment(t)
	archived, err := app.ArchiveExperiment(experiment.ID, true)
	if err != nil || archived.Status != "archived" {
		t.Fatalf("archived = %#v, %v", archived, err)
	}
	servers, err := app.ListServersContext(project.ID, experiment.ID)
	if err != nil || len(servers) != 0 {
		t.Fatalf("archived servers = %#v, %v", servers, err)
	}
	restored, err := app.RestoreExperiment(experiment.ID, true)
	if err != nil || restored.Status != "active" {
		t.Fatalf("restored = %#v, %v", restored, err)
	}
	servers, err = app.ListServersContext(project.ID, experiment.ID)
	if err != nil || len(servers) != 1 || servers[0].ID == server.ID || servers[0].BaseServerID == nil || *servers[0].BaseServerID != *experiment.BaseServerID {
		t.Fatalf("restored server = %#v, %v", servers, err)
	}
}

func TestWSLRebuildReplaysTrackedFilesInsideCleanServer(t *testing.T) {
	app, project, projectApp, _, _, _ := newLifecycleExperiment(t)
	provider := newRecordingWSLRebuildProvider()
	app.servers = newServerManager(app.database, nil, map[string]ServerProvider{"wsl": provider})
	base, err := app.CreateServerDraft(ServerInput{
		ProjectID: project.ID, AppID: projectApp.ID, Name: "WSL Base", Provider: "wsl",
		Distro: "Debian 12", CPULimit: 1, MemoryMB: 512, DiskGB: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	experiment, err := app.CreateExperiment(ExperimentCreateInput{
		ProjectID: project.ID, Kind: "server", AppID: projectApp.ID, BaseServerID: base.ID,
		Name: "WSL replay", Objective: "replay files", CreatedBy: "user", Confirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(experiment.WorktreePath, "server.conf"), []byte("enabled=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(experiment.WorktreePath, "seizen-rebuild.sh"), []byte("test -f server.conf\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := app.ExportServerReproducibleConfig(experiment.ID, []string{"server.conf", "seizen-rebuild.sh"}, true)
	if err != nil || !result.Rebuilt {
		t.Fatalf("WSL replay export = %#v, %v", result, err)
	}
	joined := strings.Join(provider.commands, "\n")
	if !strings.Contains(joined, "base64 -d") || !strings.Contains(joined, "cd /root/seizen-rebuild && /bin/sh 'seizen-rebuild.sh'") {
		t.Fatalf("rebuild commands = %s", joined)
	}
}
