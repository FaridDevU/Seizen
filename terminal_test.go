package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProjectTerminalValidation(t *testing.T) {
	for _, shell := range []string{"cmd", "wsl", "codex", "claude"} {
		if err := validateTerminalShell(shell); err != nil {
			t.Errorf("expected %q to be accepted: %v", shell, err)
		}
	}
	for _, shell := range []string{"", "CMD", "powershell", "cmd.exe", "wsl.exe"} {
		if err := validateTerminalShell(shell); err == nil {
			t.Errorf("expected %q to be rejected", shell)
		}
	}

	app, _, project, root := thumbnailTestProject(t)
	if _, err := app.StartProjectTerminal(project.ID, root, "powershell"); err == nil {
		t.Fatal("expected an unsupported shell to be rejected")
	}
	if _, err := app.StartProjectTerminal(project.ID, root+"-other", "cmd"); err == nil {
		t.Fatal("expected a mismatched project path to be rejected")
	}
	if _, err := app.StartProjectTerminal("missing", root, "cmd"); err == nil {
		t.Fatal("expected an unknown project to be rejected")
	}
}

func TestTerminalManagerInputLimitsAndBinaryInput(t *testing.T) {
	backend := newTerminalTestBackend(false)
	manager := newTerminalManager(nil)
	if err := manager.startWith("session", func() (terminalBackend, error) { return backend, nil }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.stopAll)

	if err := manager.write("session", "echo á\r"); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	app.terminals = manager
	binary := []byte{0, 0xff, 0x1b, '[', 'M', 32, 33, 34}
	if err := app.WriteProjectTerminalBinary("session", base64.StdEncoding.EncodeToString(binary)); err != nil {
		t.Fatal(err)
	}
	want := append([]byte("echo á\r"), binary...)
	if got := backend.inputBytes(); !bytes.Equal(got, want) {
		t.Fatalf("unexpected terminal input %v, want %v", got, want)
	}
	if err := manager.write("session", strings.Repeat("x", maxTerminalInput+1)); err == nil {
		t.Fatal("expected oversized text input to be rejected")
	}
	if err := manager.write("session", string([]byte{0xff})); err == nil {
		t.Fatal("expected invalid UTF-8 text input to be rejected")
	}
	oversized := base64.StdEncoding.EncodeToString(make([]byte, maxTerminalInput+1))
	if err := app.WriteProjectTerminalBinary("session", oversized); err == nil {
		t.Fatal("expected oversized binary input to be rejected")
	}
	if err := app.WriteProjectTerminalBinary("session", "not base64!"); err == nil {
		t.Fatal("expected malformed base64 input to be rejected")
	}
	if err := manager.write("missing", "x"); err == nil {
		t.Fatal("expected an unknown session to be rejected")
	}
}

func TestTerminalManagerResizeValidation(t *testing.T) {
	backend := newTerminalTestBackend(false)
	manager := newTerminalManager(nil)
	if err := manager.startWith("session", func() (terminalBackend, error) { return backend, nil }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.stopAll)

	if err := manager.resize("session", 120, 40); err != nil {
		t.Fatal(err)
	}
	if columns, rows := backend.size(); columns != 120 || rows != 40 {
		t.Fatalf("unexpected terminal size %dx%d", columns, rows)
	}
	for _, size := range [][2]int{{0, 24}, {80, 0}, {maxTerminalColumns + 1, 24}, {80, maxTerminalRows + 1}} {
		if err := manager.resize("session", size[0], size[1]); err == nil {
			t.Fatalf("expected terminal size %dx%d to be rejected", size[0], size[1])
		}
	}
	if err := manager.resize("missing", 80, 24); err == nil {
		t.Fatal("expected an unknown session to be rejected")
	}
}

func TestStoppingServerDisconnectsOnlyItsRootTerminal(t *testing.T) {
	serverBackend := newTerminalTestBackend(false)
	workspaceBackend := newTerminalTestBackend(false)
	manager := newTerminalManager(nil)
	if err := manager.startWithScope("server-root", "project", "server", func() (terminalBackend, error) {
		return serverBackend, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := manager.startWith("workspace", func() (terminalBackend, error) {
		return workspaceBackend, nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.stopAll)

	manager.stopServer("server")
	if manager.session("server-root") != nil {
		t.Fatal("the root terminal was still connected after stopping the server")
	}
	if manager.session("workspace") == nil {
		t.Fatal("a regular Workspace terminal was closed")
	}
}

func TestTerminalManagerLimitsActiveSessions(t *testing.T) {
	manager := newTerminalManager(nil)
	t.Cleanup(manager.stopAll)
	for index := range maxTerminalSessions {
		backend := newTerminalTestBackend(false)
		if err := manager.startWith(fmt.Sprintf("session-%d", index), func() (terminalBackend, error) {
			return backend, nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	startCalled := false
	err := manager.startWith("over-limit", func() (terminalBackend, error) {
		startCalled = true
		return newTerminalTestBackend(false), nil
	})
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("limit of %d active terminals", maxTerminalSessions)) {
		t.Fatalf("expected a clear session-limit error, got %v", err)
	}
	if startCalled {
		t.Fatal("backend was started after reaching the session limit")
	}
}

func TestTerminalManagerStopAllUsesOneDeadline(t *testing.T) {
	if terminalStopAllTimeout != 5*time.Second {
		t.Fatalf("unexpected stopAll timeout %v", terminalStopAllTimeout)
	}
	manager := newTerminalManager(nil)
	backends := make([]*terminalStubbornBackend, 0, 2)
	sessions := make([]*terminalSession, 0, 2)
	for _, id := range []string{"one", "two"} {
		backend := newTerminalStubbornBackend()
		if err := manager.startWith(id, func() (terminalBackend, error) { return backend, nil }); err != nil {
			t.Fatal(err)
		}
		backends = append(backends, backend)
		sessions = append(sessions, manager.session(id))
	}

	deadline := make(chan time.Time)
	close(deadline)
	manager.stopAllBefore(deadline)
	if !manager.closed {
		t.Fatal("terminal manager was not closed")
	}
	for _, backend := range backends {
		close(backend.blocked)
	}
	for _, session := range sessions {
		select {
		case <-session.done:
		case <-time.After(time.Second):
			t.Fatal("terminal test session did not finish")
		}
	}
}

func TestStoppingTerminalSerializesWithActiveWrite(t *testing.T) {
	backend := newTerminalCoordinatedBackend()
	manager := newTerminalManager(nil)
	if err := manager.startWith("blocked", func() (terminalBackend, error) { return backend, nil }); err != nil {
		t.Fatal(err)
	}
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- manager.write("blocked", "blocked")
	}()
	select {
	case <-backend.started:
	case <-time.After(time.Second):
		t.Fatal("write did not block")
	}

	stopDone := make(chan error, 1)
	go func() { stopDone <- manager.stop("blocked") }()
	select {
	case <-backend.closed:
		t.Fatal("terminal closed concurrently with an active write")
	case <-time.After(100 * time.Millisecond):
	}
	close(backend.releaseWrite)
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("write did not finish after it was released")
	}
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal did not stop after the active write finished")
	}
}

func TestTerminalUTF8ChunkKeepsSplitRune(t *testing.T) {
	text, pending := terminalUTF8Chunk([]byte{'A', 0xe2, 0x82}, false)
	if text != "A" || len(pending) != 2 {
		t.Fatalf("unexpected first chunk %q, %v", text, pending)
	}
	text, pending = terminalUTF8Chunk(append(pending, 0xac), false)
	if text != "€" || len(pending) != 0 {
		t.Fatalf("unexpected completed chunk %q, %v", text, pending)
	}
}

func TestTerminalManagerLifecycle(t *testing.T) {
	events := make(chan terminalTestEvent, 16)
	manager := newTerminalManager(func(name string, payload any) {
		events <- terminalTestEvent{name: name, payload: payload}
	})
	backend := newTerminalTestBackend(true)
	if err := manager.startWith("session", func() (terminalBackend, error) { return backend, nil }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.stopAll)
	if err := manager.write("session", "hello €\r"); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			if event.name != terminalOutputEvent {
				continue
			}
			payload := event.payload.(TerminalOutputEvent)
			if payload.SessionID == "session" && strings.Contains(payload.Data, "hello €") {
				backend.finish(nil)
				goto waitForExit
			}
		case <-deadline:
			t.Fatal("timed out waiting for terminal output")
		}
	}

waitForExit:
	deadline = time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			if event.name != terminalExitEvent {
				continue
			}
			payload := event.payload.(TerminalExitEvent)
			if payload.SessionID != "session" || payload.Error != "" {
				t.Fatalf("unexpected exit payload %#v", payload)
			}
			manager.mu.Lock()
			remaining := len(manager.sessions)
			manager.mu.Unlock()
			if remaining != 0 {
				t.Fatalf("expected session cleanup, got %d sessions", remaining)
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for terminal exit")
		}
	}
}

func TestTerminalManagerDrainsFinalOutput(t *testing.T) {
	events := make(chan terminalTestEvent, 16)
	manager := newTerminalManager(func(name string, payload any) {
		events <- terminalTestEvent{name: name, payload: payload}
	})
	backend := newTerminalTestBackend(false)
	if err := manager.startWith("final", func() (terminalBackend, error) { return backend, nil }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.stopAll)
	go func() {
		_, _ = backend.writeOutput([]byte("final output €"))
		backend.finish(nil)
	}()

	var output strings.Builder
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			switch event.name {
			case terminalOutputEvent:
				output.WriteString(event.payload.(TerminalOutputEvent).Data)
			case terminalExitEvent:
				if !strings.Contains(output.String(), "final output €") {
					t.Fatalf("final output was truncated: %q", output.String())
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for natural terminal exit")
		}
	}
}

type terminalTestEvent struct {
	name    string
	payload any
}

type terminalTestBackend struct {
	inputMu     sync.Mutex
	input       bytes.Buffer
	outputRead  *io.PipeReader
	outputWrite *io.PipeWriter
	echo        bool
	exited      chan struct{}
	exitOnce    sync.Once
	closeOnce   sync.Once
	waitErr     error
	sizeMu      sync.Mutex
	columns     int
	rows        int
}

func newTerminalTestBackend(echo bool) *terminalTestBackend {
	outputRead, outputWrite := io.Pipe()
	return &terminalTestBackend{
		outputRead:  outputRead,
		outputWrite: outputWrite,
		echo:        echo,
		exited:      make(chan struct{}),
	}
}

func (backend *terminalTestBackend) Read(data []byte) (int, error) {
	return backend.outputRead.Read(data)
}

func (backend *terminalTestBackend) Write(data []byte) (int, error) {
	backend.inputMu.Lock()
	_, _ = backend.input.Write(data)
	backend.inputMu.Unlock()
	if backend.echo {
		if _, err := backend.outputWrite.Write(data); err != nil {
			return 0, err
		}
	}
	return len(data), nil
}

func (backend *terminalTestBackend) resize(columns, rows int) error {
	backend.sizeMu.Lock()
	backend.columns = columns
	backend.rows = rows
	backend.sizeMu.Unlock()
	return nil
}

func (backend *terminalTestBackend) wait() error {
	<-backend.exited
	return backend.waitErr
}

func (backend *terminalTestBackend) close() error {
	backend.closeOnce.Do(func() {
		backend.exitOnce.Do(func() { close(backend.exited) })
		_ = backend.outputWrite.Close()
	})
	return nil
}

func (backend *terminalTestBackend) finish(err error) {
	backend.waitErr = err
	backend.exitOnce.Do(func() { close(backend.exited) })
}

func (backend *terminalTestBackend) writeOutput(data []byte) (int, error) {
	return backend.outputWrite.Write(data)
}

func (backend *terminalTestBackend) inputBytes() []byte {
	backend.inputMu.Lock()
	defer backend.inputMu.Unlock()
	return append([]byte(nil), backend.input.Bytes()...)
}

func (backend *terminalTestBackend) size() (int, int) {
	backend.sizeMu.Lock()
	defer backend.sizeMu.Unlock()
	return backend.columns, backend.rows
}

type terminalCoordinatedBackend struct {
	started      chan struct{}
	releaseWrite chan struct{}
	closed       chan struct{}
	startOnce    sync.Once
	closeOnce    sync.Once
}

type terminalStubbornBackend struct {
	blocked chan struct{}
}

func newTerminalStubbornBackend() *terminalStubbornBackend {
	return &terminalStubbornBackend{blocked: make(chan struct{})}
}

func (backend *terminalStubbornBackend) Read([]byte) (int, error) {
	<-backend.blocked
	return 0, io.EOF
}

func (*terminalStubbornBackend) Write(data []byte) (int, error) { return len(data), nil }
func (*terminalStubbornBackend) resize(int, int) error          { return nil }

func (backend *terminalStubbornBackend) wait() error {
	<-backend.blocked
	return nil
}

func (*terminalStubbornBackend) close() error { return nil }

func newTerminalCoordinatedBackend() *terminalCoordinatedBackend {
	return &terminalCoordinatedBackend{
		started:      make(chan struct{}),
		releaseWrite: make(chan struct{}),
		closed:       make(chan struct{}),
	}
}

func (backend *terminalCoordinatedBackend) Read([]byte) (int, error) {
	<-backend.closed
	return 0, io.EOF
}

func (backend *terminalCoordinatedBackend) Write(data []byte) (int, error) {
	backend.startOnce.Do(func() { close(backend.started) })
	<-backend.releaseWrite
	return len(data), nil
}

func (*terminalCoordinatedBackend) resize(int, int) error { return nil }

func (backend *terminalCoordinatedBackend) wait() error {
	<-backend.closed
	return nil
}

func (backend *terminalCoordinatedBackend) close() error {
	backend.closeOnce.Do(func() { close(backend.closed) })
	return nil
}
