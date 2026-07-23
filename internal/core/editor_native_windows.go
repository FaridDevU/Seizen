//go:build windows

package core

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Native editors (Zed, Cursor, Antigravity): they have no web UI. They used to
// be embedded by re-parenting their Win32 window into Seizen's, but GPU
// editors like Zed break as WS_CHILD (fullscreen crashes, no minimize). Now
// they stay DETACHED — a normal top-level window the OS manages — and Seizen
// only tracks the window for lifecycle and focus; the workspace shows a small
// controller card instead of a fake viewport.

const (
	nativeEditorSessionPrefix = "native-"
	nativeEditorFindTimeout   = 20 * time.Second
	nativeEditorPollEvery     = 150 * time.Millisecond
	nativeEditorWatchEvery    = 400 * time.Millisecond
)

const (
	wmClose   = 0x0010
	swRestore = 9
)

var (
	procIsWindow            = user32Window.NewProc("IsWindow")
	procPostMessage         = user32Window.NewProc("PostMessageW")
	procShowWindow          = user32Window.NewProc("ShowWindow")
	procSetForegroundWindow = user32Window.NewProc("SetForegroundWindow")
	procGetWindowTextLength = user32Window.NewProc("GetWindowTextLengthW")
	procQueryFullImageName  = windows.NewLazySystemDLL("kernel32.dll").NewProc("QueryFullProcessImageNameW")
)

type nativeEditorManager struct {
	mu       sync.Mutex
	sessions map[string]*nativeEditorSession
	emit     func(EditorExitEvent)
}

type nativeEditorSession struct {
	id     string
	window uintptr
	// guarded by manager.mu
	stopped bool
}

func (a *App) nativeEditorSessionManager() *nativeEditorManager {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.nativeEditors == nil {
		a.nativeEditors = &nativeEditorManager{sessions: make(map[string]*nativeEditorSession)}
		a.nativeEditors.emit = func(event EditorExitEvent) {
			a.emitAgentEvent(editorExitEvent, event)
		}
	}
	return a.nativeEditors
}

func (a *App) currentNativeEditorManager() *nativeEditorManager {
	a.mu.RLock()
	manager := a.nativeEditors
	a.mu.RUnlock()
	return manager
}

// StartNativeEditor opens the project in a native editor as a normal detached
// OS window and tracks it. Returns a session without a URL; the workspace
// shows a controller card for it.
func (a *App) StartNativeEditor(projectPath, id string) (EditorSession, error) {
	definition, err := a.requireExternalEditor(id)
	if err != nil {
		return EditorSession{}, err
	}
	_, folder, err := a.registeredEditorProject(projectPath)
	if err != nil {
		return EditorSession{}, err
	}
	executable, err := resolveEditorCommand(definition)
	if err != nil {
		return EditorSession{}, err
	}
	exe := strings.ToLower(definition.Name + ".exe")
	before := visibleWindowsForExe(exe)

	command := exec.Command(executable, folder)
	command.Dir = folder
	hideWindow(command)
	if err = command.Start(); err != nil {
		return EditorSession{}, fmt.Errorf("could not open %s: %w", definition.Name, err)
	}
	if err = command.Process.Release(); err != nil {
		return EditorSession{}, err
	}

	window := waitForNewEditorWindow(exe, before)
	if window == 0 {
		return EditorSession{}, fmt.Errorf("%s started but its window did not appear; check the taskbar and retry", definition.Name)
	}
	sessionID := nativeEditorSessionPrefix + definition.ID
	if uuid, uuidErr := newUUID(); uuidErr == nil {
		sessionID = nativeEditorSessionPrefix + uuid
	}
	session := &nativeEditorSession{id: sessionID, window: window}

	manager := a.nativeEditorSessionManager()
	manager.mu.Lock()
	manager.sessions[sessionID] = session
	manager.mu.Unlock()
	go manager.watch(session)
	return EditorSession{SessionID: sessionID, URL: ""}, nil
}

// FocusNativeEditor brings the detached editor window to the front,
// restoring it if it was minimized.
func (a *App) FocusNativeEditor(sessionID string) error {
	session, err := a.nativeEditorSessionByID(sessionID)
	if err != nil {
		return err
	}
	_, _, _ = procShowWindow.Call(session.window, swRestore)
	_, _, _ = procSetForegroundWindow.Call(session.window)
	return nil
}

func (a *App) stopNativeEditor(sessionID string) error {
	session, err := a.nativeEditorSessionByID(sessionID)
	if err != nil {
		return err
	}
	manager := a.currentNativeEditorManager()
	manager.mu.Lock()
	session.stopped = true
	manager.mu.Unlock()
	// A polite close: if the editor shows a "save changes" prompt, the window
	// stays visible and usable instead of losing data.
	_, _, _ = procPostMessage.Call(session.window, wmClose, 0, 0)
	return nil
}

func (a *App) nativeEditorSessionByID(sessionID string) (*nativeEditorSession, error) {
	manager := a.currentNativeEditorManager()
	if manager == nil {
		return nil, errors.New("the editor session was not found")
	}
	manager.mu.Lock()
	session := manager.sessions[sessionID]
	manager.mu.Unlock()
	if session == nil {
		return nil, errors.New("the editor session was not found")
	}
	return session, nil
}

func (manager *nativeEditorManager) watch(session *nativeEditorSession) {
	ticker := time.NewTicker(nativeEditorWatchEvery)
	defer ticker.Stop()
	for range ticker.C {
		if alive, _, _ := procIsWindow.Call(session.window); alive != 0 {
			continue
		}
		manager.mu.Lock()
		stopped := session.stopped
		if manager.sessions[session.id] == session {
			delete(manager.sessions, session.id)
		}
		emit := manager.emit
		manager.mu.Unlock()
		if emit != nil {
			emit(EditorExitEvent{SessionID: session.id, Stopped: stopped})
		}
		return
	}
}

// close marks every session stopped; detached editor windows belong to the
// user and stay open when Seizen exits.
func (manager *nativeEditorManager) close() {
	manager.mu.Lock()
	for _, session := range manager.sessions {
		session.stopped = true
	}
	manager.mu.Unlock()
}

func waitForNewEditorWindow(exe string, before map[uintptr]bool) uintptr {
	deadline := time.Now().Add(nativeEditorFindTimeout)
	for time.Now().Before(deadline) {
		for window := range visibleWindowsForExe(exe) {
			if !before[window] {
				return window
			}
		}
		time.Sleep(nativeEditorPollEvery)
	}
	return 0
}

// visibleWindowsForExe enumerates visible top-level windows with a title
// whose process runs the given executable (lowercase base name). The
// EnumWindows callback is created only once: syscall.NewCallback never
// releases callbacks and creating one per call exhausts the process limit.
var windowEnum struct {
	mu       sync.Mutex
	once     sync.Once
	callback uintptr
	exe      string
	found    map[uintptr]bool
	pids     map[uint32]bool
}

func visibleWindowsForExe(exe string) map[uintptr]bool {
	windows, _ := enumVisibleWindowsForExe(exe)
	return windows
}

func enumVisibleWindowsForExe(exe string) (map[uintptr]bool, map[uint32]bool) {
	windowEnum.once.Do(func() {
		windowEnum.callback = syscall.NewCallback(func(window, _ uintptr) uintptr {
			if visible, _, _ := isWindowVisible.Call(window); visible == 0 {
				return 1
			}
			if length, _, _ := procGetWindowTextLength.Call(window); length == 0 {
				return 1
			}
			var pid uint32
			_, _, _ = getWindowThreadProcessID.Call(window, uintptr(unsafe.Pointer(&pid)))
			if pid == 0 || processExeBase(pid) != windowEnum.exe {
				return 1
			}
			windowEnum.found[window] = true
			windowEnum.pids[pid] = true
			return 1
		})
	})
	windowEnum.mu.Lock()
	defer windowEnum.mu.Unlock()
	windowEnum.exe = strings.ToLower(exe)
	windowEnum.found = make(map[uintptr]bool)
	windowEnum.pids = make(map[uint32]bool)
	_, _, _ = enumWindows.Call(windowEnum.callback, 0)
	return windowEnum.found, windowEnum.pids
}

func processExeBase(pid uint32) string {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var buffer [windows.MAX_PATH]uint16
	size := uint32(len(buffer))
	if ok, _, _ := procQueryFullImageName.Call(uintptr(handle), 0, uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size))); ok == 0 {
		return ""
	}
	return strings.ToLower(filepath.Base(windows.UTF16ToString(buffer[:size])))
}
