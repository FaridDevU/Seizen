package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

const experimentGitTimeout = 2 * time.Minute

type ExperimentCreateInput struct {
	ProjectID         string `json:"projectId"`
	Kind              string `json:"kind"`
	AppID             string `json:"appId"`
	BaseServerID      string `json:"baseServerId"`
	Name              string `json:"name"`
	Objective         string `json:"objective"`
	BranchName        string `json:"branchName"`
	CreatedBy         string `json:"createdBy"`
	AgentSessionID    string `json:"agentSessionId"`
	RiskLevel         string `json:"riskLevel"`
	RiskReasonsJSON   string `json:"riskReasonsJson"`
	ConfigurationJSON string `json:"configurationJson"`
	Confirmed         bool   `json:"confirmed"`
}

func (a *App) CreateExperiment(input ExperimentCreateInput) (Experiment, error) {
	if !input.Confirmed {
		return Experiment{}, errors.New("confirm the creation of the experiment")
	}
	ctx := a.context()
	db, err := a.database.Pool(ctx)
	if err != nil {
		return Experiment{}, err
	}
	var projectPath string
	if err = db.QueryRowContext(ctx, `SELECT path FROM projects WHERE id = ?`, strings.TrimSpace(input.ProjectID)).Scan(&projectPath); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Experiment{}, errors.New("the project was not found")
		}
		return Experiment{}, err
	}
	projectPath, err = existingDirectory(projectPath)
	if err != nil {
		return Experiment{}, err
	}
	input.ConfigurationJSON, err = a.defaultExperimentConfiguration(ctx, db, input)
	if err != nil {
		return Experiment{}, err
	}
	id, err := newUUID()
	if err != nil {
		return Experiment{}, err
	}
	a.emitAgentEvent("experiment.creating", map[string]any{"id": id, "projectId": input.ProjectID, "name": input.Name})
	repositoryRoot, relativeProject, baseBranch, baseCommit, checkpoint, err := prepareExperimentRepository(ctx, projectPath)
	if err != nil {
		a.emitAgentEvent("experiment.failed", map[string]any{"id": id, "projectId": input.ProjectID, "error": err.Error()})
		return Experiment{}, err
	}
	branch := strings.TrimSpace(input.BranchName)
	if branch == "" {
		branch = "experiment/" + experimentSlug(input.Name) + "-" + id[:8]
	}
	if err = validateExperimentBranch(branch); err != nil {
		return Experiment{}, err
	}
	root, err := managedExperimentRoot()
	if err != nil {
		return Experiment{}, err
	}
	checkout := filepath.Join(root, input.ProjectID, id)
	if err = validateManagedExperimentPath(root, checkout); err != nil {
		return Experiment{}, err
	}
	if _, statErr := os.Lstat(checkout); !errors.Is(statErr, os.ErrNotExist) {
		return Experiment{}, errors.New("the experiment's managed folder already exists")
	}
	if err = os.MkdirAll(filepath.Dir(checkout), 0o700); err != nil {
		return Experiment{}, fmt.Errorf("could not prepare the experiments folder: %w", err)
	}
	gitCtx, cancel := context.WithTimeout(ctx, experimentGitTimeout)
	defer cancel()
	if output, runErr := runExperimentGit(gitCtx, repositoryRoot, nil, "worktree", "add", "-b", branch, checkout, checkpoint); runErr != nil {
		return Experiment{}, gitOperationError("could not create the worktree", runErr, output)
	}
	activePath := filepath.Join(checkout, relativeProject)
	if _, err = existingDirectory(activePath); err != nil {
		_ = removeExperimentWorktree(context.Background(), repositoryRoot, checkout, branch)
		return Experiment{}, errors.New("the worktree does not contain the project path")
	}
	experiment, err := createExperimentRecord(ctx, db, experimentRecordInput{
		ID: id, ProjectID: input.ProjectID, Kind: input.Kind, AppID: input.AppID,
		BaseServerID: input.BaseServerID, Name: input.Name, Objective: input.Objective,
		BaseBranch: baseBranch, BranchName: branch, BaseCommit: baseCommit,
		WorktreePath: activePath, Status: "active", CreatedBy: input.CreatedBy,
		AgentSessionID: input.AgentSessionID, RiskLevel: input.RiskLevel,
		RiskReasonsJSON: input.RiskReasonsJSON, ConfigurationJSON: input.ConfigurationJSON,
	})
	if err != nil {
		_ = removeExperimentWorktree(context.Background(), repositoryRoot, checkout, branch)
		return Experiment{}, err
	}
	if err = a.cloneExperimentServer(experiment); err != nil {
		_, _ = db.ExecContext(ctx, `DELETE FROM experiments WHERE id = ?`, experiment.ID)
		_ = removeExperimentWorktree(context.Background(), repositoryRoot, checkout, branch)
		return Experiment{}, err
	}
	a.emitAgentEvent("experiment.checkpoint.created", map[string]any{
		"id": experiment.ID, "projectId": experiment.ProjectID, "commit": checkpoint,
	})
	a.emitAgentEvent("experiment.created", experiment)
	if _, err = a.SelectProjectExperiment(experiment.ProjectID, experiment.ID); err != nil {
		return Experiment{}, err
	}
	return experiment, nil
}

func (a *App) CreateExperimentCheckpoint(experimentID string) (string, error) {
	db, err := a.database.Pool(a.context())
	if err != nil {
		return "", err
	}
	experiment, err := scanExperiment(db.QueryRow(`SELECT `+experimentColumns+` FROM experiments WHERE id = ?`, strings.TrimSpace(experimentID)))
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("the experiment was not found")
	}
	if err != nil {
		return "", err
	}
	if experiment.Status == "discarded" || experiment.Status == "archived" || experiment.Status == "integrated" {
		return "", errors.New("the experiment no longer accepts checkpoints")
	}
	if _, err = validateExperimentWorktree(experiment); err != nil {
		return "", err
	}
	status, err := gitText(a.context(), experiment.WorktreePath, nil, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	if status != "" {
		if output, addErr := runExperimentGit(a.context(), experiment.WorktreePath, nil, "add", "-A"); addErr != nil {
			return "", gitOperationError("could not prepare the checkpoint", addErr, output)
		}
		message := "Seizen checkpoint " + experiment.Name
		if output, commitErr := runExperimentGit(a.context(), experiment.WorktreePath, nil,
			"-c", "user.name=Seizen", "-c", "user.email=seizen@local", "commit", "-m", message); commitErr != nil {
			return "", gitOperationError("could not save the checkpoint", commitErr, output)
		}
	}
	commit, err := gitText(a.context(), experiment.WorktreePath, nil, "rev-parse", "HEAD")
	if err == nil {
		a.emitAgentEvent("experiment.checkpoint.created", map[string]any{
			"id": experiment.ID, "projectId": experiment.ProjectID, "commit": commit,
		})
	}
	return commit, err
}

func prepareExperimentRepository(ctx context.Context, projectPath string) (root, relative, branch, baseCommit, checkpoint string, err error) {
	if _, err = exec.LookPath("git"); err != nil {
		err = errors.New("Git is not available")
		return
	}
	if root, err = gitText(ctx, projectPath, nil, "rev-parse", "--show-toplevel"); err != nil {
		err = errors.New("the project is not yet a Git repository")
		return
	}
	root, err = existingDirectory(root)
	if err != nil {
		return
	}
	relative, err = filepath.Rel(root, projectPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		err = errors.New("the project path is outside the Git repository")
		return
	}
	if relative == "." {
		relative = ""
	}
	branch, err = gitText(ctx, root, nil, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || branch == "" {
		err = errors.New("select a Git branch before creating the experiment")
		return
	}
	baseCommit, err = gitText(ctx, root, nil, "rev-parse", "HEAD")
	if err != nil {
		return
	}
	checkpoint = baseCommit
	status, statusErr := gitText(ctx, root, nil, "status", "--porcelain")
	if statusErr != nil {
		err = statusErr
		return
	}
	if status == "" {
		return
	}
	index, indexErr := os.CreateTemp("", "seizen-checkpoint-*.index")
	if indexErr != nil {
		err = indexErr
		return
	}
	indexPath := index.Name()
	_ = index.Close()
	_ = os.Remove(indexPath)
	defer os.Remove(indexPath)
	environment := append(os.Environ(), "GIT_INDEX_FILE="+indexPath)
	if _, err = runExperimentGit(ctx, root, environment, "read-tree", "HEAD"); err != nil {
		return
	}
	if _, err = runExperimentGit(ctx, root, environment, "add", "-A"); err != nil {
		return
	}
	var tree string
	if tree, err = gitText(ctx, root, environment, "write-tree"); err != nil {
		return
	}
	identity := append(environment,
		"GIT_AUTHOR_NAME=Seizen", "GIT_AUTHOR_EMAIL=seizen@local",
		"GIT_COMMITTER_NAME=Seizen", "GIT_COMMITTER_EMAIL=seizen@local")
	checkpoint, err = gitText(ctx, root, identity, "commit-tree", tree, "-p", baseCommit, "-m", "Seizen checkpoint")
	return
}

func managedExperimentRoot() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("could not find Seizen's local folder: %w", err)
	}
	return filepath.Join(cache, "Seizen", "experiments"), nil
}

func validateManagedExperimentPath(root, path string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	path, err = filepath.Abs(path)
	if err != nil || !pathInside(root, path) || samePath(root, path) {
		return errors.New("the worktree path is outside the managed folder")
	}
	return nil
}

func validateExperimentWorktree(experiment Experiment) (string, error) {
	root, err := managedExperimentRoot()
	if err != nil {
		return "", err
	}
	path, err := existingDirectory(experiment.WorktreePath)
	if err != nil {
		return "", errors.New("the experiment's worktree is not available")
	}
	if err = validateManagedExperimentPath(root, path); err != nil {
		return "", err
	}
	repository, err := gitText(context.Background(), path, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", errors.New("the experiment's worktree is not a valid Git repository")
	}
	return repository, nil
}

func validateExperimentBranch(branch string) error {
	if !strings.HasPrefix(branch, "experiment/") || strings.ContainsAny(branch, " \t\r\n~^:?*[\\") || strings.Contains(branch, "..") || strings.HasSuffix(branch, ".") || strings.HasSuffix(branch, "/") {
		return errors.New("the branch must use a safe name under experiment/")
	}
	if _, err := gitText(context.Background(), ".", nil, "check-ref-format", "--branch", branch); err != nil {
		return errors.New("the branch name is not valid")
	}
	return nil
}

func experimentSlug(value string) string {
	var result strings.Builder
	dash := false
	for _, character := range strings.ToLower(strings.TrimSpace(value)) {
		switch character {
		case 'á', 'à', 'ä', 'â':
			character = 'a'
		case 'é', 'è', 'ë', 'ê':
			character = 'e'
		case 'í', 'ì', 'ï', 'î':
			character = 'i'
		case 'ó', 'ò', 'ö', 'ô':
			character = 'o'
		case 'ú', 'ù', 'ü', 'û':
			character = 'u'
		case 'ñ':
			character = 'n'
		}
		if unicode.IsLetter(character) && character <= unicode.MaxASCII || unicode.IsDigit(character) {
			result.WriteRune(character)
			dash = false
		} else if result.Len() > 0 && !dash {
			result.WriteByte('-')
			dash = true
		}
	}
	slug := strings.Trim(result.String(), "-")
	if slug == "" {
		return "change"
	}
	if len(slug) > 48 {
		slug = strings.TrimRight(slug[:48], "-")
	}
	return slug
}

func gitText(ctx context.Context, directory string, environment []string, arguments ...string) (string, error) {
	output, err := runExperimentGit(ctx, directory, environment, arguments...)
	if err != nil {
		return "", gitOperationError("Git could not complete the operation", err, output)
	}
	return strings.TrimSpace(string(output)), nil
}

func runExperimentGit(ctx context.Context, directory string, environment []string, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", directory}, arguments...)...)
	if environment != nil {
		command.Env = environment
	}
	hideWindow(command)
	return command.CombinedOutput()
}

func gitOperationError(message string, err error, output []byte) error {
	detail := strings.Join(strings.Fields(strings.ToValidUTF8(string(output), "�")), " ")
	if detail != "" {
		return fmt.Errorf("%s: %s", message, detail)
	}
	return fmt.Errorf("%s: %w", message, err)
}

func removeExperimentWorktree(ctx context.Context, repositoryRoot, checkout, branch string) error {
	var result error
	if output, err := runExperimentGit(ctx, repositoryRoot, nil, "worktree", "remove", "--force", checkout); err != nil {
		result = gitOperationError("could not clean up the incomplete worktree", err, output)
	}
	if output, err := runExperimentGit(ctx, repositoryRoot, nil, "branch", "-D", branch); err != nil {
		result = errors.Join(result, gitOperationError("could not clean up the incomplete branch", err, output))
	}
	return result
}
