package main

import (
	"context"
	"path/filepath"
	"testing"
)

func newAppServerTestApp(t *testing.T) (*App, Project) {
	t.Helper()
	base := t.TempDir()
	database := newDatabase(filepath.Join(base, "seizen.db"), filepath.Join(base, "projects"))
	t.Cleanup(database.Close)
	ctx := context.Background()
	db, err := database.Pool(ctx)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "project")
	if _, err = ensureProjectRoot(path); err != nil {
		t.Fatal(err)
	}
	project, err := upsertProject(ctx, db, FSProjectInfo{Name: "Project", Path: path}, ProjectCreated)
	if err != nil {
		t.Fatal(err)
	}
	return &App{database: database}, project
}

func testAppInput(project Project) AppInput {
	return AppInput{
		ProjectID:        project.ID,
		Name:             "Web",
		Kind:             "web",
		WorkingDirectory: project.Path,
		StartCommand:     "npm run dev",
		ArgumentsJSON:    "[]",
		PreviewURL:       "http://localhost:3000",
		HealthcheckURL:   "http://127.0.0.1:3000/health",
	}
}

func TestAppAndServerStoreLifecycle(t *testing.T) {
	app, project := newAppServerTestApp(t)
	created, err := app.CreateApp(testAppInput(project))
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != "stopped" || created.ProjectID != project.ID {
		t.Fatalf("unexpected App: %+v", created)
	}
	apps, err := app.ListApps(project.ID)
	if err != nil || len(apps) != 1 {
		t.Fatalf("expected one App, got %d, %v", len(apps), err)
	}

	server, err := app.CreateServerDraft(ServerInput{
		ProjectID: project.ID,
		AppID:     created.ID,
		Name:      "Tests",
		Provider:  "mock",
		Distro:    "Debian 12",
		CPULimit:  2,
		MemoryMB:  2048,
		DiskGB:    20,
	})
	if err != nil || server.Status != "draft" {
		t.Fatalf("expected a draft, got %+v, %v", server, err)
	}
	server, err = app.StartMockServer(server.ID)
	if err != nil || server.Status != "running" || server.RuntimeReference == "" {
		t.Fatalf("expected running mock, got %+v, %v", server, err)
	}
	server, err = app.StopMockServer(server.ID)
	if err != nil || server.Status != "stopped" {
		t.Fatalf("expected stopped mock, got %+v, %v", server, err)
	}
	if err = app.DeleteServer(server.ID); err != nil {
		t.Fatal(err)
	}
	servers, err := app.ListServers(project.ID)
	if err != nil || len(servers) != 0 {
		t.Fatalf("expected no servers, got %d, %v", len(servers), err)
	}
}

func TestDatabaseRejectsOrphanAndCrossProjectServers(t *testing.T) {
	app, project := newAppServerTestApp(t)
	created, err := app.CreateApp(testAppInput(project))
	if err != nil {
		t.Fatal(err)
	}
	db, err := app.database.Pool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO servers (id, project_id, app_id, name, provider, distro, status,
cpu_limit, memory_mb, disk_gb, created_at, updated_at) VALUES (?, ?, ?, 'x', 'mock',
'Debian 12', 'draft', 1, 512, 5, ` + projectNow + `, ` + projectNow + `)`
	if _, err = db.Exec(insert, "orphan", project.ID, "missing"); err == nil {
		t.Fatal("expected an orphan server to be rejected")
	}

	secondPath := filepath.Join(t.TempDir(), "project-two")
	if _, err = ensureProjectRoot(secondPath); err != nil {
		t.Fatal(err)
	}
	second, err := upsertProject(context.Background(), db, FSProjectInfo{Name: "Two", Path: secondPath}, ProjectCreated)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(insert, "cross-project", second.ID, created.ID); err == nil {
		t.Fatal("expected a cross-project App link to be rejected")
	}
}

func TestAppInputRejectsUnsafeURLsAndExternalDirectory(t *testing.T) {
	app, project := newAppServerTestApp(t)
	for _, rawURL := range []string{
		"http://wails.localhost",
		"https://user:password@example.com",
		"javascript:alert(1)",
	} {
		input := testAppInput(project)
		input.PreviewURL = rawURL
		if _, err := app.CreateApp(input); err == nil {
			t.Fatalf("expected %q to be rejected", rawURL)
		}
	}
	input := testAppInput(project)
	input.WorkingDirectory = t.TempDir()
	if _, err := app.CreateApp(input); err == nil {
		t.Fatal("expected a directory outside the project to be rejected")
	}
}

func TestServerRequiresAnAppFromItsProjectAndKeepAliveStaysDisabled(t *testing.T) {
	app, project := newAppServerTestApp(t)
	input := ServerInput{ProjectID: project.ID, AppID: "missing", Name: "x", Provider: "mock", CPULimit: 1, MemoryMB: 512, DiskGB: 5}
	if _, err := app.CreateServerDraft(input); err == nil {
		t.Fatal("expected a missing App to be rejected")
	}
	created, err := app.CreateApp(testAppInput(project))
	if err != nil {
		t.Fatal(err)
	}
	input.AppID = created.ID
	input.KeepAlive = true
	if _, err = app.CreateServerDraft(input); err == nil {
		t.Fatal("expected keep_alive to remain disabled")
	}
	input.KeepAlive, input.Provider, input.Distro = false, "wsl", "Ubuntu"
	if _, err = app.CreateServerDraft(input); err == nil {
		t.Fatal("expected WSL to reject a non-Debian 12 distro")
	}
}

func TestDeleteAppRejectsLinkedServer(t *testing.T) {
	app, project := newAppServerTestApp(t)
	projectApp, err := app.CreateApp(testAppInput(project))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.CreateServerDraft(ServerInput{
		ProjectID: project.ID, AppID: projectApp.ID, Name: "Tests", Provider: "mock",
		CPULimit: 1, MemoryMB: 512, DiskGB: 5,
	}); err != nil {
		t.Fatal(err)
	}
	if err = app.DeleteApp(projectApp.ID); err == nil {
		t.Fatal("expected an App with a linked server to be preserved")
	}
	if apps, listErr := app.ListApps(project.ID); listErr != nil || len(apps) != 1 {
		t.Fatalf("linked App was deleted: %+v, %v", apps, listErr)
	}
}
