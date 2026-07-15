package main

import (
	"context"
	"strings"
	"testing"
)

func TestExperimentHandoffCreatesNewScopedSessionAndKeepsSource(t *testing.T) {
	app, project, projectApp, experiment, _, _ := newLifecycleExperiment(t)
	manager := newTerminalManager(nil)
	app.terminals = manager
	t.Cleanup(manager.stopAll)
	sourceBackend := newTerminalTestBackend(false)
	if err := manager.startWithScopeExperimentProfile(
		"source-agent", project.ID, "", "", currentProjectSpaceID, "codex", "codex",
		func() (terminalBackend, error) { return sourceBackend, nil },
	); err != nil {
		t.Fatal(err)
	}
	targetBackend := newTerminalTestBackend(false)
	app.startExperimentAgent = func(projectID, experimentID, shell, appID string) (string, error) {
		const targetID = "target-agent"
		err := manager.startWithScopeExperimentProfile(
			targetID, projectID, experimentID, "", currentProjectSpaceID, shell, shell,
			func() (terminalBackend, error) { return targetBackend, nil },
		)
		if session := manager.session(targetID); session != nil {
			session.appID = appID
		}
		return targetID, err
	}
	handoff, err := app.handoffExperimentAgent(AgentTokenScope{
		SessionID: "source-agent", ProjectID: project.ID, AppID: projectApp.ID,
	}, experiment, `{"steps":["change","test"]}`, `[{"decision":"isolated"}]`)
	if err != nil {
		t.Fatal(err)
	}
	if handoff.SourceSessionID != "source-agent" || handoff.TargetSessionID != "target-agent" || handoff.ExperimentID != experiment.ID {
		t.Fatalf("handoff = %#v", handoff)
	}
	if manager.session("source-agent") == nil {
		t.Fatal("source session was redirected or closed")
	}
	target := manager.session("target-agent")
	if target == nil || target.experimentID != experiment.ID || target.appID != projectApp.ID {
		t.Fatalf("target session = %#v", target)
	}
	if prompt := string(targetBackend.inputBytes()); !strings.Contains(prompt, experiment.Objective) || !strings.Contains(prompt, "seizen_project_context") {
		t.Fatalf("handoff prompt = %q", prompt)
	}
	db, _ := app.database.Pool(context.Background())
	var storedTarget string
	if err = db.QueryRow(`SELECT target_session_id FROM experiment_handoffs WHERE id = ?`, handoff.ID).Scan(&storedTarget); err != nil || storedTarget != "target-agent" {
		t.Fatalf("stored target = %q, %v", storedTarget, err)
	}
}
