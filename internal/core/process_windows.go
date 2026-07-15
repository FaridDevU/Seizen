//go:build windows

package core

import (
	"os/exec"
	"syscall"
)

func hideWindow(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
