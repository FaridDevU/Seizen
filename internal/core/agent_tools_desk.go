package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Desk tools let an agent leave results on the user's board (documents, notes,
// checklists) and organize project files with human approval. Everything is
// contained to the authorized project's context path; file mutations reuse the
// single-use approval gate the rest of the bridge already trusts.

const deskActionEvent = "seizen:desk-action"
const maxDeskNoteCharacters = 20_000
const maxDeskTodoItems = 200
const maxFilesListEntries = 500

// DeskActionEvent is consumed by the workspace that owns the project context.
type DeskActionEvent struct {
	ProjectID    string          `json:"projectId"`
	ExperimentID string          `json:"experimentId"`
	Kind         string          `json:"kind"`
	Payload      json.RawMessage `json:"payload"`
}

type agentDeskOpenInput struct {
	Path string `json:"path,omitempty" jsonschema:"Project-relative path of a document to open on the user's board (PDF, Word, image, video, audio, or text)."`
	URL  string `json:"url,omitempty" jsonschema:"HTTP or HTTPS address to open as a browser panel instead of a document."`
}

type agentDeskNoteInput struct {
	Text string `json:"text" jsonschema:"Markdown text of the note placed on the user's board."`
}

type agentDeskTodoInput struct {
	Items []string `json:"items" jsonschema:"Checklist entries placed on the user's board."`
}

type agentFilesListInput struct {
	Path string `json:"path,omitempty" jsonschema:"Project-relative folder to list; empty for the project root."`
}

type agentFilesMoveInput struct {
	From       string `json:"from" jsonschema:"Project-relative path of the file or folder to move."`
	To         string `json:"to" jsonschema:"Project-relative destination path, including the new name."`
	ApprovalID string `json:"approvalId,omitempty" jsonschema:"Single-use approval returned by the first request."`
}

type agentFilesRenameInput struct {
	Path       string `json:"path" jsonschema:"Project-relative path of the file or folder to rename."`
	Name       string `json:"name" jsonschema:"New name, without any path separators."`
	ApprovalID string `json:"approvalId,omitempty" jsonschema:"Single-use approval returned by the first request."`
}

type agentFileEntry struct {
	Name     string `json:"name"`
	Dir      bool   `json:"dir"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
}

func (bridge *AgentBridge) callDeskTool(ctx context.Context, scope AgentTokenScope, tool string, arguments json.RawMessage) (any, error) {
	switch tool {
	case "seizen_desk_open":
		var input agentDeskOpenInput
		if err := decodeAgentArguments(arguments, &input); err != nil {
			return nil, err
		}
		return bridge.deskOpen(ctx, scope, input)
	case "seizen_desk_add_note":
		var input agentDeskNoteInput
		if err := decodeAgentArguments(arguments, &input); err != nil {
			return nil, err
		}
		text := strings.TrimSpace(input.Text)
		if text == "" {
			return nil, errors.New("the note text is required")
		}
		if len(text) > maxDeskNoteCharacters {
			text = text[:maxDeskNoteCharacters]
		}
		return bridge.emitDeskAction(scope, "note", map[string]string{"text": text})
	case "seizen_desk_add_todo":
		var input agentDeskTodoInput
		if err := decodeAgentArguments(arguments, &input); err != nil {
			return nil, err
		}
		items := make([]string, 0, len(input.Items))
		for _, item := range input.Items {
			item = strings.TrimSpace(item)
			if item != "" {
				items = append(items, item)
			}
			if len(items) >= maxDeskTodoItems {
				break
			}
		}
		if len(items) == 0 {
			return nil, errors.New("the checklist needs at least one entry")
		}
		return bridge.emitDeskAction(scope, "todo", map[string]any{"items": items})
	case "seizen_desk_tidy":
		return bridge.emitDeskAction(scope, "tidy", map[string]any{})
	case "seizen_files_list":
		var input agentFilesListInput
		if err := decodeAgentArguments(arguments, &input); err != nil {
			return nil, err
		}
		return bridge.filesList(ctx, scope, input.Path)
	case "seizen_files_move":
		var input agentFilesMoveInput
		if err := decodeAgentArguments(arguments, &input); err != nil {
			return nil, err
		}
		return bridge.filesMove(ctx, scope, input)
	case "seizen_files_rename":
		var input agentFilesRenameInput
		if err := decodeAgentArguments(arguments, &input); err != nil {
			return nil, err
		}
		return bridge.filesRename(ctx, scope, input)
	default:
		return nil, errors.New("unrecognized agent tool")
	}
}

func (bridge *AgentBridge) deskOpen(ctx context.Context, scope AgentTokenScope, input agentDeskOpenInput) (any, error) {
	hasPath := strings.TrimSpace(input.Path) != ""
	hasURL := strings.TrimSpace(input.URL) != ""
	if hasPath == hasURL {
		return nil, errors.New("provide exactly one of path or url")
	}
	if hasURL {
		url := strings.TrimSpace(input.URL)
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			return nil, errors.New("only HTTP or HTTPS addresses can be opened")
		}
		return bridge.emitDeskAction(scope, "browser", map[string]string{"url": url})
	}
	_, absolute, err := bridge.resolveProjectEntry(ctx, scope, input.Path)
	if err != nil {
		return nil, err
	}
	asset, err := bridge.app.storeProjectWorkspaceAsset(scope.ProjectID, absolute)
	if err != nil {
		return nil, err
	}
	return bridge.emitDeskAction(scope, "document", asset)
}

func (bridge *AgentBridge) emitDeskAction(scope AgentTokenScope, kind string, payload any) (any, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("the desk action is not valid")
	}
	bridge.app.emitAgentEvent(deskActionEvent, DeskActionEvent{
		ProjectID:    scope.ProjectID,
		ExperimentID: scope.ExperimentID,
		Kind:         kind,
		Payload:      encoded,
	})
	return map[string]any{
		"delivered": true,
		"note":      "the panel appears on the user's board for this project context",
	}, nil
}

// resolveProjectEntry maps a project-relative path to an absolute one and
// refuses anything that escapes the project's context root.
func (bridge *AgentBridge) resolveProjectEntry(ctx context.Context, scope AgentTokenScope, relative string) (root, absolute string, err error) {
	db, err := bridge.app.database.Pool(ctx)
	if err != nil {
		return "", "", err
	}
	storedPath, err := projectPathForExperiment(ctx, db, scope.ProjectID, scope.ExperimentID)
	if err != nil {
		return "", "", err
	}
	root, err = existingDirectory(storedPath)
	if err != nil {
		return "", "", err
	}
	relative = strings.TrimSpace(relative)
	if relative == "" {
		return root, root, nil
	}
	joined := filepath.Join(root, filepath.FromSlash(relative))
	contained, err := filepath.Rel(root, joined)
	if err != nil || contained == ".." ||
		strings.HasPrefix(contained, ".."+string(os.PathSeparator)) {
		return "", "", errors.New("the path is outside the project")
	}
	return root, joined, nil
}

func (bridge *AgentBridge) filesList(ctx context.Context, scope AgentTokenScope, relative string) (any, error) {
	_, absolute, err := bridge.resolveProjectEntry(ctx, scope, relative)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(absolute)
	if err != nil {
		return nil, fmt.Errorf("could not list the folder: %w", err)
	}
	sort.Slice(entries, func(a, b int) bool {
		if entries[a].IsDir() != entries[b].IsDir() {
			return entries[a].IsDir()
		}
		return entries[a].Name() < entries[b].Name()
	})
	truncated := false
	if len(entries) > maxFilesListEntries {
		entries = entries[:maxFilesListEntries]
		truncated = true
	}
	items := make([]agentFileEntry, 0, len(entries))
	for _, entry := range entries {
		item := agentFileEntry{Name: entry.Name(), Dir: entry.IsDir()}
		if info, infoErr := entry.Info(); infoErr == nil {
			item.Size = info.Size()
			item.Modified = info.ModTime().UTC().Format("2006-01-02T15:04:05Z")
		}
		items = append(items, item)
	}
	return map[string]any{"entries": items, "truncated": truncated}, nil
}

func (bridge *AgentBridge) filesMove(ctx context.Context, scope AgentTokenScope, input agentFilesMoveInput) (any, error) {
	from := strings.TrimSpace(input.From)
	to := strings.TrimSpace(input.To)
	if from == "" || to == "" {
		return nil, errors.New("both from and to are required")
	}
	request := map[string]string{"from": from, "to": to}
	pending, approved, err := bridge.requireApproval(scope, input.ApprovalID,
		"files.move", approvalResource("files.move", request), request)
	if err != nil {
		return nil, err
	}
	if !approved {
		return pending, nil
	}
	root, source, err := bridge.resolveProjectEntry(ctx, scope, from)
	if err != nil {
		return nil, err
	}
	if source == root {
		return nil, errors.New("the project root cannot be moved")
	}
	_, destination, err := bridge.resolveProjectEntry(ctx, scope, to)
	if err != nil {
		return nil, err
	}
	return executeProjectRename(source, destination)
}

func (bridge *AgentBridge) filesRename(ctx context.Context, scope AgentTokenScope, input agentFilesRenameInput) (any, error) {
	path := strings.TrimSpace(input.Path)
	name := strings.TrimSpace(input.Name)
	if path == "" || name == "" {
		return nil, errors.New("both path and name are required")
	}
	if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return nil, errors.New("the new name cannot contain path separators")
	}
	request := map[string]string{"path": path, "name": name}
	pending, approved, err := bridge.requireApproval(scope, input.ApprovalID,
		"files.rename", approvalResource("files.rename", request), request)
	if err != nil {
		return nil, err
	}
	if !approved {
		return pending, nil
	}
	root, source, err := bridge.resolveProjectEntry(ctx, scope, path)
	if err != nil {
		return nil, err
	}
	if source == root {
		return nil, errors.New("the project root cannot be renamed")
	}
	return executeProjectRename(source, filepath.Join(filepath.Dir(source), name))
}

func executeProjectRename(source, destination string) (any, error) {
	if _, err := os.Lstat(source); err != nil {
		return nil, errors.New("the source does not exist")
	}
	if _, err := os.Lstat(destination); err == nil {
		return nil, errors.New("the destination already exists")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return nil, fmt.Errorf("could not prepare the destination folder: %w", err)
	}
	if err := os.Rename(source, destination); err != nil {
		return nil, fmt.Errorf("could not move: %w", err)
	}
	return map[string]any{"moved": true, "to": displayPath(destination)}, nil
}
