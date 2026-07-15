package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditorIntegrationsDefaultAndPersist(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	path := filepath.Join(base, "config", "seizen.db")
	database := newDatabase(path, filepath.Join(base, "projects"))

	integrations, err := database.EditorIntegrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"vscode": true, "cursor": false, "antigravity": false, "zed": false}
	if len(integrations) != len(want) {
		t.Fatalf("expected %d editor integrations, got %#v", len(want), integrations)
	}
	for _, integration := range integrations {
		if integration.Enabled != want[integration.ID] {
			t.Fatalf("unexpected defaults: %#v", integrations)
		}
		if integration.Embedded != (integration.ID == "vscode") {
			t.Fatalf("only VS Code should be embedded, got %#v", integrations)
		}
	}
	if err = database.SetEditorIntegrationEnabled(ctx, "cursor", true); err != nil {
		t.Fatal(err)
	}
	database.Close()

	reopened := newDatabase(path, filepath.Join(base, "unused-projects"))
	defer reopened.Close()
	integrations, err = reopened.EditorIntegrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, integration := range integrations {
		if integration.ID == "cursor" && !integration.Enabled {
			t.Fatalf("expected Cursor to stay enabled, got %#v", integrations)
		}
	}
}

func TestLegacyEditorAPIRejectsExternalWindows(t *testing.T) {
	base := t.TempDir()
	app := &App{
		database: newDatabase(filepath.Join(base, "seizen.db"), filepath.Join(base, "projects")),
		vscode:   &managedVSCodeInstaller{root: filepath.Join(base, "managed-vscode")},
	}
	defer app.database.Close()

	if err := app.OpenProjectInEditor(base, "unknown"); err == nil || !strings.Contains(err.Error(), "is not valid") {
		t.Fatalf("expected an unknown editor to be rejected, got %v", err)
	}
	// External editors do open, but they require being enabled in Resources.
	if err := app.OpenProjectInEditor(base, "cursor"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected a disabled external editor to be rejected, got %v", err)
	}
	if err := app.OpenProjectInEditor(base, "vscode"); err == nil || !strings.Contains(err.Error(), "workspace") {
		t.Fatalf("expected an external VS Code window to be rejected, got %v", err)
	}
	t.Setenv("PATH", t.TempDir())
	// Without this, a Zed/Cursor installed on the dev machine sneaks in
	// through the LOCALAPPDATA fallback and the test stops being deterministic.
	t.Setenv("LOCALAPPDATA", t.TempDir())
	integrations, err := app.GetEditorIntegrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, integration := range integrations {
		if integration.Available {
			t.Fatalf("expected an empty PATH to mark editors unavailable, got %#v", integrations)
		}
	}
}
