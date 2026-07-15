package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

func appRuntimeKey(appID, experimentID string) string {
	if experimentID == "" {
		return appID
	}
	return appID + "\x00" + experimentID
}

func contextualProjectApp(ctx context.Context, db *sql.DB, id, experimentID string) (ProjectApp, error) {
	app, err := getProjectApp(ctx, db, strings.TrimSpace(id))
	if err != nil || strings.TrimSpace(experimentID) == "" {
		return app, err
	}
	experimentID = strings.TrimSpace(experimentID)
	var configurationJSON string
	if err = db.QueryRowContext(ctx, `SELECT configuration_json FROM experiments
WHERE id = ? AND project_id = ? AND app_id = ? AND status NOT IN ('discarded', 'archived')`,
		experimentID, app.ProjectID, app.ID).Scan(&configurationJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProjectApp{}, errors.New("the experiment does not belong to the App or is no longer active")
		}
		return ProjectApp{}, err
	}
	var configuration experimentConfiguration
	if err = json.Unmarshal([]byte(configurationJSON), &configuration); err != nil {
		return ProjectApp{}, errors.New("the experiment configuration is not valid")
	}
	if input := configuration.App; input != nil {
		app.Name, app.Kind, app.WorkingDirectory = input.Name, input.Kind, input.WorkingDirectory
		app.StartCommand, app.StopCommand, app.TestCommand = input.StartCommand, input.StopCommand, input.TestCommand
		app.Executable, app.ArgumentsJSON = input.Executable, input.ArgumentsJSON
		app.PreviewURL, app.HealthcheckURL = input.PreviewURL, input.HealthcheckURL
	}
	mainRoot, err := projectPathForExperiment(ctx, db, app.ProjectID, "")
	if err != nil {
		return ProjectApp{}, err
	}
	experimentRoot, err := projectPathForExperiment(ctx, db, app.ProjectID, experimentID)
	if err != nil {
		return ProjectApp{}, err
	}
	mainWorking, err := existingDirectory(app.WorkingDirectory)
	if err != nil {
		return ProjectApp{}, err
	}
	relative, err := filepath.Rel(mainRoot, mainWorking)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return ProjectApp{}, errors.New("the App's directory is not inside the project")
	}
	app.WorkingDirectory, err = existingDirectory(filepath.Join(experimentRoot, relative))
	if err != nil {
		return ProjectApp{}, fmt.Errorf("the App's directory does not exist in the experiment: %w", err)
	}
	app.ExperimentID = experimentID
	app.Status = "stopped"
	var preview string
	err = db.QueryRowContext(ctx, `SELECT status, preview_url FROM app_runs
WHERE app_id = ? AND experiment_id = ? ORDER BY started_at DESC LIMIT 1`, app.ID, experimentID).Scan(&app.Status, &preview)
	if err == nil {
		app.PreviewURL = preview
	} else if !errors.Is(err, sql.ErrNoRows) {
		return ProjectApp{}, err
	}
	return app, nil
}

func (manager *AppRuntimeManager) StartAppContext(ctx context.Context, id, experimentID string) (ProjectApp, error) {
	experimentID = strings.TrimSpace(experimentID)
	if experimentID == "" {
		return manager.StartApp(ctx, id)
	}
	db, err := manager.database.Pool(ctx)
	if err != nil {
		return ProjectApp{}, err
	}
	app, err := contextualProjectApp(ctx, db, id, experimentID)
	if err != nil {
		return ProjectApp{}, err
	}
	key := appRuntimeKey(app.ID, experimentID)
	manager.mu.Lock()
	_, exists := manager.processes[key]
	manager.mu.Unlock()
	if exists {
		return ProjectApp{}, errors.New("the App is already running in this context")
	}
	runID, err := newUUID()
	if err != nil {
		return ProjectApp{}, err
	}
	app, err = beginAppRunContext(ctx, db, app, experimentID, runID)
	if err != nil {
		return ProjectApp{}, err
	}
	launchContext, cancel := context.WithCancel(context.Background())
	logs := &appLogRing{}
	session := &appRuntimeSession{
		appID: app.ID, projectID: app.ProjectID, experimentID: experimentID,
		key: key, runID: runID, logs: logs, cancel: cancel, done: make(chan struct{}),
	}
	manager.mu.Lock()
	if _, exists = manager.processes[key]; exists {
		manager.mu.Unlock()
		cancel()
		_, _ = setAppRuntimeStateContext(context.Background(), db, app.ID, experimentID, "stopped")
		return ProjectApp{}, errors.New("the App is already running in this context")
	}
	manager.processes[key] = session
	manager.logs[key] = logs
	manager.mu.Unlock()
	manager.emit("app.starting", app)
	go manager.launch(launchContext, session, app)
	return app, nil
}

func (manager *AppRuntimeManager) StopAppContext(ctx context.Context, id, experimentID string) (ProjectApp, error) {
	experimentID = strings.TrimSpace(experimentID)
	if experimentID == "" {
		return manager.StopApp(ctx, id)
	}
	db, err := manager.database.Pool(ctx)
	if err != nil {
		return ProjectApp{}, err
	}
	app, err := contextualProjectApp(ctx, db, id, experimentID)
	if err != nil {
		return ProjectApp{}, err
	}
	key := appRuntimeKey(app.ID, experimentID)
	manager.mu.Lock()
	session, auxiliary := manager.processes[key], manager.auxiliary[key]
	manager.mu.Unlock()
	if session == nil {
		if auxiliary != nil {
			_ = stopAppAuxiliary(ctx, auxiliary)
		}
		stopped, stateErr := setAppRuntimeStateContext(ctx, db, app.ID, experimentID, "stopped")
		if stateErr == nil {
			manager.emit("app.stopped", stopped)
		}
		return stopped, stateErr
	}
	stopping, err := setAppRuntimeStateContext(ctx, db, app.ID, experimentID, "stopping")
	if err != nil {
		return ProjectApp{}, err
	}
	manager.emit("app.stopping", stopping)
	auxiliaryErr := stopAppAuxiliary(ctx, auxiliary)
	process := session.requestStop("")
	if app.StopCommand != "" {
		_ = manager.runOneShot(app.WorkingDirectory, app.StopCommand, session.logs, appCommandStopTimeout)
	}
	if process != nil {
		_ = process.Stop()
	}
	select {
	case <-session.done:
	case <-ctx.Done():
		return ProjectApp{}, ctx.Err()
	case <-time.After(appProcessStopTimeout):
		return ProjectApp{}, errors.New("the App did not stop within the expected time")
	}
	app, err = contextualProjectApp(ctx, db, id, experimentID)
	return app, errors.Join(err, auxiliaryErr)
}

func (manager *AppRuntimeManager) RestartAppContext(ctx context.Context, id, experimentID string) (ProjectApp, error) {
	if _, err := manager.StopAppContext(ctx, id, experimentID); err != nil {
		return ProjectApp{}, err
	}
	return manager.StartAppContext(ctx, id, experimentID)
}

func (manager *AppRuntimeManager) GetAppStatusContext(ctx context.Context, id, experimentID string) (AppRuntimeStatus, error) {
	experimentID = strings.TrimSpace(experimentID)
	if experimentID == "" {
		return manager.GetAppStatus(ctx, id)
	}
	db, err := manager.database.Pool(ctx)
	if err != nil {
		return AppRuntimeStatus{}, err
	}
	app, err := contextualProjectApp(ctx, db, id, experimentID)
	if err != nil {
		return AppRuntimeStatus{}, err
	}
	key := appRuntimeKey(app.ID, experimentID)
	manager.mu.Lock()
	session := manager.processes[key]
	manager.mu.Unlock()
	status := AppRuntimeStatus{App: app, ExperimentID: experimentID}
	if session != nil {
		process, _, _, health := session.snapshot()
		status.HealthcheckPassed = health
		if process != nil {
			status.PID = process.PID()
			status.RuntimeReference = fmt.Sprint(status.PID)
			status.ProcessAlive = true
		}
		return status, nil
	}
	if app.Status == "starting" || app.Status == "running" || app.Status == "testing" || app.Status == "stopping" {
		app, err = setAppRuntimeStateContext(ctx, db, id, experimentID, "failed")
		if err != nil {
			return AppRuntimeStatus{}, err
		}
		status.App = app
		manager.emit("app.failed", app)
	}
	return status, nil
}

func (manager *AppRuntimeManager) SetPreviewURLContext(ctx context.Context, id, experimentID, rawURL string) (ProjectApp, error) {
	experimentID, rawURL = strings.TrimSpace(experimentID), strings.TrimSpace(rawURL)
	if experimentID == "" {
		return manager.SetPreviewURL(ctx, id, rawURL)
	}
	if err := validateLocalAppURL(rawURL); err != nil {
		return ProjectApp{}, err
	}
	db, err := manager.database.Pool(ctx)
	if err != nil {
		return ProjectApp{}, err
	}
	app, err := contextualProjectApp(ctx, db, id, experimentID)
	if err != nil {
		return ProjectApp{}, err
	}
	_, err = db.ExecContext(ctx, `UPDATE app_runs SET preview_url = ? WHERE id = (
SELECT id FROM app_runs WHERE app_id = ? AND experiment_id = ? ORDER BY started_at DESC LIMIT 1)`, rawURL, app.ID, experimentID)
	if err != nil {
		return ProjectApp{}, err
	}
	app.PreviewURL = rawURL
	manager.emit("app.preview.updated", app)
	return app, nil
}

func (manager *AppRuntimeManager) RunAppTestsContext(ctx context.Context, id, experimentID string) (ProjectApp, error) {
	experimentID = strings.TrimSpace(experimentID)
	if experimentID == "" {
		return manager.RunAppTests(ctx, id)
	}
	db, err := manager.database.Pool(ctx)
	if err != nil {
		return ProjectApp{}, err
	}
	app, err := contextualProjectApp(ctx, db, id, experimentID)
	if err != nil {
		return ProjectApp{}, err
	}
	if app.TestCommand == "" {
		return ProjectApp{}, errors.New("the App does not have a test command")
	}
	key := appRuntimeKey(app.ID, experimentID)
	manager.mu.Lock()
	session := manager.processes[key]
	if session == nil || app.Status != "running" {
		manager.mu.Unlock()
		return ProjectApp{}, errors.New("the App must be running to test it")
	}
	if manager.auxiliary[key] != nil {
		manager.mu.Unlock()
		return ProjectApp{}, errors.New("the tests are already running")
	}
	auxiliary := &appAuxiliaryProcess{done: make(chan struct{})}
	manager.auxiliary[key] = auxiliary
	manager.mu.Unlock()
	release := func() {
		manager.mu.Lock()
		if manager.auxiliary[key] == auxiliary {
			delete(manager.auxiliary, key)
		}
		manager.mu.Unlock()
		auxiliary.finish()
	}
	testingApp, err := setAppRuntimeStateContext(ctx, db, id, experimentID, "testing")
	if err != nil {
		release()
		return ProjectApp{}, err
	}
	manager.emit("app.testing", testingApp)
	spec, err := shellProcessSpec(app.WorkingDirectory, app.TestCommand)
	if err != nil {
		release()
		_, _, _ = restoreAppRunningAfterTestsContext(ctx, db, id, experimentID)
		return ProjectApp{}, err
	}
	process, err := manager.starter(spec, session.logs)
	if err != nil {
		release()
		_, _, _ = restoreAppRunningAfterTestsContext(ctx, db, id, experimentID)
		return ProjectApp{}, err
	}
	if !auxiliary.install(process) {
		_ = process.Stop()
	}
	manager.emit("app.test.started", testingApp)
	exitCode, waitErr := process.Wait()
	release()
	manager.mu.Lock()
	_, stillRunning := manager.processes[key]
	manager.mu.Unlock()
	if stillRunning {
		var changed bool
		app, changed, _ = restoreAppRunningAfterTestsContext(context.Background(), db, id, experimentID)
		if changed {
			manager.emit("app.running", app)
		}
	} else {
		app, _ = contextualProjectApp(context.Background(), db, id, experimentID)
	}
	completion := map[string]any{"projectId": app.ProjectID, "appId": id, "experimentId": experimentID, "exitCode": exitCode, "status": "passed"}
	if waitErr != nil || exitCode != 0 {
		completion["status"] = "failed"
		if waitErr != nil {
			completion["error"] = waitErr.Error()
		}
	}
	manager.emit("app.test.completed", completion)
	if waitErr != nil || exitCode != 0 {
		return app, fmt.Errorf("the tests finished with code %d: %w", exitCode, waitErr)
	}
	return app, nil
}

func (manager *AppRuntimeManager) GetAppLogsContext(id, experimentID string) string {
	manager.mu.Lock()
	logs := manager.logs[appRuntimeKey(id, strings.TrimSpace(experimentID))]
	manager.mu.Unlock()
	if logs == nil {
		return ""
	}
	return logs.String()
}

func (manager *AppRuntimeManager) ownsPortContext(id, experimentID string, port int) bool {
	manager.mu.Lock()
	session := manager.processes[appRuntimeKey(id, strings.TrimSpace(experimentID))]
	manager.mu.Unlock()
	if session == nil {
		return false
	}
	process, _, _, _ := session.snapshot()
	if process == nil {
		return false
	}
	if known, owns := platformManagedAppOwnsPort(process, port); known {
		return owns
	}
	return canConnectLocalPort("127.0.0.1", port)
}

func beginAppRunContext(ctx context.Context, db *sql.DB, app ProjectApp, experimentID, runID string) (ProjectApp, error) {
	if experimentID == "" {
		return beginAppRun(ctx, db, app.ID, runID)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ProjectApp{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `UPDATE app_runs SET status = 'failed', stopped_at = `+projectNow+`, error_message = 'previous runtime unavailable'
WHERE app_id = ? AND experiment_id = ? AND stopped_at IS NULL`, app.ID, experimentID)
	if err == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO app_runs
(id, project_id, app_id, experiment_id, target, runtime_provider, status, preview_url, started_at)
VALUES (?, ?, ?, ?, 'development', 'local', 'starting', ?, `+projectNow+`)`, runID, app.ProjectID, app.ID, experimentID, app.PreviewURL)
	}
	if err == nil {
		err = tx.Commit()
	}
	app.Status = "starting"
	return app, err
}

func markAppRunningContext(ctx context.Context, db *sql.DB, id, experimentID, runID, runtimeReference string) (ProjectApp, bool) {
	if experimentID == "" {
		return markAppRunning(ctx, db, id, runID, runtimeReference)
	}
	result, err := db.ExecContext(ctx, `UPDATE app_runs SET status = 'running', runtime_reference = ?
WHERE id = ? AND app_id = ? AND experiment_id = ? AND status = 'starting'`, runtimeReference, runID, id, experimentID)
	if err != nil {
		return ProjectApp{}, false
	}
	changed, _ := result.RowsAffected()
	app, loadErr := contextualProjectApp(ctx, db, id, experimentID)
	return app, changed == 1 && loadErr == nil
}

func setAppRuntimeStateContext(ctx context.Context, db *sql.DB, id, experimentID, status string) (ProjectApp, error) {
	if experimentID == "" {
		return setAppRuntimeState(ctx, db, id, status)
	}
	app, err := contextualProjectApp(ctx, db, id, experimentID)
	if err != nil {
		return ProjectApp{}, err
	}
	_, err = db.ExecContext(ctx, `UPDATE app_runs SET status = ?,
stopped_at = CASE WHEN ? IN ('stopped', 'failed') THEN `+projectNow+` ELSE stopped_at END
WHERE id = (SELECT id FROM app_runs WHERE app_id = ? AND experiment_id = ? ORDER BY started_at DESC LIMIT 1)`,
		status, status, id, experimentID)
	if err != nil {
		return ProjectApp{}, err
	}
	app.Status = status
	return app, nil
}

func restoreAppRunningAfterTestsContext(ctx context.Context, db *sql.DB, id, experimentID string) (ProjectApp, bool, error) {
	if experimentID == "" {
		return restoreAppRunningAfterTests(ctx, db, id)
	}
	result, err := db.ExecContext(ctx, `UPDATE app_runs SET status = 'running'
WHERE id = (SELECT id FROM app_runs WHERE app_id = ? AND experiment_id = ? ORDER BY started_at DESC LIMIT 1) AND status = 'testing'`, id, experimentID)
	if err != nil {
		return ProjectApp{}, false, err
	}
	changed, _ := result.RowsAffected()
	app, err := contextualProjectApp(ctx, db, id, experimentID)
	return app, changed == 1, err
}
