//go:build !windows

package core

import "errors"

func platformManagedTerminalPorts(*terminalSession) ([]DetectedTerminalEndpoint, error) {
	return nil, errors.New("terminal port detection is only available on Windows")
}

func platformManagedAppOwnsPort(managedProcess, int) (bool, bool) { return false, false }

func platformManagedAppDetectedPort(managedProcess) (bool, int) { return false, 0 }
