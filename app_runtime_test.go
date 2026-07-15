package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeProcessExit struct {
	code int
	err  error
}

type fakeManagedProcess struct {
	pid      int
	exit     chan fakeProcessExit
	stopOnce sync.Once
	stops    atomic.Int32
}

func newFakeManagedProcess(pid int) *fakeManagedProcess {
	return &fakeManagedProcess{pid: pid, exit: make(chan fakeProcessExit, 1)}
}

func (process *fakeManagedProcess) PID() int { return process.pid }

func (process *fakeManagedProcess) Wait() (int, error) {
	result := <-process.exit
	return result.code, result.err
}

func (process *fakeManagedProcess) Stop() error {
	process.stops.Add(1)
	process.stopOnce.Do(func() { process.exit <- fakeProcessExit{code: 1, err: errors.New("killed")} })
	return nil
}

func TestAppRuntimeTransitionsLogsEventsAndCleanup(t *testing.T) {
	app, project := newAppServerTestApp(t)
	input := testAppInput(project)
	input.Kind, input.HealthcheckURL, input.PreviewURL = "desktop", "", ""
	input.StopCommand = ""
	created, err := app.CreateApp(input)
	if err != nil {
		t.Fatal(err)
	}

	var eventsMu sync.Mutex
	events := make([]string, 0)
	manager := newAppRuntimeManager(app.database, func(name string, _ any) {
		eventsMu.Lock()
		events = append(events, name)
		eventsMu.Unlock()
	})
	process := newFakeManagedProcess(4242)
	manager.starter = func(_ managedProcessSpec, output io.Writer) (managedProcess, error) {
		_, _ = output.Write([]byte("app ready\n"))
		return process, nil
	}

	starting, err := manager.StartApp(context.Background(), created.ID)
	if err != nil || starting.Status != "starting" {
		t.Fatalf("starting = %+v, %v", starting, err)
	}
	waitForAppStatus(t, app.database, created.ID, "running")
	status, err := manager.GetAppStatus(context.Background(), created.ID)
	if err != nil || !status.ProcessAlive || status.PID != 4242 {
		t.Fatalf("runtime status = %+v, %v", status, err)
	}
	if logs := manager.GetAppLogs(created.ID); logs != "app ready\n" {
		t.Fatalf("logs = %q", logs)
	}

	if err = manager.CleanupProjectRuntime(context.Background(), project.ID); err != nil {
		t.Fatal(err)
	}
	stopped := waitForAppStatus(t, app.database, created.ID, "stopped")
	if stopped.Status != "stopped" || process.stops.Load() == 0 {
		t.Fatalf("cleanup left %+v, stops=%d", stopped, process.stops.Load())
	}
	var runStatus, runtimeReference string
	var exitCode int
	db, _ := app.database.Pool(context.Background())
	if err = db.QueryRow(`SELECT status, runtime_reference, exit_code FROM app_runs WHERE app_id = ?`, created.ID).
		Scan(&runStatus, &runtimeReference, &exitCode); err != nil {
		t.Fatal(err)
	}
	if runStatus != "stopped" || runtimeReference != "4242" || exitCode != 1 {
		t.Fatalf("run = status %q, ref %q, exit %d", runStatus, runtimeReference, exitCode)
	}

	eventsMu.Lock()
	defer eventsMu.Unlock()
	for _, expected := range []string{"app.starting", "app.running", "app.stopping", "app.stopped"} {
		if !containsString(events, expected) {
			t.Fatalf("missing %q in events %v", expected, events)
		}
	}
}

func TestAppRuntimeCancelsStartingHealthcheck(t *testing.T) {
	app, project := newAppServerTestApp(t)
	input := testAppInput(project)
	input.HealthcheckURL = "http://127.0.0.1:1/health"
	created, err := app.CreateApp(input)
	if err != nil {
		t.Fatal(err)
	}
	manager := newAppRuntimeManager(app.database, nil)
	manager.healthTimeout = time.Minute
	process := newFakeManagedProcess(91)
	started := make(chan struct{})
	manager.starter = func(_ managedProcessSpec, _ io.Writer) (managedProcess, error) {
		close(started)
		return process, nil
	}
	if _, err = manager.StartApp(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err = manager.StopApp(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	waitForAppStatus(t, app.database, created.ID, "stopped")
}

func TestStopAppCancelsOneActiveTestProcess(t *testing.T) {
	app, project := newAppServerTestApp(t)
	input := testAppInput(project)
	input.Kind, input.HealthcheckURL, input.PreviewURL = "desktop", "", ""
	input.StopCommand = ""
	input.TestCommand = "test"
	created, err := app.CreateApp(input)
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan string, 16)
	manager := newAppRuntimeManager(app.database, func(name string, _ any) { events <- name })
	mainProcess := newFakeManagedProcess(101)
	testProcess := newFakeManagedProcess(102)
	testStarted := make(chan struct{})
	var starts atomic.Int32
	manager.starter = func(_ managedProcessSpec, _ io.Writer) (managedProcess, error) {
		if starts.Add(1) == 1 {
			return mainProcess, nil
		}
		close(testStarted)
		return testProcess, nil
	}
	if _, err = manager.StartApp(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	waitForAppStatus(t, app.database, created.ID, "running")
	testDone := make(chan error, 1)
	go func() {
		_, runErr := manager.RunAppTests(context.Background(), created.ID)
		testDone <- runErr
	}()
	<-testStarted
	if _, err = manager.RunAppTests(context.Background(), created.ID); err == nil {
		t.Fatal("expected concurrent tests to be rejected")
	}
	if _, err = manager.StopApp(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	if err = <-testDone; err == nil {
		t.Fatal("expected the canceled test to report failure")
	}
	if mainProcess.stops.Load() == 0 || testProcess.stops.Load() == 0 {
		t.Fatalf("StopApp did not stop every process: main=%d test=%d", mainProcess.stops.Load(), testProcess.stops.Load())
	}
	if stopped := waitForAppStatus(t, app.database, created.ID, "stopped"); stopped.Status != "stopped" {
		t.Fatalf("tests resurrected a stopped App: %+v", stopped)
	}
	close(events)
	startedEvent, completedEvent := false, false
	for event := range events {
		startedEvent = startedEvent || event == "app.test.started"
		completedEvent = completedEvent || event == "app.test.completed"
	}
	if !startedEvent || !completedEvent {
		t.Fatalf("missing real test progress events: started=%t completed=%t", startedEvent, completedEvent)
	}
}

func TestConfirmCloseStopsActiveApp(t *testing.T) {
	app, project := newAppServerTestApp(t)
	input := testAppInput(project)
	input.Kind, input.HealthcheckURL, input.PreviewURL, input.StopCommand = "desktop", "", "", ""
	created, err := app.CreateApp(input)
	if err != nil {
		t.Fatal(err)
	}
	manager := newAppRuntimeManager(app.database, nil)
	process := newFakeManagedProcess(103)
	manager.starter = func(managedProcessSpec, io.Writer) (managedProcess, error) { return process, nil }
	app.appRuntimes = manager
	app.quit = func(context.Context) {}
	app.startup(context.Background())
	if _, err = manager.StartApp(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	waitForAppStatus(t, app.database, created.ID, "running")
	app.ConfirmClose()
	if process.stops.Load() == 0 {
		t.Fatal("ConfirmClose left the App process active")
	}
	if stopped := waitForAppStatus(t, app.database, created.ID, "stopped"); stopped.Status != "stopped" {
		t.Fatalf("ConfirmClose left %+v", stopped)
	}
}

func TestAppHealthClientRejectsUnsafeRedirects(t *testing.T) {
	app, _ := newAppServerTestApp(t)
	manager := newAppRuntimeManager(app.database, nil)
	for _, rawURL := range []string{
		"http://wails.localhost/",
		"https://user:secret@example.com/",
		"file:///C:/secret.txt",
	} {
		request, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = manager.healthClient.CheckRedirect(request, nil); err == nil {
			t.Fatalf("expected redirect %q to be rejected", rawURL)
		}
	}
}

func TestInitializeReconcilesStaleAppRun(t *testing.T) {
	app, project := newAppServerTestApp(t)
	input := testAppInput(project)
	input.HealthcheckURL = ""
	created, err := app.CreateApp(input)
	if err != nil {
		t.Fatal(err)
	}
	db, _ := app.database.Pool(context.Background())
	if _, err = db.Exec(`UPDATE apps SET status = 'running' WHERE id = ?;
INSERT INTO app_runs (id, project_id, app_id, target, runtime_provider, status, started_at)
VALUES ('stale-run', ?, ?, 'development', 'local', 'running', `+projectNow+`)`, created.ProjectID, created.ID, created.ID); err != nil {
		t.Fatal(err)
	}
	path := app.database.path
	root := app.database.defaultProjectRoot
	app.database.Close()
	reopened := newDatabase(path, root)
	t.Cleanup(reopened.Close)
	reopenedDB, err := reopened.Pool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var appStatus, runStatus string
	var stoppedAt *string
	if err = reopenedDB.QueryRow(`SELECT status FROM apps WHERE id = ?`, created.ID).Scan(&appStatus); err != nil {
		t.Fatal(err)
	}
	if err = reopenedDB.QueryRow(`SELECT status, stopped_at FROM app_runs WHERE id = 'stale-run'`).Scan(&runStatus, &stoppedAt); err != nil {
		t.Fatal(err)
	}
	if appStatus != "stopped" || runStatus != "stopped" || stoppedAt == nil {
		t.Fatalf("reconciled app=%q run=%q stoppedAt=%v", appStatus, runStatus, stoppedAt)
	}
}

func waitForAppStatus(t *testing.T, database *Database, id, expected string) ProjectApp {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		db, err := database.Pool(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		app, err := getProjectApp(context.Background(), db, id)
		if err == nil && app.Status == expected {
			return app
		}
		if time.Now().After(deadline) {
			t.Fatalf("App status never became %q; last=%+v, err=%v", expected, app, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
