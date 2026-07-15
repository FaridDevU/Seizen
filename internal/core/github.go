package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

func (a *App) SetProjectGitHub(id, path, rawURL string) (Project, error) {
	githubURL, err := validateGitHubURL(rawURL)
	if err != nil {
		return Project{}, err
	}
	ctx := a.context()
	db, err := a.database.Pool(ctx)
	if err != nil {
		return Project{}, err
	}
	storedPath, _, err := loadGitHubProject(ctx, db, id, path)
	if err != nil {
		return Project{}, err
	}
	folder, err := existingDirectory(storedPath)
	if err != nil {
		return Project{}, err
	}
	origin, err := captureProjectOrigin(ctx, folder)
	if err != nil {
		return Project{}, err
	}
	if err = configureGitHubOrigin(ctx, folder, githubURL); err != nil {
		return Project{}, rollbackProjectOrigin(ctx, folder, origin, err)
	}

	branch, err := currentGitBranch(ctx, folder)
	if err != nil {
		return Project{}, rollbackProjectOrigin(ctx, folder, origin, err)
	}
	project, err := scanProject(db.QueryRowContext(ctx, `UPDATE projects
SET git_remote = ?, branch = ?, updated_at = `+projectNow+`
WHERE id = ? AND path = ?
RETURNING `+projectColumns, githubURL, nullableString(branch), id, storedPath))
	if err != nil {
		persistenceErr := fmt.Errorf("could not save GitHub in the library: %w", err)
		if errors.Is(err, sql.ErrNoRows) {
			persistenceErr = errors.New("the project path changed while GitHub was being configured")
		}
		return Project{}, rollbackProjectOrigin(ctx, folder, origin, persistenceErr)
	}
	return project, nil
}

func (a *App) BackupProject(id, path string) (string, error) {
	ctx, cancel := context.WithTimeout(a.context(), 30*time.Minute)
	defer cancel()
	db, err := a.database.Pool(ctx)
	if err != nil {
		return "", err
	}
	storedPath, storedRemote, err := loadGitHubProject(ctx, db, id, path)
	if err != nil {
		return "", err
	}
	if storedRemote == "" {
		return "", errors.New("associate a GitHub repository with this project first")
	}
	githubURL, err := validateGitHubURL(storedRemote)
	if err != nil {
		return "", fmt.Errorf("the saved repository is no longer a valid GitHub URL: %w", err)
	}
	folder, err := existingDirectory(storedPath)
	if err != nil {
		return "", err
	}
	if err = configureGitHubOrigin(ctx, folder, githubURL); err != nil {
		return "", err
	}
	branch, err := currentGitBranch(ctx, folder)
	if err != nil {
		return "", err
	}
	if branch == "" {
		return "", errors.New("cannot back up the project because Git has no active branch")
	}

	if output, commandErr := runGit(ctx, folder, "add", "-A"); commandErr != nil {
		return "", gitError("Git could not stage the changes", commandErr, output)
	}
	changed, err := hasStagedChanges(ctx, folder)
	if err != nil {
		return "", err
	}
	if changed {
		message := "Seizen backup " + time.Now().UTC().Format("2006-01-02 15:04:05 UTC")
		if output, commandErr := runGit(ctx, folder, "commit", "-m", message); commandErr != nil {
			return "", gitError("Git could not create the commit; check user.name and user.email", commandErr, output)
		}
	} else {
		hasCommits, commitErr := hasGitCommits(ctx, folder)
		if commitErr != nil {
			return "", commitErr
		}
		if !hasCommits {
			if err = updateGitProject(ctx, db, id, storedPath, githubURL, branch); err != nil {
				return "", err
			}
			return "There are no files or commits to back up.", nil
		}
	}

	if output, commandErr := runGit(ctx, folder, "push", "--set-upstream", "origin", branch); commandErr != nil {
		return "", backupPushError(changed, commandErr, output)
	}
	if err = updateGitProject(ctx, db, id, storedPath, githubURL, branch); err != nil {
		return "", fmt.Errorf("the backup reached GitHub, but %w", err)
	}
	if changed {
		return "Backup complete: a commit was created and pushed to GitHub.", nil
	}
	return "Backup complete: there were no new changes and the pending commits were pushed to GitHub.", nil
}

func loadGitHubProject(ctx context.Context, db *sql.DB, id, requestedPath string) (string, string, error) {
	var storedPath string
	var remote sql.NullString
	err := db.QueryRowContext(ctx, `SELECT path, git_remote FROM projects WHERE id = ?`, id).Scan(&storedPath, &remote)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", errors.New("the project was not found")
	}
	if err != nil {
		return "", "", fmt.Errorf("could not load the project: %w", err)
	}
	if !sameRequestedPath(storedPath, requestedPath) {
		return "", "", errors.New("the given path does not match the project's saved path")
	}
	return storedPath, remote.String, nil
}

type gitOriginSnapshot struct {
	existed  bool
	urls     []string
	pushURLs []string
}

func captureProjectOrigin(ctx context.Context, path string) (gitOriginSnapshot, error) {
	output, err := runGit(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil {
		return gitOriginSnapshot{}, nil
	}
	root, err := canonicalPath(strings.TrimSpace(string(output)))
	if err != nil {
		return gitOriginSnapshot{}, fmt.Errorf("Git returned an invalid root: %w", err)
	}
	project, err := canonicalPath(path)
	if err != nil {
		return gitOriginSnapshot{}, fmt.Errorf("could not check the project folder: %w", err)
	}
	if !samePath(root, project) {
		return gitOriginSnapshot{}, nil
	}
	existed, err := gitRemoteExists(ctx, path, "origin")
	if err != nil || !existed {
		return gitOriginSnapshot{existed: existed}, err
	}
	urls, err := gitConfigValues(ctx, path, "remote.origin.url")
	if err != nil {
		return gitOriginSnapshot{}, err
	}
	pushURLs, err := gitConfigValues(ctx, path, "remote.origin.pushurl")
	if err != nil {
		return gitOriginSnapshot{}, err
	}
	return gitOriginSnapshot{existed: true, urls: urls, pushURLs: pushURLs}, nil
}

func restoreProjectOrigin(ctx context.Context, path string, origin gitOriginSnapshot) error {
	if !origin.existed {
		exists, err := gitRemoteExists(ctx, path, "origin")
		if err != nil || !exists {
			return err
		}
		if output, err := runGit(ctx, path, "remote", "remove", "origin"); err != nil {
			return gitError("Git could not remove the new origin", err, output)
		}
		return nil
	}
	for key, values := range map[string][]string{
		"remote.origin.url":     origin.urls,
		"remote.origin.pushurl": origin.pushURLs,
	} {
		output, err := runGit(ctx, path, "config", "--local", "--unset-all", key)
		if err != nil && !gitExitCode(err, 1, 5) {
			return gitError("Git could not clean up the new origin configuration", err, output)
		}
		for _, value := range values {
			if output, err = runGit(ctx, path, "config", "--local", "--add", key, value); err != nil {
				return gitError("Git could not restore the previous origin configuration", err, output)
			}
		}
	}
	return nil
}

func rollbackProjectOrigin(ctx context.Context, path string, origin gitOriginSnapshot, cause error) error {
	if rollbackErr := restoreProjectOrigin(ctx, path, origin); rollbackErr != nil {
		return fmt.Errorf("%v; origin could not be restored either: %w", cause, rollbackErr)
	}
	return fmt.Errorf("%w; the previous origin configuration was restored", cause)
}

func gitRemoteExists(ctx context.Context, path, name string) (bool, error) {
	output, err := runGit(ctx, path, "remote")
	if err != nil {
		return false, gitError("Git could not read the remote repositories", err, output)
	}
	for _, remote := range strings.Fields(string(output)) {
		if remote == name {
			return true, nil
		}
	}
	return false, nil
}

func gitConfigValues(ctx context.Context, path, key string) ([]string, error) {
	output, err := runGit(ctx, path, "config", "--local", "--get-all", key)
	if err != nil {
		if gitExitCode(err, 1) {
			return nil, nil
		}
		return nil, gitError("Git could not read the previous origin configuration", err, output)
	}
	text := strings.TrimSuffix(string(output), "\n")
	if text == "" {
		return nil, nil
	}
	values := strings.Split(text, "\n")
	for index := range values {
		values[index] = strings.TrimSuffix(values[index], "\r")
	}
	return values, nil
}

func configureGitHubOrigin(ctx context.Context, path, githubURL string) error {
	if err := ensureGitRepository(ctx, path); err != nil {
		return err
	}
	output, err := runGit(ctx, path, "remote")
	if err != nil {
		return gitError("Git could not read the remote repositories", err, output)
	}
	command := []string{"remote", "add", "origin", githubURL}
	for _, remote := range strings.Fields(string(output)) {
		if remote == "origin" {
			command = []string{"remote", "set-url", "origin", githubURL}
			break
		}
	}
	if output, err = runGit(ctx, path, command...); err != nil {
		return gitError("Git could not configure origin", err, output)
	}
	if output, err = runGit(ctx, path, "config", "--local", "--replace-all", "remote.origin.pushurl", githubURL); err != nil {
		return gitError("Git could not configure the origin push URL", err, output)
	}
	return nil
}

func ensureGitRepository(ctx context.Context, path string) error {
	output, err := runGit(ctx, path, "rev-parse", "--show-toplevel")
	if err == nil {
		root, rootErr := canonicalPath(strings.TrimSpace(string(output)))
		project, projectErr := canonicalPath(path)
		if rootErr == nil && projectErr == nil && samePath(root, project) {
			return nil
		}
	}
	if output, err = runGit(ctx, path, "init"); err != nil {
		return gitError("Git could not initialize the project", err, output)
	}
	return nil
}

func currentGitBranch(ctx context.Context, path string) (string, error) {
	output, err := runGit(ctx, path, "branch", "--show-current")
	if err != nil {
		return "", gitError("Git could not check the current branch", err, output)
	}
	return strings.TrimSpace(string(output)), nil
}

func hasStagedChanges(ctx context.Context, path string) (bool, error) {
	output, err := runGit(ctx, path, "diff", "--cached", "--quiet", "--exit-code")
	if err == nil {
		return false, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) && exitError.ExitCode() == 1 {
		return true, nil
	}
	return false, gitError("Git could not check the staged changes", err, output)
}

func gitExitCode(err error, codes ...int) bool {
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		return false
	}
	for _, code := range codes {
		if exitError.ExitCode() == code {
			return true
		}
	}
	return false
}

func backupPushError(committed bool, err error, output []byte) error {
	if committed {
		return gitError("The commit was saved locally, but did not reach GitHub", err, output)
	}
	return gitError("Git could not push the backup to GitHub", err, output)
}

func hasGitCommits(ctx context.Context, path string) (bool, error) {
	output, err := runGit(ctx, path, "rev-list", "--count", "--all")
	if err != nil {
		return false, gitError("Git could not check the history", err, output)
	}
	return strings.TrimSpace(string(output)) != "0", nil
}

func updateGitProject(ctx context.Context, db *sql.DB, id, path, githubURL, branch string) error {
	result, err := db.ExecContext(ctx, `UPDATE projects
SET git_remote = ?, branch = ?, updated_at = `+projectNow+`
WHERE id = ? AND path = ?`, githubURL, branch, id, path)
	if err != nil {
		return fmt.Errorf("could not update the library: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return errors.New("the project changed in the library")
	}
	return nil
}

func runGit(ctx context.Context, path string, arguments ...string) ([]byte, error) {
	arguments = append([]string{"-C", path}, arguments...)
	command := exec.CommandContext(ctx, "git", arguments...)
	hideWindow(command)
	output, err := command.CombinedOutput()
	if err != nil && ctx.Err() != nil {
		return output, ctx.Err()
	}
	return output, err
}

func validateGitHubURL(value string) (string, error) {
	if value == "" || strings.TrimSpace(value) != value {
		return "", errors.New("the GitHub URL is not valid")
	}
	value = strings.TrimSuffix(value, "/")
	lower := strings.ToLower(value)
	var repositoryPath string
	if strings.HasPrefix(lower, "git@github.com:") {
		repositoryPath = value[len("git@github.com:"):]
	} else {
		parsed, err := url.Parse(value)
		if err != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
			return "", errors.New("the GitHub URL is not valid")
		}
		switch strings.ToLower(parsed.Scheme) {
		case "https":
			if parsed.User != nil || !strings.EqualFold(parsed.Hostname(), "github.com") || (parsed.Port() != "" && parsed.Port() != "443") {
				return "", errors.New("the HTTPS URL must belong to github.com")
			}
		case "ssh":
			password, hasPassword := "", false
			if parsed.User != nil {
				password, hasPassword = parsed.User.Password()
			}
			if parsed.User == nil || parsed.User.Username() != "git" || hasPassword || password != "" ||
				!strings.EqualFold(parsed.Hostname(), "github.com") || (parsed.Port() != "" && parsed.Port() != "22") {
				return "", errors.New("the SSH URL must use git@github.com")
			}
		default:
			return "", errors.New("use an HTTPS or SSH GitHub URL")
		}
		repositoryPath = strings.TrimPrefix(parsed.Path, "/")
	}
	if !validGitHubRepositoryPath(repositoryPath) {
		return "", errors.New("the URL must identify a GitHub repository")
	}
	return value, nil
}

func validGitHubRepositoryPath(path string) bool {
	segments := strings.Split(path, "/")
	if len(segments) != 2 || !validGitHubSegment(segments[0]) {
		return false
	}
	repository := segments[1]
	if strings.HasSuffix(strings.ToLower(repository), ".git") {
		repository = repository[:len(repository)-4]
	}
	return validGitHubSegment(repository)
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
