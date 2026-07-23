package core

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Documents dropped or picked into a workspace are copied into Seizen's managed
// asset folder and streamed back over the asset server, so the original file can
// move or disappear without breaking the canvas.
const maxWorkspaceAssetSize = 512 << 20 // 512 MiB

const workspaceAssetURLPrefix = "/workspace-asset/"

const workspaceBackgroundURLPrefix = "/workspace-background/"

// WorkspaceDocumentAsset is a managed file that a workspace document node references.
type WorkspaceDocumentAsset struct {
	AssetID string `json:"assetId"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
}

var workspaceTextExtensions = map[string]bool{
	".md": true, ".markdown": true, ".txt": true, ".log": true, ".csv": true,
	".json": true, ".yaml": true, ".yml": true, ".toml": true, ".ini": true,
}

func classifyWorkspaceAsset(mimeType, name string) string {
	extension := strings.ToLower(filepath.Ext(name))
	mimeType = strings.ToLower(strings.TrimSpace(strings.SplitN(mimeType, ";", 2)[0]))
	switch {
	case mimeType == "application/pdf" || extension == ".pdf":
		return "pdf"
	case extension == ".docx" && mimeType == "application/zip":
		return "docx"
	case strings.HasPrefix(mimeType, "image/"):
		return "image"
	case strings.HasPrefix(mimeType, "video/"):
		return "video"
	case strings.HasPrefix(mimeType, "audio/"):
		return "audio"
	case strings.HasPrefix(mimeType, "text/") || workspaceTextExtensions[extension]:
		return "text"
	}
	return ""
}

// ChooseProjectWorkspaceFile selects a document with the native picker and imports it.
// A zero value means the picker was cancelled.
func (a *App) ChooseProjectWorkspaceFile(projectID, path string) (WorkspaceDocumentAsset, error) {
	if err := a.validateWorkspaceBackgroundProject(projectID, path); err != nil {
		return WorkspaceDocumentAsset{}, err
	}
	selected, err := wailsruntime.OpenFileDialog(a.context(), wailsruntime.OpenDialogOptions{
		Title: "Open document",
		Filters: []wailsruntime.FileFilter{
			{DisplayName: "Documents", Pattern: "*.pdf;*.docx;*.md;*.txt;*.csv;*.json;*.log"},
			{DisplayName: "Images", Pattern: "*.png;*.jpg;*.jpeg;*.gif;*.webp;*.bmp"},
			{DisplayName: "Video and audio", Pattern: "*.mp4;*.webm;*.mp3;*.wav;*.m4a;*.ogg"},
			{DisplayName: "All files", Pattern: "*.*"},
		},
	})
	if err != nil {
		return WorkspaceDocumentAsset{}, fmt.Errorf("could not open the file picker: %w", err)
	}
	if selected == "" {
		return WorkspaceDocumentAsset{}, nil
	}
	return a.storeProjectWorkspaceAsset(projectID, selected)
}

// ImportProjectWorkspaceAsset copies a file (for example one dropped onto the window)
// into the managed asset folder and returns its reference.
func (a *App) ImportProjectWorkspaceAsset(projectID, path, source string) (WorkspaceDocumentAsset, error) {
	if err := a.validateWorkspaceBackgroundProject(projectID, path); err != nil {
		return WorkspaceDocumentAsset{}, err
	}
	return a.storeProjectWorkspaceAsset(projectID, source)
}

func (a *App) DeleteProjectWorkspaceAsset(projectID, path, assetID string) error {
	if err := a.validateWorkspaceBackgroundProject(projectID, path); err != nil {
		return err
	}
	target, err := a.workspaceAssetPath(projectID, assetID)
	if err != nil {
		return err
	}
	if err = os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("could not remove the document: %w", err)
	}
	_ = os.Remove(filepath.Dir(target)) // Remove the project asset directory only when empty.
	return nil
}

func (a *App) storeProjectWorkspaceAsset(projectID, source string) (WorkspaceDocumentAsset, error) {
	absolute, err := filepath.Abs(source)
	if err != nil {
		return WorkspaceDocumentAsset{}, fmt.Errorf("the path is not valid: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return WorkspaceDocumentAsset{}, fmt.Errorf("could not open the file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return WorkspaceDocumentAsset{}, errors.New("the document must be a regular file, not a link")
	}
	if info.Size() <= 0 || info.Size() > maxWorkspaceAssetSize {
		return WorkspaceDocumentAsset{}, errors.New("the document must be at most 512 MiB")
	}

	file, err := os.Open(absolute)
	if err != nil {
		return WorkspaceDocumentAsset{}, fmt.Errorf("could not read the file: %w", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return WorkspaceDocumentAsset{}, errors.New("the file changed while it was being opened")
	}

	header := make([]byte, 512)
	headerLength, err := io.ReadFull(file, header)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return WorkspaceDocumentAsset{}, fmt.Errorf("could not read the file: %w", err)
	}
	header = header[:headerLength]
	name := filepath.Base(absolute)
	kind := classifyWorkspaceAsset(http.DetectContentType(header), name)
	if kind == "" {
		return WorkspaceDocumentAsset{}, errors.New("this file type is not supported; open it with its own application instead")
	}

	assetID, err := newUUID()
	if err != nil {
		return WorkspaceDocumentAsset{}, fmt.Errorf("could not create the document identifier: %w", err)
	}
	target, err := a.workspaceAssetPath(projectID, assetID)
	if err != nil {
		return WorkspaceDocumentAsset{}, err
	}
	if err = writeManagedWorkspaceAsset(target, header, file, openedInfo.Size()); err != nil {
		return WorkspaceDocumentAsset{}, fmt.Errorf("could not save the document: %w", err)
	}
	return WorkspaceDocumentAsset{AssetID: assetID, Kind: kind, Name: name}, nil
}

// writeManagedWorkspaceAsset streams header+rest to a temp file and renames it into
// place, so a failed copy never leaves a half-written asset behind.
func writeManagedWorkspaceAsset(target string, header []byte, rest io.Reader, expectedSize int64) error {
	directory := filepath.Dir(target)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("could not create the documents folder: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".workspace-asset-*.tmp")
	if err != nil {
		return fmt.Errorf("could not prepare the document: %w", err)
	}
	temporaryPath := temporary.Name()
	complete := false
	defer func() {
		_ = temporary.Close()
		if !complete {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err = temporary.Chmod(0o600); err != nil {
		return err
	}
	written, err := temporary.Write(header)
	if err != nil {
		return err
	}
	copied, err := io.Copy(temporary, io.LimitReader(rest, maxWorkspaceAssetSize+1))
	if err != nil {
		return err
	}
	if int64(written)+copied != expectedSize || copied+int64(written) > maxWorkspaceAssetSize {
		return errors.New("the file changed while it was being copied")
	}
	if err = temporary.Sync(); err != nil {
		return err
	}
	if err = temporary.Close(); err != nil {
		return err
	}
	if err = os.Rename(temporaryPath, target); err != nil {
		return fmt.Errorf("could not activate the document: %w", err)
	}
	complete = true
	return nil
}

func (a *App) workspaceAssetPath(projectID, assetID string) (string, error) {
	if err := validateWorkspacePhotoAssetID(assetID); err != nil {
		return "", errors.New("the document identifier is not valid")
	}
	databasePath, err := a.database.databasePath()
	if err != nil {
		return "", fmt.Errorf("could not find Seizen's data folder: %w", err)
	}
	projectDirectory := fmt.Sprintf("%x", sha256.Sum256([]byte(projectID)))
	return filepath.Join(filepath.Dir(databasePath), "workspace-assets", projectDirectory, assetID+".asset"), nil
}

func (a *App) removeProjectWorkspaceAssets(projectID string) error {
	databasePath, err := a.database.databasePath()
	if err != nil {
		return fmt.Errorf("could not find Seizen's data folder: %w", err)
	}
	projectDirectory := fmt.Sprintf("%x", sha256.Sum256([]byte(projectID)))
	target := filepath.Join(filepath.Dir(databasePath), "workspace-assets", projectDirectory)
	if err = os.RemoveAll(target); err != nil {
		return fmt.Errorf("could not remove the workspace documents: %w", err)
	}
	return nil
}

// workspaceAssetHandler streams managed assets with range support, so PDFs and
// videos never travel as base64 data URLs. The path is /workspace-asset/{projectID}/{assetID};
// the project component is hashed and the asset component is a validated UUID, so no
// request can escape the managed folder.
func (a *App) workspaceAssetHandler() http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var target string
		var err error
		if trail, ok := strings.CutPrefix(request.URL.Path, workspaceAssetURLPrefix); ok {
			projectID, assetID, split := strings.Cut(trail, "/")
			if !split || projectID == "" || strings.ContainsAny(projectID, `/\`) || strings.Contains(assetID, "/") {
				http.NotFound(response, request)
				return
			}
			target, err = a.workspaceAssetPath(projectID, assetID)
		} else if projectID, ok := strings.CutPrefix(request.URL.Path, workspaceBackgroundURLPrefix); ok {
			if projectID == "" || strings.ContainsAny(projectID, `/\`) {
				http.NotFound(response, request)
				return
			}
			target, err = a.workspaceBackgroundPath(projectID)
		} else {
			http.NotFound(response, request)
			return
		}
		if err != nil {
			http.NotFound(response, request)
			return
		}
		file, err := os.Open(target)
		if err != nil {
			http.NotFound(response, request)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			http.NotFound(response, request)
			return
		}

		header := make([]byte, 512)
		headerLength, err := io.ReadFull(file, header)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
			http.Error(response, "could not read the document", http.StatusInternalServerError)
			return
		}
		if _, err = file.Seek(0, io.SeekStart); err != nil {
			http.Error(response, "could not read the document", http.StatusInternalServerError)
			return
		}
		response.Header().Set("Content-Type", http.DetectContentType(header[:headerLength]))
		response.Header().Set("Content-Disposition", "inline")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(response, request, "", info.ModTime(), file)
	})
}
