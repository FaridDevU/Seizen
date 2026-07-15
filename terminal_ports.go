package main

import (
	"errors"
	"fmt"
	"sort"
)

type DetectedTerminalEndpoint struct {
	PID     int    `json:"pid"`
	Port    int    `json:"port"`
	URL     string `json:"url"`
	Managed bool   `json:"managed"`
}

type ManagedAppDetection struct {
	TerminalSessionID string `json:"terminalSessionId"`
	ProcessID         int    `json:"processId"`
	Port              int    `json:"port"`
	PreviewURL        string `json:"previewUrl"`
	Verified          bool   `json:"verified"`
	Message           string `json:"message"`
}

var terminalPortLookup = platformManagedTerminalPorts

func (a *App) DetectTerminalApp(sessionID string) (ManagedAppDetection, error) {
	manager := a.currentTerminalManager()
	if manager == nil {
		return ManagedAppDetection{}, errors.New("the managed terminal was not found")
	}
	session := manager.session(sessionID)
	if session == nil || session.projectID == "" || session.serverID != "" || session.stopping.Load() {
		return ManagedAppDetection{}, errors.New("the terminal does not belong to an open project")
	}
	result := ManagedAppDetection{TerminalSessionID: session.id}
	if session.shell == "wsl" {
		result.Message = "WSL does not allow reliably attributing the port to this terminal's process; link a URL and confirm it manually."
		return result, nil
	}
	session.ioMu.Lock()
	candidates, err := terminalPortLookup(session)
	session.ioMu.Unlock()
	if err != nil {
		return result, err
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Port < candidates[j].Port })
	if len(candidates) == 0 {
		result.Message = "No listening ports were found within the terminal's managed process tree."
	} else {
		candidate := candidates[0]
		result.ProcessID, result.Port, result.PreviewURL, result.Verified = candidate.PID, candidate.Port, candidate.URL, candidate.Managed
		result.Message = fmt.Sprintf("Port %d was verified within the terminal's Job Object.", candidate.Port)
	}
	return result, nil
}

func (a *App) terminalOwnsPort(sessionID string, port int) bool {
	manager := a.currentTerminalManager()
	if manager == nil {
		return false
	}
	session := manager.session(sessionID)
	if session == nil || session.shell == "wsl" {
		return false
	}
	session.ioMu.Lock()
	candidates, err := terminalPortLookup(session)
	session.ioMu.Unlock()
	if err != nil {
		return false
	}
	for _, candidate := range candidates {
		if candidate.Port == port && candidate.Managed {
			return true
		}
	}
	return false
}
