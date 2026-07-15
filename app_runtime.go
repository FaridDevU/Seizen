package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	appLogLimit           = 256 * 1024
	appHealthTimeout      = 30 * time.Second
	appHealthPoll         = 300 * time.Millisecond
	appProcessStopTimeout = 8 * time.Second
	appCommandStopTimeout = 5 * time.Second
)

type managedProcessSpec struct {
	Path       string
	Args       []string
	Dir        string
	Env        []string
	HideWindow bool
}

type managedProcess interface {
	PID() int
	Wait() (int, error)
	Stop() error
}

type managedProcessStarter func(managedProcessSpec, io.Writer) (managedProcess, error)

type AppRuntimeStatus struct {
	App               ProjectApp `json:"app"`
	ExperimentID      string     `json:"experimentId,omitempty"`
	RuntimeReference  string     `json:"runtimeReference"`
	PID               int        `json:"pid"`
	ProcessAlive      bool       `json:"processAlive"`
	HealthcheckPassed bool       `json:"healthcheckPassed"`
}

type appRuntimeSession struct {
	mu                sync.Mutex
	finishOnce        sync.Once
	appID             string
	projectID         string
	experimentID      string
	key               string
	runID             string
	process           managedProcess
	logs              *appLogRing
	cancel            context.CancelFunc
	done              chan struct{}
	stopRequested     bool
	failureMessage    string
	healthcheckPassed bool
}

type appAuxiliaryProcess struct {
	mu            sync.Mutex
	process       managedProcess
	stopRequested bool
	done          chan struct{}
	finishOnce    sync.Once
}

func (auxiliary *appAuxiliaryProcess) install(process managedProcess) bool {
	auxiliary.mu.Lock()
	auxiliary.process = process
	stopping := auxiliary.stopRequested
	auxiliary.mu.Unlock()
	return !stopping
}

func (auxiliary *appAuxiliaryProcess) requestStop() managedProcess {
	auxiliary.mu.Lock()
	auxiliary.stopRequested = true
	process := auxiliary.process
	auxiliary.mu.Unlock()
	return process
}

func (auxiliary *appAuxiliaryProcess) finish() {
	auxiliary.finishOnce.Do(func() { close(auxiliary.done) })
}

func (session *appRuntimeSession) installProcess(process managedProcess) bool {
	session.mu.Lock()
	session.process = process
	stopping := session.stopRequested
	session.mu.Unlock()
	return !stopping
}

func (session *appRuntimeSession) requestStop(failure string) managedProcess {
	session.mu.Lock()
	session.stopRequested = true
	if failure != "" {
		session.failureMessage = failure
	}
	process := session.process
	session.cancel()
	session.mu.Unlock()
	return process
}

func (session *appRuntimeSession) snapshot() (managedProcess, bool, string, bool) {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.process, session.stopRequested, session.failureMessage, session.healthcheckPassed
}

type appLogRing struct {
	mu   sync.Mutex
	data []byte
}

func (logs *appLogRing) Write(data []byte) (int, error) {
	logs.mu.Lock()
	logs.data = append(logs.data, data...)
	if len(logs.data) > appLogLimit {
		logs.data = append([]byte(nil), logs.data[len(logs.data)-appLogLimit:]...)
	}
	logs.mu.Unlock()
	return len(data), nil
}

func (logs *appLogRing) String() string {
	logs.mu.Lock()
	defer logs.mu.Unlock()
	return strings.ToValidUTF8(string(logs.data), "\uFFFD")
}

type AppRuntimeManager struct {
	mu            sync.Mutex
	database      *Database
	processes     map[string]*appRuntimeSession
	auxiliary     map[string]*appAuxiliaryProcess
	logs          map[string]*appLogRing
	starter       managedProcessStarter
	healthClient  *http.Client
	healthTimeout time.Duration
	emit          func(string, any)
	configure     func(string, AppInput) (ProjectApp, error)
}

func newAppRuntimeManager(database *Database, emit func(string, any)) *AppRuntimeManager {
	if emit == nil {
		emit = func(string, any) {}
	}
	healthClient := &http.Client{Timeout: 2 * time.Second}
	healthClient.CheckRedirect = func(request *http.Request, _ []*http.Request) error {
		return validateLocalAppURL(request.URL.String())
	}
	return &AppRuntimeManager{
		database:      database,
		processes:     make(map[string]*appRuntimeSession),
		auxiliary:     make(map[string]*appAuxiliaryProcess),
		logs:          make(map[string]*appLogRing),
		starter:       startPlatformManagedProcess,
		healthClient:  healthClient,
		healthTimeout: appHealthTimeout,
		emit:          emit,
	}
}

func (a *App) ConfigureApp(id string, input AppInput) (ProjectApp, error) {
	return a.projectAppRuntimeManager().ConfigureApp(id, input)
}

func (manager *AppRuntimeManager) ConfigureApp(id string, input AppInput) (ProjectApp, error) {
	if manager.configure == nil {
		return ProjectApp{}, errors.New("App storage is not available")
	}
	return manager.configure(id, input)
}

func (a *App) StartApp(id string) (ProjectApp, error) {
	return a.StartAppContext(id, "")
}

func (a *App) StartAppContext(id, experimentID string) (ProjectApp, error) {
	return a.projectAppRuntimeManager().StartAppContext(a.context(), id, experimentID)
}

func (a *App) StopApp(id string) (ProjectApp, error) {
	return a.StopAppContext(id, "")
}

func (a *App) StopAppContext(id, experimentID string) (ProjectApp, error) {
	if experimentID == "" {
		if app, handled, err := a.stopAttachedApp(a.context(), id); handled {
			return app, err
		}
	}
	return a.projectAppRuntimeManager().StopAppContext(a.context(), id, experimentID)
}

func (a *App) RestartApp(id string) (ProjectApp, error) {
	return a.RestartAppContext(id, "")
}

func (a *App) RestartAppContext(id, experimentID string) (ProjectApp, error) {
	if experimentID == "" {
		if _, handled, err := a.stopAttachedApp(a.context(), id); handled {
			if err != nil {
				return ProjectApp{}, err
			}
			return a.projectAppRuntimeManager().StartAppContext(a.context(), id, "")
		}
	}
	return a.projectAppRuntimeManager().RestartAppContext(a.context(), id, experimentID)
}

func (a *App) GetAppStatus(id string) (AppRuntimeStatus, error) {
	return a.GetAppStatusContext(id, "")
}

func (a *App) GetAppStatusContext(id, experimentID string) (AppRuntimeStatus, error) {
	if experimentID == "" {
		if status, attached, err := a.attachedAppStatus(a.context(), id); attached {
			return status, err
		}
	}
	return a.projectAppRuntimeManager().GetAppStatusContext(a.context(), id, experimentID)
}

func (a *App) SetPreviewURL(id, rawURL string) (ProjectApp, error) {
	return a.SetPreviewURLContext(id, "", rawURL)
}

func (a *App) SetPreviewURLContext(id, experimentID, rawURL string) (ProjectApp, error) {
	return a.projectAppRuntimeManager().SetPreviewURLContext(a.context(), id, experimentID, rawURL)
}

func (a *App) RunAppTests(id string) (ProjectApp, error) {
	return a.RunAppTestsContext(id, "")
}

func (a *App) RunAppTestsContext(id, experimentID string) (ProjectApp, error) {
	return a.projectAppRuntimeManager().RunAppTestsContext(a.context(), id, experimentID)
}

func (a *App) GetAppLogs(id string) (string, error) {
	return a.GetAppLogsContext(id, "")
}

func (a *App) GetAppLogsContext(id, experimentID string) (string, error) {
	db, err := a.database.Pool(a.context())
	if err != nil {
		return "", err
	}
	if _, err = contextualProjectApp(a.context(), db, id, experimentID); err != nil {
		return "", err
	}
	return a.projectAppRuntimeManager().GetAppLogsContext(id, experimentID), nil
}

func (a *App) CleanupProjectRuntime(projectID string) error {
	err := a.cleanupAttachedApps(a.context(), projectID)
	err = errors.Join(err, a.projectAppRuntimeManager().CleanupProjectRuntime(a.context(), projectID))
	if editors := a.currentEditorManager(); editors != nil {
		err = errors.Join(err, editors.stopProject(projectID))
	}
	a.ensureAgentTokenStore().RevokeProject(projectID)
	return err
}

func (a *App) PrepareRuntimeClose() error {
	attachedErr := a.cleanupAttachedApps(a.context(), "")
	a.mu.RLock()
	manager := a.appRuntimes
	a.mu.RUnlock()
	if manager == nil {
		return attachedErr
	}
	return errors.Join(attachedErr, manager.PrepareRuntimeClose(a.context()))
}

func (manager *AppRuntimeManager) StartApp(ctx context.Context, id string) (ProjectApp, error) {
	db, err := manager.database.Pool(ctx)
	if err != nil {
		return ProjectApp{}, err
	}
	manager.mu.Lock()
	if _, exists := manager.processes[id]; exists {
		manager.mu.Unlock()
		return ProjectApp{}, errors.New("the App is already running")
	}
	manager.mu.Unlock()

	runID, err := newUUID()
	if err != nil {
		return ProjectApp{}, fmt.Errorf("could not create the App run: %w", err)
	}
	app, err := beginAppRun(ctx, db, id, runID)
	if err != nil {
		return ProjectApp{}, err
	}
	launchContext, cancel := context.WithCancel(context.Background())
	logs := &appLogRing{}
	session := &appRuntimeSession{
		appID: app.ID, projectID: app.ProjectID, key: id, runID: runID,
		logs: logs, cancel: cancel, done: make(chan struct{}),
	}
	manager.mu.Lock()
	if _, exists := manager.processes[id]; exists {
		manager.mu.Unlock()
		cancel()
		_, _ = setAppRuntimeState(context.Background(), db, id, "stopped")
		return ProjectApp{}, errors.New("the App is already running")
	}
	manager.processes[id] = session
	manager.logs[id] = logs
	manager.mu.Unlock()
	manager.emit("app.starting", app)
	go manager.launch(launchContext, session, app)
	return app, nil
}

func (manager *AppRuntimeManager) launch(ctx context.Context, session *appRuntimeSession, app ProjectApp) {
	spec, err := appProcessSpec(app)
	if err != nil {
		manager.finish(session, -1, err)
		return
	}
	process, err := manager.starter(spec, session.logs)
	if err != nil {
		manager.finish(session, -1, err)
		return
	}
	if !session.installProcess(process) {
		_ = process.Stop()
	}

	db, err := manager.database.Pool(context.Background())
	if err != nil {
		session.requestStop(err.Error())
		_ = process.Stop()
		manager.finish(session, -1, err)
		return
	}
	runtimeReference := strconv.Itoa(process.PID())
	_, _ = db.Exec(`UPDATE app_runs SET runtime_reference = ? WHERE id = ?`, runtimeReference, session.runID)
	go func() {
		exitCode, waitErr := process.Wait()
		manager.finish(session, exitCode, waitErr)
	}()

	if _, stopping, _, _ := session.snapshot(); stopping {
		_ = process.Stop()
		return
	}
	if app.Kind == "web" {
		readyURL := app.HealthcheckURL
		if readyURL == "" {
			readyURL = app.PreviewURL
		}
		if readyURL == "" {
			var detectedPort int
			detectedPort, err = manager.waitForManagedPort(ctx, process, session.done)
			if err == nil {
				readyURL = fmt.Sprintf("http://127.0.0.1:%d", detectedPort)
				app.PreviewURL = readyURL
				if session.experimentID == "" {
					_, _ = db.Exec(`UPDATE apps SET preview_url = ?, updated_at = `+projectNow+` WHERE id = ?`, readyURL, app.ID)
				}
				_, _ = db.Exec(`UPDATE app_runs SET preview_url = ?, detected_port = ?, last_verified_at = `+projectNow+` WHERE id = ?`, readyURL, detectedPort, session.runID)
				manager.emit("app.port.detected", map[string]any{"projectId": app.ProjectID, "appId": app.ID, "experimentId": session.experimentID, "port": detectedPort, "previewUrl": readyURL})
				manager.emit("app.preview.updated", app)
			}
		} else if err = manager.waitForHealth(ctx, readyURL, session.done); err == nil {
			parsed, _ := url.Parse(readyURL)
			port, _ := strconv.Atoi(parsed.Port())
			if port == 0 && parsed.Scheme == "http" {
				port = 80
			} else if port == 0 {
				port = 443
			}
			if known, owns := platformManagedAppOwnsPort(process, port); known && !owns {
				err = errors.New("the verified port does not belong to the App's Job Object")
			}
		}
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				session.requestStop("the web App was not ready: " + err.Error())
			}
			_ = process.Stop()
			return
		}
		session.mu.Lock()
		session.healthcheckPassed = true
		session.mu.Unlock()
	}
	if running, changed := markAppRunningContext(context.Background(), db, session.appID, session.experimentID, session.runID, runtimeReference); changed {
		manager.emit("app.running", running)
	}
}

func (manager *AppRuntimeManager) waitForManagedPort(ctx context.Context, process managedProcess, done <-chan struct{}) (int, error) {
	timeout := manager.healthTimeout
	if timeout <= 0 {
		timeout = appHealthTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(appHealthPoll)
	defer ticker.Stop()
	for {
		known, port := platformManagedAppDetectedPort(process)
		if known && port > 0 {
			return port, nil
		}
		if !known {
			return 0, errors.New("runtime port detection is not available")
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-done:
			return 0, errors.New("the process exited before opening a port")
		case <-timer.C:
			return 0, errors.New("no managed port was detected")
		case <-ticker.C:
		}
	}
}

func (manager *AppRuntimeManager) StopApp(ctx context.Context, id string) (ProjectApp, error) {
	db, err := manager.database.Pool(ctx)
	if err != nil {
		return ProjectApp{}, err
	}
	app, err := getProjectApp(ctx, db, id)
	if err != nil {
		return ProjectApp{}, err
	}
	manager.mu.Lock()
	session := manager.processes[id]
	auxiliary := manager.auxiliary[id]
	manager.mu.Unlock()
	if session == nil {
		if auxiliary != nil {
			_ = stopAppAuxiliary(ctx, auxiliary)
		}
		if app.Status == "stopped" || app.Status == "unconfigured" {
			return app, nil
		}
		stopped, stateErr := setAppRuntimeState(ctx, db, id, "stopped")
		if stateErr == nil {
			_, _ = db.ExecContext(ctx, `UPDATE app_runs SET status = 'stopped', stopped_at = `+projectNow+`
WHERE id = (SELECT id FROM app_runs WHERE app_id = ? AND stopped_at IS NULL ORDER BY started_at DESC LIMIT 1)`, id)
			manager.emit("app.stopped", stopped)
		}
		return stopped, stateErr
	}

	stopping, err := setAppRuntimeState(ctx, db, id, "stopping")
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
	case <-time.After(appProcessStopTimeout):
		return ProjectApp{}, errors.New("the App did not stop within the expected time")
	}
	app, err = getProjectApp(ctx, db, id)
	return app, errors.Join(err, auxiliaryErr)
}

func (manager *AppRuntimeManager) RestartApp(ctx context.Context, id string) (ProjectApp, error) {
	if _, err := manager.StopApp(ctx, id); err != nil {
		return ProjectApp{}, err
	}
	return manager.StartApp(ctx, id)
}

func (manager *AppRuntimeManager) GetAppStatus(ctx context.Context, id string) (AppRuntimeStatus, error) {
	db, err := manager.database.Pool(ctx)
	if err != nil {
		return AppRuntimeStatus{}, err
	}
	app, err := getProjectApp(ctx, db, id)
	if err != nil {
		return AppRuntimeStatus{}, err
	}
	manager.mu.Lock()
	session := manager.processes[id]
	manager.mu.Unlock()
	status := AppRuntimeStatus{App: app}
	if session != nil {
		process, _, _, health := session.snapshot()
		status.HealthcheckPassed = health
		if process != nil {
			status.PID = process.PID()
			status.RuntimeReference = strconv.Itoa(status.PID)
			status.ProcessAlive = true
		}
		return status, nil
	}
	if app.Status == "starting" || app.Status == "running" || app.Status == "testing" || app.Status == "stopping" {
		app, err = setAppRuntimeState(ctx, db, id, "failed")
		if err != nil {
			return AppRuntimeStatus{}, err
		}
		status.App = app
		manager.emit("app.failed", app)
	}
	return status, nil
}

func (manager *AppRuntimeManager) SetPreviewURL(ctx context.Context, id, rawURL string) (ProjectApp, error) {
	rawURL = strings.TrimSpace(rawURL)
	if err := validateLocalAppURL(rawURL); err != nil {
		return ProjectApp{}, err
	}
	db, err := manager.database.Pool(ctx)
	if err != nil {
		return ProjectApp{}, err
	}
	app, err := scanProjectApp(db.QueryRowContext(ctx, `UPDATE apps SET preview_url = ?, updated_at = `+projectNow+`
WHERE id = ? RETURNING `+appColumns, rawURL, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectApp{}, errors.New("the App was not found")
	}
	if err != nil {
		return ProjectApp{}, fmt.Errorf("could not update the preview: %w", err)
	}
	_, _ = db.ExecContext(ctx, `UPDATE app_runs SET preview_url = ? WHERE app_id = ? AND stopped_at IS NULL`, rawURL, id)
	manager.emit("app.preview.updated", app)
	return app, nil
}

func (manager *AppRuntimeManager) RunAppTests(ctx context.Context, id string) (ProjectApp, error) {
	db, err := manager.database.Pool(ctx)
	if err != nil {
		return ProjectApp{}, err
	}
	app, err := getProjectApp(ctx, db, id)
	if err != nil {
		return ProjectApp{}, err
	}
	if app.TestCommand == "" {
		return ProjectApp{}, errors.New("the App does not have a test command")
	}
	manager.mu.Lock()
	session := manager.processes[id]
	if session == nil || app.Status != "running" {
		manager.mu.Unlock()
		return ProjectApp{}, errors.New("the App must be running to test it")
	}
	if _, testing := manager.auxiliary[id]; testing {
		manager.mu.Unlock()
		return ProjectApp{}, errors.New("the tests are already running")
	}
	auxiliary := &appAuxiliaryProcess{done: make(chan struct{})}
	manager.auxiliary[id] = auxiliary
	manager.mu.Unlock()
	release := func() {
		manager.mu.Lock()
		if manager.auxiliary[id] == auxiliary {
			delete(manager.auxiliary, id)
		}
		manager.mu.Unlock()
		auxiliary.finish()
	}
	testingApp, err := setAppRuntimeState(ctx, db, id, "testing")
	if err != nil {
		release()
		return ProjectApp{}, err
	}
	manager.emit("app.testing", testingApp)
	spec, err := shellProcessSpec(app.WorkingDirectory, app.TestCommand)
	if err != nil {
		release()
		_, _, _ = restoreAppRunningAfterTests(ctx, db, id)
		return ProjectApp{}, err
	}
	process, err := manager.starter(spec, session.logs)
	if err != nil {
		release()
		_, _, _ = restoreAppRunningAfterTests(ctx, db, id)
		return ProjectApp{}, err
	}
	if !auxiliary.install(process) {
		_ = process.Stop()
	}
	manager.emit("app.test.started", testingApp)
	exitCode, waitErr := process.Wait()
	release()
	manager.mu.Lock()
	_, stillRunning := manager.processes[id]
	manager.mu.Unlock()
	if stillRunning {
		var changed bool
		app, changed, _ = restoreAppRunningAfterTests(context.Background(), db, id)
		if changed {
			manager.emit("app.running", app)
		}
	} else {
		app, _ = getProjectApp(context.Background(), db, id)
	}
	completion := map[string]any{"projectId": app.ProjectID, "appId": id, "exitCode": exitCode, "status": "passed"}
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

func (manager *AppRuntimeManager) GetAppLogs(id string) string {
	return manager.GetAppLogsContext(id, "")
}

func (manager *AppRuntimeManager) ownsPort(id string, port int) bool {
	manager.mu.Lock()
	session := manager.processes[id]
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

func (manager *AppRuntimeManager) CleanupProjectRuntime(ctx context.Context, projectID string) error {
	manager.mu.Lock()
	sessions := make([]*appRuntimeSession, 0)
	for _, session := range manager.processes {
		if session.projectID == projectID {
			sessions = append(sessions, session)
		}
	}
	manager.mu.Unlock()
	var cleanupErr error
	for _, session := range sessions {
		if _, err := manager.StopAppContext(ctx, session.appID, session.experimentID); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	if db, err := manager.database.Pool(ctx); err == nil {
		_, err = db.ExecContext(ctx, `UPDATE apps SET status = 'stopped', updated_at = `+projectNow+`
WHERE project_id = ? AND status IN ('starting', 'running', 'testing', 'stopping')`, projectID)
		cleanupErr = errors.Join(cleanupErr, err)
	} else {
		cleanupErr = errors.Join(cleanupErr, err)
	}
	return cleanupErr
}

func (manager *AppRuntimeManager) PrepareRuntimeClose(ctx context.Context) error {
	manager.mu.Lock()
	sessions := make([]*appRuntimeSession, 0, len(manager.processes))
	for _, session := range manager.processes {
		sessions = append(sessions, session)
	}
	auxiliary := make([]*appAuxiliaryProcess, 0, len(manager.auxiliary))
	for _, process := range manager.auxiliary {
		auxiliary = append(auxiliary, process)
	}
	manager.mu.Unlock()
	for _, process := range auxiliary {
		if managed := process.requestStop(); managed != nil {
			_ = managed.Stop()
		}
	}
	var cleanupErr error
	for _, session := range sessions {
		if _, err := manager.StopAppContext(ctx, session.appID, session.experimentID); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	return cleanupErr
}

func stopAppAuxiliary(ctx context.Context, auxiliary *appAuxiliaryProcess) error {
	if auxiliary == nil {
		return nil
	}
	if process := auxiliary.requestStop(); process != nil {
		_ = process.Stop()
	}
	select {
	case <-auxiliary.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(appProcessStopTimeout):
		return errors.New("the App's tests did not stop within the expected time")
	}
}

func (manager *AppRuntimeManager) finish(session *appRuntimeSession, exitCode int, waitErr error) {
	session.finishOnce.Do(func() {
		_, stopping, failure, _ := session.snapshot()
		status := "stopped"
		errorMessage := ""
		if failure != "" {
			status, errorMessage = "failed", failure
		} else if !stopping && (waitErr != nil || exitCode != 0) {
			status = "failed"
			errorMessage = fmt.Sprintf("the process exited with code %d", exitCode)
			if waitErr != nil {
				errorMessage += ": " + waitErr.Error()
			}
		}
		db, err := manager.database.Pool(context.Background())
		var app ProjectApp
		if err == nil {
			app, err = setAppRuntimeStateContext(context.Background(), db, session.appID, session.experimentID, status)
			_, _ = db.Exec(`UPDATE app_runs SET status = ?, stopped_at = `+projectNow+`, exit_code = ?, error_message = ? WHERE id = ?`, status, exitCode, errorMessage, session.runID)
		}
		manager.mu.Lock()
		if manager.processes[session.key] == session {
			delete(manager.processes, session.key)
		}
		manager.mu.Unlock()
		session.cancel()
		close(session.done)
		if err == nil {
			manager.emit("app."+status, app)
		}
	})
}

func (manager *AppRuntimeManager) waitForHealth(ctx context.Context, rawURL string, processDone <-chan struct{}) error {
	deadline := manager.healthTimeout
	if deadline <= 0 {
		deadline = appHealthTimeout
	}
	healthContext, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	ticker := time.NewTicker(appHealthPoll)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(healthContext, http.MethodGet, rawURL, nil)
		if err != nil {
			return err
		}
		response, requestErr := manager.healthClient.Do(request)
		if requestErr == nil {
			_ = response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 400 {
				return nil
			}
		}
		select {
		case <-healthContext.Done():
			return healthContext.Err()
		case <-processDone:
			return errors.New("the process exited before the healthcheck")
		case <-ticker.C:
		}
	}
}

func (manager *AppRuntimeManager) runOneShot(directory, command string, output io.Writer, timeout time.Duration) error {
	spec, err := shellProcessSpec(directory, command)
	if err != nil {
		return err
	}
	process, err := manager.starter(spec, output)
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		exitCode, waitErr := process.Wait()
		if waitErr != nil || exitCode != 0 {
			done <- fmt.Errorf("the command finished with code %d: %w", exitCode, waitErr)
			return
		}
		done <- nil
	}()
	select {
	case err = <-done:
		return err
	case <-time.After(timeout):
		_ = process.Stop()
		select {
		case err = <-done:
			return err
		case <-time.After(2 * time.Second):
			return errors.New("the auxiliary command did not stop")
		}
	}
}

func beginAppRun(ctx context.Context, db *sql.DB, id, runID string) (ProjectApp, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ProjectApp{}, err
	}
	defer tx.Rollback()
	app, err := scanProjectApp(tx.QueryRowContext(ctx, `UPDATE apps SET status = 'starting', updated_at = `+projectNow+`
WHERE id = ? AND status IN ('stopped', 'failed') RETURNING `+appColumns, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectApp{}, errors.New("the App must be stopped to run it")
	}
	if err != nil {
		return ProjectApp{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO app_runs (id, project_id, app_id, target, runtime_provider, status, preview_url, started_at)
VALUES (?, ?, ?, 'development', 'local', 'starting', ?, `+projectNow+`)`, runID, app.ProjectID, id, app.PreviewURL)
	if err == nil {
		err = tx.Commit()
	}
	return app, err
}

func markAppRunning(ctx context.Context, db *sql.DB, id, runID, runtimeReference string) (ProjectApp, bool) {
	app, err := scanProjectApp(db.QueryRowContext(ctx, `UPDATE apps SET status = 'running', updated_at = `+projectNow+`
WHERE id = ? AND status = 'starting' RETURNING `+appColumns, id))
	if err != nil {
		return ProjectApp{}, false
	}
	_, _ = db.ExecContext(ctx, `UPDATE app_runs SET status = 'running', runtime_reference = ? WHERE id = ?`, runtimeReference, runID)
	return app, true
}

func setAppRuntimeState(ctx context.Context, db *sql.DB, id, status string) (ProjectApp, error) {
	app, err := scanProjectApp(db.QueryRowContext(ctx, `UPDATE apps SET status = ?, updated_at = `+projectNow+`
WHERE id = ? RETURNING `+appColumns, status, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectApp{}, errors.New("the App was not found")
	}
	return app, err
}

func restoreAppRunningAfterTests(ctx context.Context, db *sql.DB, id string) (ProjectApp, bool, error) {
	app, err := scanProjectApp(db.QueryRowContext(ctx, `UPDATE apps SET status = 'running', updated_at = `+projectNow+`
WHERE id = ? AND status = 'testing' RETURNING `+appColumns, id))
	if errors.Is(err, sql.ErrNoRows) {
		app, err = getProjectApp(ctx, db, id)
		return app, false, err
	}
	return app, err == nil, err
}

func getProjectApp(ctx context.Context, db *sql.DB, id string) (ProjectApp, error) {
	app, err := scanProjectApp(db.QueryRowContext(ctx, `SELECT `+appColumns+` FROM apps WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectApp{}, errors.New("the App was not found")
	}
	return app, err
}

func appProcessSpec(app ProjectApp) (managedProcessSpec, error) {
	if app.Kind == "desktop" && app.Executable != "" {
		var arguments []string
		if err := json.Unmarshal([]byte(app.ArgumentsJSON), &arguments); err != nil {
			return managedProcessSpec{}, errors.New("the App's arguments are not valid")
		}
		executable := app.Executable
		if !filepath.IsAbs(executable) {
			executable = filepath.Join(app.WorkingDirectory, executable)
		}
		return managedProcessSpec{Path: executable, Args: arguments, Dir: app.WorkingDirectory, Env: os.Environ()}, nil
	}
	return shellProcessSpec(app.WorkingDirectory, app.StartCommand)
}

func shellProcessSpec(directory, command string) (managedProcessSpec, error) {
	if strings.TrimSpace(command) == "" {
		return managedProcessSpec{}, errors.New("the command is empty")
	}
	if runtime.GOOS == "windows" {
		shell := os.Getenv("ComSpec")
		if shell == "" {
			var err error
			shell, err = exec.LookPath("cmd.exe")
			if err != nil {
				return managedProcessSpec{}, errors.New("cmd.exe is not available")
			}
		}
		return managedProcessSpec{Path: shell, Args: []string{"/D", "/S", "/C", command}, Dir: directory, Env: os.Environ(), HideWindow: true}, nil
	}
	return managedProcessSpec{Path: "/bin/sh", Args: []string{"-c", command}, Dir: directory, Env: os.Environ()}, nil
}
