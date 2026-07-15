package main

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const (
	terminalOutputEvent     = "seizen:terminal-output"
	terminalExitEvent       = "seizen:terminal-exit"
	maxTerminalInput        = 64 * 1024
	defaultTerminalColumns  = 80
	defaultTerminalRows     = 24
	maxTerminalColumns      = 1000
	maxTerminalRows         = 1000
	terminalReadBufferBytes = 32 * 1024
	maxTerminalSessions     = 16
	terminalStopAllTimeout  = 5 * time.Second
	currentProjectSpaceID   = "workspace"
)

type TerminalOutputEvent struct {
	SessionID string `json:"sessionId"`
	Data      string `json:"data"`
}

type TerminalExitEvent struct {
	SessionID string `json:"sessionId"`
	Error     string `json:"error"`
}

type terminalCommand struct {
	path string
	args []string
	dir  string
	env  []string
}

type agentTerminalBridgeConfig struct {
	URL              string
	Token            string
	ProjectID        string
	Environment      string
	Unrestricted     bool
	SharedExtensions bool
}

type terminalBackend interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	resize(columns, rows int) error
	wait() error
	close() error
}

type terminalSession struct {
	id           string
	projectID    string
	experimentID string
	serverID     string
	spaceID      string
	shell        string
	agent        string
	appID        string
	backend      terminalBackend
	output       *terminalOutputWriter
	readDone     chan struct{}
	done         chan struct{}
	ioMu         sync.Mutex
	stopping     atomic.Bool
}

type terminalOutputWriter struct {
	mu      sync.Mutex
	id      string
	emit    func(string, any)
	pending []byte
	queued  []byte
	timer   *time.Timer
	flushed bool
}

type terminalManager struct {
	mu       sync.Mutex
	sessions map[string]*terminalSession
	emit     func(string, any)
	closed   bool
}

func newTerminalManager(emit func(string, any)) *terminalManager {
	if emit == nil {
		emit = func(string, any) {}
	}
	return &terminalManager{sessions: make(map[string]*terminalSession), emit: emit}
}

func (a *App) StartProjectTerminal(projectID, path, shell string) (string, error) {
	return a.startProjectTerminal(projectID, "", path, shell)
}

func (a *App) StartProjectTerminalContext(projectID, experimentID, shell string) (string, error) {
	return a.startProjectTerminal(projectID, experimentID, "", shell)
}

func (a *App) startProjectTerminal(projectID, experimentID, requestedPath, shell string) (string, error) {
	if shell == "codex" || shell == "claude" || shell == "opencode" {
		return a.startProjectAgentTerminal(projectID, experimentID, requestedPath, shell, "")
	}
	if err := validateTerminalShell(shell); err != nil {
		return "", err
	}
	ctx := a.context()
	db, err := a.database.Pool(ctx)
	if err != nil {
		return "", err
	}
	storedPath, err := projectPathForExperiment(ctx, db, projectID, experimentID)
	if err != nil {
		return "", err
	}
	if requestedPath != "" && !sameRequestedPath(storedPath, requestedPath) {
		return "", errors.New("the given path does not match the project's saved path")
	}
	folder, err := existingDirectory(storedPath)
	if err != nil {
		return "", err
	}
	wslRuntime := ""
	if shell == "wsl" {
		definition, definitionErr := a.database.WSLDistribution(ctx)
		if definitionErr != nil {
			return "", definitionErr
		}
		installed, installedErr := isWSLRuntimeInstalled(ctx, definition.RuntimeName)
		if installedErr != nil {
			return "", installedErr
		}
		if !installed {
			return "", fmt.Errorf("%s is not installed; set it up from Resources > WSL Environments", definition.Name)
		}
		wslRuntime = definition.RuntimeName
	}
	command, err := projectTerminalCommand(shell, folder, wslRuntime)
	if err != nil {
		return "", err
	}
	sessionID, err := newUUID()
	if err != nil {
		return "", fmt.Errorf("could not create terminal session: %w", err)
	}
	if err = a.projectTerminalManager().startScopedExperimentProfile(sessionID, projectID, experimentID, "", currentProjectSpaceID, shell, "", command); err != nil {
		return "", err
	}
	return sessionID, nil
}

func (a *App) WriteProjectTerminal(sessionID, input string) error {
	return a.projectTerminalManager().write(sessionID, input)
}

func (a *App) WriteProjectTerminalBinary(sessionID, base64Input string) error {
	input, err := decodeTerminalBinaryInput(base64Input)
	if err != nil {
		return err
	}
	return a.projectTerminalManager().writeBytes(sessionID, input)
}

func (a *App) ResizeProjectTerminal(sessionID string, columns, rows int) error {
	return a.projectTerminalManager().resize(sessionID, columns, rows)
}

func (a *App) StopProjectTerminal(sessionID string) error {
	err := a.projectTerminalManager().stop(sessionID)
	a.ensureAgentTokenStore().RevokeSession(sessionID)
	return err
}

// StartProjectAgentTerminal starts a managed Claude Code or Codex terminal with
// a temporary MCP credential scoped to this project and selected App.
func (a *App) StartProjectAgentTerminal(projectID, path, shell, appID string) (string, error) {
	return a.startProjectAgentTerminal(projectID, "", path, shell, appID)
}

func (a *App) StartProjectAgentTerminalContext(projectID, experimentID, shell, appID string) (string, error) {
	return a.startProjectAgentTerminal(projectID, experimentID, "", shell, appID)
}

func (a *App) startProjectAgentTerminal(projectID, experimentID, requestedPath, shell, appID string) (string, error) {
	if shell != "codex" && shell != "claude" && shell != "opencode" {
		return "", errors.New("the agent bridge only supports codex, claude, or opencode")
	}
	ctx := a.context()
	db, err := a.database.Pool(ctx)
	if err != nil {
		return "", err
	}
	storedPath, err := projectPathForExperiment(ctx, db, projectID, experimentID)
	if err != nil {
		return "", err
	}
	if requestedPath != "" && !sameRequestedPath(storedPath, requestedPath) {
		return "", errors.New("the given path does not match the project's saved path")
	}
	folder, err := existingDirectory(storedPath)
	if err != nil {
		return "", err
	}
	if appID != "" {
		var exists int
		err = db.QueryRowContext(ctx, `SELECT 1 FROM apps WHERE id = ? AND project_id = ?`, appID, projectID).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return "", errors.New("the selected App does not belong to the project")
		}
		if err != nil {
			return "", err
		}
	}
	_, bridgeURL, err := a.ensureAgentBridge()
	if err != nil {
		return "", err
	}
	sessionID, err := newUUID()
	if err != nil {
		return "", fmt.Errorf("could not create terminal session: %w", err)
	}
	token, err := a.ensureAgentTokenStore().Issue(AgentTokenScope{
		SessionID: sessionID, ProjectID: projectID, AppID: appID,
		ExperimentID: experimentID, SpaceID: currentProjectSpaceID, Permissions: appAgentPermissions,
	}, agentTokenLifetime)
	if err != nil {
		return "", err
	}
	settings, err := a.database.AgentResourceSettings(ctx)
	if err != nil {
		a.ensureAgentTokenStore().Revoke(token)
		return "", err
	}
	environment, unrestricted := agentEnvironment(settings, shell)
	if environment != "cmd" {
		definition, ok := wslDistributionByID(environment)
		if !ok {
			a.ensureAgentTokenStore().Revoke(token)
			return "", errors.New("the agent's WSL environment is not valid")
		}
		installed, installedErr := isWSLRuntimeInstalled(ctx, definition.RuntimeName)
		if installedErr != nil || !installed {
			a.ensureAgentTokenStore().Revoke(token)
			if installedErr != nil {
				return "", installedErr
			}
			return "", fmt.Errorf("%s is not installed; set it up from Resources > WSL Environments", definition.Name)
		}
	}
	command, err := projectAgentTerminalCommand(shell, folder, agentTerminalBridgeConfig{
		URL: bridgeURL, Token: token, ProjectID: projectID, Environment: environment,
		Unrestricted: unrestricted, SharedExtensions: settings.SharedExtensions,
	})
	if err == nil {
		err = a.projectTerminalManager().startScopedExperimentProfile(sessionID, projectID, experimentID, "", currentProjectSpaceID, shell, shell, command)
	}
	if err != nil {
		a.ensureAgentTokenStore().Revoke(token)
		return "", err
	}
	if session := a.projectTerminalManager().session(sessionID); session != nil {
		session.appID = appID
	}
	return sessionID, nil
}

func validateTerminalShell(shell string) error {
	if shell != "cmd" && shell != "wsl" && shell != "codex" && shell != "claude" && shell != "opencode" {
		return errors.New("the terminal only supports cmd, wsl, codex, claude, or opencode")
	}
	return nil
}

func validateTerminalSize(columns, rows int) error {
	if columns < 1 || columns > maxTerminalColumns {
		return fmt.Errorf("columns must be between 1 and %d", maxTerminalColumns)
	}
	if rows < 1 || rows > maxTerminalRows {
		return fmt.Errorf("rows must be between 1 and %d", maxTerminalRows)
	}
	return nil
}

func decodeTerminalBinaryInput(encoded string) ([]byte, error) {
	if len(encoded) > base64.StdEncoding.EncodedLen(maxTerminalInput) {
		return nil, fmt.Errorf("input exceeds the limit of %d KiB", maxTerminalInput/1024)
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return nil, errors.New("the terminal's binary input is not valid base64")
	}
	if len(decoded) > maxTerminalInput {
		return nil, fmt.Errorf("input exceeds the limit of %d KiB", maxTerminalInput/1024)
	}
	return decoded, nil
}

func (m *terminalManager) start(id string, command *terminalCommand) error {
	return m.startScoped(id, "", "", command)
}

func (m *terminalManager) startScoped(id, projectID, serverID string, command *terminalCommand) error {
	return m.startScopedProfile(id, projectID, serverID, "", "", "", command)
}

func (m *terminalManager) startScopedProfile(id, projectID, serverID, spaceID, shell, agent string, command *terminalCommand) error {
	return m.startScopedExperimentProfile(id, projectID, "", serverID, spaceID, shell, agent, command)
}

func (m *terminalManager) startScopedExperimentProfile(id, projectID, experimentID, serverID, spaceID, shell, agent string, command *terminalCommand) error {
	return m.startWithScopeExperimentProfile(id, projectID, experimentID, serverID, spaceID, shell, agent, func() (terminalBackend, error) {
		return startTerminalBackend(command, defaultTerminalColumns, defaultTerminalRows)
	})
}

func (m *terminalManager) startWith(id string, start func() (terminalBackend, error)) error {
	return m.startWithScope(id, "", "", start)
}

func (m *terminalManager) startWithScope(id, projectID, serverID string, start func() (terminalBackend, error)) error {
	return m.startWithScopeProfile(id, projectID, serverID, "", "", "", start)
}

func (m *terminalManager) startWithScopeProfile(id, projectID, serverID, spaceID, shell, agent string, start func() (terminalBackend, error)) error {
	return m.startWithScopeExperimentProfile(id, projectID, "", serverID, spaceID, shell, agent, start)
}

func (m *terminalManager) startWithScopeExperimentProfile(id, projectID, experimentID, serverID, spaceID, shell, agent string, start func() (terminalBackend, error)) error {
	if id == "" {
		return errors.New("the terminal identifier is not valid")
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("the terminal manager is closed")
	}
	if _, exists := m.sessions[id]; exists {
		m.mu.Unlock()
		return errors.New("a terminal with that identifier already exists")
	}
	if len(m.sessions) >= maxTerminalSessions {
		m.mu.Unlock()
		return fmt.Errorf("reached the limit of %d active terminals; close one before opening another", maxTerminalSessions)
	}
	backend, err := start()
	if err != nil {
		m.mu.Unlock()
		return err
	}
	if backend == nil {
		m.mu.Unlock()
		return errors.New("could not start the terminal")
	}
	session := &terminalSession{
		id:           id,
		projectID:    projectID,
		experimentID: experimentID,
		serverID:     serverID,
		spaceID:      spaceID,
		shell:        shell,
		agent:        agent,
		backend:      backend,
		output:       newTerminalOutputWriter(id, m.emit),
		readDone:     make(chan struct{}),
		done:         make(chan struct{}),
	}
	m.sessions[id] = session
	m.mu.Unlock()

	go m.read(session)
	go m.wait(session)
	return nil
}

func (m *terminalManager) write(id, input string) error {
	if !utf8.ValidString(input) {
		return errors.New("the terminal input must be UTF-8")
	}
	return m.writeBytes(id, []byte(input))
}

func (m *terminalManager) writeBytes(id string, input []byte) error {
	if id == "" {
		return errors.New("the terminal identifier is not valid")
	}
	if len(input) > maxTerminalInput {
		return fmt.Errorf("input exceeds the limit of %d KiB", maxTerminalInput/1024)
	}
	session := m.session(id)
	if session == nil {
		return errors.New("the terminal session was not found")
	}
	if session.stopping.Load() {
		return errors.New("the terminal session is closing")
	}
	session.ioMu.Lock()
	defer session.ioMu.Unlock()
	if session.stopping.Load() {
		return errors.New("the terminal session is closing")
	}
	for len(input) > 0 {
		written, err := session.backend.Write(input)
		if err != nil {
			return fmt.Errorf("could not write to the terminal: %w", err)
		}
		if written <= 0 || written > len(input) {
			return fmt.Errorf("could not write to the terminal: %w", io.ErrShortWrite)
		}
		input = input[written:]
	}
	return nil
}

func (m *terminalManager) resize(id string, columns, rows int) error {
	if id == "" {
		return errors.New("the terminal identifier is not valid")
	}
	if err := validateTerminalSize(columns, rows); err != nil {
		return err
	}
	session := m.session(id)
	if session == nil {
		return errors.New("the terminal session was not found")
	}
	if session.stopping.Load() {
		return errors.New("the terminal session is closing")
	}
	session.ioMu.Lock()
	defer session.ioMu.Unlock()
	if session.stopping.Load() {
		return errors.New("the terminal session is closing")
	}
	if err := session.backend.resize(columns, rows); err != nil {
		return fmt.Errorf("could not resize the terminal: %w", err)
	}
	return nil
}

func (m *terminalManager) session(id string) *terminalSession {
	m.mu.Lock()
	session := m.sessions[id]
	m.mu.Unlock()
	return session
}

func (m *terminalManager) stop(id string) error {
	if id == "" {
		return errors.New("the terminal identifier is not valid")
	}
	session := m.session(id)
	if session == nil {
		return errors.New("the terminal session was not found")
	}
	return stopTerminalSession(session)
}

func (m *terminalManager) stopAll() {
	deadline := time.NewTimer(terminalStopAllTimeout)
	defer deadline.Stop()
	m.stopAllBefore(deadline.C)
}

func (m *terminalManager) stopServer(serverID string) {
	m.stopMatching(func(session *terminalSession) bool { return session.serverID == serverID })
}

func (m *terminalManager) stopProjectServers(projectID string) {
	m.stopMatching(func(session *terminalSession) bool {
		return session.projectID == projectID && session.serverID != ""
	})
}

func (m *terminalManager) stopProject(projectID string) {
	m.stopMatching(func(session *terminalSession) bool { return session.projectID == projectID })
}

func (m *terminalManager) stopExperiment(projectID, experimentID string) {
	m.stopMatching(func(session *terminalSession) bool {
		return session.projectID == projectID && session.experimentID == experimentID
	})
}

func (m *terminalManager) stopMatching(matches func(*terminalSession) bool) {
	m.mu.Lock()
	sessions := make([]*terminalSession, 0)
	for _, session := range m.sessions {
		if matches(session) {
			sessions = append(sessions, session)
		}
	}
	m.mu.Unlock()
	for _, session := range sessions {
		_ = requestTerminalStop(session)
	}
	deadline := time.NewTimer(terminalStopAllTimeout)
	defer deadline.Stop()
	for _, session := range sessions {
		select {
		case <-session.done:
		case <-deadline.C:
			return
		}
	}
}

func (m *terminalManager) stopAllBefore(deadline <-chan time.Time) {
	m.mu.Lock()
	m.closed = true
	sessions := make([]*terminalSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.mu.Unlock()
	for _, session := range sessions {
		go func() { _ = requestTerminalStop(session) }()
	}
	for _, session := range sessions {
		select {
		case <-session.done:
		case <-deadline:
			return
		}
	}
}

func stopTerminalSession(session *terminalSession) error {
	terminateErr := requestTerminalStop(session)
	select {
	case <-session.done:
		if terminateErr != nil {
			return fmt.Errorf("could not fully stop the terminal: %w", terminateErr)
		}
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("the terminal did not close within the expected time")
	}
}

func requestTerminalStop(session *terminalSession) error {
	if !session.stopping.CompareAndSwap(false, true) {
		return nil
	}
	session.ioMu.Lock()
	defer session.ioMu.Unlock()
	return session.backend.close()
}

func (m *terminalManager) read(session *terminalSession) {
	defer close(session.readDone)
	defer session.output.flush()
	buffer := make([]byte, terminalReadBufferBytes)
	for {
		count, err := session.backend.Read(buffer)
		if count > 0 {
			_, _ = session.output.Write(buffer[:count])
		}
		if err != nil {
			return
		}
	}
}

func (m *terminalManager) wait(session *terminalSession) {
	waitErr := session.backend.wait()
	wasStopping := session.stopping.Swap(true)
	session.ioMu.Lock()
	closeErr := session.backend.close()
	session.ioMu.Unlock()
	<-session.readDone

	m.mu.Lock()
	if m.sessions[session.id] == session {
		delete(m.sessions, session.id)
	}
	m.mu.Unlock()
	errorMessage := ""
	if waitErr != nil && !wasStopping {
		errorMessage = waitErr.Error()
	}
	if closeErr != nil {
		if errorMessage == "" {
			errorMessage = "could not close the terminal: " + closeErr.Error()
		} else {
			errorMessage += "; could not close the terminal: " + closeErr.Error()
		}
	}
	m.emit(terminalExitEvent, TerminalExitEvent{SessionID: session.id, Error: errorMessage})
	close(session.done)
}

func newTerminalOutputWriter(id string, emit func(string, any)) *terminalOutputWriter {
	return &terminalOutputWriter{id: id, emit: emit, pending: make([]byte, 0, utf8.UTFMax)}
}

// Output is batched ~16ms before being emitted: an agent spewing text was
// generating hundreds of Wails events per second; with coalescing it's ~60/s max.
const terminalOutputFlushDelay = 16 * time.Millisecond
const terminalOutputFlushBytes = 32 * 1024

func (writer *terminalOutputWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.flushed {
		return 0, io.ErrClosedPipe
	}
	writer.pending = append(writer.pending, data...)
	text, remainder := terminalUTF8Chunk(writer.pending, false)
	writer.pending = remainder
	if text == "" {
		return len(data), nil
	}
	writer.queued = append(writer.queued, text...)
	if len(writer.queued) >= terminalOutputFlushBytes {
		writer.emitQueuedLocked()
	} else if writer.timer == nil {
		writer.timer = time.AfterFunc(terminalOutputFlushDelay, writer.flushQueued)
	}
	return len(data), nil
}

func (writer *terminalOutputWriter) flushQueued() {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.emitQueuedLocked()
}

func (writer *terminalOutputWriter) emitQueuedLocked() {
	if writer.timer != nil {
		writer.timer.Stop()
		writer.timer = nil
	}
	if len(writer.queued) == 0 {
		return
	}
	text := string(writer.queued)
	writer.queued = writer.queued[:0]
	writer.emit(terminalOutputEvent, TerminalOutputEvent{SessionID: writer.id, Data: text})
}

func (writer *terminalOutputWriter) flush() {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.flushed {
		return
	}
	writer.flushed = true
	if writer.timer != nil {
		writer.timer.Stop()
		writer.timer = nil
	}
	if text, _ := terminalUTF8Chunk(writer.pending, true); text != "" {
		writer.queued = append(writer.queued, text...)
	}
	if len(writer.queued) > 0 {
		writer.emit(terminalOutputEvent, TerminalOutputEvent{SessionID: writer.id, Data: string(writer.queued)})
	}
	writer.pending = nil
	writer.queued = nil
}

func terminalUTF8Chunk(data []byte, final bool) (string, []byte) {
	cut := len(data)
	if !final {
		start := max(0, len(data)-utf8.UTFMax)
		for index := len(data) - 1; index >= start; index-- {
			if utf8.RuneStart(data[index]) {
				if !utf8.FullRune(data[index:]) {
					cut = index
				}
				break
			}
		}
	}
	text := strings.ToValidUTF8(string(data[:cut]), "�")
	remainder := append([]byte(nil), data[cut:]...)
	return text, remainder
}
