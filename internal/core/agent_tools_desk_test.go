package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDeskAndFilesToolsStayScopedAndGated(t *testing.T) {
	app, project := newAppServerTestApp(t)
	app.agentTokens = newAgentTokenStore()
	bridge := newAgentBridge(app, app.agentTokens)
	token, err := app.agentTokens.Issue(AgentTokenScope{
		SessionID: "desk-session", ProjectID: project.ID, Permissions: appAgentPermissions,
	}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	call := func(tool string, input any) (any, error) {
		return bridge.callTool(context.Background(), token, tool, mustAgentJSON(input))
	}

	if err := os.WriteFile(filepath.Join(project.Path, "informe.txt"), []byte("hola"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(project.Path, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}

	listed, err := call("seizen_files_list", agentFilesListInput{})
	if err != nil {
		t.Fatal(err)
	}
	entries := listed.(map[string]any)["entries"].([]agentFileEntry)
	if len(entries) < 2 || !entries[0].Dir {
		t.Fatalf("unexpected listing: %#v", entries)
	}
	if _, err = call("seizen_files_list", agentFilesListInput{Path: "../outside"}); err == nil {
		t.Fatal("expected an escaping path to be rejected")
	}

	// Mutations demand a single-use approval before touching the filesystem.
	move := agentFilesMoveInput{From: "informe.txt", To: "docs/informe.txt"}
	pending, err := call("seizen_files_move", move)
	if err != nil {
		t.Fatal(err)
	}
	required := pending.(agentApprovalRequired)
	if !required.ApprovalRequired {
		t.Fatalf("expected an approval request, got %#v", pending)
	}
	if _, statErr := os.Stat(filepath.Join(project.Path, "informe.txt")); statErr != nil {
		t.Fatal("the file moved before approval")
	}
	if _, err = app.ResolveAgentApproval(required.Approval.ID, true); err != nil {
		t.Fatal(err)
	}
	move.ApprovalID = required.Approval.ID
	if _, err = call("seizen_files_move", move); err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(filepath.Join(project.Path, "docs", "informe.txt")); statErr != nil {
		t.Fatal("the approved move did not happen")
	}
	// The approval is single-use: replaying the same call must fail.
	if _, err = call("seizen_files_move", move); err == nil {
		t.Fatal("expected the consumed approval to be rejected")
	}

	rename := agentFilesRenameInput{Path: "docs/informe.txt", Name: "../escape.txt"}
	if _, err = call("seizen_files_rename", rename); err == nil {
		t.Fatal("expected a separator in the new name to be rejected")
	}

	if _, err = call("seizen_desk_open", agentDeskOpenInput{URL: "file:///etc/passwd"}); err == nil {
		t.Fatal("expected a non-HTTP URL to be rejected")
	}
	delivered, err := call("seizen_desk_add_note", agentDeskNoteInput{Text: "resumen"})
	if err != nil || delivered.(map[string]any)["delivered"] != true {
		t.Fatalf("expected the note to be delivered: %#v, %v", delivered, err)
	}
	opened, err := call("seizen_desk_open", agentDeskOpenInput{Path: "docs/informe.txt"})
	if err != nil || opened.(map[string]any)["delivered"] != true {
		t.Fatalf("expected the document to open on the desk: %#v, %v", opened, err)
	}
	if _, err = call("seizen_desk_open", agentDeskOpenInput{Path: "../outside.txt"}); err == nil {
		t.Fatal("expected an escaping document path to be rejected")
	}
}
