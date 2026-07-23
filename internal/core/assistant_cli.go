package core

// Pure parsing for the subscription-based assistant: headless CLI output and
// the CLIs' local model caches. Ported from CHAI's orchestrator runners — the
// sanctioned way to use a Claude Pro/Max or ChatGPT subscription is driving the
// real `claude` / `codex` binaries headless, never their OAuth tokens directly.

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Claude Code's agentic/meta tools, disabled for a pure conversation turn so the
// reply comes back as text instead of a tool_use that would burn the turn budget.
var assistantConversationalDisallowed = strings.Join([]string{
	"Task", "Bash", "BashOutput", "KillShell", "Glob", "Grep", "Read", "Edit", "Write",
	"MultiEdit", "NotebookEdit", "WebFetch", "WebSearch", "TodoWrite", "PowerShell", "Skill",
	"ToolSearch", "AskUserQuestion", "EnterPlanMode", "ExitPlanMode",
}, ",")

// Workspace turns run inside the project folder with read-only code access:
// Read/Glob/Grep stay enabled so the assistant can analyze the code itself;
// anything that mutates or reaches the network stays off.
var assistantWorkspaceDisallowed = strings.Join([]string{
	"Task", "Bash", "BashOutput", "KillShell", "Edit", "Write",
	"MultiEdit", "NotebookEdit", "WebFetch", "WebSearch", "TodoWrite", "PowerShell", "Skill",
	"ToolSearch", "AskUserQuestion", "EnterPlanMode", "ExitPlanMode",
}, ",")

// parseClaudeHeadlessResult scans `claude -p --output-format stream-json` output
// for the final result event and returns its text plus the session id that a
// later turn can --resume.
func parseClaudeHeadlessResult(output string) (string, string, error) {
	text := ""
	session := ""
	seen := false
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var event struct {
			Type      string `json:"type"`
			Result    string `json:"result"`
			IsError   bool   `json:"is_error"`
			Subtype   string `json:"subtype"`
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		if event.SessionID != "" {
			session = event.SessionID
		}
		if event.Type == "result" {
			seen = true
			if event.IsError {
				message := event.Result
				if message == "" {
					message = event.Subtype
				}
				return "", "", fmt.Errorf("claude failed: %s", message)
			}
			text = event.Result
		}
	}
	if !seen {
		return "", "", errors.New("claude produced no result (is the account signed in?)")
	}
	return text, session, nil
}

// parseCodexHeadlessResult scans `codex exec --json` JSONL for the final agent
// message plus the thread id that `codex exec resume` continues.
func parseCodexHeadlessResult(output string) (string, string, error) {
	text := ""
	thread := ""
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var event struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
			Item     struct {
				Type string `json:"item_type"`
				T2   string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		if event.ThreadID != "" {
			thread = event.ThreadID
		}
		switch event.Type {
		case "item.completed":
			if event.Item.Type == "agent_message" || event.Item.T2 == "agent_message" {
				text = event.Item.Text
			}
		case "turn.failed":
			return "", "", fmt.Errorf("codex failed: %s", event.Error.Message)
		case "error":
			return "", "", fmt.Errorf("codex failed: %s", event.Message)
		}
	}
	if text == "" {
		return "", "", errors.New("codex produced no reply (is the account signed in?)")
	}
	return text, thread, nil
}

// parseAssistantJSONReply extracts the {"text": ..., "actions": [...]} object a
// subscription model returns as plain text (possibly wrapped in code fences).
func parseAssistantJSONReply(raw string) (AssistantReply, error) {
	trimmed := strings.TrimSpace(raw)
	if start := strings.Index(trimmed, "{"); start > 0 {
		trimmed = trimmed[start:]
	}
	if end := strings.LastIndex(trimmed, "}"); end >= 0 {
		trimmed = trimmed[:end+1]
	}
	var reply AssistantReply
	if err := json.Unmarshal([]byte(trimmed), &reply); err != nil {
		// Not JSON at all: treat the whole answer as plain text with no actions.
		return AssistantReply{Text: strings.TrimSpace(raw), Actions: []AssistantAction{}}, nil
	}
	if reply.Actions == nil {
		reply.Actions = []AssistantAction{}
	}
	return reply, nil
}

// parseClaudeModelCache reads `.claude.json`'s additionalModelOptionsCache: the
// extra picker entries the CLI fetched for this account. The standard families
// are built into the binary, so callers merge these onto the curated base.
func parseClaudeModelCache(raw string) []AssistantModel {
	var parsed struct {
		AdditionalModelOptionsCache []struct {
			Value    string `json:"value"`
			Label    string `json:"label"`
			Disabled bool   `json:"disabled"`
		} `json:"additionalModelOptionsCache"`
	}
	if json.Unmarshal([]byte(raw), &parsed) != nil {
		return nil
	}
	models := []AssistantModel{}
	for _, entry := range parsed.AdditionalModelOptionsCache {
		if entry.Value == "" || entry.Disabled {
			continue
		}
		value := entry.Value
		if cut := strings.Index(value, "["); cut > 0 {
			value = strings.TrimSpace(value[:cut])
		}
		name := entry.Label
		if name == "" {
			name = value
		}
		models = append(models, AssistantModel{ID: value, Name: name})
	}
	return models
}

// parseCodexModelCache reads Codex's models_cache.json (what its /model picker
// shows). Only visibility:"list" entries are selectable, ordered by priority.
func parseCodexModelCache(raw string) []AssistantModel {
	var parsed struct {
		Models []struct {
			Slug        string `json:"slug"`
			DisplayName string `json:"display_name"`
			Visibility  string `json:"visibility"`
			Priority    int    `json:"priority"`
		} `json:"models"`
	}
	if json.Unmarshal([]byte(raw), &parsed) != nil {
		return nil
	}
	type option struct {
		AssistantModel
		priority int
	}
	options := []option{}
	for _, model := range parsed.Models {
		if model.Visibility != "list" || model.Slug == "" {
			continue
		}
		name := model.DisplayName
		if name == "" {
			name = model.Slug
		}
		options = append(options, option{AssistantModel{ID: model.Slug, Name: name}, model.Priority})
	}
	sort.SliceStable(options, func(i, j int) bool { return options[i].priority < options[j].priority })
	models := make([]AssistantModel, 0, len(options))
	for _, entry := range options {
		models = append(models, entry.AssistantModel)
	}
	return models
}
