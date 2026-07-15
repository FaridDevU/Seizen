package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func createServerManagerFixture(t *testing.T, provider string) (*App, Project, Server) {
	t.Helper()
	app, project := newAppServerTestApp(t)
	projectApp, err := app.CreateApp(testAppInput(project))
	if err != nil {
		t.Fatal(err)
	}
	server, err := app.CreateServerDraft(ServerInput{
		ProjectID: project.ID,
		AppID:     projectApp.ID,
		Name:      "Test Debian",
		Provider:  provider,
		Distro:    "Debian 12",
		CPULimit:  2,
		MemoryMB:  1024,
		DiskGB:    10,
	})
	if err != nil {
		t.Fatal(err)
	}
	return app, project, server
}

func TestServerManagerLifecycleAndEvents(t *testing.T) {
	app, _, server := createServerManagerFixture(t, "mock")
	provider := NewMockServerProvider()
	var mu sync.Mutex
	var events []string
	manager := newServerManager(app.database, func(name string, _ any) {
		mu.Lock()
		events = append(events, name)
		mu.Unlock()
	}, map[string]ServerProvider{"mock": provider})

	running, err := manager.StartServer(context.Background(), server.ID)
	if err != nil || running.Status != "running" || running.RuntimeReference == "" {
		t.Fatalf("expected a running server, got %+v, %v", running, err)
	}
	health, err := manager.CheckHealth(context.Background(), server.ID)
	if err != nil || !health.Healthy {
		t.Fatalf("expected a healthy server, got %+v, %v", health, err)
	}
	stats, err := manager.Stats(context.Background(), server.ID)
	if err != nil || stats.MemoryLimitMB != 1024 || stats.LimitsEnforced {
		t.Fatalf("unexpected stats: %+v, %v", stats, err)
	}
	stopped, err := manager.StopServer(context.Background(), server.ID)
	if err != nil || stopped.Status != "stopped" {
		t.Fatalf("expected a stopped server, got %+v, %v", stopped, err)
	}
	if _, err = manager.RestartServer(context.Background(), server.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = manager.StopServer(context.Background(), server.ID); err != nil {
		t.Fatal(err)
	}
	if err = manager.DestroyServer(context.Background(), server.ID); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, expected := range []string{"server.provisioning", "server.starting", "server.running", "server.stopping", "server.stopped", "server.deleted"} {
		found := false
		for _, event := range events {
			found = found || event == expected
		}
		if !found {
			t.Fatalf("missing event %s in %v", expected, events)
		}
	}
}

func TestServerManagerCleanupStopsProjectServers(t *testing.T) {
	app, project, server := createServerManagerFixture(t, "mock")
	manager := newServerManager(app.database, nil, map[string]ServerProvider{"mock": NewMockServerProvider()})
	if _, err := manager.StartServer(context.Background(), server.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.CleanupProjectServers(context.Background(), project.ID); err != nil {
		t.Fatal(err)
	}
	db, err := app.database.Pool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	server, err = getServer(context.Background(), db, server.ID)
	if err != nil || server.Status != "stopped" {
		t.Fatalf("cleanup left server active: %+v, %v", server, err)
	}
}

type cancellableServerProvider struct {
	started chan struct{}
}

func (provider *cancellableServerProvider) Create(ctx context.Context, _ Server) (string, error) {
	close(provider.started)
	<-ctx.Done()
	return "", ctx.Err()
}
func (*cancellableServerProvider) Start(context.Context, Server) error   { return nil }
func (*cancellableServerProvider) Stop(context.Context, Server) error    { return nil }
func (*cancellableServerProvider) Restart(context.Context, Server) error { return nil }
func (*cancellableServerProvider) Destroy(context.Context, Server) error { return nil }
func (*cancellableServerProvider) Exec(context.Context, Server, string) (ServerExecResult, error) {
	return ServerExecResult{}, nil
}
func (*cancellableServerProvider) Stats(context.Context, Server) (ServerStats, error) {
	return ServerStats{}, nil
}
func (*cancellableServerProvider) InspectServices(context.Context, Server) ([]ServerService, error) {
	return nil, nil
}
func (*cancellableServerProvider) CheckHealth(context.Context, Server) (ServerHealth, error) {
	return ServerHealth{}, nil
}

func TestServerManagerStopCancelsProvisioning(t *testing.T) {
	app, _, server := createServerManagerFixture(t, "mock")
	provider := &cancellableServerProvider{started: make(chan struct{})}
	manager := newServerManager(app.database, nil, map[string]ServerProvider{"mock": provider})
	startDone := make(chan error, 1)
	go func() {
		_, err := manager.StartServer(context.Background(), server.ID)
		startDone <- err
	}()
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("provisioning did not start")
	}
	stopped, err := manager.StopServer(context.Background(), server.ID)
	if err != nil || stopped.Status != "stopped" {
		t.Fatalf("expected cancellation to stop the server, got %+v, %v", stopped, err)
	}
	select {
	case err = <-startDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected canceled start, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled provisioning did not return")
	}
}

func TestIncusProviderIsExplicitlyUnavailable(t *testing.T) {
	_, err := (IncusServerProvider{}).Create(context.Background(), Server{})
	if !errors.Is(err, ErrServerProviderUnavailable) {
		t.Fatalf("unexpected Incus error: %v", err)
	}
}

type wakeDetectingProvider struct {
	*MockServerProvider
	statsCalls  int
	healthCalls int
}

func (provider *wakeDetectingProvider) Stats(ctx context.Context, server Server) (ServerStats, error) {
	provider.statsCalls++
	return provider.MockServerProvider.Stats(ctx, server)
}

func (provider *wakeDetectingProvider) CheckHealth(ctx context.Context, server Server) (ServerHealth, error) {
	provider.healthCalls++
	return provider.MockServerProvider.CheckHealth(ctx, server)
}

func TestStoppedServerInspectionNeverCallsProvider(t *testing.T) {
	app, _, server := createServerManagerFixture(t, "mock")
	provider := &wakeDetectingProvider{MockServerProvider: NewMockServerProvider()}
	manager := newServerManager(app.database, nil, map[string]ServerProvider{"mock": provider})
	if _, err := manager.StartServer(context.Background(), server.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.StopServer(context.Background(), server.ID); err != nil {
		t.Fatal(err)
	}
	provider.statsCalls, provider.healthCalls = 0, 0
	if _, err := manager.Stats(context.Background(), server.ID); err == nil {
		t.Fatal("expected stopped stats to be rejected")
	}
	if health, err := manager.CheckHealth(context.Background(), server.ID); err != nil || health.Healthy {
		t.Fatalf("unexpected stopped health: %+v, %v", health, err)
	}
	if provider.statsCalls != 0 || provider.healthCalls != 0 {
		t.Fatalf("stopped inspection woke the provider: stats=%d health=%d", provider.statsCalls, provider.healthCalls)
	}
}

func TestHealthTransitionCannotResurrectStoppedServer(t *testing.T) {
	app, _, server := createServerManagerFixture(t, "mock")
	manager := newServerManager(app.database, nil, map[string]ServerProvider{"mock": NewMockServerProvider()})
	db, err := app.database.Pool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	stopped, changed, err := manager.transitionFrom(context.Background(), db, server.ID, "running", "degraded", "")
	if err != nil || changed || stopped.Status != "draft" {
		t.Fatalf("stale healthcheck changed the server: %+v changed=%v err=%v", stopped, changed, err)
	}
}

type blockingHealthProvider struct {
	*MockServerProvider
	started chan struct{}
}

func (provider *blockingHealthProvider) CheckHealth(ctx context.Context, _ Server) (ServerHealth, error) {
	close(provider.started)
	<-ctx.Done()
	return ServerHealth{}, ctx.Err()
}

func TestStopCancelsConcurrentHealthcheckBeforeStoppingProvider(t *testing.T) {
	app, _, server := createServerManagerFixture(t, "mock")
	provider := &blockingHealthProvider{MockServerProvider: NewMockServerProvider(), started: make(chan struct{})}
	manager := newServerManager(app.database, nil, map[string]ServerProvider{"mock": provider})
	if _, err := manager.StartServer(context.Background(), server.ID); err != nil {
		t.Fatal(err)
	}
	healthDone := make(chan error, 1)
	go func() {
		_, err := manager.CheckHealth(context.Background(), server.ID)
		healthDone <- err
	}()
	<-provider.started
	stopped, err := manager.StopServer(context.Background(), server.ID)
	if err != nil || stopped.Status != "stopped" {
		t.Fatalf("stop raced with healthcheck: %+v, %v", stopped, err)
	}
	if err = <-healthDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("healthcheck was not canceled: %v", err)
	}
}

type failedCreateProvider struct{ ServerProvider }

func (*failedCreateProvider) Create(context.Context, Server) (string, error) {
	return "managed-recovery-reference", errors.New("partial create")
}

func TestFailedProvisioningPersistsProviderRecoveryReference(t *testing.T) {
	app, _, server := createServerManagerFixture(t, "mock")
	provider := &failedCreateProvider{ServerProvider: IncusServerProvider{}}
	manager := newServerManager(app.database, nil, map[string]ServerProvider{"mock": provider})
	failed, err := manager.StartServer(context.Background(), server.ID)
	if err == nil || failed.Status != "failed" || failed.RuntimeReference != "managed-recovery-reference" {
		t.Fatalf("provider recovery reference was lost: %+v, %v", failed, err)
	}
}
