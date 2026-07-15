package core

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateGitHubURL(t *testing.T) {
	valid := map[string]string{
		"https://github.com/openai/codex":          "https://github.com/openai/codex",
		"https://github.com/openai/codex.git/":     "https://github.com/openai/codex.git",
		"git@github.com:openai/codex.git":          "git@github.com:openai/codex.git",
		"ssh://git@github.com/openai/codex.git":    "ssh://git@github.com/openai/codex.git",
		"ssh://git@github.com:22/openai/codex.git": "ssh://git@github.com:22/openai/codex.git",
	}
	for value, want := range valid {
		got, err := validateGitHubURL(value)
		if err != nil || got != want {
			t.Errorf("%q: expected %q, got %q, %v", value, want, got, err)
		}
	}

	for _, value := range []string{
		"http://github.com/openai/codex.git",
		"https://example.com/openai/codex.git",
		"https://github.com/openai/codex.git?token=x",
		"https://github.com/openai/too/many",
		"ssh://root@github.com/openai/codex.git",
		"ssh://git@github.com:2222/openai/codex.git",
		" git@github.com:openai/codex.git",
	} {
		if _, err := validateGitHubURL(value); err == nil {
			t.Errorf("expected %q to be rejected", value)
		}
	}
}

func TestSetProjectGitHubConfiguresOriginAndDatabase(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is not installed")
	}
	ctx := context.Background()
	base := t.TempDir()
	root := filepath.Join(base, "projects")
	database := newDatabase(filepath.Join(base, "config", "seizen.db"), root)
	if err := database.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	db, err := database.Pool(ctx)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "Demo")
	if err = os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	project, err := upsertProject(ctx, db, FSProjectInfo{Name: "Demo", Path: path}, ProjectCreated)
	if err != nil {
		t.Fatal(err)
	}

	app := &App{database: database}
	githubURL := "https://github.com/openai/codex.git"
	project, err = app.SetProjectGitHub(project.ID, project.Path, githubURL)
	if err != nil {
		t.Fatal(err)
	}
	if project.GitRemote == nil || *project.GitRemote != githubURL {
		t.Fatalf("expected stored GitHub URL %q, got %#v", githubURL, project.GitRemote)
	}
	output, err := runGit(ctx, path, "remote", "get-url", "origin")
	if err != nil || string(output) != githubURL+"\n" {
		t.Fatalf("expected configured origin %q, got %q, %v", githubURL, output, err)
	}
	output, err = runGit(ctx, path, "remote", "get-url", "--push", "origin")
	if err != nil || string(output) != githubURL+"\n" {
		t.Fatalf("expected configured push URL %q, got %q, %v", githubURL, output, err)
	}
	if _, _, err = loadGitHubProject(ctx, db, project.ID, filepath.Join(root, "Other")); err == nil {
		t.Fatal("expected a mismatched path to be rejected")
	}
}

func TestBackupProjectCommitsAndPushesWithoutNetwork(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is not installed")
	}
	ctx := context.Background()
	base := t.TempDir()
	globalConfig := filepath.Join(base, "gitconfig")
	t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	git := func(arguments ...string) string {
		command := exec.CommandContext(ctx, "git", arguments...)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(arguments, " "), err, output)
		}
		return strings.TrimSpace(string(output))
	}
	git("config", "--global", "user.name", "Seizen Test")
	git("config", "--global", "user.email", "seizen-test@example.invalid")
	git("config", "--global", "init.defaultBranch", "main")

	remote := filepath.Join(base, "remote.git")
	git("init", "--bare", remote)
	remoteURL := "file:///" + strings.TrimPrefix(filepath.ToSlash(remote), "/")
	githubURL := "https://github.com/seizen/backup-test.git"
	git("config", "--global", "url."+remoteURL+".insteadOf", githubURL)

	root := filepath.Join(base, "projects")
	database := newDatabase(filepath.Join(base, "config", "seizen.db"), root)
	if err := database.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	db, err := database.Pool(ctx)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "Demo")
	if err = os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(path, "README.md"), []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	project, err := upsertProject(ctx, db, FSProjectInfo{Name: "Demo", Path: path}, ProjectCreated)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{database: database}
	project, err = app.SetProjectGitHub(project.ID, project.Path, githubURL)
	if err != nil {
		t.Fatal(err)
	}

	message, err := app.BackupProject(project.ID, project.Path)
	if err != nil || !strings.Contains(message, "commit") {
		t.Fatalf("expected a committed backup, got %q, %v", message, err)
	}
	if count := git("--git-dir", remote, "rev-list", "--count", "--all"); count != "1" {
		t.Fatalf("expected one pushed commit, got %q", count)
	}

	message, err = app.BackupProject(project.ID, project.Path)
	if err != nil || !strings.Contains(message, "no new changes") {
		t.Fatalf("expected a no-change backup, got %q, %v", message, err)
	}
	if count := git("--git-dir", remote, "rev-list", "--count", "--all"); count != "1" {
		t.Fatalf("expected no extra commit, got %q", count)
	}
}

func TestSetProjectGitHubRestoresOriginWhenDatabaseFails(t *testing.T) {
	t.Run("restores previous origin", func(t *testing.T) {
		ctx := context.Background()
		app, db, project, path := githubRollbackTestProject(t)
		fetchURL := "https://github.com/openai/old-fetch.git"
		pushURL := "git@github.com:openai/old-push.git"
		if err := ensureGitRepository(ctx, path); err != nil {
			t.Fatal(err)
		}
		if output, err := runGit(ctx, path, "remote", "add", "origin", fetchURL); err != nil {
			t.Fatalf("could not configure old fetch URL: %v: %s", err, output)
		}
		if output, err := runGit(ctx, path, "config", "--local", "--add", "remote.origin.pushurl", pushURL); err != nil {
			t.Fatalf("could not configure old push URL: %v: %s", err, output)
		}
		forceGitHubUpdateFailure(t, db)

		_, err := app.SetProjectGitHub(project.ID, project.Path, "https://github.com/openai/new.git")
		if err == nil || !strings.Contains(err.Error(), "was restored") {
			t.Fatalf("expected persistence failure with successful rollback, got %v", err)
		}
		assertGitRemoteURLs(t, path, fetchURL, pushURL)
	})

	t.Run("removes new origin but keeps git repository", func(t *testing.T) {
		app, db, project, path := githubRollbackTestProject(t)
		forceGitHubUpdateFailure(t, db)

		_, err := app.SetProjectGitHub(project.ID, project.Path, "https://github.com/openai/new.git")
		if err == nil {
			t.Fatal("expected database persistence to fail")
		}
		if info, statErr := os.Stat(filepath.Join(path, ".git")); statErr != nil || !info.IsDir() {
			t.Fatalf("expected .git to remain: %v", statErr)
		}
		output, commandErr := runGit(context.Background(), path, "remote")
		if commandErr != nil || strings.Contains(string(output), "origin") {
			t.Fatalf("expected the new origin to be removed, got %q, %v", output, commandErr)
		}
	})
}

func TestBackupPushErrorExplainsLocalCommit(t *testing.T) {
	err := backupPushError(true, errors.New("rejected"), nil)
	if !strings.Contains(err.Error(), "was saved locally") || !strings.Contains(err.Error(), "did not reach GitHub") {
		t.Fatalf("expected a clear local-only commit error, got %v", err)
	}
}

func githubRollbackTestProject(t *testing.T) (*App, *sql.DB, Project, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is not installed")
	}
	ctx := context.Background()
	base := t.TempDir()
	root := filepath.Join(base, "projects")
	database := newDatabase(filepath.Join(base, "config", "seizen.db"), root)
	if err := database.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	db, err := database.Pool(ctx)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "Demo")
	if err = os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	project, err := upsertProject(ctx, db, FSProjectInfo{Name: "Demo", Path: path}, ProjectCreated)
	if err != nil {
		t.Fatal(err)
	}
	return &App{database: database}, db, project, path
}

func forceGitHubUpdateFailure(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `CREATE TRIGGER fail_github_update
BEFORE UPDATE OF git_remote ON projects BEGIN
    SELECT RAISE(FAIL, 'forced database failure');
END`)
	if err != nil {
		t.Fatal(err)
	}
}

func assertGitRemoteURLs(t *testing.T, path, wantFetch, wantPush string) {
	t.Helper()
	for want, arguments := range map[string][]string{
		wantFetch: {"remote", "get-url", "origin"},
		wantPush:  {"remote", "get-url", "--push", "origin"},
	} {
		output, err := runGit(context.Background(), path, arguments...)
		if err != nil || strings.TrimSpace(string(output)) != want {
			t.Fatalf("expected origin URL %q, got %q, %v", want, output, err)
		}
	}
}
