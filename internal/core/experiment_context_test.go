package core

import (
	"context"
	"testing"
	"time"
)

func TestExperimentAndPrincipalAppRunsStayIndependent(t *testing.T) {
	app, _, projectApp, experiment, manager, _ := newLifecycleExperiment(t)
	if _, err := app.StartAppContext(projectApp.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := app.StartAppContext(projectApp.ID, experiment.ID); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		mainStatus, mainErr := app.GetAppStatusContext(projectApp.ID, "")
		experimentStatus, experimentErr := app.GetAppStatusContext(projectApp.ID, experiment.ID)
		if mainErr == nil && experimentErr == nil && mainStatus.ProcessAlive && experimentStatus.ProcessAlive {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mainStatus, mainErr := app.GetAppStatusContext(projectApp.ID, "")
	experimentStatus, experimentErr := app.GetAppStatusContext(projectApp.ID, experiment.ID)
	if mainErr != nil || experimentErr != nil || !mainStatus.ProcessAlive || !experimentStatus.ProcessAlive {
		t.Fatalf("main=%#v/%v experiment=%#v/%v", mainStatus, mainErr, experimentStatus, experimentErr)
	}
	db, _ := app.database.Pool(context.Background())
	var mainRuns, experimentRuns int
	if err := db.QueryRow(`SELECT
SUM(CASE WHEN experiment_id IS NULL THEN 1 ELSE 0 END),
SUM(CASE WHEN experiment_id = ? THEN 1 ELSE 0 END)
FROM app_runs WHERE app_id = ?`, experiment.ID, projectApp.ID).Scan(&mainRuns, &experimentRuns); err != nil {
		t.Fatal(err)
	}
	if mainRuns != 1 || experimentRuns != 1 {
		t.Fatalf("run scopes main=%d experiment=%d", mainRuns, experimentRuns)
	}
	if _, err := manager.StopAppContext(context.Background(), projectApp.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.StopAppContext(context.Background(), projectApp.ID, experiment.ID); err != nil {
		t.Fatal(err)
	}
}

func TestExperimentWorkspaceAndTerminalPathsStayContextual(t *testing.T) {
	app, project, _, experiment, _, _ := newLifecycleExperiment(t)
	mainLayout := `{"version":1,"nodes":[{"id":"main"}]}`
	experimentLayout := `{"version":1,"nodes":[{"id":"experiment"}]}`
	if err := app.SaveProjectWorkspaceContext(project.ID, "", mainLayout); err != nil {
		t.Fatal(err)
	}
	if err := app.SaveProjectWorkspaceContext(project.ID, experiment.ID, experimentLayout); err != nil {
		t.Fatal(err)
	}
	loadedMain, err := app.GetProjectWorkspaceContext(project.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	loadedExperiment, err := app.GetProjectWorkspaceContext(project.ID, experiment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loadedMain != mainLayout || loadedExperiment != experimentLayout {
		t.Fatalf("layouts crossed contexts: main=%q experiment=%q", loadedMain, loadedExperiment)
	}
	db, _ := app.database.Pool(context.Background())
	mainPath, err := projectPathForExperiment(context.Background(), db, project.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	experimentPath, err := projectPathForExperiment(context.Background(), db, project.ID, experiment.ID)
	if err != nil {
		t.Fatal(err)
	}
	mainCommand, err := projectTerminalCommand("cmd", mainPath, "")
	if err != nil {
		t.Fatal(err)
	}
	experimentCommand, err := projectTerminalCommand("cmd", experimentPath, "")
	if err != nil {
		t.Fatal(err)
	}
	if sameRequestedPath(mainCommand.dir, experimentCommand.dir) || !sameRequestedPath(experimentCommand.dir, experiment.WorktreePath) {
		t.Fatalf("terminal contexts main=%q experiment=%q", mainCommand.dir, experimentCommand.dir)
	}
}

func TestExperimentTokenCannotReadAnotherExperiment(t *testing.T) {
	app, project, projectApp, first, _, _ := newLifecycleExperiment(t)
	second, err := app.CreateExperiment(ExperimentCreateInput{
		ProjectID: project.ID, Kind: "app", AppID: projectApp.ID, Name: "Second",
		Objective: "isolated", CreatedBy: "user", Confirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	app.agentTokens = newAgentTokenStore()
	token, err := app.agentTokens.Issue(AgentTokenScope{
		SessionID: "first-agent", ProjectID: project.ID, ExperimentID: first.ID,
		AppID: projectApp.ID, Permissions: appAgentPermissions,
	}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	bridge := newAgentBridge(app, app.agentTokens)
	if _, err = bridge.callTool(context.Background(), token, "seizen_experiment_status", mustAgentJSON(agentExperimentIDInput{ExperimentID: second.ID})); err == nil {
		t.Fatal("experiment token read another experiment")
	}
	value, err := bridge.callTool(context.Background(), token, "seizen_experiment_status", mustAgentJSON(agentExperimentIDInput{ExperimentID: first.ID}))
	if err != nil || value.(Experiment).ID != first.ID {
		t.Fatalf("own experiment unavailable: %#v, %v", value, err)
	}
}

func TestClosingSeizenStopsActiveExperimentRuntime(t *testing.T) {
	app, _, projectApp, experiment, manager, _ := newLifecycleExperiment(t)
	if _, err := app.StartAppContext(projectApp.ID, experiment.ID); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		status, err := app.GetAppStatusContext(projectApp.ID, experiment.ID)
		if err == nil && status.ProcessAlive {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	prepareManagedClose(manager, nil, nil, nil)
	manager.mu.Lock()
	active := len(manager.processes)
	manager.mu.Unlock()
	if active != 0 {
		t.Fatalf("%d experiment runtimes remain after close", active)
	}
	status, err := app.GetAppStatusContext(projectApp.ID, experiment.ID)
	if err != nil || status.ProcessAlive {
		t.Fatalf("runtime still alive after close: %#v, %v", status, err)
	}
}

