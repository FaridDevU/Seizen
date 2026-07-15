package core

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func topologyTestServer(t *testing.T) (*App, Project, Server) {
	t.Helper()
	app, project := newAppServerTestApp(t)
	projectApp, err := app.CreateApp(testAppInput(project))
	if err != nil {
		t.Fatal(err)
	}
	server, err := app.CreateServerDraft(ServerInput{
		ProjectID: project.ID, AppID: projectApp.ID, Name: "Lab", Provider: "mock",
		CPULimit: 1, MemoryMB: 512, DiskGB: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err = app.StartMockServer(server.ID)
	if err != nil {
		t.Fatal(err)
	}
	return app, project, server
}

func TestTopologyDeclarationsValidateOwnershipAndAgentClaims(t *testing.T) {
	app, project, server := topologyTestServer(t)
	port := 8080
	service, err := app.RegisterServerService(ServerServiceInput{
		ProjectID: project.ID, ServerID: server.ID, Name: "API", Kind: "backend",
		Host: "127.0.0.1", Port: &port, Protocol: "http", MetadataJSON: `{"pid":12}`,
		PositionJSON: `{"x":1,"y":2}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if service.Source != "declared" || service.Status != "unknown" {
		t.Fatalf("agent declaration became runtime truth: %+v", service)
	}
	internet, err := app.RegisterServerService(ServerServiceInput{
		ProjectID: project.ID, ServerID: server.ID, Name: "Internet", Kind: "internet",
		Protocol: "https",
	})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := app.RegisterServerConnection(ServerConnectionInput{
		ProjectID: project.ID, ServerID: server.ID, SourceServiceID: internet.ID,
		TargetServiceID: service.ID, Protocol: "http", Port: &port,
	})
	if err != nil || connection.Source != "declared" || connection.TrafficRate != 0 {
		t.Fatalf("unexpected connection: %+v, %v", connection, err)
	}

	updated, err := app.UpdateServicePosition(project.ID, server.ID, service.ID, `{"x":20,"y":40}`)
	if err != nil || updated.PositionJSON != `{"x":20,"y":40}` {
		t.Fatalf("position was not persisted: %+v, %v", updated, err)
	}
	if _, err = app.ListServerServices("other-project", server.ID); err == nil {
		t.Fatal("expected project ownership rejection")
	}
	badPort := 70000
	if _, err = app.RegisterServerService(ServerServiceInput{
		ProjectID: project.ID, ServerID: server.ID, Name: "bad", Kind: "shell",
		Port: &badPort, MetadataJSON: `[]`, HealthcheckURL: "http://wails.localhost",
	}); err == nil {
		t.Fatal("expected invalid topology input to be rejected")
	}
}

func TestTopologyRejectsCrossServerConnections(t *testing.T) {
	app, project, first := topologyTestServer(t)
	firstApp, err := app.ListApps(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.CreateServerDraft(ServerInput{
		ProjectID: project.ID, AppID: firstApp[0].ID, Name: "Other", Provider: "mock",
		CPULimit: 1, MemoryMB: 512, DiskGB: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	one, err := app.RegisterServerService(ServerServiceInput{
		ProjectID: project.ID, ServerID: first.ID, Name: "one", Kind: "frontend",
	})
	if err != nil {
		t.Fatal(err)
	}
	two, err := app.RegisterServerService(ServerServiceInput{
		ProjectID: project.ID, ServerID: second.ID, Name: "two", Kind: "backend",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.RegisterServerConnection(ServerConnectionInput{
		ProjectID: project.ID, ServerID: first.ID, SourceServiceID: one.ID,
		TargetServiceID: two.ID, Protocol: "tcp",
	}); err == nil {
		t.Fatal("expected cross-server connection to be rejected")
	}
	if _, err = app.UpdateServerService(ServerServiceInput{
		ID: one.ID, ProjectID: project.ID, ServerID: second.ID, Name: "moved", Kind: "frontend",
	}); err == nil {
		t.Fatal("expected cross-server service update to be rejected")
	}
}

func TestTopologyVerifiesTCPHTTPAndEmitsHealthPulse(t *testing.T) {
	app, project, server := topologyTestServer(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			_ = connection.Close()
		}
	}()
	tcpPort := listener.Addr().(*net.TCPAddr).Port
	health := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(health.Close)

	var mu sync.Mutex
	var events []string
	app.ctx = context.Background()
	app.emitEvent = func(_ context.Context, name string, _ ...interface{}) {
		mu.Lock()
		events = append(events, name)
		mu.Unlock()
	}
	source, err := app.RegisterServerService(ServerServiceInput{
		ProjectID: project.ID, ServerID: server.ID, Name: "Frontend", Kind: "frontend",
		HealthcheckURL: health.URL, Protocol: "http",
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := app.RegisterServerService(ServerServiceInput{
		ProjectID: project.ID, ServerID: server.ID, Name: "Backend", Kind: "backend",
		Host: "127.0.0.1", Port: &tcpPort, Protocol: "tcp",
	})
	if err != nil {
		t.Fatal(err)
	}
	verifiedHTTP, err := app.VerifyServerService(project.ID, server.ID, source.ID)
	if err != nil || verifiedHTTP.Source != "verified" || verifiedHTTP.Status != "healthy" {
		t.Fatalf("HTTP service was not verified: %+v, %v", verifiedHTTP, err)
	}
	verifiedTCP, err := app.VerifyServerService(project.ID, server.ID, target.ID)
	if err != nil || verifiedTCP.Source != "verified" {
		t.Fatalf("TCP service was not verified: %+v, %v", verifiedTCP, err)
	}
	result, err := app.RunServerServiceHealthcheck(project.ID, server.ID, source.ID)
	if err != nil || !result.Healthy || result.StatusCode != http.StatusNoContent || result.SequenceID == "" {
		t.Fatalf("unexpected healthcheck: %+v, %v", result, err)
	}
	connection, err := app.RegisterServerConnection(ServerConnectionInput{
		ProjectID: project.ID, ServerID: server.ID, SourceServiceID: source.ID,
		TargetServiceID: target.ID, Protocol: "tcp", Port: &tcpPort,
	})
	if err != nil {
		t.Fatal(err)
	}
	verifiedConnection, err := app.VerifyServerConnection(project.ID, server.ID, connection.ID)
	if err != nil || verifiedConnection.Source != "verified" || verifiedConnection.Status != "healthy" {
		t.Fatalf("connection was not verified: %+v, %v", verifiedConnection, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !containsString(events, "server.topology.healthcheck.pulse") ||
		!containsString(events, "server.topology.healthcheck.result") {
		t.Fatalf("missing topology health events: %v", events)
	}
}

func TestFailedTopologyCheckNeverClaimsVerification(t *testing.T) {
	app, project, server := topologyTestServer(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	service, err := app.RegisterServerService(ServerServiceInput{
		ProjectID: project.ID, ServerID: server.ID, Name: "Closed", Kind: "backend",
		Host: "127.0.0.1", Port: &port, Protocol: "tcp",
	})
	if err != nil {
		t.Fatal(err)
	}
	checked, err := app.VerifyServerService(project.ID, server.ID, service.ID)
	if err == nil || checked.Source != "declared" || checked.Status != "failed" {
		t.Fatalf("failed check became verified: %+v, %v", checked, err)
	}
}

func TestTopologyStoppedServerCannotBeVerified(t *testing.T) {
	app, project, server := topologyTestServer(t)
	if _, err := app.StopMockServer(server.ID); err != nil {
		t.Fatal(err)
	}
	port := 80
	service, err := app.RegisterServerService(ServerServiceInput{
		ProjectID: project.ID, ServerID: server.ID, Name: "API", Kind: "backend",
		Host: "127.0.0.1", Port: &port, Protocol: "tcp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.VerifyServerService(project.ID, server.ID, service.ID); err == nil {
		t.Fatal("expected stopped server verification to be rejected")
	}
}

type topologyExecProvider struct {
	ServerProvider
	commands []string
}

func (provider *topologyExecProvider) Create(context.Context, Server) (string, error) {
	return "managed-wsl-test", nil
}
func (*topologyExecProvider) Start(context.Context, Server) error { return nil }
func (*topologyExecProvider) Stop(context.Context, Server) error  { return nil }
func (provider *topologyExecProvider) Exec(_ context.Context, _ Server, command string) (ServerExecResult, error) {
	provider.commands = append(provider.commands, command)
	return ServerExecResult{ExitCode: 0}, nil
}
func (*topologyExecProvider) CheckHealth(context.Context, Server) (ServerHealth, error) {
	return ServerHealth{Healthy: true}, nil
}

func TestWSLTopologyChecksRunInsideSelectedProvider(t *testing.T) {
	app, project := newAppServerTestApp(t)
	projectApp, err := app.CreateApp(testAppInput(project))
	if err != nil {
		t.Fatal(err)
	}
	server, err := app.CreateServerDraft(ServerInput{
		ProjectID: project.ID, AppID: projectApp.ID, Name: "WSL", Provider: "wsl",
		Distro: "Debian 12", CPULimit: 1, MemoryMB: 512, DiskGB: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	provider := &topologyExecProvider{ServerProvider: IncusServerProvider{}}
	manager := newServerManager(app.database, nil, map[string]ServerProvider{"wsl": provider})
	app.servers = manager
	if server, err = manager.StartServer(context.Background(), server.ID); err != nil {
		t.Fatal(err)
	}
	port := 9
	service, err := app.RegisterServerService(ServerServiceInput{
		ProjectID: project.ID, ServerID: server.ID, Name: "Internal", Kind: "backend",
		Host: "127.0.0.1", Port: &port, Protocol: "tcp",
	})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := app.VerifyServerService(project.ID, server.ID, service.ID)
	if err != nil || verified.Source != "verified" {
		t.Fatalf("internal WSL check failed: %+v, %v", verified, err)
	}
	if len(provider.commands) != 1 || !strings.Contains(provider.commands[0], "/dev/tcp/") {
		t.Fatalf("verification did not execute inside WSL provider: %v", provider.commands)
	}
}
