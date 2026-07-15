package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateExperimentPreservesMainAndCopiesDirtyCheckpoint(t *testing.T) {
	app, project := newAppServerTestApp(t)
	t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "local"))
	if output, err := runGit(context.Background(), project.Path, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %s, %v", output, err)
	}
	tracked := filepath.Join(project.Path, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := runGit(context.Background(), project.Path, "add", "-A"); err != nil {
		t.Fatalf("git add: %s, %v", output, err)
	}
	if output, err := runGit(context.Background(), project.Path,
		"-c", "user.name=Test", "-c", "user.email=test@local", "commit", "-m", "base"); err != nil {
		t.Fatalf("git commit: %s, %v", output, err)
	}
	projectApp, err := app.CreateApp(testAppInput(project))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(tracked, []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(project.Path, "new.txt"), []byte("untracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeHead, _ := gitText(context.Background(), project.Path, nil, "rev-parse", "HEAD")
	beforeStatus, _ := gitText(context.Background(), project.Path, nil, "status", "--porcelain")

	experiment, err := app.CreateExperiment(ExperimentCreateInput{
		ProjectID: project.ID, Kind: "app", AppID: projectApp.ID,
		Name: "Cart redesign", Objective: "Test the checkout", CreatedBy: "user", Confirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if experiment.BaseCommit != beforeHead || !strings.HasPrefix(experiment.BranchName, "experiment/cart-redesign-") {
		t.Fatalf("unexpected Git metadata: %#v", experiment)
	}
	afterHead, _ := gitText(context.Background(), project.Path, nil, "rev-parse", "HEAD")
	afterBranch, _ := gitText(context.Background(), project.Path, nil, "branch", "--show-current")
	afterStatus, _ := gitText(context.Background(), project.Path, nil, "status", "--porcelain")
	if afterHead != beforeHead || afterBranch != "main" || afterStatus != beforeStatus {
		t.Fatalf("main changed: head %q/%q branch=%q status %q/%q", beforeHead, afterHead, afterBranch, beforeStatus, afterStatus)
	}
	for name, expected := range map[string]string{"tracked.txt": "dirty\n", "new.txt": "untracked\n"} {
		contents, readErr := os.ReadFile(filepath.Join(experiment.WorktreePath, name))
		if readErr != nil || strings.ReplaceAll(string(contents), "\r\n", "\n") != expected {
			t.Fatalf("checkpoint did not include %s: %q, %v", name, contents, readErr)
		}
	}
	branch, _ := gitText(context.Background(), experiment.WorktreePath, nil, "branch", "--show-current")
	if branch != experiment.BranchName {
		t.Fatalf("worktree branch = %q, want %q", branch, experiment.BranchName)
	}
	if err = os.WriteFile(filepath.Join(experiment.WorktreePath, "checkpoint.txt"), []byte("saved"), 0o600); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := app.CreateExperimentCheckpoint(experiment.ID)
	if err != nil || checkpoint == "" {
		t.Fatalf("checkpoint failed: %q, %v", checkpoint, err)
	}
}

func TestExperimentNamesAndManagedPathsAreSafe(t *testing.T) {
	if got := experimentSlug("  Nueva autenticación / API  "); got != "nueva-autenticacion-api" {
		t.Fatalf("unexpected slug %q", got)
	}
	for _, branch := range []string{"main", "experiment/../main", "experiment/bad name", "experiment/"} {
		if validateExperimentBranch(branch) == nil {
			t.Fatalf("unsafe branch %q was accepted", branch)
		}
	}
	root := filepath.Join(t.TempDir(), "managed")
	if validateManagedExperimentPath(root, filepath.Join(root, "project", "experiment")) != nil {
		t.Fatal("managed child path was rejected")
	}
	if validateManagedExperimentPath(root, filepath.Join(root, "..", "outside")) == nil {
		t.Fatal("outside worktree path was accepted")
	}
}
