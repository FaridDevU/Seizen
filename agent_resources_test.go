package main

import (
	"context"
	"path/filepath"
	"testing"
)

func TestAgentResourceSettingsDefaultToDebianAndPersist(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	path := filepath.Join(base, "config", "seizen.db")
	database := newDatabase(path, filepath.Join(base, "projects"))

	settings, err := database.AgentResourceSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.CodexEnvironment != "debian" || settings.ClaudeEnvironment != "debian" ||
		settings.OpencodeEnvironment != "debian" || settings.CodexUnrestricted ||
		settings.ClaudeUnrestricted || settings.OpencodeUnrestricted || settings.SharedExtensions {
		t.Fatalf("unexpected defaults: %#v", settings)
	}
	settings = AgentResourceSettings{
		CodexEnvironment: "fedora", ClaudeEnvironment: "cmd", OpencodeEnvironment: "ubuntu",
		CodexUnrestricted: true, ClaudeUnrestricted: true, OpencodeUnrestricted: true, SharedExtensions: true,
	}
	if _, err = database.SetAgentResourceSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if _, err = database.SetAgentResourceSettings(ctx, AgentResourceSettings{CodexEnvironment: "kali", ClaudeEnvironment: "debian", OpencodeEnvironment: "debian"}); err == nil {
		t.Fatal("expected an unmanaged environment to be rejected")
	}
	if _, err = database.SetAgentResourceSettings(ctx, AgentResourceSettings{CodexEnvironment: "debian", ClaudeEnvironment: "debian", OpencodeEnvironment: "cmd"}); err == nil {
		t.Fatal("expected cmd to be rejected for opencode")
	}
	if environment, unrestricted := agentEnvironment(AgentResourceSettings{ClaudeEnvironment: "debian", ClaudeUnrestricted: true}, "claude"); environment != "cmd" || !unrestricted {
		t.Fatalf("expected unrestricted claude to be forced to cmd, got %q", environment)
	}
	database.Close()

	reopened := newDatabase(path, filepath.Join(base, "unused"))
	defer reopened.Close()
	stored, err := reopened.AgentResourceSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stored != settings {
		t.Fatalf("settings did not persist: got %#v want %#v", stored, settings)
	}
}

func TestAgentProfilesAreProjectScopedUnlessSharingIsEnabled(t *testing.T) {
	root := filepath.Join(t.TempDir(), "profiles")
	project, err := agentProfileRoot(root, &agentTerminalBridgeConfig{ProjectID: "project-one"})
	if err != nil {
		t.Fatal(err)
	}
	if project != filepath.Join(root, "projects", "project-one") {
		t.Fatalf("unexpected project profile %q", project)
	}
	shared, err := agentProfileRoot(root, &agentTerminalBridgeConfig{ProjectID: "project-one", SharedExtensions: true})
	if err != nil || shared != root {
		t.Fatalf("unexpected shared profile %q, %v", shared, err)
	}
	if _, err = agentProfileRoot(root, &agentTerminalBridgeConfig{ProjectID: `..\outside`}); err == nil {
		t.Fatal("expected an unsafe profile scope to be rejected")
	}
}
