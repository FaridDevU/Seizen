package core

import (
	"context"
	"path/filepath"
	"testing"
)

func TestAppearanceDefaultsAndPersists(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	databasePath := filepath.Join(base, "config", "seizen.db")
	database := newDatabase(databasePath, filepath.Join(base, "projects"))

	appearance, err := database.Appearance(ctx)
	if err != nil || appearance != defaultAppearance {
		t.Fatalf("expected default appearance %#v, got %#v, %v", defaultAppearance, appearance, err)
	}
	appearance, err = database.SetAppearance(ctx, "dark", "violet")
	if err != nil || appearance != (Appearance{Mode: "dark", Accent: "violet"}) {
		t.Fatalf("expected saved appearance, got %#v, %v", appearance, err)
	}
	database.Close()

	reopened := newDatabase(databasePath, filepath.Join(base, "unused-projects"))
	defer reopened.Close()
	appearance, err = reopened.Appearance(ctx)
	if err != nil || appearance != (Appearance{Mode: "dark", Accent: "violet"}) {
		t.Fatalf("expected persisted appearance, got %#v, %v", appearance, err)
	}
}

func TestInvalidAppearanceDoesNotOverwrite(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	database := newDatabase(filepath.Join(base, "seizen.db"), filepath.Join(base, "projects"))
	defer database.Close()

	if _, err := database.SetAppearance(ctx, "dark", "emerald"); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []Appearance{
		{Mode: "auto", Accent: "blue"},
		{Mode: "light", Accent: "pink"},
	} {
		if _, err := database.SetAppearance(ctx, invalid.Mode, invalid.Accent); err == nil {
			t.Fatalf("expected %#v to be rejected", invalid)
		}
	}
	appearance, err := database.Appearance(ctx)
	if err != nil || appearance != (Appearance{Mode: "dark", Accent: "emerald"}) {
		t.Fatalf("expected invalid values not to overwrite settings, got %#v, %v", appearance, err)
	}
}
