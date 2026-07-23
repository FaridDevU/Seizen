package core

// The workspace command bar's assistant: same provider routing and chat memory
// as Home, but scoped to one project — it sees the project's files and its
// tools act on the board, including fanning work out to independent agent
// terminals that each start on their own task.

import (
	"errors"
	"os"
	"sort"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

const maxWorkspaceListing = 40

var workspaceAssistantTools = []anthropic.ToolUnionParam{
	{OfTool: &anthropic.ToolParam{
		Name:        "open_terminal",
		Description: anthropic.String("Open an AI agent terminal in this project that immediately starts working on the given task. For independent parallel workstreams, call this once per task with a complete, self-contained brief (in the user's language); each terminal is an isolated session, so the briefs must not depend on each other."),
		InputSchema: anthropic.ToolInputSchemaParam{Properties: map[string]any{
			"shell": map[string]any{"type": "string", "enum": []string{"claude", "codex"}, "description": "Which agent runs the task. Default claude."},
			"task":  map[string]any{"type": "string", "description": "The full brief the agent starts working on. Empty opens an idle terminal."},
		}},
	}},
	{OfTool: &anthropic.ToolParam{
		Name:        "add_note",
		Description: anthropic.String("Put a markdown note on the board (analysis results, summaries, plans)."),
		InputSchema: anthropic.ToolInputSchemaParam{Properties: map[string]any{
			"text": map[string]any{"type": "string"},
		}},
	}},
	{OfTool: &anthropic.ToolParam{
		Name:        "add_todo",
		Description: anthropic.String("Put a checklist on the board."),
		InputSchema: anthropic.ToolInputSchemaParam{Properties: map[string]any{
			"items": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		}},
	}},
	{OfTool: &anthropic.ToolParam{
		Name:        "open_browser",
		Description: anthropic.String("Open a browser panel at an HTTP or HTTPS address."),
		InputSchema: anthropic.ToolInputSchemaParam{Properties: map[string]any{
			"url": map[string]any{"type": "string"},
		}},
	}},
	{OfTool: &anthropic.ToolParam{
		Name:        "open_editor",
		Description: anthropic.String("Open a code editor over the project folder. Only the editors listed as installed in the system prompt work."),
		InputSchema: anthropic.ToolInputSchemaParam{Properties: map[string]any{
			"editor": map[string]any{"type": "string", "description": "Editor id from the installed list, e.g. zed, vscode, cursor."},
		}},
	}},
	{OfTool: &anthropic.ToolParam{
		Name:        "tidy",
		Description: anthropic.String("Arrange the board's panels neatly."),
		InputSchema: anthropic.ToolInputSchemaParam{Properties: map[string]any{}},
	}},
}

func workspaceAssistantPrompt(name, path string) string {
	var b strings.Builder
	b.WriteString(`You are Seizen's assistant inside the project "` + name + `". The project is a workspace board of panels (terminals, notes, checklists, documents, browsers) over the project folder.

You control the board by calling tools. To analyze or work on the code itself, delegate: open agent terminals with clear task briefs — each terminal is an isolated AI session working in the project folder. Split independent work across separate terminals so the streams never interfere; keep dependent work in one terminal. The terminal agents also carry Seizen's own tools (MCP): they can create and manage servers, run isolated experiments (cloned sandboxed environments), and mount Apps for this project — so for requests like "set up an isolated server", delegate to a terminal with a brief that names exactly what to set up; the agent will use those tools and ask the user for approval where needed. Finish with one short sentence in the user's language saying what you set in motion.

Top-level contents of the project folder:
`)
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) == 0 {
		b.WriteString("(empty or unreadable)\n")
		return b.String()
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for i, entry := range entries {
		if i == maxWorkspaceListing {
			b.WriteString("- …\n")
			break
		}
		b.WriteString("- ")
		b.WriteString(entry.Name())
		if entry.IsDir() {
			b.WriteString("/")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// AskWorkspaceAssistant handles one turn of the project command bar's chat.
// chatID "" starts a new chat for this project.
func (a *App) AskWorkspaceAssistant(projectID, chatID, prompt string) (AssistantChatReply, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return AssistantChatReply{}, errors.New("empty prompt")
	}
	config, err := a.database.assistantConfig(a.context())
	if err != nil {
		return AssistantChatReply{}, err
	}
	db, err := a.database.Pool(a.context())
	if err != nil {
		return AssistantChatReply{}, err
	}
	var name, path string
	if err := db.QueryRowContext(a.context(),
		`SELECT name, path FROM projects WHERE id = ?`, projectID).Scan(&name, &path); err != nil {
		return AssistantChatReply{}, errors.New("unknown project")
	}
	system := workspaceAssistantPrompt(name, path)
	if integrations, integrationsErr := a.GetEditorIntegrations(); integrationsErr == nil {
		installed := []string{}
		for _, editor := range integrations {
			if editor.Available {
				installed = append(installed, editor.ID)
			}
		}
		if len(installed) > 0 {
			system += "\nEditors installed on this computer (usable with open_editor): " +
				strings.Join(installed, ", ") + "\n"
		}
	}
	return a.runAssistantTurn(config, chatID, prompt, name, system, workspaceAssistantTools)
}
