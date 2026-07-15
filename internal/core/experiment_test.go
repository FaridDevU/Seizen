package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestExperimentPersistenceAndContext(t *testing.T) {
	app, project := newAppServerTestApp(t)
	projectApp, err := app.CreateApp(testAppInput(project))
	if err != nil {
		t.Fatal(err)
	}
	db, err := app.database.Pool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = createExperimentRecord(context.Background(), db, experimentRecordInput{
		ProjectID: project.ID, Kind: "app", AppID: "missing", Name: "Invalid",
	}); err == nil {
		t.Fatal("expected an App experiment without an App to be rejected")
	}
	if _, err = createExperimentRecord(context.Background(), db, experimentRecordInput{
		ProjectID: project.ID, Kind: "server", AppID: projectApp.ID, Name: "Invalid",
	}); err == nil {
		t.Fatal("expected a server experiment without a base server to be rejected")
	}
	t.Setenv("LOCALAPPDATA", t.TempDir())
	managedRoot, err := managedExperimentRoot()
	if err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(managedRoot, project.ID, "exp-context")
	if _, err = ensureProjectRoot(worktree); err != nil {
		t.Fatal(err)
	}
	if _, err = runExperimentGit(context.Background(), worktree, os.Environ(), "init"); err != nil {
		t.Fatal(err)
	}
	experiment, err := createExperimentRecord(context.Background(), db, experimentRecordInput{
		ProjectID: project.ID, Kind: "app", AppID: projectApp.ID, Name: "Cart",
		Objective: "Test another checkout", BaseBranch: "main", BranchName: "experiment/carrito-a1b2c3d4",
		BaseCommit: "abc123", WorktreePath: worktree, Status: "active", CreatedBy: "user",
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err := app.ListExperiments(project.ID, "app")
	if err != nil || len(items) != 1 || items[0].ID != experiment.ID {
		t.Fatalf("unexpected experiment list: %#v, %v", items, err)
	}
	mainContext, err := app.GetProjectContext(project.ID)
	if err != nil || mainContext.ExperimentID != "" || mainContext.Path != project.Path {
		t.Fatalf("unexpected main context: %#v, %v", mainContext, err)
	}
	selected, err := app.SelectProjectExperiment(project.ID, experiment.ID)
	if err != nil || selected.ExperimentID != experiment.ID || selected.Path != worktree || selected.BranchName != experiment.BranchName {
		t.Fatalf("unexpected experiment context: %#v, %v", selected, err)
	}
	stored, err := app.GetProjectContext(project.ID)
	if err != nil || stored != selected {
		t.Fatalf("context did not persist: %#v, %v", stored, err)
	}
	principal, err := app.SelectProjectExperiment(project.ID, "")
	if err != nil || principal.ExperimentID != "" || principal.Name != "Principal" {
		t.Fatalf("could not restore main context: %#v, %v", principal, err)
	}
}

func TestServerExperimentRequiresMatchingBaseAndScopesServers(t *testing.T) {
	app, project := newAppServerTestApp(t)
	projectApp, err := app.CreateApp(testAppInput(project))
	if err != nil {
		t.Fatal(err)
	}
	base, err := app.CreateServerDraft(ServerInput{
		ProjectID: project.ID, AppID: projectApp.ID, Name: "Principal", Provider: "mock",
		CPULimit: 1, MemoryMB: 512, DiskGB: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	db, _ := app.database.Pool(context.Background())
	experiment, err := createExperimentRecord(context.Background(), db, experimentRecordInput{
		ProjectID: project.ID, Kind: "server", AppID: projectApp.ID, BaseServerID: base.ID,
		Name: "Redis", Status: "active", CreatedBy: "user",
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := app.CreateServerDraft(ServerInput{
		ProjectID: project.ID, AppID: projectApp.ID, ExperimentID: experiment.ID, BaseServerID: base.ID,
		Name: "Redis", Provider: "mock", CPULimit: 1, MemoryMB: 512, DiskGB: 5,
	})
	if err != nil || server.ExperimentID == nil || *server.ExperimentID != experiment.ID || server.BaseServerID == nil || *server.BaseServerID != base.ID {
		t.Fatalf("unexpected experimental server: %#v, %v", server, err)
	}
	if _, err = db.Exec(`INSERT INTO app_runs (id, project_id, app_id, experiment_id, target, runtime_provider, status, started_at)
VALUES ('wrong-scope', ?, ?, ?, 'development', 'local', 'stopped', `+projectNow+`)`,
		project.ID, projectApp.ID, experiment.ID); err != nil {
		t.Fatalf("matching App run scope was rejected: %v", err)
	}
}
