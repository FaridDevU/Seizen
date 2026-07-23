package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// AssistantAction is one app action the model asked for; the frontend executes them in order.
type AssistantAction struct {
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type AssistantReply struct {
	Text    string            `json:"text"`
	Actions []AssistantAction `json:"actions"`
}

// AssistantChatReply is a turn's answer plus the chat it landed in (created on
// the first message).
type AssistantChatReply struct {
	ChatID  string            `json:"chatId"`
	Text    string            `json:"text"`
	Actions []AssistantAction `json:"actions"`
}

func assistantSystemPrompt(projects []Project) string {
	var b strings.Builder
	b.WriteString(`You are Seizen's built-in assistant. Seizen is a desktop app that organizes projects (called "spaces"), each with a workspace board of panels: terminals, notes, to-do lists, and documents.

You control the app by calling tools. Chain as many tool calls as needed, in the order they should run. To work inside a project, call open_project first, then add_panel for each panel. Finish with one short sentence in the user's language saying what you did.

App sections: home, folders (the project library), servers, settings.

Projects, most recently used first:
`)
	for i, project := range projects {
		if i == 20 {
			break
		}
		b.WriteString("- ")
		b.WriteString(project.Name)
		b.WriteString("\n")
	}
	if len(projects) == 0 {
		b.WriteString("(none yet)\n")
	}
	return b.String()
}

var assistantTools = []anthropic.ToolUnionParam{
	{OfTool: &anthropic.ToolParam{
		Name:        "open_project",
		Description: anthropic.String("Open a project (space) by its exact name from the project list. Shows its workspace board. For \"the most recent project\" use the first name in the list."),
		InputSchema: anthropic.ToolInputSchemaParam{Properties: map[string]any{
			"name": map[string]any{"type": "string", "description": "Exact project name from the list"},
		}},
	}},
	{OfTool: &anthropic.ToolParam{
		Name:        "open_section",
		Description: anthropic.String("Navigate to an app section."),
		InputSchema: anthropic.ToolInputSchemaParam{Properties: map[string]any{
			"section": map[string]any{"type": "string", "enum": []string{"home", "folders", "servers", "settings"}},
		}},
	}},
	{OfTool: &anthropic.ToolParam{
		Name:        "add_panel",
		Description: anthropic.String("Add panels to the open project's workspace (open_project first if needed). 'tidy' arranges the board instead of adding. For terminals pick the shell: cmd, wsl, or the AI agents claude (Claude Code), codex, opencode."),
		InputSchema: anthropic.ToolInputSchemaParam{Properties: map[string]any{
			"panel": map[string]any{"type": "string", "enum": []string{"note", "todo", "document", "terminal", "tidy"}},
			"shell": map[string]any{"type": "string", "enum": []string{"cmd", "wsl", "claude", "codex", "opencode"}, "description": "Terminal shell, only for panel=terminal"},
			"count": map[string]any{"type": "integer", "description": "How many to add, 1-4. Default 1."},
		}},
	}},
}

// AskAssistant sends one chat turn to the configured provider and returns the
// planned actions for the frontend to execute. chatID "" starts a new chat; the
// conversation's memory is the CLI's own session files (resumed per turn, no
// process left running) or the stored messages for the API provider.
func (a *App) AskAssistant(chatID, prompt string) (AssistantChatReply, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return AssistantChatReply{}, errors.New("empty prompt")
	}
	config, err := a.database.assistantConfig(a.context())
	if err != nil {
		return AssistantChatReply{}, err
	}

	projects, err := a.ListProjects("", "")
	if err != nil {
		projects = nil
	}
	active := projects[:0]
	for _, project := range projects {
		if !project.Archived && !project.Missing {
			active = append(active, project)
		}
	}
	sort.SliceStable(active, func(i, j int) bool { return active[i].UpdatedAt > active[j].UpdatedAt })

	system := assistantSystemPrompt(active)
	return a.runAssistantTurn(config, chatID, prompt, "", system, assistantTools, "")
}

// runAssistantTurn routes one chat turn to the configured provider, keeping the
// chat's stored history and CLI session in sync. system is the surface's prompt
// (Home or a project workspace); tools is its API tool catalog, and the CLI
// path derives a JSON protocol from the same catalog automatically via
// cliProtocol. titlePrefix labels new chats (e.g. the project name).
func (a *App) runAssistantTurn(config assistantStoredConfig, chatID, prompt, titlePrefix, system string, tools []anthropic.ToolUnionParam, projectDir string) (AssistantChatReply, error) {
	provider := config.provider()
	title := prompt
	if titlePrefix != "" {
		title = titlePrefix + ": " + prompt
	}
	chat, err := a.loadOrCreateAssistantChat(a.context(), chatID, provider, title)
	if err != nil {
		return AssistantChatReply{}, err
	}
	// Sessions belong to their provider: switching providers restarts the memory.
	session := chat.Session
	if chat.Provider != provider {
		session = ""
	}

	var reply AssistantReply
	if provider == "claude-cli" || provider == "codex-cli" {
		var newSession string
		cliSystem := system + cliProtocol(tools)
		reply, newSession, err = a.askAssistantCLI(strings.TrimSuffix(provider, "-cli"), config.modelFor(provider), config.ClaudeOAuthToken, session, cliSystem, prompt, projectDir)
		if err == nil {
			_ = a.setAssistantChatSession(a.context(), chat.ID, provider, newSession)
		}
	} else {
		reply, err = a.askAssistantAPI(config, system, tools, chat.ID, prompt)
	}
	if err != nil {
		return AssistantChatReply{}, err
	}

	_ = a.appendAssistantMessage(a.context(), chat.ID, "user", prompt)
	_ = a.appendAssistantMessage(a.context(), chat.ID, "assistant", reply.Text)
	return AssistantChatReply{ChatID: chat.ID, Text: reply.Text, Actions: reply.Actions}, nil
}

// cliProtocol renders the API tool catalog as the JSON-reply protocol the
// subscription CLIs follow (they cannot call our tools directly).
func cliProtocol(tools []anthropic.ToolUnionParam) string {
	var b strings.Builder
	b.WriteString(`
You cannot call tools directly. Instead, respond with ONLY a JSON object, no prose around it:
{"text": "<one short sentence in the user's language>", "actions": [{"name": "<tool>", "input": {...}}]}
Available tools (name, input schema):
`)
	for _, tool := range tools {
		if tool.OfTool == nil {
			continue
		}
		schema, _ := json.Marshal(tool.OfTool.InputSchema.Properties)
		b.WriteString("- ")
		b.WriteString(tool.OfTool.Name)
		b.WriteString(" ")
		b.Write(schema)
		if tool.OfTool.Description.Valid() {
			b.WriteString(" — ")
			b.WriteString(tool.OfTool.Description.Value)
		}
		b.WriteString("\n")
	}
	b.WriteString(`Actions run in order. An empty actions array is fine for questions.`)
	return b.String()
}

// askAssistantAPI answers through the Anthropic API, replaying the chat's
// stored messages so the model remembers the conversation.
func (a *App) askAssistantAPI(config assistantStoredConfig, system string, tools []anthropic.ToolUnionParam, chatID, prompt string) (AssistantReply, error) {
	key := config.activeKey()
	if key == "" {
		return AssistantReply{}, errors.New("no API key: add one in Settings > Agent APIs")
	}
	model := config.modelFor("api")

	history, err := a.GetAssistantChatMessages(chatID)
	if err != nil {
		history = nil
	}
	// Cap the replay so an old chat can't grow the request without bound.
	if len(history) > 30 {
		history = history[len(history)-30:]
	}
	messages := []anthropic.MessageParam{}
	for _, message := range history {
		if strings.TrimSpace(message.Content) == "" {
			continue
		}
		if message.Role == "assistant" {
			messages = append(messages, anthropic.NewAssistantMessage(anthropic.NewTextBlock(message.Content)))
		} else {
			messages = append(messages, anthropic.NewUserMessage(anthropic.NewTextBlock(message.Content)))
		}
	}
	messages = append(messages, anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)))

	ctx, cancel := context.WithTimeout(a.context(), 60*time.Second)
	defer cancel()

	client := anthropic.NewClient(option.WithAPIKey(key))
	response, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: 2048,
		System:    []anthropic.TextBlockParam{{Text: system}},
		Messages:  messages,
		Tools:     tools,
	})
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) && apiErr.StatusCode == 401 {
			return AssistantReply{}, errors.New("the API key was rejected: check it in Settings")
		}
		return AssistantReply{}, fmt.Errorf("assistant request failed: %w", err)
	}
	if response.StopReason == anthropic.StopReasonRefusal {
		return AssistantReply{}, errors.New("the assistant declined this request")
	}

	reply := AssistantReply{Actions: []AssistantAction{}}
	var text []string
	for _, block := range response.Content {
		switch variant := block.AsAny().(type) {
		case anthropic.TextBlock:
			text = append(text, variant.Text)
		case anthropic.ToolUseBlock:
			reply.Actions = append(reply.Actions, AssistantAction{
				Name:  variant.Name,
				Input: json.RawMessage(variant.JSON.Input.Raw()),
			})
		}
	}
	reply.Text = strings.TrimSpace(strings.Join(text, " "))
	return reply, nil
}

// askAssistantCLI answers through the subscription CLI (claude/codex) in one
// headless conversational turn: no API key involved, the model plans actions as
// a JSON object we parse and hand to the frontend. session resumes the chat's
// prior turns; the returned session id continues it next time.
func (a *App) askAssistantCLI(agent, model, token, session, system, prompt, projectDir string) (AssistantReply, string, error) {
	settings, err := a.GetAgentResourceSettings()
	if err != nil {
		settings = AgentResourceSettings{}
	}
	environment := settings.ClaudeEnvironment
	if agent == "codex" {
		environment = settings.CodexEnvironment
	}
	raw, newSession, err := runAssistantCLI(a.context(), agent, environment, model, system, prompt, token, session, projectDir)
	if err != nil {
		return AssistantReply{}, "", err
	}
	reply, err := parseAssistantJSONReply(raw)
	return reply, newSession, err
}
