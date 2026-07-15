package main

import (
	"context"
	"strings"
	"testing"
)

func TestServerAPIListsLogsAndCleansActiveServer(t *testing.T) {
	app, project := newAppServerTestApp(t)
	projectApp, err := app.CreateApp(testAppInput(project))
	if err != nil {
		t.Fatal(err)
	}
	server, err := app.CreateServerDraft(ServerInput{
		ProjectID: project.ID, AppID: projectApp.ID, Name: "API Server",
		Provider: "mock", Distro: "Debian 12", CPULimit: 2, MemoryMB: 1024, DiskGB: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err = app.StartServer(server.ID)
	if err != nil || server.Status != "running" {
		t.Fatalf("start = %+v, %v", server, err)
	}
	all, err := app.ListAllServers()
	if err != nil || len(all) != 1 || all[0].ProjectName != project.Name || all[0].AppName != projectApp.Name {
		t.Fatalf("global servers = %+v, %v", all, err)
	}
	logs, err := app.GetServerLogs(server.ID)
	if err != nil || !strings.Contains(logs, "provisioning") || !strings.Contains(logs, "running") {
		t.Fatalf("logs = %q, %v", logs, err)
	}
	if err = app.CleanupProjectServers(project.ID); err != nil {
		t.Fatal(err)
	}
	db, _ := app.database.Pool(context.Background())
	stopped, err := getServer(context.Background(), db, server.ID)
	if err != nil || stopped.Status != "stopped" {
		t.Fatalf("cleanup = %+v, %v", stopped, err)
	}
}

func TestConfirmCloseStopsActiveServer(t *testing.T) {
	app, project := newAppServerTestApp(t)
	projectApp, err := app.CreateApp(testAppInput(project))
	if err != nil {
		t.Fatal(err)
	}
	server, err := app.CreateServerDraft(ServerInput{
		ProjectID: project.ID, AppID: projectApp.ID, Name: "Close Server",
		Provider: "mock", Distro: "Debian 12", CPULimit: 1, MemoryMB: 512, DiskGB: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.StartServer(server.ID); err != nil {
		t.Fatal(err)
	}
	app.quit = func(context.Context) {}
	app.startup(context.Background())
	app.ConfirmClose()
	db, _ := app.database.Pool(context.Background())
	stopped, err := getServer(context.Background(), db, server.ID)
	if err != nil || stopped.Status != "stopped" {
		t.Fatalf("close left server %+v, %v", stopped, err)
	}
}
