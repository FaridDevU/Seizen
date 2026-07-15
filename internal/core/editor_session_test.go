package core

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEditorReadinessOnlyAcceptsOfficialWebUILine(t *testing.T) {
	writer := &editorReadinessWriter{ready: make(chan string, 1)}
	_, _ = writer.Write([]byte("healthcheck http://127.0.0.1:41000\n"))
	select {
	case value := <-writer.ready:
		t.Fatalf("accepted an unrelated local URL: %q", value)
	default:
	}
	_, _ = writer.Write([]byte("Web UI available at http://localhost:42000/?tkn=ignored\n"))
	select {
	case value := <-writer.ready:
		if value != "http://127.0.0.1:42000" {
			t.Fatalf("ready URL = %q", value)
		}
	case <-time.After(time.Second):
		t.Fatal("official readiness line was not accepted")
	}
}

func TestEditorSessionEmitsUnexpectedExit(t *testing.T) {
	app, db, root := deletionTestApp(t)
	project := deletionTestProject(t, db, filepath.Join(root, "Workspace"), ProjectCreated)
	manager, _ := editorTestRuntime(t, app)
	process := newFakeManagedProcess(7123)
	events := make(chan EditorExitEvent, 1)
	manager.emit = func(event EditorExitEvent) { events <- event }
	manager.starter = func(_ managedProcessSpec, output io.Writer) (managedProcess, error) {
		_, _ = output.Write([]byte("Web UI available at http://127.0.0.1:49123\n"))
		return process, nil
	}

	session, err := app.StartProjectEditor(project.Path, "vscode")
	if err != nil {
		t.Fatal(err)
	}
	process.exit <- fakeProcessExit{code: 7, err: errors.New("boom")}
	select {
	case event := <-events:
		if event.SessionID != session.SessionID || event.ExitCode != 7 || event.Error != "boom" {
			t.Fatalf("unexpected exit event: %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("missing editor exit event")
	}
}

func TestProjectEditorStartsManagedWebVSCodeAndStopsByProject(t *testing.T) {
	app, db, root := deletionTestApp(t)
	project := deletionTestProject(t, db, filepath.Join(root, "Workspace"), ProjectCreated)
	other := deletionTestProject(t, db, filepath.Join(root, "Other"), ProjectCreated)
	manager, executable := editorTestRuntime(t, app)

	var mu sync.Mutex
	var specs []managedProcessSpec
	var processes []*fakeManagedProcess
	manager.starter = func(spec managedProcessSpec, output io.Writer) (managedProcess, error) {
		mu.Lock()
		process := newFakeManagedProcess(7000 + len(processes))
		processes = append(processes, process)
		specs = append(specs, spec)
		mu.Unlock()
		_, _ = output.Write([]byte("Web UI available at http://127.0."))
		_, _ = output.Write([]byte("0.1:49321/?tkn=not-logged\n"))
		return process, nil
	}

	session, err := app.StartProjectEditor(project.Path, "vscode")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(session.URL)
	if err != nil || parsed.Host != "127.0.0.1:48200" || parsed.RawQuery != "" || parsed.Fragment != "" {
		t.Fatalf("unexpected editor URL %q: %v", session.URL, err)
	}
	if session.SessionID == "" {
		t.Fatal("expected a session id")
	}

	mu.Lock()
	spec := specs[0]
	process := processes[0]
	mu.Unlock()
	if spec.Path != executable || !spec.HideWindow || !sameRequestedPath(spec.Dir, project.Path) {
		t.Fatalf("unexpected process spec: %#v", spec)
	}
	for flag, want := range map[string]string{
		"--host": "127.0.0.1", "--port": "0", "--default-folder": spec.Dir,
	} {
		if got := editorArgument(spec.Args, flag); got != want {
			t.Fatalf("%s = %q, want %q; args=%q", flag, got, want, spec.Args)
		}
	}
	for _, flag := range []string{"serve-web", "--accept-server-license-terms", "--disable-telemetry"} {
		if !containsString(spec.Args, flag) {
			t.Fatalf("missing %s in %q", flag, spec.Args)
		}
	}
	if token := editorArgument(spec.Args, "--connection-token"); token == "" || strings.Contains(session.URL, token) {
		t.Fatalf("the private process token leaked into the returned URL")
	}
	basePath := editorArgument(spec.Args, "--server-base-path")
	if !validEditorBasePath(basePath) || parsed.Path != basePath+"/" {
		t.Fatalf("unexpected private base path %q in %q", basePath, session.URL)
	}
	for _, flag := range []string{"--cli-data-dir", "--server-data-dir"} {
		path := editorArgument(spec.Args, flag)
		relative, relErr := filepath.Rel(app.vscode.root, path)
		if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			t.Fatalf("%s escaped Seizen: %q", flag, path)
		}
		if info, statErr := os.Stat(path); statErr != nil || !info.IsDir() {
			t.Fatalf("%s was not prepared: %v", flag, statErr)
		}
	}

	if err = app.CleanupProjectRuntime(other.ID); err != nil {
		t.Fatal(err)
	}
	if process.stops.Load() != 0 {
		t.Fatal("cleaning another project stopped the editor")
	}
	if err = app.CleanupProjectRuntime(project.ID); err != nil {
		t.Fatal(err)
	}
	if process.stops.Load() != 1 {
		t.Fatalf("project cleanup stops = %d", process.stops.Load())
	}
	if err = app.StopProjectEditor(session.SessionID); err == nil {
		t.Fatal("expected the cleaned session to be gone")
	}

	second, err := app.StartProjectEditor(project.Path, "vscode")
	if err != nil || second.SessionID == session.SessionID {
		t.Fatalf("second session = %+v, %v", second, err)
	}
	mu.Lock()
	secondProcess := processes[1]
	mu.Unlock()
	app.shutdown(context.Background())
	if secondProcess.stops.Load() != 1 {
		t.Fatalf("shutdown stops = %d", secondProcess.stops.Load())
	}
}

func TestProjectEditorRejectsUnregisteredDisabledAndUnsupportedFolders(t *testing.T) {
	app, _, _ := deletionTestApp(t)
	manager, _ := editorTestRuntime(t, app)
	started := false
	manager.starter = func(managedProcessSpec, io.Writer) (managedProcess, error) {
		started = true
		return newFakeManagedProcess(1), nil
	}

	unregistered := t.TempDir()
	if _, err := app.StartProjectEditor(unregistered, "vscode"); err == nil || !strings.Contains(err.Error(), "library") {
		t.Fatalf("expected an unregistered folder rejection, got %v", err)
	}
	if _, err := app.StartProjectEditor(unregistered, "cursor"); err == nil || !strings.Contains(err.Error(), "only VS Code") {
		t.Fatalf("expected an unsupported editor rejection, got %v", err)
	}
	if err := app.database.SetEditorIntegrationEnabled(context.Background(), "vscode", false); err != nil {
		t.Fatal(err)
	}
	if _, err := app.StartProjectEditor(unregistered, "vscode"); err == nil || !strings.Contains(err.Error(), "Resources") {
		t.Fatalf("expected a disabled editor rejection, got %v", err)
	}
	if started {
		t.Fatal("validation started a process")
	}
}

func TestProjectCleanupCancelsEditorGatewayBootstrap(t *testing.T) {
	app, db, root := deletionTestApp(t)
	project := deletionTestProject(t, db, filepath.Join(root, "Workspace"), ProjectCreated)
	manager, _ := editorTestRuntime(t, app)
	manager.starter = func(_ managedProcessSpec, output io.Writer) (managedProcess, error) {
		_, _ = output.Write([]byte("Web UI available at http://127.0.0.1:49123\n"))
		return newFakeManagedProcess(7124), nil
	}
	bootstrapStarted := make(chan struct{})
	manager.startGateway = func(ctx context.Context, _, _, _ string) (string, func() error, error) {
		close(bootstrapStarted)
		<-ctx.Done()
		return "", nil, ctx.Err()
	}
	startResult := make(chan error, 1)
	go func() {
		_, err := app.StartProjectEditor(project.Path, "vscode")
		startResult <- err
	}()
	select {
	case <-bootstrapStarted:
	case <-time.After(time.Second):
		t.Fatal("gateway bootstrap did not start")
	}
	if err := app.CleanupProjectRuntime(project.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-startResult:
		if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "authenticate") {
			t.Fatalf("unexpected canceled start error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("project cleanup did not cancel gateway bootstrap")
	}
}

func TestEditorGatewayBootstrapSharesReadinessDeadline(t *testing.T) {
	app, db, root := deletionTestApp(t)
	project := deletionTestProject(t, db, filepath.Join(root, "Workspace"), ProjectCreated)
	manager, _ := editorTestRuntime(t, app)
	manager.readyTimeout = 50 * time.Millisecond
	manager.starter = func(_ managedProcessSpec, output io.Writer) (managedProcess, error) {
		_, _ = output.Write([]byte("Web UI available at http://127.0.0.1:49123\n"))
		return newFakeManagedProcess(7125), nil
	}
	manager.startGateway = func(ctx context.Context, _, _, _ string) (string, func() error, error) {
		<-ctx.Done()
		return "", nil, ctx.Err()
	}
	started := time.Now()
	_, err := app.StartProjectEditor(project.Path, "vscode")
	if err == nil || !strings.Contains(err.Error(), "expected time") {
		t.Fatalf("expected one readiness timeout, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("gateway added a second timeout: %v", elapsed)
	}
}

func editorTestRuntime(t *testing.T, app *App) (*editorSessionManager, string) {
	t.Helper()
	installerRoot := filepath.Join(t.TempDir(), "Seizen", "tools", "vscode")
	executable := filepath.Join(installerRoot, "current", "bin", "code-tunnel.exe")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("fake"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := newEditorSessionManager()
	manager.readyTimeout = time.Second
	manager.stopTimeout = time.Second
	manager.startGateway = func(_ context.Context, _ string, basePath, token string) (string, func() error, error) {
		if !validEditorBasePath(basePath) || token == "" {
			return "", nil, errors.New("invalid gateway input")
		}
		return "http://127.0.0.1:48200" + basePath + "/", func() error { return nil }, nil
	}
	app.vscode = &managedVSCodeInstaller{root: installerRoot}
	app.editors = manager
	return manager, executable
}

func editorArgument(arguments []string, name string) string {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == name {
			return arguments[index+1]
		}
	}
	return ""
}
