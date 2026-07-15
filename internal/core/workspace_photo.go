package core

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// WorkspacePhotoAsset is a managed image that can be referenced by a workspace node.
type WorkspacePhotoAsset struct {
	AssetID string `json:"assetId"`
	DataURL string `json:"dataURL"`
}

// ChooseProjectWorkspacePhoto selects and stores one image without retaining its source path.
// A zero value means the native picker was cancelled.
func (a *App) ChooseProjectWorkspacePhoto(projectID, path string) (WorkspacePhotoAsset, error) {
	if err := a.validateWorkspaceBackgroundProject(projectID, path); err != nil {
		return WorkspacePhotoAsset{}, err
	}
	selected, err := wailsruntime.OpenFileDialog(a.context(), wailsruntime.OpenDialogOptions{
		Title: "Add photo to workspace",
		Filters: []wailsruntime.FileFilter{
			{DisplayName: "PNG or JPEG images", Pattern: "*.png;*.jpg;*.jpeg"},
		},
	})
	if err != nil {
		return WorkspacePhotoAsset{}, fmt.Errorf("could not open the image picker: %w", err)
	}
	if selected == "" {
		return WorkspacePhotoAsset{}, nil
	}
	return a.storeProjectWorkspacePhoto(projectID, selected)
}

func (a *App) GetProjectWorkspacePhoto(projectID, path, assetID string) (string, error) {
	if err := a.validateWorkspaceBackgroundProject(projectID, path); err != nil {
		return "", err
	}
	target, err := a.workspacePhotoPath(projectID, assetID)
	if err != nil {
		return "", err
	}
	data, format, err := readWorkspaceBackground(target, false)
	if errors.Is(err, os.ErrNotExist) {
		return "", errors.New("the workspace photo was not found")
	}
	if err != nil {
		return "", fmt.Errorf("the saved photo is not valid: %w", err)
	}
	return workspaceBackgroundDataURL(data, format), nil
}

func (a *App) DeleteProjectWorkspacePhoto(projectID, path, assetID string) error {
	if err := a.validateWorkspaceBackgroundProject(projectID, path); err != nil {
		return err
	}
	target, err := a.workspacePhotoPath(projectID, assetID)
	if err != nil {
		return err
	}
	if err = os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("could not remove the workspace photo: %w", err)
	}
	_ = os.Remove(filepath.Dir(target)) // Remove the project image directory only when empty.
	return nil
}

// setProjectWorkspacePhoto keeps the file picker out of unit tests.
func (a *App) setProjectWorkspacePhoto(projectID, path, source string) (WorkspacePhotoAsset, error) {
	if err := a.validateWorkspaceBackgroundProject(projectID, path); err != nil {
		return WorkspacePhotoAsset{}, err
	}
	return a.storeProjectWorkspacePhoto(projectID, source)
}

func (a *App) storeProjectWorkspacePhoto(projectID, source string) (WorkspacePhotoAsset, error) {
	data, format, err := readWorkspaceBackground(source, true)
	if err != nil {
		return WorkspacePhotoAsset{}, fmt.Errorf("the chosen image is not valid: %w", err)
	}
	assetID, err := newUUID()
	if err != nil {
		return WorkspacePhotoAsset{}, fmt.Errorf("could not create the photo identifier: %w", err)
	}
	target, err := a.workspacePhotoPath(projectID, assetID)
	if err != nil {
		return WorkspacePhotoAsset{}, err
	}
	if err = writeManagedWorkspaceImage(target, data); err != nil {
		return WorkspacePhotoAsset{}, fmt.Errorf("could not save the photo: %w", err)
	}
	return WorkspacePhotoAsset{AssetID: assetID, DataURL: workspaceBackgroundDataURL(data, format)}, nil
}

func (a *App) workspacePhotoPath(projectID, assetID string) (string, error) {
	if err := validateWorkspacePhotoAssetID(assetID); err != nil {
		return "", err
	}
	databasePath, err := a.database.databasePath()
	if err != nil {
		return "", fmt.Errorf("could not find Seizen's data folder: %w", err)
	}
	projectDirectory := fmt.Sprintf("%x", sha256.Sum256([]byte(projectID)))
	return filepath.Join(filepath.Dir(databasePath), "workspace-images", projectDirectory, assetID+".image"), nil
}

func (a *App) removeProjectWorkspacePhotos(projectID string) error {
	databasePath, err := a.database.databasePath()
	if err != nil {
		return fmt.Errorf("could not find Seizen's data folder: %w", err)
	}
	projectDirectory := fmt.Sprintf("%x", sha256.Sum256([]byte(projectID)))
	target := filepath.Join(filepath.Dir(databasePath), "workspace-images", projectDirectory)
	if err = os.RemoveAll(target); err != nil {
		return fmt.Errorf("could not remove the workspace photos: %w", err)
	}
	return nil
}

func validateWorkspacePhotoAssetID(assetID string) error {
	if len(assetID) != 36 || assetID[8] != '-' || assetID[13] != '-' || assetID[18] != '-' || assetID[23] != '-' {
		return errors.New("the photo identifier is not valid")
	}
	for index, character := range assetID {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if !strings.ContainsRune("0123456789abcdef", character) {
			return errors.New("the photo identifier is not valid")
		}
	}
	return nil
}
