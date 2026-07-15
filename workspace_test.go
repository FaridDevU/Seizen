package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectWorkspacePersistsAndReplaces(t *testing.T) {
	app, db, root := deletionTestApp(t)
	project := deletionTestProject(t, db, filepath.Join(root, "Workspace"), ProjectCreated)

	if layout, err := app.GetProjectWorkspace(project.ID, project.Path); err != nil || layout != "" {
		t.Fatalf("expected an empty workspace, got %q, %v", layout, err)
	}
	first := `{"version":1,"nodes":[{"id":"one","x":10}]}`
	if err := app.SaveProjectWorkspace(project.ID, project.Path, first); err != nil {
		t.Fatal(err)
	}
	if layout, err := app.GetProjectWorkspace(project.ID, project.Path); err != nil || layout != first {
		t.Fatalf("expected first workspace %q, got %q, %v", first, layout, err)
	}

	second := `{"version":1,"nodes":[],"viewport":{"x":4,"y":8,"zoom":1.25}}`
	if err := app.SaveProjectWorkspace(project.ID, project.Path, second); err != nil {
		t.Fatal(err)
	}
	if layout, err := app.GetProjectWorkspace(project.ID, project.Path); err != nil || layout != second {
		t.Fatalf("expected replacement workspace %q, got %q, %v", second, layout, err)
	}
}

func TestProjectWorkspaceRejectsMismatchInvalidJSONAndOversize(t *testing.T) {
	app, db, root := deletionTestApp(t)
	project := deletionTestProject(t, db, filepath.Join(root, "Workspace"), ProjectCreated)
	wrongPath := filepath.Join(root, "Other")

	if err := app.SaveProjectWorkspace(project.ID, wrongPath, `{}`); err == nil {
		t.Fatal("expected a mismatched save path to be rejected")
	}
	if _, err := app.GetProjectWorkspace(project.ID, wrongPath); err == nil {
		t.Fatal("expected a mismatched load path to be rejected")
	}
	if err := app.SaveProjectWorkspace(project.ID, project.Path, `{`); err == nil {
		t.Fatal("expected invalid JSON to be rejected")
	}
	oversize := `"` + strings.Repeat("a", maxWorkspaceLayoutSize) + `"`
	if err := app.SaveProjectWorkspace(project.ID, project.Path, oversize); err == nil {
		t.Fatal("expected an oversized workspace to be rejected")
	}
}

func TestProjectWorkspaceCascadesWhenProjectIsDeleted(t *testing.T) {
	app, db, root := deletionTestApp(t)
	project := deletionTestProject(t, db, filepath.Join(root, "Workspace"), ProjectCreated)
	if err := app.SaveProjectWorkspace(project.ID, project.Path, `{"version":1}`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), `DELETE FROM projects WHERE id = ?`, project.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM workspace_layouts WHERE project_id = ?`, project.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected workspace cascade deletion, got %d rows", count)
	}
}
