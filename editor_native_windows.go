//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Native editors (Zed, Cursor, Antigravity): they have no web UI, so they are
// embedded by re-parenting their Win32 window inside Seizen's window and
// following the workspace node's rect via MoveNativeEditor.

const (
	nativeEditorSessionPrefix = "native-"
	nativeEditorFindTimeout   = 20 * time.Second
	nativeEditorPollEvery     = 150 * time.Millisecond
	nativeEditorWatchEvery    = 400 * time.Millisecond
)

const (
	wmClose = 0x0010
	// GWL_STYLE is -16; as a uintptr the 32-bit two's complement is passed
	// (the API reads the index as a 32-bit int).
	gwlStyle       = uintptr(0xFFFFFFF0)
	wsChild        = 0x40000000
	wsPopup        = 0x80000000
	wsCaption      = 0x00C00000
	wsThickFrame   = 0x00040000
	wsSysMenu      = 0x00080000
	wsMinimizeBox  = 0x00020000
	wsMaximizeBox  = 0x00010000
	swpNoZOrder    = 0x0004
	swpNoActivate  = 0x0010
	swpFrameChange = 0x0020
	swpShowWindow  = 0x0040
)

var (
	procSetParent           = user32Window.NewProc("SetParent")
	procMoveWindow          = user32Window.NewProc("MoveWindow")
	procIsWindow            = user32Window.NewProc("IsWindow")
	procPostMessage         = user32Window.NewProc("PostMessageW")
	procGetWindowLongPtr    = user32Window.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtr    = user32Window.NewProc("SetWindowLongPtrW")
	procSetWindowPos        = user32Window.NewProc("SetWindowPos")
	procGetWindowRect       = user32Window.NewProc("GetWindowRect")
	procMapWindowPoints     = user32Window.NewProc("MapWindowPoints")
	procGetWindowTextLength = user32Window.NewProc("GetWindowTextLengthW")
	procQueryFullImageName  = windows.NewLazySystemDLL("kernel32.dll").NewProc("QueryFullProcessImageNameW")
)

type nativeEditorManager struct {
	mu       sync.Mutex
	sessions map[string]*nativeEditorSession
	emit     func(EditorExitEvent)
}

type nativeEditorSession struct {
	id        string
	window    uintptr
	parent    uintptr
	prevStyle uintptr
	// guarded by manager.mu
	stopped    bool
	hasRect    bool
	x, y, w, h int
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

// StartNativeEditor opens the project in a native editor and re-parents its
// window inside Seizen's window. Returns a session without a URL; the
// frontend positions the window with MoveNativeEditor.
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
	parent, err := seizenMainWindow()
	if err != nil {
		return EditorSession{}, err
	}
	exe := strings.ToLower(definition.Name + ".exe")
	killOrphanEditorProcesses(exe)
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
		return EditorSession{}, fmt.Errorf("%s started but its window did not appear; close it from the taskbar and retry", definition.Name)
	}
	sessionID := nativeEditorSessionPrefix + definition.ID
	if uuid, uuidErr := newUUID(); uuidErr == nil {
		sessionID = nativeEditorSessionPrefix + uuid
	}
	session := &nativeEditorSession{id: sessionID, window: window, parent: parent}
	session.prevStyle = embedNativeEditorWindow(window, parent)

	manager := a.nativeEditorSessionManager()
	manager.mu.Lock()
	manager.sessions[sessionID] = session
	manager.mu.Unlock()
	go manager.watch(session)
	return EditorSession{SessionID: sessionID, URL: ""}, nil
}

// MoveNativeEditor places the embedded window over the workspace node's
// rect. Coordinates in physical pixels relative to the client area.
func (a *App) MoveNativeEditor(sessionID string, x, y, width, height int) error {
	manager := a.currentNativeEditorManager()
	if manager == nil {
		return errors.New("the editor session was not found")
	}
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	manager.mu.Lock()
	session := manager.sessions[sessionID]
	if session != nil {
		session.x, session.y, session.w, session.h = x, y, width, height
		session.hasRect = true
	}
	manager.mu.Unlock()
	if session == nil {
		return errors.New("the editor session was not found")
	}
	_, _, _ = procMoveWindow.Call(session.window, uintptr(x), uintptr(y), uintptr(width), uintptr(height), 1)
	return nil
}

func (a *App) stopNativeEditor(sessionID string) error {
	manager := a.currentNativeEditorManager()
	if manager == nil {
		return errors.New("the editor session was not found")
	}
	manager.mu.Lock()
	session := manager.sessions[sessionID]
	if session != nil {
		session.stopped = true
	}
	manager.mu.Unlock()
	if session == nil {
		return errors.New("the editor session was not found")
	}
	releaseNativeEditorWindow(session)
	return nil
}

func (manager *nativeEditorManager) watch(session *nativeEditorSession) {
	ticker := time.NewTicker(nativeEditorWatchEvery)
	defer ticker.Stop()
	for range ticker.C {
		if alive, _, _ := procIsWindow.Call(session.window); alive != 0 {
			manager.mu.Lock()
			x, y, w, h := session.x, session.y, session.w, session.h
			pin := session.hasRect && !session.stopped
			manager.mu.Unlock()
			if pin {
				keepNativeEditorPinned(session, x, y, w, h)
			}
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

func (manager *nativeEditorManager) close() {
	manager.mu.Lock()
	remaining := make([]*nativeEditorSession, 0, len(manager.sessions))
	for _, session := range manager.sessions {
		session.stopped = true
		remaining = append(remaining, session)
	}
	manager.mu.Unlock()
	for _, session := range remaining {
		releaseNativeEditorWindow(session)
	}
}

// embedNativeEditorWindow turns the window into a child of Seizen. Returns
// the original style to restore it when releasing it.
func embedNativeEditorWindow(window, parent uintptr) uintptr {
	prevStyle, _, _ := procGetWindowLongPtr.Call(window, uintptr(gwlStyle))
	style := (uint32(prevStyle) &^ uint32(wsPopup|wsCaption|wsThickFrame|wsSysMenu|wsMinimizeBox|wsMaximizeBox)) | wsChild
	_, _, _ = procSetWindowLongPtr.Call(window, uintptr(gwlStyle), uintptr(style))
	_, _, _ = procSetParent.Call(window, parent)
	// Zero size until the frontend sends the node's first rect.
	_, _, _ = procSetWindowPos.Call(window, 0, 0, 0, 0, 0, swpNoZOrder|swpNoActivate|swpFrameChange)
	return prevStyle
}

// keepNativeEditorPinned returns the window to the node's rect if it moved:
// editors with their own title bar (Zed, Cursor) start a system move-loop
// when dragged, which moves the child window inside Seizen. If the editor
// restored its top-level style, it embeds it again.
func keepNativeEditorPinned(session *nativeEditorSession, x, y, width, height int) {
	if style, _, _ := procGetWindowLongPtr.Call(session.window, uintptr(gwlStyle)); style&wsChild == 0 {
		embedNativeEditorWindow(session.window, session.parent)
	}
	var rect windows.Rect
	if ok, _, _ := procGetWindowRect.Call(session.window, uintptr(unsafe.Pointer(&rect))); ok == 0 {
		return
	}
	_, _, _ = procMapWindowPoints.Call(0, session.parent, uintptr(unsafe.Pointer(&rect)), 2)
	if int(rect.Left) == x && int(rect.Top) == y && int(rect.Right-rect.Left) == width && int(rect.Bottom-rect.Top) == height {
		return
	}
	_, _, _ = procMoveWindow.Call(session.window, uintptr(x), uintptr(y), uintptr(width), uintptr(height), 1)
}

// releaseNativeEditorWindow returns the window to the desktop with its
// original style and asks it to close; if the editor shows a "save changes"
// prompt, the window stays visible and usable instead of losing data.
func releaseNativeEditorWindow(session *nativeEditorSession) {
	if alive, _, _ := procIsWindow.Call(session.window); alive == 0 {
		return
	}
	_, _, _ = procSetWindowLongPtr.Call(session.window, uintptr(gwlStyle), session.prevStyle)
	_, _, _ = procSetParent.Call(session.window, 0)
	_, _, _ = procSetWindowPos.Call(session.window, 0, 120, 120, 1100, 720, swpNoZOrder|swpFrameChange|swpShowWindow)
	_, _, _ = procPostMessage.Call(session.window, wmClose, 0, 0)
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

func seizenMainWindow() (uintptr, error) {
	self, err := os.Executable()
	if err != nil {
		return 0, err
	}
	for window := range visibleWindowsForExe(strings.ToLower(filepath.Base(self))) {
		return window, nil
	}
	return 0, errors.New("Seizen's main window was not found")
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

// killOrphanEditorProcesses terminates editor processes without any visible
// window. They become orphaned when Seizen dies without releasing the
// embedded window (the child window dies with the parent); since these
// editors are single-instance, the orphan hogs subsequent opens and no new
// window ever appears.
func killOrphanEditorProcesses(exe string) {
	_, windowed := enumVisibleWindowsForExe(exe)
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if strings.ToLower(windows.UTF16ToString(entry.ExeFile[:])) != exe || windowed[entry.ProcessID] {
			continue
		}
		killIfNotStarting(entry.ProcessID)
	}
}

// killIfNotStarting terminates the process unless it has been alive for less
// than 10s (a legitimately just-launched editor doesn't have a window yet).
func killIfNotStarting(pid uint32) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var creation, exit, kernel, user windows.Filetime
	if windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user) != nil {
		return
	}
	if time.Since(time.Unix(0, creation.Nanoseconds())) < 10*time.Second {
		return
	}
	if windows.TerminateProcess(handle, 1) == nil {
		// Waits for it to release the single-instance lock before relaunching.
		_, _ = windows.WaitForSingleObject(handle, 3000)
	}
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
