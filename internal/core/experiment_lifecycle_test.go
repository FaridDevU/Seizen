package core

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newLifecycleExperiment(t *testing.T) (*App, Project, ProjectApp, Experiment, *AppRuntimeManager, *fakeManagedProcess) {
	t.Helper()
	app, project := newAppServerTestApp(t)
	t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "local"))
	if output, err := runGit(context.Background(), project.Path, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %s, %v", output, err)
	}
	if err := os.WriteFile(filepath.Join(project.Path, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := runGit(context.Background(), project.Path, "add", "-A"); err != nil {
		t.Fatalf("git add: %s, %v", output, err)
	}
	if output, err := runGit(context.Background(), project.Path, "-c", "user.name=Test", "-c", "user.email=test@local", "commit", "-m", "base"); err != nil {
		t.Fatalf("git commit: %s, %v", output, err)
	}
	input := testAppInput(project)
	input.Kind, input.HealthcheckURL, input.PreviewURL = "desktop", "", ""
	input.StartCommand, input.StopCommand, input.TestCommand = "run", "", "exit 0"
	projectApp, err := app.CreateApp(input)
	if err != nil {
		t.Fatal(err)
	}
	experiment, err := app.CreateExperiment(ExperimentCreateInput{
		ProjectID: project.ID, Kind: "app", AppID: projectApp.ID, Name: "Lifecycle", Objective: "test integration", CreatedBy: "user", Confirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := newAppRuntimeManager(app.database, nil)
	process := newFakeManagedProcess(9001)
	nextPID := 9001
	manager.starter = func(managedProcessSpec, io.Writer) (managedProcess, error) {
		created := newFakeManagedProcess(nextPID)
		nextPID++
		return created, nil
	}
	app.appRuntimes = manager
	t.Cleanup(func() { _, _ = manager.StopAppContext(context.Background(), projectApp.ID, experiment.ID) })
	return app, project, projectApp, experiment, manager, process
}

func TestExperimentIntegrationRejectsChangedMainAfterReview(t *testing.T) {
	app, project, _, experiment, _, _ := newLifecycleExperiment(t)
	if err := os.WriteFile(filepath.Join(experiment.WorktreePath, "feature.txt"), []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.PrepareExperimentIntegration(experiment.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project.Path, "main-later.txt"), []byte("main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := runGit(context.Background(), project.Path, "add", "-A"); err != nil {
		t.Fatalf("git add: %s, %v", output, err)
	}
	if output, err := runGit(context.Background(), project.Path, "-c", "user.name=Test", "-c", "user.email=test@local", "commit", "-m", "main moved"); err != nil {
		t.Fatalf("git commit: %s, %v", output, err)
	}
	expected, _ := gitText(context.Background(), project.Path, nil, "rev-parse", "HEAD")
	approval, err := app.RequestExperimentIntegration(experiment.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.ResolveAgentApproval(approval.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err = app.IntegrateExperiment(experiment.ID, approval.ID, true); err == nil || !strings.Contains(err.Error(), "Principal") {
		t.Fatalf("changed Principal was integrated: %v", err)
	}
	after, _ := gitText(context.Background(), project.Path, nil, "rev-parse", "HEAD")
	if after != expected {
		t.Fatalf("Principal changed after rejected integration: %s -> %s", expected, after)
	}
}

func TestExperimentReviewStopsOnFailingTests(t *testing.T) {
	app, _, _, experiment, _, _ := newLifecycleExperiment(t)
	configuration, err := decodeExperimentConfiguration(experiment)
	if err != nil {
		t.Fatal(err)
	}
	configuration.App.TestCommand = "exit 7"
	encoded, _ := json.Marshal(configuration)
	db, _ := app.database.Pool(context.Background())
	if _, err = db.Exec(`UPDATE experiments SET configuration_json = ? WHERE id = ?`, encoded, experiment.ID); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(experiment.WorktreePath, "failure.txt"), []byte("change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = app.PrepareExperimentIntegration(experiment.ID, true); err == nil || !strings.Contains(err.Error(), "tests failed") {
		t.Fatalf("failing tests did not block review: %v", err)
	}
}

func TestExperimentDiscardRequiresDirtyBackupAndCleansResources(t *testing.T) {
	app, _, _, experiment, _, _ := newLifecycleExperiment(t)
	app.agentTokens = newAgentTokenStore()
	token, err := app.agentTokens.Issue(AgentTokenScope{
		SessionID: "discard-agent", ProjectID: experiment.ProjectID, ExperimentID: experiment.ID,
		Permissions: appAgentPermissions,
	}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(experiment.WorktreePath, "dirty.txt"), []byte("backup me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = app.DiscardExperiment(ExperimentCleanupInput{ExperimentID: experiment.ID, Confirmed: true}); err == nil || !strings.Contains(err.Error(), "dirty.txt") {
		t.Fatalf("dirty discard did not explain the loss: %v", err)
	}
	discarded, err := app.DiscardExperiment(ExperimentCleanupInput{ExperimentID: experiment.ID, Confirmed: true, BackupDirty: true})
	if err != nil || discarded.Status != "discarded" {
		t.Fatalf("discarded = %#v, %v", discarded, err)
	}
	if _, err = os.Stat(experiment.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("discarded worktree remains: %v", err)
	}
	if _, err = app.agentTokens.Authorize(token, "seizen_experiment_status"); err == nil {
		t.Fatal("discarded experiment token remains authorized")
	}
}

func TestExperimentArchiveRestoresBranchWorktreeAndMetadata(t *testing.T) {
	app, _, _, experiment, _, _ := newLifecycleExperiment(t)
	if err := os.WriteFile(filepath.Join(experiment.WorktreePath, "kept.txt"), []byte("preserved\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CreateExperimentCheckpoint(experiment.ID); err != nil {
		t.Fatal(err)
	}
	archived, err := app.ArchiveExperiment(experiment.ID, true)
	if err != nil || archived.Status != "archived" {
		t.Fatalf("archived = %#v, %v", archived, err)
	}
	if _, err = os.Stat(experiment.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("archived worktree remains: %v", err)
	}
	restored, err := app.RestoreExperiment(experiment.ID, true)
	if err != nil || restored.Status != "active" || restored.BranchName != experiment.BranchName {
		t.Fatalf("restored = %#v, %v", restored, err)
	}
	contents, err := os.ReadFile(filepath.Join(restored.WorktreePath, "kept.txt"))
	if err != nil || strings.TrimSpace(string(contents)) != "preserved" {
		t.Fatalf("restored contents = %q, %v", contents, err)
	}
}

func TestExperimentReviewEmitsRealLifecycleEvents(t *testing.T) {
	app, _, _, experiment, _, _ := newLifecycleExperiment(t)
	var events []string
	app.mu.Lock()
	app.ctx = context.Background()
	app.emitEvent = func(_ context.Context, name string, _ ...interface{}) { events = append(events, name) }
	app.mu.Unlock()
	if err := os.WriteFile(filepath.Join(experiment.WorktreePath, "events.txt"), []byte("events\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.PrepareExperimentIntegration(experiment.ID, true); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"experiment.review.started", "experiment.checkpoint.created", "experiment.integration.preparing",
		"experiment.status.updated", "experiment.review.ready", "experiment.integration.ready",
	} {
		found := false
		for _, name := range events {
			if name == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing event %s in %#v", expected, events)
		}
	}
}

func TestExperimentIntegrationUsesVerifiedTemporaryWorktreeAndApproval(t *testing.T) {
	app, project, _, experiment, _, _ := newLifecycleExperiment(t)
	if err := os.WriteFile(filepath.Join(experiment.WorktreePath, "feature.txt"), []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	review, err := app.PrepareExperimentIntegration(experiment.ID, true)
	if err != nil || !review.TestsPassed || !review.AppVerified || review.IntegrationPath == "" {
		t.Fatalf("review = %#v, %v", review, err)
	}
	if _, err = app.IntegrateExperiment(experiment.ID, "missing", true); err == nil {
		t.Fatal("integration succeeded without approval")
	}
	approval, err := app.RequestExperimentIntegration(experiment.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.ResolveAgentApproval(approval.ID, true); err != nil {
		t.Fatal(err)
	}
	integrated, err := app.IntegrateExperiment(experiment.ID, approval.ID, true)
	if err != nil || integrated.Status != "integrated" {
		t.Fatalf("integrated = %#v, %v", integrated, err)
	}
	contents, err := os.ReadFile(filepath.Join(project.Path, "feature.txt"))
	if err != nil || strings.ReplaceAll(string(contents), "\r\n", "\n") != "ready\n" {
		t.Fatalf("main result = %q, %v", contents, err)
	}
}

func TestExperimentIntegrationDetectsConflictWithoutChangingMain(t *testing.T) {
	app, project, _, experiment, _, _ := newLifecycleExperiment(t)
	if err := os.WriteFile(filepath.Join(experiment.WorktreePath, "base.txt"), []byte("experiment\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project.Path, "base.txt"), []byte("main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := runGit(context.Background(), project.Path, "add", "-A"); err != nil {
		t.Fatalf("git add: %s, %v", output, err)
	}
	if output, err := runGit(context.Background(), project.Path, "-c", "user.name=Test", "-c", "user.email=test@local", "commit", "-m", "main changed"); err != nil {
		t.Fatalf("git commit: %s, %v", output, err)
	}
	mainHead, _ := gitText(context.Background(), project.Path, nil, "rev-parse", "HEAD")
	review, err := app.PrepareExperimentIntegration(experiment.ID, true)
	if err == nil || len(review.Conflicts) == 0 {
		t.Fatalf("expected conflict, review=%#v err=%v", review, err)
	}
	afterHead, _ := gitText(context.Background(), project.Path, nil, "rev-parse", "HEAD")
	if mainHead != afterHead {
		t.Fatalf("main changed during conflict: %q -> %q", mainHead, afterHead)
	}
}

func TestExperimentSecretScanOnlyChecksAddedHighSignalValues(t *testing.T) {
	patch := "+normal = true\n+password = \"super-secret-value\"\n context\n"
	findings := scanExperimentSecrets(patch)
	if len(findings) != 1 || !strings.Contains(findings[0], "password") {
		t.Fatalf("findings = %#v", findings)
	}
}
