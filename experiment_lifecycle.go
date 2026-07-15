package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type ExperimentComparison struct {
	ExperimentID string `json:"experimentId"`
	BaseCommit   string `json:"baseCommit"`
	HeadCommit   string `json:"headCommit"`
	CommitCount  int    `json:"commitCount"`
	Stat         string `json:"stat"`
	Files        string `json:"files"`
	Patch        string `json:"patch"`
}

type ExperimentReview struct {
	Experiment           Experiment           `json:"experiment"`
	Comparison           ExperimentComparison `json:"comparison"`
	TestsPassed          bool                 `json:"testsPassed"`
	TestOutput           string               `json:"testOutput"`
	AppVerified          bool                 `json:"appVerified"`
	SecretFindings       []string             `json:"secretFindings"`
	Conflicts            []string             `json:"conflicts"`
	IntegrationPath      string               `json:"integrationPath"`
	MainHead             string               `json:"mainHead"`
	ReproducibleVerified bool                 `json:"reproducibleVerified"`
}

type ExperimentCleanupInput struct {
	ExperimentID string `json:"experimentId"`
	Confirmed    bool   `json:"confirmed"`
	BackupDirty  bool   `json:"backupDirty"`
	DeleteBranch bool   `json:"deleteBranch"`
}

func (a *App) CompareExperiment(experimentID string) (ExperimentComparison, error) {
	experiment, db, err := a.loadExperiment(experimentID)
	if err != nil {
		return ExperimentComparison{}, err
	}
	_ = db
	if _, err = validateExperimentWorktree(experiment); err != nil {
		return ExperimentComparison{}, err
	}
	head, err := gitText(a.context(), experiment.WorktreePath, nil, "rev-parse", "HEAD")
	if err != nil {
		return ExperimentComparison{}, err
	}
	stat, err := gitText(a.context(), experiment.WorktreePath, nil, "diff", "--stat", experiment.BaseCommit)
	if err != nil {
		return ExperimentComparison{}, err
	}
	files, err := gitText(a.context(), experiment.WorktreePath, nil, "diff", "--name-status", experiment.BaseCommit)
	if err != nil {
		return ExperimentComparison{}, err
	}
	patch, err := gitText(a.context(), experiment.WorktreePath, nil, "diff", "--no-ext-diff", "--unified=3", experiment.BaseCommit)
	if err != nil {
		return ExperimentComparison{}, err
	}
	countText, _ := gitText(a.context(), experiment.WorktreePath, nil, "rev-list", "--count", experiment.BaseCommit+"..HEAD")
	var count int
	_, _ = fmt.Sscan(countText, &count)
	return ExperimentComparison{
		ExperimentID: experiment.ID, BaseCommit: experiment.BaseCommit, HeadCommit: head,
		CommitCount: count, Stat: stat, Files: files, Patch: tailText(patch, appLogLimit),
	}, nil
}

func (a *App) PrepareExperimentIntegration(experimentID string, confirmed bool) (ExperimentReview, error) {
	if !confirmed {
		return ExperimentReview{}, errors.New("confirm the review and temporary integration")
	}
	experiment, db, err := a.loadExperiment(experimentID)
	if err != nil {
		return ExperimentReview{}, err
	}
	if experiment.Status == "integrated" || experiment.Status == "discarded" || experiment.Status == "archived" {
		return ExperimentReview{}, errors.New("the experiment can no longer be prepared")
	}
	a.emitAgentEvent("experiment.review.started", experiment)
	if _, err = a.CreateExperimentCheckpoint(experiment.ID); err != nil {
		return ExperimentReview{}, err
	}
	comparison, err := a.CompareExperiment(experiment.ID)
	if err != nil {
		return ExperimentReview{}, err
	}
	if comparison.CommitCount == 0 {
		return ExperimentReview{}, errors.New("the experiment has no commits to integrate")
	}
	findings := scanExperimentSecrets(comparison.Patch)
	if len(findings) > 0 {
		_, _ = a.updateExperimentStatus(db, experiment.ID, "failed")
		return ExperimentReview{Experiment: experiment, Comparison: comparison, SecretFindings: findings}, errors.New("possible secrets were detected in the change")
	}
	testOutput, testsPassed, err := a.runExperimentTests(experiment, experiment.WorktreePath)
	if err != nil {
		_ = a.storeExperimentReview(db, experiment.ID, comparison, testOutput, false, findings, "", "", "", false)
		_, _ = a.updateExperimentStatus(db, experiment.ID, "failed")
		return ExperimentReview{Experiment: experiment, Comparison: comparison, TestOutput: testOutput}, err
	}
	appVerified, err := a.verifyExperimentApp(experiment)
	if err != nil {
		_, _ = a.updateExperimentStatus(db, experiment.ID, "failed")
		return ExperimentReview{Experiment: experiment, Comparison: comparison, TestsPassed: testsPassed, TestOutput: testOutput}, err
	}
	a.emitAgentEvent("experiment.integration.preparing", experiment)
	integrationPath, mainHead, conflicts, integrationTests, err := a.prepareIntegrationWorktree(experiment)
	if err != nil {
		status := "failed"
		if len(conflicts) > 0 {
			status = "conflicted"
			a.emitAgentEvent("experiment.integration.conflicted", map[string]any{"experiment": experiment, "conflicts": conflicts, "path": integrationPath})
		}
		_ = a.storeExperimentReview(db, experiment.ID, comparison, integrationTests, false, findings, integrationPath, "", mainHead, false)
		_, _ = a.updateExperimentStatus(db, experiment.ID, status)
		return ExperimentReview{Experiment: experiment, Comparison: comparison, TestsPassed: testsPassed, TestOutput: integrationTests, AppVerified: appVerified, SecretFindings: findings, Conflicts: conflicts, IntegrationPath: integrationPath, MainHead: mainHead}, err
	}
	reproducible := experiment.Kind == "app"
	if experiment.Kind == "server" {
		var configuration experimentConfiguration
		_ = json.Unmarshal([]byte(experiment.ConfigurationJSON), &configuration)
		reproducible = configuration.Reproducible && len(configuration.ReproducibleFiles) > 0
		if !reproducible {
			return ExperimentReview{Experiment: experiment, Comparison: comparison, TestsPassed: true, TestOutput: integrationTests, AppVerified: appVerified, IntegrationPath: integrationPath, MainHead: mainHead}, errors.New("the experiment works, but contains non-reproducible manual changes")
		}
	}
	if err = a.storeExperimentReview(db, experiment.ID, comparison, integrationTests, true, findings, integrationPath, "", mainHead, reproducible); err != nil {
		return ExperimentReview{}, err
	}
	experiment, err = a.updateExperimentStatus(db, experiment.ID, "review_ready")
	if err != nil {
		return ExperimentReview{}, err
	}
	a.emitAgentEvent("experiment.review.ready", experiment)
	a.emitAgentEvent("experiment.integration.ready", experiment)
	return ExperimentReview{Experiment: experiment, Comparison: comparison, TestsPassed: true, TestOutput: integrationTests, AppVerified: appVerified, IntegrationPath: integrationPath, MainHead: mainHead, ReproducibleVerified: reproducible}, nil
}

func (a *App) RequestExperimentIntegration(experimentID string, confirmed bool) (AgentApproval, error) {
	if !confirmed {
		return AgentApproval{}, errors.New("confirm the integration request")
	}
	experiment, _, err := a.loadExperiment(experimentID)
	if err != nil {
		return AgentApproval{}, err
	}
	if experiment.Status != "review_ready" {
		return AgentApproval{}, errors.New("the experiment is not ready to integrate yet")
	}
	sessionID := "user"
	if experiment.AgentSessionID != nil {
		sessionID = *experiment.AgentSessionID
	}
	return a.requestAgentApproval(AgentTokenScope{SessionID: sessionID, ProjectID: experiment.ProjectID, ExperimentID: experiment.ID, AppID: experiment.AppID},
		"experiment.integrate", experiment.ID, experiment)
}

func (a *App) IntegrateExperiment(experimentID, approvalID string, confirmed bool) (Experiment, error) {
	if !confirmed {
		return Experiment{}, errors.New("confirm the final integration")
	}
	experiment, db, err := a.loadExperiment(experimentID)
	if err != nil {
		return Experiment{}, err
	}
	if experiment.Status != "review_ready" {
		return Experiment{}, errors.New("the experiment is not ready to integrate")
	}
	sessionID := "user"
	if experiment.AgentSessionID != nil {
		sessionID = *experiment.AgentSessionID
	}
	scope := AgentTokenScope{SessionID: sessionID, ProjectID: experiment.ProjectID, ExperimentID: experiment.ID, AppID: experiment.AppID}
	if err = a.consumeAgentApproval(scope, approvalID, "experiment.integrate", experiment.ID); err != nil {
		return Experiment{}, err
	}
	var integrationPath, expectedMain string
	if err = db.QueryRow(`SELECT integration_path, main_head FROM experiment_reviews WHERE experiment_id = ?`, experiment.ID).Scan(&integrationPath, &expectedMain); err != nil {
		return Experiment{}, errors.New("the verified temporary integration was not found")
	}
	mainPath, err := projectPathForExperiment(a.context(), db, experiment.ProjectID, "")
	if err != nil {
		return Experiment{}, err
	}
	repositoryRoot, _, err := locateExperimentRepository(a.context(), mainPath)
	if err != nil {
		return Experiment{}, err
	}
	mainHead, err := gitText(a.context(), repositoryRoot, nil, "rev-parse", "HEAD")
	if err != nil || mainHead != expectedMain {
		return Experiment{}, errors.New("Principal changed after verification; prepare the integration again")
	}
	status, _ := gitText(a.context(), repositoryRoot, nil, "status", "--porcelain")
	if status != "" {
		return Experiment{}, errors.New("Principal has unsaved changes; nothing was modified")
	}
	if conflicts, _ := gitText(a.context(), integrationPath, nil, "diff", "--name-only", "--diff-filter=U"); conflicts != "" {
		return Experiment{}, errors.New("the temporary integration still has conflicts")
	}
	if output, commitErr := runExperimentGit(a.context(), integrationPath, nil,
		"-c", "user.name=Seizen", "-c", "user.email=seizen@local", "commit", "-m", "Integrate experiment: "+experiment.Name); commitErr != nil {
		return Experiment{}, gitOperationError("could not create the integration commit", commitErr, output)
	}
	integrationCommit, err := gitText(a.context(), integrationPath, nil, "rev-parse", "HEAD")
	if err != nil {
		return Experiment{}, err
	}
	mainHead, _ = gitText(a.context(), repositoryRoot, nil, "rev-parse", "HEAD")
	if mainHead != expectedMain {
		return Experiment{}, errors.New("Principal changed before integrating; nothing was modified")
	}
	a.emitAgentEvent("experiment.integration.started", experiment)
	if output, mergeErr := runExperimentGit(a.context(), repositoryRoot, nil, "merge", "--ff-only", integrationCommit); mergeErr != nil {
		return Experiment{}, gitOperationError("could not integrate into Principal", mergeErr, output)
	}
	_, _ = db.Exec(`UPDATE experiment_reviews SET integration_commit = ?, updated_at = `+projectNow+` WHERE experiment_id = ?`, integrationCommit, experiment.ID)
	experiment, err = scanExperiment(db.QueryRow(`UPDATE experiments SET status = 'integrated', integrated_at = `+projectNow+`, updated_at = `+projectNow+`
WHERE id = ? RETURNING `+experimentColumns, experiment.ID))
	if err == nil {
		a.emitAgentEvent("experiment.status.updated", experiment)
		a.emitAgentEvent("experiment.integrated", experiment)
		_ = a.releaseIntegrationWorktree(repositoryRoot, integrationPath, experiment.ID)
	}
	return experiment, err
}

func (a *App) DiscardExperiment(input ExperimentCleanupInput) (Experiment, error) {
	if !input.Confirmed {
		return Experiment{}, errors.New("confirm discarding the experiment")
	}
	experiment, db, err := a.loadExperiment(input.ExperimentID)
	if err != nil {
		return Experiment{}, err
	}
	a.emitAgentEvent("experiment.discarding", experiment)
	if _, err = validateExperimentWorktree(experiment); err == nil {
		dirty, _ := gitText(a.context(), experiment.WorktreePath, nil, "status", "--porcelain")
		if dirty != "" && !input.BackupDirty {
			return Experiment{}, fmt.Errorf("the worktree has uncommitted changes that would be lost:\n%s", dirty)
		}
		if dirty != "" {
			if _, err = a.CreateExperimentCheckpoint(experiment.ID); err != nil {
				return Experiment{}, err
			}
		}
	}
	if err = a.cleanupExperimentResources(experiment); err != nil {
		return Experiment{}, err
	}
	if err = a.releaseExperimentWorktree(experiment, input.DeleteBranch); err != nil {
		return Experiment{}, err
	}
	experiment, err = scanExperiment(db.QueryRow(`UPDATE experiments SET status = 'discarded', discarded_at = `+projectNow+`, updated_at = `+projectNow+`
WHERE id = ? RETURNING `+experimentColumns, experiment.ID))
	if err == nil {
		a.resetSelectedExperiment(experiment)
		a.emitAgentEvent("experiment.status.updated", experiment)
		a.emitAgentEvent("experiment.discarded", experiment)
	}
	return experiment, err
}

func (a *App) ArchiveExperiment(experimentID string, confirmed bool) (Experiment, error) {
	if !confirmed {
		return Experiment{}, errors.New("confirm archiving the experiment")
	}
	experiment, db, err := a.loadExperiment(experimentID)
	if err != nil {
		return Experiment{}, err
	}
	if err = a.cleanupExperimentResources(experiment); err != nil {
		return Experiment{}, err
	}
	if _, err = validateExperimentWorktree(experiment); err == nil {
		dirty, _ := gitText(a.context(), experiment.WorktreePath, nil, "status", "--porcelain")
		if dirty != "" {
			return Experiment{}, errors.New("save or discard the uncommitted changes before archiving")
		}
		if err = a.releaseExperimentWorktree(experiment, false); err != nil {
			return Experiment{}, err
		}
	}
	experiment, err = a.updateExperimentStatus(db, experiment.ID, "archived")
	if err == nil {
		a.resetSelectedExperiment(experiment)
		a.emitAgentEvent("experiment.archived", experiment)
	}
	return experiment, err
}

func (a *App) RestoreExperiment(experimentID string, confirmed bool) (Experiment, error) {
	if !confirmed {
		return Experiment{}, errors.New("confirm restoring the experiment")
	}
	experiment, db, err := a.loadExperiment(experimentID)
	if err != nil {
		return Experiment{}, err
	}
	if experiment.Status != "archived" {
		return Experiment{}, errors.New("only an archived experiment can be restored")
	}
	mainPath, err := projectPathForExperiment(a.context(), db, experiment.ProjectID, "")
	if err != nil {
		return Experiment{}, err
	}
	repositoryRoot, relativeProject, err := locateExperimentRepository(a.context(), mainPath)
	if err != nil {
		return Experiment{}, err
	}
	root, err := managedExperimentRoot()
	if err != nil {
		return Experiment{}, err
	}
	checkout := filepath.Join(root, experiment.ProjectID, experiment.ID)
	if err = validateManagedExperimentPath(root, checkout); err != nil {
		return Experiment{}, err
	}
	if _, statErr := os.Lstat(checkout); !errors.Is(statErr, os.ErrNotExist) {
		return Experiment{}, errors.New("the experiment's managed folder already exists")
	}
	if err = os.MkdirAll(filepath.Dir(checkout), 0o700); err != nil {
		return Experiment{}, err
	}
	if output, addErr := runExperimentGit(a.context(), repositoryRoot, nil, "worktree", "add", checkout, experiment.BranchName); addErr != nil {
		return Experiment{}, gitOperationError("could not restore the worktree", addErr, output)
	}
	activePath := filepath.Join(checkout, relativeProject)
	if _, err = existingDirectory(activePath); err != nil {
		_, _ = runExperimentGit(a.context(), repositoryRoot, nil, "worktree", "remove", checkout)
		return Experiment{}, errors.New("the restored worktree does not contain the project path")
	}
	experiment, err = scanExperiment(db.QueryRow(`UPDATE experiments SET worktree_path = ?, status = 'active', updated_at = `+projectNow+`
WHERE id = ? RETURNING `+experimentColumns, activePath, experiment.ID))
	if err != nil {
		_, _ = runExperimentGit(a.context(), repositoryRoot, nil, "worktree", "remove", checkout)
		return Experiment{}, err
	}
	if err = a.cloneExperimentServer(experiment); err != nil {
		_, _ = db.Exec(`UPDATE experiments SET status = 'archived', updated_at = `+projectNow+` WHERE id = ?`, experiment.ID)
		_, _ = runExperimentGit(a.context(), repositoryRoot, nil, "worktree", "remove", checkout)
		return Experiment{}, err
	}
	a.emitAgentEvent("experiment.restored", experiment)
	a.emitAgentEvent("experiment.status.updated", experiment)
	_, err = a.SelectProjectExperiment(experiment.ProjectID, experiment.ID)
	return experiment, err
}

func (a *App) loadExperiment(id string) (Experiment, *sql.DB, error) {
	db, err := a.database.Pool(a.context())
	if err != nil {
		return Experiment{}, nil, err
	}
	experiment, err := scanExperiment(db.QueryRow(`SELECT `+experimentColumns+` FROM experiments WHERE id = ?`, strings.TrimSpace(id)))
	if errors.Is(err, sql.ErrNoRows) {
		err = errors.New("the experiment was not found")
	}
	return experiment, db, err
}

func (a *App) updateExperimentStatus(db *sql.DB, id, status string) (Experiment, error) {
	experiment, err := scanExperiment(db.QueryRow(`UPDATE experiments SET status = ?,
review_ready_at = CASE WHEN ? = 'review_ready' THEN `+projectNow+` ELSE review_ready_at END,
updated_at = `+projectNow+` WHERE id = ? RETURNING `+experimentColumns, status, status, id))
	if err == nil {
		a.emitAgentEvent("experiment.status.updated", experiment)
	}
	return experiment, err
}

func (a *App) runExperimentTests(experiment Experiment, projectPath string) (string, bool, error) {
	var configuration experimentConfiguration
	if err := json.Unmarshal([]byte(experiment.ConfigurationJSON), &configuration); err != nil || configuration.App == nil {
		return "", false, errors.New("the App configuration is not valid")
	}
	command := strings.TrimSpace(configuration.App.TestCommand)
	if command == "" {
		return "No test command configured.", true, nil
	}
	working := projectPath
	mainPath, err := projectPathForExperiment(a.context(), mustDatabase(a), experiment.ProjectID, "")
	if err == nil {
		if mainWorking, resolveErr := existingDirectory(configuration.App.WorkingDirectory); resolveErr == nil {
			if relative, relErr := filepath.Rel(mainPath, mainWorking); relErr == nil {
				working = filepath.Join(projectPath, relative)
			}
		}
	}
	output, err := runReviewCommand(working, command, 10*time.Minute)
	if err != nil {
		return output, false, fmt.Errorf("tests failed: %w", err)
	}
	return output, true, nil
}

func mustDatabase(a *App) *sql.DB {
	db, _ := a.database.Pool(a.context())
	return db
}

func runReviewCommand(directory, command string, timeout time.Duration) (string, error) {
	spec, err := shellProcessSpec(directory, command)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	process := exec.CommandContext(ctx, spec.Path, spec.Args...)
	process.Dir, process.Env = spec.Dir, spec.Env
	logs := &appLogRing{}
	process.Stdout, process.Stderr = logs, logs
	err = process.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return logs.String(), errors.New("tests exceeded the maximum time")
	}
	return logs.String(), err
}

func (a *App) verifyExperimentApp(experiment Experiment) (bool, error) {
	status, err := a.GetAppStatusContext(experiment.AppID, experiment.ID)
	if err != nil {
		return false, err
	}
	started := false
	if status.App.Status == "stopped" || status.App.Status == "failed" {
		if _, err = a.StartAppContext(experiment.AppID, experiment.ID); err != nil {
			return false, err
		}
		started = true
	}
	deadline := time.Now().Add(appHealthTimeout)
	for time.Now().Before(deadline) {
		status, err = a.GetAppStatusContext(experiment.AppID, experiment.ID)
		if err != nil {
			return false, err
		}
		if status.App.Status == "running" && status.ProcessAlive && (status.App.Kind != "web" || status.HealthcheckPassed) {
			return true, nil
		}
		if status.App.Status == "failed" || status.App.Status == "stopped" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if started {
		_, _ = a.StopAppContext(experiment.AppID, experiment.ID)
	}
	return false, errors.New("the experimental App did not pass verification")
}

func (a *App) prepareIntegrationWorktree(experiment Experiment) (string, string, []string, string, error) {
	db := mustDatabase(a)
	mainPath, err := projectPathForExperiment(a.context(), db, experiment.ProjectID, "")
	if err != nil {
		return "", "", nil, "", err
	}
	repositoryRoot, relativeProject, err := locateExperimentRepository(a.context(), mainPath)
	if err != nil {
		return "", "", nil, "", err
	}
	mainHead, err := gitText(a.context(), repositoryRoot, nil, "rev-parse", "HEAD")
	if err != nil {
		return "", "", nil, "", err
	}
	root, err := managedIntegrationRoot()
	if err != nil {
		return "", "", nil, "", err
	}
	checkout := filepath.Join(root, experiment.ProjectID, experiment.ID)
	if metadata, statErr := os.Stat(checkout); statErr == nil && metadata.IsDir() {
		_ = a.releaseIntegrationWorktree(repositoryRoot, filepath.Join(checkout, relativeProject), experiment.ID)
	}
	if err = os.MkdirAll(filepath.Dir(checkout), 0o700); err != nil {
		return "", "", nil, "", err
	}
	branch := "seizen/integration/" + experiment.ID[:8]
	if output, addErr := runExperimentGit(a.context(), repositoryRoot, nil, "worktree", "add", "-b", branch, checkout, mainHead); addErr != nil {
		return "", mainHead, nil, "", gitOperationError("could not create the temporary integration", addErr, output)
	}
	projectPath := filepath.Join(checkout, relativeProject)
	if output, mergeErr := runExperimentGit(a.context(), checkout, nil,
		"-c", "user.name=Seizen", "-c", "user.email=seizen@local",
		"merge", "--no-commit", "--no-ff", experiment.BranchName); mergeErr != nil {
		conflictText, _ := gitText(a.context(), checkout, nil, "diff", "--name-only", "--diff-filter=U")
		conflicts := splitNonemptyLines(conflictText)
		return projectPath, mainHead, conflicts, string(output), gitOperationError("the temporary integration has conflicts", mergeErr, output)
	}
	stagedPatch, _ := gitText(a.context(), checkout, nil, "diff", "--cached", "--unified=0")
	if findings := scanExperimentSecrets(stagedPatch); len(findings) > 0 {
		return projectPath, mainHead, nil, strings.Join(findings, "\n"), errors.New("the temporary integration contains possible secrets")
	}
	testOutput, _, err := a.runExperimentTests(experiment, projectPath)
	if err == nil {
		err = a.verifyIntegrationApp(experiment, projectPath)
	}
	return projectPath, mainHead, nil, testOutput, err
}

func (a *App) verifyIntegrationApp(experiment Experiment, projectPath string) (resultErr error) {
	configuration, err := decodeExperimentConfiguration(experiment)
	if err != nil || configuration.App == nil {
		return errors.New("the App configuration is not valid")
	}
	db := mustDatabase(a)
	app, err := contextualProjectApp(a.context(), db, experiment.AppID, experiment.ID)
	if err != nil {
		return err
	}
	mainPath, err := projectPathForExperiment(a.context(), db, experiment.ProjectID, "")
	if err != nil {
		return err
	}
	if mainWorking, resolveErr := existingDirectory(configuration.App.WorkingDirectory); resolveErr == nil {
		if relative, relErr := filepath.Rel(mainPath, mainWorking); relErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			app.WorkingDirectory = filepath.Join(projectPath, relative)
		}
	}
	status, err := a.GetAppStatusContext(experiment.AppID, experiment.ID)
	wasRunning := err == nil && status.ProcessAlive
	if wasRunning {
		if _, err = a.StopAppContext(experiment.AppID, experiment.ID); err != nil {
			return err
		}
		defer func() {
			if _, restartErr := a.StartAppContext(experiment.AppID, experiment.ID); restartErr != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("could not restore the experimental App: %w", restartErr))
			}
		}()
	}
	manager := a.projectAppRuntimeManager()
	spec, err := appProcessSpec(app)
	if err != nil {
		return err
	}
	logs := &appLogRing{}
	process, err := manager.starter(spec, logs)
	if err != nil {
		return fmt.Errorf("the integration App could not start: %w", err)
	}
	done := make(chan error, 1)
	processDone := make(chan struct{})
	go func() {
		defer close(processDone)
		exitCode, waitErr := process.Wait()
		if waitErr != nil || exitCode != 0 {
			done <- fmt.Errorf("the integration App exited with code %d: %w", exitCode, waitErr)
			return
		}
		done <- errors.New("the integration App exited before it could be verified")
	}()
	defer func() {
		_ = process.Stop()
		select {
		case <-processDone:
		case <-time.After(2 * time.Second):
			resultErr = errors.Join(resultErr, errors.New("the integration App did not stop"))
		}
	}()
	if process.PID() <= 0 {
		return errors.New("the integration App did not produce a valid process")
	}
	if app.Kind != "web" {
		select {
		case err = <-done:
			return err
		case <-time.After(150 * time.Millisecond):
			return nil
		}
	}
	readyURL := app.HealthcheckURL
	if readyURL == "" {
		readyURL = app.PreviewURL
	}
	ctx, cancel := context.WithTimeout(a.context(), appHealthTimeout)
	defer cancel()
	if readyURL == "" {
		_, err = manager.waitForManagedPort(ctx, process, processDone)
	} else {
		err = manager.waitForHealth(ctx, readyURL, processDone)
	}
	if err != nil {
		return fmt.Errorf("the integration App did not pass the healthcheck: %w; %s", err, logs.String())
	}
	return nil
}

func managedIntegrationRoot() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "Seizen", "integrations"), nil
}

func locateExperimentRepository(ctx context.Context, projectPath string) (string, string, error) {
	root, err := gitText(ctx, projectPath, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", errors.New("the project is not a valid Git repository")
	}
	root, err = existingDirectory(root)
	if err != nil {
		return "", "", err
	}
	relative, err := filepath.Rel(root, projectPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", errors.New("the project is outside its Git repository")
	}
	if relative == "." {
		relative = ""
	}
	return root, relative, nil
}

func (a *App) storeExperimentReview(db *sql.DB, id string, comparison ExperimentComparison, testOutput string, passed bool, findings []string, integrationPath, integrationCommit, mainHead string, reproducible bool) error {
	comparisonJSON, _ := json.Marshal(comparison)
	testsJSON, _ := json.Marshal(map[string]any{"passed": passed, "output": testOutput})
	secretsJSON, _ := json.Marshal(findings)
	_, err := db.Exec(`INSERT INTO experiment_reviews
(experiment_id, comparison_json, tests_json, secrets_json, integration_path, integration_commit, main_head, reproducible_verified, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, `+projectNow+`) ON CONFLICT (experiment_id) DO UPDATE SET
comparison_json=excluded.comparison_json, tests_json=excluded.tests_json, secrets_json=excluded.secrets_json,
integration_path=excluded.integration_path, integration_commit=excluded.integration_commit, main_head=excluded.main_head,
reproducible_verified=excluded.reproducible_verified, updated_at=excluded.updated_at`, id, comparisonJSON, testsJSON,
		secretsJSON, integrationPath, integrationCommit, mainHead, reproducible)
	return err
}

var experimentSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`-----BEGIN (?:RSA |OPENSSH |EC |DSA )?PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)(?:api[_-]?key|client[_-]?secret|password)\s*[:=]\s*["'][^"']{8,}["']`),
}

func scanExperimentSecrets(patch string) []string {
	findings := make([]string, 0)
	for index, line := range strings.Split(patch, "\n") {
		if !strings.HasPrefix(line, "+") || strings.HasPrefix(line, "+++") {
			continue
		}
		for _, pattern := range experimentSecretPatterns {
			if pattern.MatchString(line) {
				findings = append(findings, fmt.Sprintf("added line %d matches %s", index+1, pattern.String()))
				break
			}
		}
	}
	return findings
}

func splitNonemptyLines(value string) []string {
	result := make([]string, 0)
	for _, line := range strings.Split(value, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			result = append(result, line)
		}
	}
	return result
}

func (a *App) cleanupExperimentResources(experiment Experiment) error {
	var cleanupErr error
	if manager := a.projectAppRuntimeManager(); manager != nil {
		_, err := manager.StopAppContext(a.context(), experiment.AppID, experiment.ID)
		if err != nil && !strings.Contains(err.Error(), "was not found") {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	db, err := a.database.Pool(a.context())
	if err == nil {
		rows, queryErr := db.Query(`SELECT id, status FROM servers WHERE project_id = ? AND experiment_id = ?`, experiment.ProjectID, experiment.ID)
		if queryErr == nil {
			var ids []string
			for rows.Next() {
				var id, status string
				_ = rows.Scan(&id, &status)
				ids = append(ids, id)
			}
			_ = rows.Close()
			for _, id := range ids {
				server, loadErr := getServer(a.context(), db, id)
				if loadErr == nil && (server.Status == "running" || server.Status == "degraded" || server.Status == "starting") {
					_, _ = a.StopServer(id)
				}
				cleanupErr = errors.Join(cleanupErr, a.DestroyServer(id))
			}
		}
	}
	if manager := a.currentTerminalManager(); manager != nil {
		manager.stopExperiment(experiment.ProjectID, experiment.ID)
	}
	a.ensureAgentTokenStore().RevokeExperiment(experiment.ProjectID, experiment.ID)
	return cleanupErr
}

func (a *App) releaseExperimentWorktree(experiment Experiment, deleteBranch bool) error {
	mainPath, err := projectPathForExperiment(a.context(), mustDatabase(a), experiment.ProjectID, "")
	if err != nil {
		return err
	}
	repositoryRoot, _, err := locateExperimentRepository(a.context(), mainPath)
	if err != nil {
		return err
	}
	checkoutRoot, err := validateExperimentWorktree(experiment)
	if err != nil {
		if os.IsNotExist(err) || strings.Contains(err.Error(), "is not available") {
			return nil
		}
		return err
	}
	status, _ := gitText(a.context(), checkoutRoot, nil, "status", "--porcelain")
	if status != "" {
		return errors.New("the worktree still has unsaved changes")
	}
	if output, removeErr := runExperimentGit(a.context(), repositoryRoot, nil, "worktree", "remove", checkoutRoot); removeErr != nil {
		return gitOperationError("could not delete the worktree", removeErr, output)
	}
	if deleteBranch {
		if output, branchErr := runExperimentGit(a.context(), repositoryRoot, nil, "branch", "-D", experiment.BranchName); branchErr != nil {
			return gitOperationError("could not delete the confirmed branch", branchErr, output)
		}
	}
	return nil
}

func (a *App) releaseIntegrationWorktree(repositoryRoot, projectPath, experimentID string) error {
	checkout := projectPath
	if root, err := gitText(a.context(), projectPath, nil, "rev-parse", "--show-toplevel"); err == nil {
		checkout = root
	}
	managedRoot, err := managedIntegrationRoot()
	if err != nil {
		return err
	}
	if err = validateManagedExperimentPath(managedRoot, checkout); err != nil {
		return errors.New("the temporary integration is outside the managed folder")
	}
	_, _ = runExperimentGit(a.context(), checkout, nil, "merge", "--abort")
	output, err := runExperimentGit(a.context(), repositoryRoot, nil, "worktree", "remove", "--force", checkout)
	if err != nil {
		return gitOperationError("could not release the temporary integration", err, output)
	}
	_, _ = runExperimentGit(a.context(), repositoryRoot, nil, "branch", "-D", "seizen/integration/"+experimentID[:8])
	return nil
}

func (a *App) resetSelectedExperiment(experiment Experiment) {
	db, err := a.database.Pool(a.context())
	if err != nil {
		return
	}
	_, _ = db.Exec(`UPDATE project_contexts SET experiment_id = NULL, updated_at = `+projectNow+` WHERE project_id = ? AND experiment_id = ?`, experiment.ProjectID, experiment.ID)
}
