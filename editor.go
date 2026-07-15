package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type EditorIntegration struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
	Managed   bool   `json:"managed"`
	// Embedded indicates the editor opens inside the workspace (it has a web UI);
	// the rest open in their own system window.
	Embedded     bool   `json:"embedded"`
	Status       string `json:"status"`
	ErrorMessage string `json:"errorMessage"`
}

type editorDefinition struct {
	ID             string
	Name           string
	Command        string
	Setting        string
	DefaultEnabled bool
	Embedded       bool
}

var editorDefinitions = []editorDefinition{
	{ID: "vscode", Name: "VS Code", Command: "code", Setting: "editor_vscode_enabled", DefaultEnabled: true, Embedded: true},
	{ID: "cursor", Name: "Cursor", Command: "cursor", Setting: "editor_cursor_enabled"},
	{ID: "antigravity", Name: "Antigravity", Command: "antigravity", Setting: "editor_antigravity_enabled"},
	{ID: "zed", Name: "Zed", Command: "zed", Setting: "editor_zed_enabled"},
}

func (a *App) GetEditorIntegrations() ([]EditorIntegration, error) {
	integrations, err := a.database.EditorIntegrations(a.context())
	if err != nil {
		return nil, err
	}
	for index := range integrations {
		definition, _ := editorDefinitionByID(integrations[index].ID)
		if definition.ID == "vscode" {
			installer, installErr := a.managedVSCodeInstaller()
			if installErr != nil {
				return nil, installErr
			}
			integrations[index].Managed = true
			integrations[index].Available, integrations[index].Status, integrations[index].ErrorMessage = installer.status()
			continue
		}
		_, err = resolveEditorCommand(definition)
		integrations[index].Available = err == nil
		if integrations[index].Available {
			integrations[index].Status = "available"
		} else {
			integrations[index].Status = "unavailable"
		}
	}
	return integrations, nil
}

func (a *App) SetEditorIntegrationEnabled(id string, enabled bool) ([]EditorIntegration, error) {
	if err := a.database.SetEditorIntegrationEnabled(a.context(), id, enabled); err != nil {
		return nil, err
	}
	return a.GetEditorIntegrations()
}

func (a *App) InstallEditorIntegration(id string) ([]EditorIntegration, error) {
	if id != "vscode" {
		return nil, errors.New("that editor is not managed by Seizen")
	}
	installer, err := a.managedVSCodeInstaller()
	if err != nil {
		return nil, err
	}
	if err = installer.install(a.context()); err != nil {
		return nil, err
	}
	return a.GetEditorIntegrations()
}

// requireExternalEditor validates that the editor exists, is not embedded (those
// go through serve-web) and is enabled in Resources.
func (a *App) requireExternalEditor(id string) (editorDefinition, error) {
	definition, ok := editorDefinitionByID(id)
	if !ok {
		return editorDefinition{}, errors.New("the editor is not valid")
	}
	if definition.Embedded {
		return editorDefinition{}, fmt.Errorf("%s opens inside the workspace, not as an external editor", definition.Name)
	}
	integrations, err := a.database.EditorIntegrations(a.context())
	if err != nil {
		return editorDefinition{}, err
	}
	for _, integration := range integrations {
		if integration.ID == id && !integration.Enabled {
			return editorDefinition{}, fmt.Errorf("%s is disabled; enable it in Resources", definition.Name)
		}
	}
	return definition, nil
}

// OpenProjectInEditor opens the project folder in an external editor in its
// own window. To embed it in the workspace, see StartNativeEditor.
func (a *App) OpenProjectInEditor(projectPath, id string) error {
	definition, err := a.requireExternalEditor(id)
	if err != nil {
		return err
	}
	_, folder, err := a.registeredEditorProject(projectPath)
	if err != nil {
		return err
	}
	executable, err := resolveEditorCommand(definition)
	if err != nil {
		return err
	}
	command := exec.Command(executable, folder)
	command.Dir = folder
	hideWindow(command)
	if err = command.Start(); err != nil {
		return fmt.Errorf("could not open %s: %w", definition.Name, err)
	}
	return command.Process.Release()
}

func (d *Database) EditorIntegrations(ctx context.Context) ([]EditorIntegration, error) {
	db, err := d.Pool(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT key, value FROM settings WHERE key IN (?, ?, ?, ?)`,
		editorDefinitions[0].Setting, editorDefinitions[1].Setting,
		editorDefinitions[2].Setting, editorDefinitions[3].Setting)
	if err != nil {
		return nil, fmt.Errorf("could not load the editors: %w", err)
	}
	defer rows.Close()

	integrations := make([]EditorIntegration, len(editorDefinitions))
	for index, definition := range editorDefinitions {
		integrations[index] = EditorIntegration{ID: definition.ID, Name: definition.Name, Enabled: definition.DefaultEnabled, Embedded: definition.Embedded}
	}
	for rows.Next() {
		var key, value string
		if err = rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("could not load the editors: %w", err)
		}
		index, ok := editorDefinitionIndexBySetting(key)
		if !ok || (value != "0" && value != "1") {
			return nil, errors.New("the saved editor configuration is not valid")
		}
		integrations[index].Enabled = value == "1"
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("could not load the editors: %w", err)
	}
	return integrations, nil
}

func (d *Database) SetEditorIntegrationEnabled(ctx context.Context, id string, enabled bool) error {
	definition, ok := editorDefinitionByID(id)
	if !ok {
		return errors.New("the editor is not valid")
	}
	db, err := d.Pool(ctx)
	if err != nil {
		return err
	}
	value := "0"
	if enabled {
		value = "1"
	}
	_, err = db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value`, definition.Setting, value)
	if err != nil {
		return fmt.Errorf("could not save the editor: %w", err)
	}
	return nil
}

// resolveEditorCommand looks for the CLI on PATH and, if not found, in the
// typical Windows per-user installation paths (the Zed and Cursor installers
// don't always add their bin to PATH).
func resolveEditorCommand(definition editorDefinition) (string, error) {
	if path, err := exec.LookPath(definition.Command); err == nil {
		return path, nil
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		// Known layouts under Programs\<Name>: Zed uses bin\<cli>.exe, VS Code
		// forks (Cursor, Antigravity, ...) use bin\<cli>.cmd or
		// resources\app\bin\<cli>.cmd, and all of them ship <Name>.exe at the root.
		programs := filepath.Join(local, "Programs", definition.Name)
		candidates := []string{
			filepath.Join(programs, "bin", definition.Command+".exe"),
			filepath.Join(programs, "bin", definition.Command+".cmd"),
			filepath.Join(programs, "resources", "app", "bin", definition.Command+".cmd"),
			filepath.Join(programs, definition.Name+".exe"),
		}
		for _, candidate := range candidates {
			if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("%s is not installed or its command %q is not on PATH", definition.Name, definition.Command)
}

func editorDefinitionByID(id string) (editorDefinition, bool) {
	for _, definition := range editorDefinitions {
		if definition.ID == id {
			return definition, true
		}
	}
	return editorDefinition{}, false
}

func editorDefinitionIndexBySetting(setting string) (int, bool) {
	for index, definition := range editorDefinitions {
		if definition.Setting == setting {
			return index, true
		}
	}
	return 0, false
}
