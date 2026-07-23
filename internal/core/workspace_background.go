package core

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const maxWorkspaceBackgroundSize = 12 * 1024 * 1024

var workspaceBackgroundFormats = map[string]string{
	".png":  "png",
	".jpg":  "jpeg",
	".jpeg": "jpeg",
}

var workspaceBackgroundVideoExtensions = map[string]bool{
	".mp4":  true,
	".webm": true,
}

// ChooseProjectWorkspaceBackground selects and stores a local background for one project.
// An empty result means the native picker was cancelled.
func (a *App) ChooseProjectWorkspaceBackground(projectID, path string) (string, error) {
	if err := a.validateWorkspaceBackgroundProject(projectID, path); err != nil {
		return "", err
	}
	selected, err := wailsruntime.OpenFileDialog(a.context(), wailsruntime.OpenDialogOptions{
		Title: "Choose workspace background",
		Filters: []wailsruntime.FileFilter{
			{DisplayName: "Images and videos", Pattern: "*.png;*.jpg;*.jpeg;*.mp4;*.webm"},
			{DisplayName: "PNG or JPEG images", Pattern: "*.png;*.jpg;*.jpeg"},
			{DisplayName: "MP4 or WebM videos", Pattern: "*.mp4;*.webm"},
		},
	})
	if err != nil {
		return "", fmt.Errorf("could not open the image picker: %w", err)
	}
	if selected == "" {
		return "", nil
	}
	return a.storeProjectWorkspaceBackground(projectID, selected)
}

func (a *App) GetProjectWorkspaceBackground(projectID, path string) (string, error) {
	if err := a.validateWorkspaceBackgroundProject(projectID, path); err != nil {
		return "", err
	}
	target, err := a.workspaceBackgroundPath(projectID)
	if err != nil {
		return "", err
	}
	mimeType, info, err := sniffWorkspaceBackground(target)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("the saved background is not valid: %w", err)
	}
	if strings.HasPrefix(mimeType, "video/") {
		return workspaceBackgroundVideoURL(projectID, info), nil
	}
	data, format, err := readWorkspaceBackground(target, false)
	if err != nil {
		return "", fmt.Errorf("the saved background is not valid: %w", err)
	}
	return workspaceBackgroundDataURL(data, format), nil
}

func (a *App) ClearProjectWorkspaceBackground(projectID, path string) error {
	if err := a.validateWorkspaceBackgroundProject(projectID, path); err != nil {
		return err
	}
	return a.removeProjectWorkspaceBackground(projectID)
}

// setProjectWorkspaceBackground keeps the file picker out of unit tests.
func (a *App) setProjectWorkspaceBackground(projectID, path, source string) (string, error) {
	if err := a.validateWorkspaceBackgroundProject(projectID, path); err != nil {
		return "", err
	}
	return a.storeProjectWorkspaceBackground(projectID, source)
}

func (a *App) storeProjectWorkspaceBackground(projectID, source string) (string, error) {
	target, err := a.workspaceBackgroundPath(projectID)
	if err != nil {
		return "", err
	}
	if workspaceBackgroundVideoExtensions[strings.ToLower(filepath.Ext(source))] {
		return a.storeProjectWorkspaceBackgroundVideo(projectID, source, target)
	}
	data, format, err := readWorkspaceBackground(source, true)
	if err != nil {
		return "", fmt.Errorf("the chosen image is not valid: %w", err)
	}
	if err = writeManagedWorkspaceImage(target, data); err != nil {
		return "", fmt.Errorf("could not save the background: %w", err)
	}
	return workspaceBackgroundDataURL(data, format), nil
}

// storeProjectWorkspaceBackgroundVideo streams the video into the managed folder
// instead of loading it into memory; the frontend plays it from the asset server.
func (a *App) storeProjectWorkspaceBackgroundVideo(projectID, source, target string) (string, error) {
	absolute, err := filepath.Abs(source)
	if err != nil {
		return "", fmt.Errorf("the path is not valid: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", fmt.Errorf("could not open the video: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errors.New("the video must be a regular file, not a link")
	}
	if info.Size() <= 0 || info.Size() > maxWorkspaceAssetSize {
		return "", errors.New("the video must be at most 512 MiB")
	}
	file, err := os.Open(absolute)
	if err != nil {
		return "", fmt.Errorf("could not read the video: %w", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return "", errors.New("the video changed while it was being opened")
	}
	header := make([]byte, 512)
	headerLength, err := io.ReadFull(file, header)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("could not read the video: %w", err)
	}
	header = header[:headerLength]
	if !strings.HasPrefix(http.DetectContentType(header), "video/") {
		return "", errors.New("the content does not match a valid MP4 or WebM video")
	}
	if err = writeManagedWorkspaceAsset(target, header, file, openedInfo.Size()); err != nil {
		return "", fmt.Errorf("could not save the background: %w", err)
	}
	written, err := os.Stat(target)
	if err != nil {
		return "", fmt.Errorf("could not save the background: %w", err)
	}
	return workspaceBackgroundVideoURL(projectID, written), nil
}

func sniffWorkspaceBackground(target string) (string, os.FileInfo, error) {
	file, err := os.Open(target)
	if err != nil {
		return "", nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", nil, err
	}
	header := make([]byte, 512)
	headerLength, err := io.ReadFull(file, header)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", nil, err
	}
	return http.DetectContentType(header[:headerLength]), info, nil
}

// workspaceBackgroundVideoURL cache-busts with the file's mtime so a replaced
// video is refetched even though the URL path never changes.
func workspaceBackgroundVideoURL(projectID string, info os.FileInfo) string {
	return fmt.Sprintf("%s%s?t=%d", workspaceBackgroundURLPrefix, url.PathEscape(projectID), info.ModTime().UnixNano())
}

func writeManagedWorkspaceImage(target string, data []byte) error {
	directory := filepath.Dir(target)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("could not create the images folder: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("could not protect the images folder: %w", err)
	}

	temporary, err := os.CreateTemp(directory, ".workspace-image-*.tmp")
	if err != nil {
		return fmt.Errorf("could not prepare the image: %w", err)
	}
	temporaryPath := temporary.Name()
	complete := false
	defer func() {
		_ = temporary.Close()
		if !complete {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err = temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(data)
	}
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("could not save the image: %w", err)
	}
	if err = os.Rename(temporaryPath, target); err != nil {
		if removeErr := os.Remove(target); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return fmt.Errorf("could not replace the previous image: %w", removeErr)
		}
		if err = os.Rename(temporaryPath, target); err != nil {
			return fmt.Errorf("could not activate the image: %w", err)
		}
	}
	complete = true
	return nil
}

func (a *App) validateWorkspaceBackgroundProject(projectID, requestedPath string) error {
	ctx := a.context()
	db, err := a.database.Pool(ctx)
	if err != nil {
		return err
	}
	var storedPath string
	err = db.QueryRowContext(ctx, `SELECT path FROM projects WHERE id = ?`, projectID).Scan(&storedPath)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("the project was not found")
	}
	if err != nil {
		return fmt.Errorf("could not check the project: %w", err)
	}
	if !sameRequestedPath(storedPath, requestedPath) {
		return errors.New("the given path does not match the project's saved path")
	}
	return nil
}

func (a *App) workspaceBackgroundPath(projectID string) (string, error) {
	databasePath, err := a.database.databasePath()
	if err != nil {
		return "", fmt.Errorf("could not find Seizen's data folder: %w", err)
	}
	name := fmt.Sprintf("%x.image", sha256.Sum256([]byte(projectID)))
	return filepath.Join(filepath.Dir(databasePath), "workspace-backgrounds", name), nil
}

func (a *App) removeProjectWorkspaceBackground(projectID string) error {
	target, err := a.workspaceBackgroundPath(projectID)
	if err != nil {
		return err
	}
	if err = os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("could not remove the workspace background: %w", err)
	}
	return nil
}

func readWorkspaceBackground(path string, checkExtension bool) ([]byte, string, error) {
	expectedFormat := ""
	if checkExtension {
		var ok bool
		expectedFormat, ok = workspaceBackgroundFormats[strings.ToLower(filepath.Ext(path))]
		if !ok {
			return nil, "", errors.New("only PNG, JPEG, MP4 or WebM files are supported")
		}
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, "", fmt.Errorf("the path is not valid: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, "", fmt.Errorf("could not open the image: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, "", errors.New("the image must be a regular file, not a link")
	}
	if info.Size() <= 0 || info.Size() > maxWorkspaceBackgroundSize {
		return nil, "", errors.New("the image must be at most 12 MiB")
	}

	file, err := os.Open(absolute)
	if err != nil {
		return nil, "", fmt.Errorf("could not read the image: %w", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return nil, "", errors.New("the image changed while it was being opened")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxWorkspaceBackgroundSize+1))
	if err != nil {
		return nil, "", fmt.Errorf("could not read the image: %w", err)
	}
	if len(data) == 0 || len(data) > maxWorkspaceBackgroundSize || int64(len(data)) != openedInfo.Size() {
		return nil, "", errors.New("the image changed while it was being read")
	}

	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg") || (expectedFormat != "" && format != expectedFormat) {
		return nil, "", errors.New("the content does not match a valid PNG or JPEG image")
	}
	if !validThumbnailDimensions(config.Width, config.Height) {
		return nil, "", errors.New("the image dimensions are not valid")
	}
	decoded, decodedFormat, err := image.Decode(bytes.NewReader(data))
	if err != nil || decodedFormat != format || !validThumbnailDimensions(decoded.Bounds().Dx(), decoded.Bounds().Dy()) {
		return nil, "", errors.New("the image is corrupted or its dimensions are not valid")
	}
	return data, format, nil
}

func workspaceBackgroundDataURL(data []byte, format string) string {
	mimeType := "image/png"
	if format == "jpeg" {
		mimeType = "image/jpeg"
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
}
