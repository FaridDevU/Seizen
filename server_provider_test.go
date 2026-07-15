package main

import (
	"context"
	"testing"
)

func TestMockServerProvider(t *testing.T) {
	ctx := context.Background()
	provider := NewMockServerProvider()
	server := Server{ID: "server-1", Provider: "mock", MemoryMB: 1024}
	reference, err := provider.Create(ctx, server)
	if err != nil {
		t.Fatal(err)
	}
	server.RuntimeReference = reference
	if err = provider.Start(ctx, server); err != nil {
		t.Fatal(err)
	}
	health, err := provider.CheckHealth(ctx, server)
	if err != nil || !health.Healthy {
		t.Fatalf("expected healthy mock, got %+v, %v", health, err)
	}
	result, err := provider.Exec(ctx, server, "echo ok")
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("expected successful exec, got %+v, %v", result, err)
	}
	stats, err := provider.Stats(ctx, server)
	if err != nil || stats.MemoryLimitMB != 1024 {
		t.Fatalf("unexpected stats: %+v, %v", stats, err)
	}
	if err = provider.Stop(ctx, server); err != nil {
		t.Fatal(err)
	}
	health, err = provider.CheckHealth(ctx, server)
	if err != nil || health.Healthy {
		t.Fatalf("expected stopped mock, got %+v, %v", health, err)
	}
	if err = provider.Destroy(ctx, server); err != nil {
		t.Fatal(err)
	}
}
