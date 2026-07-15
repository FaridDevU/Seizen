package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

type AgentSession struct {
	ID                string `json:"id"`
	ProjectID         string `json:"projectId"`
	ExperimentID      string `json:"experimentId,omitempty"`
	SpaceID           string `json:"spaceId"`
	Agent             string `json:"agent"`
	Name              string `json:"name"`
	Status            string `json:"status"`
	TerminalSessionID string `json:"terminalSessionId"`
	AppID             string `json:"appId,omitempty"`
}

type ProjectTerminalSession struct {
	SessionID    string `json:"sessionId"`
	ProjectID    string `json:"projectId"`
	ExperimentID string `json:"experimentId,omitempty"`
	SpaceID      string `json:"spaceId"`
	Shell        string `json:"shell"`
	Agent        string `json:"agent,omitempty"`
	Status       string `json:"status"`
	Name         string `json:"name"`
}

func normalizeProjectSpaceID(spaceID string) (string, error) {
	spaceID = strings.TrimSpace(spaceID)
	if spaceID == "" {
		return currentProjectSpaceID, nil
	}
	if spaceID != currentProjectSpaceID {
		return "", errors.New("the requested space is not available in this project")
	}
	return spaceID, nil
}

func (a *App) ListProjectAgentSessions(projectID, spaceID string) ([]AgentSession, error) {
	return a.listProjectAgentSessions(projectID, "", spaceID, false)
}

func (a *App) ListProjectAgentSessionsContext(projectID, experimentID, spaceID string) ([]AgentSession, error) {
	return a.listProjectAgentSessions(projectID, experimentID, spaceID, true)
}

func (a *App) listProjectAgentSessions(projectID, experimentID, spaceID string, filterContext bool) ([]AgentSession, error) {
	spaceID, err := normalizeProjectSpaceID(spaceID)
	if err != nil {
		return nil, err
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, errors.New("the project is required")
	}
	manager := a.currentTerminalManager()
	if manager == nil {
		return []AgentSession{}, nil
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	items := make([]AgentSession, 0)
	for _, session := range manager.sessions {
		if session.projectID != projectID || session.spaceID != spaceID || session.agent == "" || session.stopping.Load() ||
			(filterContext && session.experimentID != experimentID) {
			continue
		}
		name := "Claude Code"
		if session.agent == "codex" {
			name = "Codex"
		} else if session.agent == "opencode" {
			name = "OpenCode"
		}
		items = append(items, AgentSession{
			ID: session.id, ProjectID: projectID, ExperimentID: session.experimentID, SpaceID: spaceID, Agent: session.agent,
			Name: fmt.Sprintf("%s · %s", name, shortSessionID(session.id)), Status: "running",
			TerminalSessionID: session.id,
			AppID:             session.appID,
		})
	}
	return items, nil
}

func (a *App) ListProjectTerminalSessions(projectID, spaceID string) ([]ProjectTerminalSession, error) {
	return a.listProjectTerminalSessions(projectID, "", spaceID, false)
}

func (a *App) ListProjectTerminalSessionsContext(projectID, experimentID, spaceID string) ([]ProjectTerminalSession, error) {
	return a.listProjectTerminalSessions(projectID, experimentID, spaceID, true)
}

func (a *App) listProjectTerminalSessions(projectID, experimentID, spaceID string, filterContext bool) ([]ProjectTerminalSession, error) {
	spaceID, err := normalizeProjectSpaceID(spaceID)
	if err != nil {
		return nil, err
	}
	manager := a.currentTerminalManager()
	if manager == nil {
		return []ProjectTerminalSession{}, nil
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	items := make([]ProjectTerminalSession, 0)
	for _, session := range manager.sessions {
		if session.projectID == projectID && session.spaceID == spaceID && session.serverID == "" && !session.stopping.Load() &&
			(!filterContext || session.experimentID == experimentID) {
			items = append(items, ProjectTerminalSession{
				SessionID: session.id, ProjectID: projectID, ExperimentID: session.experimentID, SpaceID: spaceID, Shell: session.shell,
				Agent: session.agent, Status: "running", Name: terminalSessionName(session),
			})
		}
	}
	return items, nil
}

func terminalSessionName(session *terminalSession) string {
	name := strings.ToUpper(session.shell)
	if session.agent == "claude" {
		name = "Claude Code"
	} else if session.agent == "codex" {
		name = "Codex"
	} else if session.agent == "opencode" {
		name = "OpenCode"
	}
	return fmt.Sprintf("%s · %s", name, shortSessionID(session.id))
}

func (a *App) SendAgentTask(projectID, spaceID, sessionID, task string, confirmed bool) (sendErr error) {
	return a.sendAgentTask(projectID, "", spaceID, sessionID, task, confirmed, false)
}

func (a *App) SendAgentTaskContext(projectID, experimentID, spaceID, sessionID, task string, confirmed bool) error {
	return a.sendAgentTask(projectID, experimentID, spaceID, sessionID, task, confirmed, true)
}

func (a *App) sendAgentTask(projectID, experimentID, spaceID, sessionID, task string, confirmed, filterContext bool) (sendErr error) {
	scope := AgentTokenScope{SessionID: strings.TrimSpace(sessionID), ProjectID: strings.TrimSpace(projectID), ExperimentID: strings.TrimSpace(experimentID)}
	arguments, _ := json.Marshal(map[string]any{
		"spaceId": strings.TrimSpace(spaceID), "confirmed": confirmed, "characters": len([]rune(task)),
	})
	defer func() { a.recordAgentAudit(scope, "agent.task.send", arguments, sendErr) }()
	if !confirmed {
		return errors.New("confirm the task before sending it to the agent")
	}
	spaceID, err := normalizeProjectSpaceID(spaceID)
	if err != nil {
		return err
	}
	task = strings.TrimSpace(task)
	if task == "" {
		return errors.New("the agent task is empty")
	}
	if len(task) > maxTerminalInput-32 {
		return errors.New("the agent task exceeds the allowed limit")
	}
	for _, character := range task {
		if unicode.IsControl(character) && character != '\r' && character != '\n' && character != '\t' {
			return errors.New("the task contains disallowed control characters")
		}
	}
	manager := a.currentTerminalManager()
	if manager == nil {
		return errors.New("the agent session was not found")
	}
	session := manager.session(sessionID)
	if session == nil || session.projectID != projectID || session.spaceID != spaceID || session.agent == "" ||
		(filterContext && session.experimentID != experimentID) {
		return errors.New("the session does not belong to the authorized agent, project and space")
	}
	scope.AppID = session.appID
	// Bracketed paste keeps the multi-line prompt as one PTY submission.
	if err = manager.write(sessionID, "\x1b[200~"+task+"\x1b[201~\r"); err != nil {
		return err
	}
	a.emitAgentEvent("agent.task.sent", map[string]any{
		"sessionId": sessionID, "projectId": projectID, "experimentId": session.experimentID, "spaceId": spaceID, "agent": session.agent,
	})
	return nil
}

func (a *App) CancelAgentTask(projectID, spaceID, sessionID string) (cancelErr error) {
	return a.cancelAgentTask(projectID, "", spaceID, sessionID, false)
}

func (a *App) CancelAgentTaskContext(projectID, experimentID, spaceID, sessionID string) error {
	return a.cancelAgentTask(projectID, experimentID, spaceID, sessionID, true)
}

func (a *App) cancelAgentTask(projectID, experimentID, spaceID, sessionID string, filterContext bool) (cancelErr error) {
	scope := AgentTokenScope{SessionID: strings.TrimSpace(sessionID), ProjectID: strings.TrimSpace(projectID), ExperimentID: strings.TrimSpace(experimentID)}
	arguments, _ := json.Marshal(map[string]any{"spaceId": strings.TrimSpace(spaceID)})
	defer func() { a.recordAgentAudit(scope, "agent.task.cancel", arguments, cancelErr) }()
	spaceID, err := normalizeProjectSpaceID(spaceID)
	if err != nil {
		return err
	}
	manager := a.currentTerminalManager()
	if manager == nil {
		return errors.New("the agent session was not found")
	}
	session := manager.session(sessionID)
	if session == nil || session.projectID != projectID || session.spaceID != spaceID || session.agent == "" ||
		(filterContext && session.experimentID != experimentID) {
		return errors.New("the session does not belong to the authorized agent, project and space")
	}
	scope.AppID = session.appID
	if err = manager.writeBytes(sessionID, []byte{3}); err != nil {
		return err
	}
	a.emitAgentEvent("agent.task.cancelled", map[string]any{"sessionId": sessionID, "projectId": projectID, "experimentId": session.experimentID, "spaceId": spaceID})
	return nil
}

func shortSessionID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
