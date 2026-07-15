package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	editorReadyTimeout     = 45 * time.Second
	editorStopTimeout      = 5 * time.Second
	editorOutputBufferSize = 32 * 1024
)

var editorReadyURLPattern = regexp.MustCompile(`(?i)web ui available at[ \t]+(https?://(?:127\.0\.0\.1|localhost|\[::1\]):[0-9]{1,5})`)

const editorExitEvent = "seizen:editor-exit"

type EditorSession struct {
	SessionID string `json:"sessionId"`
	URL       string `json:"url"`
}

type EditorExitEvent struct {
	SessionID string `json:"sessionId"`
	ExitCode  int    `json:"exitCode"`
	Error     string `json:"error"`
	// Stopped distinguishes a deliberate close (another project took VS Code,
	// suspension) from an actual crash.
	Stopped bool `json:"stopped"`
}

type editorSessionManager struct {
	startMu      sync.Mutex
	mu           sync.Mutex
	sessions     map[string]*managedEditorSession // one VS Code session per project
	starter      managedProcessStarter
	startGateway editorGatewayStarter
	readyTimeout time.Duration
	stopTimeout  time.Duration
	emit         func(EditorExitEvent)
	closed       bool
}

type managedEditorSession struct {
	id        string
	projectID string
	process   managedProcess
	done      chan struct{}
	cancel    context.CancelFunc
	gatewayMu sync.Mutex
	gateway   func() error
	stopping  bool
	stopOnce  sync.Once
	stopErr   error
}

type editorReadinessWriter struct {
	mu      sync.Mutex
	pending []byte
	ready   chan string
	found   bool
}

func newEditorSessionManager() *editorSessionManager {
	return &editorSessionManager{
		sessions:     make(map[string]*managedEditorSession),
		starter:      startPlatformManagedProcess,
		startGateway: startEditorGateway,
		readyTimeout: editorReadyTimeout,
		stopTimeout:  editorStopTimeout,
	}
}

func (a *App) projectEditorManager() *editorSessionManager {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.editors == nil {
		a.editors = newEditorSessionManager()
		a.editors.emit = func(event EditorExitEvent) {
			a.emitAgentEvent(editorExitEvent, event)
		}
	}
	return a.editors
}

func (a *App) currentEditorManager() *editorSessionManager {
	a.mu.RLock()
	manager := a.editors
	a.mu.RUnlock()
	return manager
}

func (a *App) StartProjectEditor(projectPath, id string) (EditorSession, error) {
	if err := a.validateProjectEditor(id); err != nil {
		return EditorSession{}, err
	}
	projectID, folder, err := a.registeredEditorProject(projectPath)
	if err != nil {
		return EditorSession{}, err
	}
	return a.startProjectEditor(projectID, folder, id)
}

func (a *App) StartProjectEditorContext(projectID, experimentID, id string) (EditorSession, error) {
	if err := a.validateProjectEditor(id); err != nil {
		return EditorSession{}, err
	}
	db, err := a.database.Pool(a.context())
	if err != nil {
		return EditorSession{}, err
	}
	folder, err := projectPathForExperiment(a.context(), db, projectID, experimentID)
	if err != nil {
		return EditorSession{}, err
	}
	return a.startProjectEditor(projectID, folder, id)
}

func (a *App) validateProjectEditor(id string) error {
	if id != "vscode" {
		return errors.New("only VS Code can be opened inside the workspace")
	}
	integrations, err := a.database.EditorIntegrations(a.context())
	if err != nil {
		return err
	}
	for _, integration := range integrations {
		if integration.ID == id && !integration.Enabled {
			return errors.New("VS Code is disabled; enable it in Resources")
		}
	}
	return nil
}

func (a *App) startProjectEditor(projectID, folder, id string) (EditorSession, error) {
	if id != "vscode" {
		return EditorSession{}, errors.New("only VS Code can be opened inside the workspace")
	}
	integrations, err := a.database.EditorIntegrations(a.context())
	if err != nil {
		return EditorSession{}, err
	}
	for _, integration := range integrations {
		if integration.ID == id && !integration.Enabled {
			return EditorSession{}, errors.New("VS Code is disabled; enable it in Resources")
		}
	}

	installer, err := a.managedVSCodeInstaller()
	if err != nil {
		return EditorSession{}, err
	}
	executable, err := installer.serverExecutable()
	if err != nil {
		return EditorSession{}, errors.New("VS Code is not ready yet in Seizen; wait for its installation in Resources to finish")
	}
	cliData := filepath.Join(installer.root, "web", "cli")
	// Per-project data: several concurrent serve-web instances cannot share
	// the same server-data-dir without clobbering each other.
	serverData := filepath.Join(installer.root, "web", "server", projectID)
	for _, directory := range []string{cliData, serverData} {
		if err = os.MkdirAll(directory, 0o700); err != nil {
			return EditorSession{}, fmt.Errorf("could not prepare VS Code's private data: %w", err)
		}
		if err = os.Chmod(directory, 0o700); err != nil {
			return EditorSession{}, fmt.Errorf("could not protect VS Code's private data: %w", err)
		}
	}
	token, err := newEditorToken()
	if err != nil {
		return EditorSession{}, fmt.Errorf("could not protect the VS Code session: %w", err)
	}
	sessionID, err := newUUID()
	if err != nil {
		return EditorSession{}, fmt.Errorf("could not create the VS Code session: %w", err)
	}
	baseSecret, err := newEditorToken()
	if err != nil {
		return EditorSession{}, fmt.Errorf("could not protect the VS Code panel: %w", err)
	}
	basePath := "/seizen/" + baseSecret
	spec := managedProcessSpec{
		Path: executable,
		Args: []string{
			"--cli-data-dir", cliData,
			"serve-web",
			"--host", "127.0.0.1",
			"--port", "0",
			"--connection-token", token,
			"--accept-server-license-terms",
			"--server-base-path", basePath,
			"--server-data-dir", serverData,
			"--default-folder", folder,
			"--disable-telemetry",
		},
		Dir:        folder,
		Env:        os.Environ(),
		HideWindow: true,
	}
	return a.projectEditorManager().start(a.context(), sessionID, projectID, token, basePath, spec)
}

func (a *App) StopProjectEditor(sessionID string) error {
	if sessionID == "" {
		return errors.New("the VS Code session identifier is not valid")
	}
	if strings.HasPrefix(sessionID, nativeEditorSessionPrefix) {
		return a.stopNativeEditor(sessionID)
	}
	manager := a.currentEditorManager()
	if manager == nil {
		return errors.New("the VS Code session was not found")
	}
	return manager.stop(sessionID)
}

func (a *App) registeredEditorProject(requestedPath string) (string, string, error) {
	folder, err := existingDirectory(requestedPath)
	if err != nil {
		return "", "", err
	}
	db, err := a.database.Pool(a.context())
	if err != nil {
		return "", "", err
	}
	rows, err := db.QueryContext(a.context(), `SELECT id, path FROM projects`)
	if err != nil {
		return "", "", fmt.Errorf("could not check the project: %w", err)
	}
	defer rows.Close()
	// ponytail: the local project library is small; add a normalized-path column only if this scan ever measures badly.
	for rows.Next() {
		var projectID, storedPath string
		if err = rows.Scan(&projectID, &storedPath); err != nil {
			return "", "", fmt.Errorf("could not check the project: %w", err)
		}
		storedFolder, pathErr := existingDirectory(storedPath)
		if pathErr == nil && samePath(storedFolder, folder) {
			return projectID, folder, nil
		}
	}
	if err = rows.Err(); err != nil {
		return "", "", fmt.Errorf("could not check the project: %w", err)
	}
	return "", "", errors.New("the folder does not belong to a project in the library")
}

func newEditorToken() (string, error) {
	var value [32]byte
	if _, err := io.ReadFull(rand.Reader, value[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}

func (manager *editorSessionManager) start(ctx context.Context, sessionID, projectID, token, basePath string, spec managedProcessSpec) (EditorSession, error) {
	manager.startMu.Lock()
	defer manager.startMu.Unlock()

	manager.mu.Lock()
	closed := manager.closed
	manager.mu.Unlock()
	if closed {
		return EditorSession{}, errors.New("VS Code sessions are shutting down")
	}
	manager.mu.Lock()
	previous := manager.sessions[projectID]
	manager.mu.Unlock()
	if previous != nil {
		if err := manager.stopSession(previous); err != nil {
			return EditorSession{}, err
		}
	}
	timeout := manager.readyTimeout
	if timeout <= 0 {
		timeout = editorReadyTimeout
	}
	sessionContext, cancelSession := context.WithCancel(ctx)
	readyContext, cancelReady := context.WithTimeout(sessionContext, timeout)
	defer cancelReady()

	output := &editorReadinessWriter{ready: make(chan string, 1)}
	process, err := manager.starter(spec, output)
	if err != nil {
		cancelSession()
		return EditorSession{}, fmt.Errorf("could not start VS Code inside the workspace: %w", err)
	}
	session := &managedEditorSession{id: sessionID, projectID: projectID, process: process, done: make(chan struct{}), cancel: cancelSession}
	manager.mu.Lock()
	rejected := manager.closed
	if !rejected {
		manager.sessions[projectID] = session
	}
	manager.mu.Unlock()
	go func() {
		exitCode, waitErr := process.Wait()
		cancelSession()
		_ = session.closeGateway()
		close(session.done)
		manager.mu.Lock()
		if manager.sessions[session.projectID] == session {
			delete(manager.sessions, session.projectID)
		}
		emit := manager.emit
		manager.mu.Unlock()
		if emit != nil {
			message := ""
			if waitErr != nil {
				message = waitErr.Error()
			}
			session.gatewayMu.Lock()
			stopped := session.stopping
			session.gatewayMu.Unlock()
			emit(EditorExitEvent{SessionID: session.id, ExitCode: exitCode, Error: message, Stopped: stopped})
		}
	}()
	if rejected {
		_ = manager.stopSession(session)
		return EditorSession{}, errors.New("VS Code sessions are shutting down")
	}

	select {
	case baseURL := <-output.ready:
		select {
		case <-session.done:
			return EditorSession{}, errors.New("VS Code exited before becoming ready")
		default:
		}
		startGateway := manager.startGateway
		if startGateway == nil {
			startGateway = startEditorGateway
		}
		gatewayURL, closeGateway, gatewayErr := startGateway(readyContext, baseURL, basePath, token)
		if gatewayErr != nil {
			_ = manager.stopSession(session)
			if errors.Is(readyContext.Err(), context.DeadlineExceeded) {
				return EditorSession{}, errors.New("VS Code was not ready within the expected time")
			}
			return EditorSession{}, fmt.Errorf("could not authenticate VS Code inside the workspace: %w", gatewayErr)
		}
		if readyContext.Err() != nil {
			_ = closeGateway()
			_ = manager.stopSession(session)
			if errors.Is(readyContext.Err(), context.DeadlineExceeded) {
				return EditorSession{}, errors.New("VS Code was not ready within the expected time")
			}
			return EditorSession{}, readyContext.Err()
		}
		if err = session.setGateway(closeGateway); err != nil {
			_ = manager.stopSession(session)
			return EditorSession{}, err
		}
		select {
		case <-session.done:
			return EditorSession{}, errors.New("VS Code exited before becoming ready")
		default:
		}
		return EditorSession{SessionID: sessionID, URL: gatewayURL}, nil
	case <-session.done:
		return EditorSession{}, errors.New("VS Code exited before becoming ready")
	case <-readyContext.Done():
		_ = manager.stopSession(session)
		if errors.Is(readyContext.Err(), context.DeadlineExceeded) {
			return EditorSession{}, errors.New("VS Code was not ready within the expected time")
		}
		return EditorSession{}, readyContext.Err()
	}
}

func (manager *editorSessionManager) stop(sessionID string) error {
	manager.mu.Lock()
	var session *managedEditorSession
	for _, candidate := range manager.sessions {
		if candidate.id == sessionID {
			session = candidate
			break
		}
	}
	manager.mu.Unlock()
	if session == nil {
		return errors.New("the VS Code session was not found")
	}
	return manager.stopSession(session)
}

func (manager *editorSessionManager) stopProject(projectID string) error {
	manager.mu.Lock()
	session := manager.sessions[projectID]
	manager.mu.Unlock()
	if session == nil {
		return nil
	}
	return manager.stopSession(session)
}

func (manager *editorSessionManager) stopSession(session *managedEditorSession) error {
	session.stopOnce.Do(func() {
		session.cancel()
		session.stopErr = errors.Join(session.closeGateway(), session.process.Stop())
	})
	timeout := manager.stopTimeout
	if timeout <= 0 {
		timeout = editorStopTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-session.done:
		if session.stopErr != nil {
			return fmt.Errorf("could not stop VS Code completely: %w", session.stopErr)
		}
		return nil
	case <-timer.C:
		return errors.New("VS Code did not close within the expected time")
	}
}

func (session *managedEditorSession) setGateway(closeGateway func() error) error {
	if closeGateway == nil {
		return errors.New("the local VS Code panel was not available")
	}
	session.gatewayMu.Lock()
	if session.stopping {
		session.gatewayMu.Unlock()
		_ = closeGateway()
		return errors.New("the VS Code session is shutting down")
	}
	session.gateway = closeGateway
	session.gatewayMu.Unlock()
	return nil
}

func (session *managedEditorSession) closeGateway() error {
	session.gatewayMu.Lock()
	session.stopping = true
	closeGateway := session.gateway
	session.gateway = nil
	session.gatewayMu.Unlock()
	if closeGateway == nil {
		return nil
	}
	return closeGateway()
}

func (manager *editorSessionManager) close() {
	manager.mu.Lock()
	manager.closed = true
	remaining := make([]*managedEditorSession, 0, len(manager.sessions))
	for _, session := range manager.sessions {
		remaining = append(remaining, session)
	}
	manager.mu.Unlock()
	for _, session := range remaining {
		_ = manager.stopSession(session)
	}
}

func (writer *editorReadinessWriter) Write(data []byte) (int, error) {
	written := len(data)
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.found {
		return written, nil
	}
	writer.pending = append(writer.pending, data...)
	if len(writer.pending) > editorOutputBufferSize {
		writer.pending = append([]byte(nil), writer.pending[len(writer.pending)-editorOutputBufferSize:]...)
	}
	match := editorReadyURLPattern.FindSubmatch(writer.pending)
	if len(match) != 2 {
		return written, nil
	}
	parsed, err := url.Parse(string(match[1]))
	if err != nil {
		return written, nil
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return written, nil
	}
	writer.found = true
	writer.pending = nil
	writer.ready <- "http://127.0.0.1:" + strconv.Itoa(port)
	return written, nil
}

var _ io.Writer = (*editorReadinessWriter)(nil)
