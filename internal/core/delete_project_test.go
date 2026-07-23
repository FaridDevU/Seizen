package core

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteProject(t *testing.T) {
	t.Run("removes managed folder and row", func(t *testing.T) {
		app, db, root := deletionTestApp(t)
		project := deletionTestProject(t, db, filepath.Join(root, "Managed"), ProjectCreated)
		remaining := deletionTestProject(t, db, filepath.Join(root, "Remaining"), ProjectCreated)
		if err := app.GroupDuplicate(DuplicateGroup{
			Title: "Versions",
			Variants: []DuplicateVariant{
				{ProjectID: project.ID, Label: "main"},
				{ProjectID: remaining.ID, Label: "test"},
			},
		}); err != nil {
			t.Fatal(err)
		}

		if err := app.DeleteProject(project.ID, project.Path); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(project.Path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected project folder to be removed, got %v", err)
		}
		assertProjectCount(t, db, project.ID, 0)
		var groupID, groupTitle, variantLabel sql.NullString
		if err := db.QueryRowContext(context.Background(), `SELECT group_id, group_title, variant_label FROM projects WHERE id = ?`, remaining.ID).
			Scan(&groupID, &groupTitle, &variantLabel); err != nil {
			t.Fatal(err)
		}
		if groupID.Valid || groupTitle.Valid || variantLabel.Valid {
			t.Fatal("expected the remaining one-project group to be cleared")
		}
	})

	t.Run("unlinks a project outside the vault without deleting its folder", func(t *testing.T) {
		app, db, root := deletionTestApp(t)
		project := deletionTestProject(t, db, filepath.Join(filepath.Dir(root), "Outside"), ProjectCreated)

		// A folder outside the vault is only unlinked from the library; its files are
		// never touched (the app never owns them).
		if err := app.DeleteProject(project.ID, project.Path); err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(project.Path); err != nil || !info.IsDir() {
			t.Fatalf("expected outside folder to remain: %v", err)
		}
		assertProjectCount(t, db, project.ID, 0)
	})

	t.Run("refuses path mismatch", func(t *testing.T) {
		app, db, root := deletionTestApp(t)
		project := deletionTestProject(t, db, filepath.Join(root, "Managed"), ProjectCreated)

		if err := app.DeleteProject(project.ID, filepath.Join(root, "Other")); err == nil {
			t.Fatal("expected a mismatched path to be refused")
		}
		if info, err := os.Stat(project.Path); err != nil || !info.IsDir() {
			t.Fatalf("expected managed folder to remain: %v", err)
		}
		assertProjectCount(t, db, project.ID, 1)
	})

	t.Run("refuses a project with managed servers", func(t *testing.T) {
		app, db, root := deletionTestApp(t)
		project := deletionTestProject(t, db, filepath.Join(root, "Managed"), ProjectCreated)
		projectApp, err := app.CreateApp(testAppInput(project))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = app.CreateServerDraft(ServerInput{
			ProjectID: project.ID, AppID: projectApp.ID, Name: "Debian", Provider: "mock",
			CPULimit: 1, MemoryMB: 512, DiskGB: 5,
		}); err != nil {
			t.Fatal(err)
		}
		if err = app.DeleteProject(project.ID, project.Path); err == nil {
			t.Fatal("expected a project with a server to be preserved")
		}
		if info, statErr := os.Stat(project.Path); statErr != nil || !info.IsDir() {
			t.Fatalf("expected managed folder to remain: %v", statErr)
		}
		assertProjectCount(t, db, project.ID, 1)
	})
}

func TestRemoveProjectFromLibrary(t *testing.T) {
	t.Run("removes the row and keeps the folder for imported projects", func(t *testing.T) {
		app, db, root := deletionTestApp(t)
		project := deletionTestProject(t, db, filepath.Join(filepath.Dir(root), "Imported"), ProjectImported)

		if err := app.RemoveProjectFromLibrary(project.ID, project.Path); err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(project.Path); err != nil || !info.IsDir() {
			t.Fatalf("expected imported folder to remain: %v", err)
		}
		assertProjectCount(t, db, project.ID, 0)
	})

	t.Run("DeleteProject unlinks an external imported project but keeps its folder", func(t *testing.T) {
		app, db, root := deletionTestApp(t)
		project := deletionTestProject(t, db, filepath.Join(filepath.Dir(root), "Imported"), ProjectImported)

		if err := app.DeleteProject(project.ID, project.Path); err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(project.Path); err != nil || !info.IsDir() {
			t.Fatalf("expected the external folder to remain: %v", err)
		}
		assertProjectCount(t, db, project.ID, 0)
	})

	t.Run("DeleteProject removes an imported copy that lives in the vault", func(t *testing.T) {
		app, db, root := deletionTestApp(t)
		project := deletionTestProject(t, db, filepath.Join(root, "VaultImport"), ProjectImported)

		// Vault copies are always safe to delete — the original was left untouched at import.
		if err := app.DeleteProject(project.ID, project.Path); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(project.Path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected the vault copy to be removed, got %v", err)
		}
		assertProjectCount(t, db, project.ID, 0)
	})
}

func TestUngroupDuplicate(t *testing.T) {
	app, db, root := deletionTestApp(t)
	first := deletionTestProject(t, db, filepath.Join(root, "App"), ProjectCreated)
	second := deletionTestProject(t, db, filepath.Join(root, "App v2"), ProjectCreated)
	if err := app.GroupDuplicate(DuplicateGroup{
		Title: "App",
		Variants: []DuplicateVariant{
			{ProjectID: first.ID, Label: "main"},
			{ProjectID: second.ID, Label: "v2"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	var groupID string
	if err := db.QueryRowContext(context.Background(), `SELECT group_id FROM projects WHERE id = ?`, first.ID).Scan(&groupID); err != nil {
		t.Fatal(err)
	}

	if err := app.UngroupDuplicate(groupID); err != nil {
		t.Fatal(err)
	}
	var grouped int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM projects
WHERE group_id IS NOT NULL OR group_title IS NOT NULL OR variant_label IS NOT NULL`).Scan(&grouped); err != nil {
		t.Fatal(err)
	}
	if grouped != 0 {
		t.Fatalf("expected all group fields cleared, got %d rows", grouped)
	}
	if err := app.UngroupDuplicate(groupID); err == nil {
		t.Fatal("expected a missing group to be rejected")
	}
}

func deletionTestApp(t *testing.T) (*App, *sql.DB, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "projects")
	database := newDatabase(filepath.Join(base, "config", "seizen.db"), root)
	if err := database.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	db, err := database.Pool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	app := &App{database: database}
	root, err = app.vaultRoot()
	if err != nil {
		t.Fatal(err)
	}
	return app, db, root
}

func deletionTestProject(t *testing.T, db *sql.DB, path string, source ProjectSource) Project {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	project, err := upsertProject(context.Background(), db, FSProjectInfo{
		Name: filepath.Base(path),
		Path: path,
	}, source)
	if err != nil {
		t.Fatal(err)
	}
	return project
}

func assertProjectCount(t *testing.T, db *sql.DB, id string, want int) {
	t.Helper()
	var count int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM projects WHERE id = ?`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("expected %d project rows, got %d", want, count)
	}
}
