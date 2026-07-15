package main

import (
	"archive/zip"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) ExportProject(projectID, path string) (string, error) {
	ctx := a.context()
	db, err := a.database.Pool(ctx)
	if err != nil {
		return "", err
	}

	var storedPath string
	err = db.QueryRowContext(ctx, `SELECT path FROM projects WHERE id = ?`, projectID).Scan(&storedPath)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("the project was not found")
	}
	if err != nil {
		return "", fmt.Errorf("could not check the project: %w", err)
	}
	if !sameRequestedPath(storedPath, path) {
		return "", errors.New("the given path does not match the project's saved path")
	}

	source, err := existingDirectory(storedPath)
	if err != nil {
		return "", err
	}
	destination, err := wailsruntime.SaveFileDialog(ctx, wailsruntime.SaveDialogOptions{
		Title:           "Download project",
		DefaultFilename: filepath.Base(source) + ".zip",
		Filters: []wailsruntime.FileFilter{
			{DisplayName: "ZIP file (*.zip)", Pattern: "*.zip"},
		},
	})
	if err != nil {
		return "", fmt.Errorf("could not open the destination picker: %w", err)
	}
	if destination == "" {
		return "", nil
	}
	if !strings.EqualFold(filepath.Ext(destination), ".zip") {
		destination += ".zip"
	}
	if err = zipProjectDirectory(source, destination); err != nil {
		return "", err
	}
	return displayPath(destination), nil
}

func zipProjectDirectory(source, destination string) error {
	absolute, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("could not check the destination: %w", err)
	}
	parent, err := existingDirectory(filepath.Dir(absolute))
	if err != nil {
		return fmt.Errorf("the destination folder is not valid: %w", err)
	}
	destination = filepath.Join(parent, filepath.Base(absolute))
	if pathInside(source, destination) {
		return errors.New("save the ZIP outside the project folder")
	}

	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("a file with that name already exists; choose another destination")
		}
		return fmt.Errorf("could not create the ZIP file: %w", err)
	}
	complete := false
	defer func() {
		_ = output.Close()
		if !complete {
			_ = os.Remove(destination)
		}
	}()

	archive := zip.NewWriter(output)
	if err = archive.AddFS(os.DirFS(source)); err != nil {
		_ = archive.Close()
		return fmt.Errorf("could not compress the project: %w", err)
	}
	if err = archive.Close(); err != nil {
		return fmt.Errorf("could not finish the ZIP file: %w", err)
	}
	if err = output.Sync(); err != nil {
		return fmt.Errorf("could not save the ZIP file: %w", err)
	}
	if err = output.Close(); err != nil {
		return fmt.Errorf("could not close the ZIP file: %w", err)
	}
	complete = true
	return nil
}
