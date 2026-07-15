//go:build windows

package core

import (
	"errors"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32Window             = windows.NewLazySystemDLL("user32.dll")
	enumWindows              = user32Window.NewProc("EnumWindows")
	getWindowThreadProcessID = user32Window.NewProc("GetWindowThreadProcessId")
	isWindowVisible          = user32Window.NewProc("IsWindowVisible")
	showWindowAsync          = user32Window.NewProc("ShowWindowAsync")
	setForegroundWindow      = user32Window.NewProc("SetForegroundWindow")
)

func focusProcessWindow(pid int) error {
	var target uintptr
	callback := syscall.NewCallback(func(window, _ uintptr) uintptr {
		var windowPID uint32
		_, _, _ = getWindowThreadProcessID.Call(window, uintptr(unsafe.Pointer(&windowPID)))
		visible, _, _ := isWindowVisible.Call(window)
		if int(windowPID) == pid && visible != 0 {
			target = window
			return 0
		}
		return 1
	})
	result, _, callErr := enumWindows.Call(callback, 0)
	if target == 0 {
		if result == 0 && !errors.Is(callErr, windows.ERROR_SUCCESS) {
			return errors.New("Windows could not enumerate the App's windows")
		}
		return errors.New("the process is active, but it does not have a visible window yet")
	}
	_, _, _ = showWindowAsync.Call(target, 9) // SW_RESTORE
	foreground, _, _ := setForegroundWindow.Call(target)
	if foreground == 0 {
		return errors.New("Windows prevented bringing the App to the front; select it from the taskbar")
	}
	return nil
}
