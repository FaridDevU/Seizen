//go:build windows

package core

var keybdEvent = user32Window.NewProc("keybd_event")

const (
	vkLWin         = 0x5B
	vkH            = 0x48
	keyeventfKeyup = 0x0002
)

// startDictation presses Win+H so the Windows dictation overlay attaches to
// the currently focused text field.
func startDictation() error {
	_, _, _ = keybdEvent.Call(vkLWin, 0, 0, 0)
	_, _, _ = keybdEvent.Call(vkH, 0, 0, 0)
	_, _, _ = keybdEvent.Call(vkH, 0, keyeventfKeyup, 0)
	_, _, _ = keybdEvent.Call(vkLWin, 0, keyeventfKeyup, 0)
	return nil
}
