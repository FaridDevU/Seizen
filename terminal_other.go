//go:build !windows

package main

import "errors"

func projectTerminalCommand(string, string, string) (*terminalCommand, error) {
	return nil, errors.New("cmd and wsl terminals are only available on Windows")
}

func projectAgentTerminalCommand(string, string, agentTerminalBridgeConfig) (*terminalCommand, error) {
	return nil, errors.New("agent terminals are only available on Windows")
}

func startTerminalBackend(*terminalCommand, int, int) (terminalBackend, error) {
	return nil, errors.New("ConPTY is only available on Windows")
}
