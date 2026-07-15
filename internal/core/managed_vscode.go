package core

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	managedVSCodeDownloadLimit = 512 << 20
	managedVSCodeExtractLimit  = 1 << 30
	managedVSCodeFileLimit     = 50_000
)

type managedVSCodeInstaller struct {
	installMu    sync.Mutex
	stateMu      sync.RWMutex
	root         string
	sourceURL    string
	downloadHost string
	client       *http.Client
	allowHTTP    bool
	installing   bool
	lastError    string
}

func (a *App) managedVSCodeInstaller() (*managedVSCodeInstaller, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.vscode != nil {
		return a.vscode, nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("could not find Seizen's local folder: %w", err)
	}
	source, err := managedVSCodeSourceURL()
	if err != nil {
		return nil, err
	}
	a.vscode = &managedVSCodeInstaller{
		root:         filepath.Join(cache, "Seizen", "tools", "vscode"),
		sourceURL:    source,
		downloadHost: "vscode.download.prss.microsoft.com",
		client:       &http.Client{Timeout: 30 * time.Minute},
	}
	return a.vscode, nil
}

func managedVSCodeSourceURL() (string, error) {
	if runtime.GOOS != "windows" {
		return "", errors.New("managed VS Code is only available on Windows")
	}
	platform := ""
	switch runtime.GOARCH {
	case "amd64":
		platform = "win32-x64-archive"
	case "arm64":
		platform = "win32-arm64-archive"
	default:
		return "", errors.New("this architecture does not support managed VS Code")
	}
	return "https://update.code.visualstudio.com/latest/" + platform + "/stable", nil
}

func (installer *managedVSCodeInstaller) status() (bool, string, string) {
	if _, err := installer.executable(); err == nil {
		return true, "installed", ""
	}
	installer.stateMu.RLock()
	defer installer.stateMu.RUnlock()
	if installer.installing {
		return false, "installing", ""
	}
	if installer.lastError != "" {
		return false, "error", installer.lastError
	}
	if runtime.GOOS != "windows" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		return false, "unsupported", ""
	}
	return false, "not_installed", ""
}

func (installer *managedVSCodeInstaller) executable() (string, error) {
	path := filepath.Join(installer.root, "current", "Code.exe")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("managed VS Code is not installed")
	}
	if _, err = installer.serverExecutable(); err != nil {
		return "", err
	}
	return path, nil
}

func (installer *managedVSCodeInstaller) serverExecutable() (string, error) {
	path := filepath.Join(installer.root, "current", "bin", "code-tunnel.exe")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("managed VS Code's web server is not installed")
	}
	return path, nil
}

func (installer *managedVSCodeInstaller) install(ctx context.Context) (err error) {
	installer.installMu.Lock()
	defer installer.installMu.Unlock()
	if _, err = installer.executable(); err == nil {
		return nil
	}
	installer.setInstallState(true, "")
	defer func() {
		message := ""
		if err != nil {
			message = err.Error()
		}
		installer.setInstallState(false, message)
	}()

	if err = os.MkdirAll(installer.root, 0o700); err != nil {
		return fmt.Errorf("could not prepare VS Code in Seizen: %w", err)
	}
	archive, err := installer.download(ctx)
	if err != nil {
		return err
	}
	defer removeVSCodeArchive(archive)

	stage, err := os.MkdirTemp(installer.root, ".vscode-install-")
	if err != nil {
		return fmt.Errorf("could not prepare the VS Code installation: %w", err)
	}
	defer os.RemoveAll(stage)
	if err = extractVSCodeArchive(archive, stage); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Join(stage, "data", "tmp"), 0o700); err != nil {
		return fmt.Errorf("could not enable VS Code's portable mode: %w", err)
	}
	for _, required := range []string{"Code.exe", filepath.Join("bin", "code-tunnel.exe")} {
		if info, statErr := os.Stat(filepath.Join(stage, required)); statErr != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("the official VS Code archive does not contain %s", required)
		}
	}

	current := filepath.Join(installer.root, "current")
	if err = os.RemoveAll(current); err != nil {
		return fmt.Errorf("could not replace the incomplete copy of VS Code: %w", err)
	}
	if err = os.Rename(stage, current); err != nil {
		return fmt.Errorf("could not publish VS Code inside Seizen: %w", err)
	}
	return nil
}

func removeVSCodeArchive(path string) {
	for range 25 {
		if err := os.Remove(path); err == nil || os.IsNotExist(err) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (installer *managedVSCodeInstaller) setInstallState(installing bool, message string) {
	installer.stateMu.Lock()
	installer.installing = installing
	installer.lastError = message
	installer.stateMu.Unlock()
}

func (installer *managedVSCodeInstaller) download(ctx context.Context) (string, error) {
	metadata, err := installer.request(ctx, installer.sourceURL)
	if err != nil {
		return "", fmt.Errorf("could not query the official VS Code download: %w", err)
	}
	defer metadata.Body.Close()
	if metadata.StatusCode < 300 || metadata.StatusCode > 399 {
		return "", fmt.Errorf("the official VS Code download responded with HTTP %d", metadata.StatusCode)
	}
	downloadURL, err := metadata.Location()
	if err != nil {
		return "", errors.New("the official VS Code download did not indicate the file")
	}
	if err = installer.validateDownloadURL(downloadURL); err != nil {
		return "", err
	}
	wantHash, err := parseSHA256(metadata.Header.Get("X-SHA256"))
	if err != nil {
		return "", err
	}

	response, err := installer.request(ctx, downloadURL.String())
	if err != nil {
		return "", fmt.Errorf("could not download VS Code: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the VS Code download responded with HTTP %d", response.StatusCode)
	}
	if response.ContentLength > managedVSCodeDownloadLimit {
		return "", errors.New("the VS Code download exceeds the safety limit")
	}

	file, err := os.CreateTemp(installer.root, ".vscode-*.zip")
	if err != nil {
		return "", fmt.Errorf("could not save the VS Code download: %w", err)
	}
	path := file.Name()
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(path)
		}
	}()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, managedVSCodeDownloadLimit+1))
	if err != nil {
		return "", fmt.Errorf("could not complete the VS Code download: %w", err)
	}
	if written > managedVSCodeDownloadLimit {
		return "", errors.New("the VS Code download exceeds the safety limit")
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), wantHash) {
		return "", errors.New("the VS Code download does not match the official SHA-256")
	}
	if err = file.Close(); err != nil {
		return "", fmt.Errorf("could not save the VS Code download: %w", err)
	}
	keep = true
	return path, nil
}

func (installer *managedVSCodeInstaller) request(ctx context.Context, target string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	client := *installer.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client.Do(request)
}

func (installer *managedVSCodeInstaller) validateDownloadURL(target *url.URL) error {
	if target == nil || target.User != nil || target.RawQuery != "" || target.Fragment != "" {
		return errors.New("the official VS Code download returned an address that is not valid")
	}
	if (!installer.allowHTTP && target.Scheme != "https") || (installer.allowHTTP && target.Scheme != "http" && target.Scheme != "https") {
		return errors.New("the official VS Code download does not use a secure connection")
	}
	if !strings.EqualFold(target.Hostname(), installer.downloadHost) {
		return errors.New("the official VS Code download redirected to a host that is not allowed")
	}
	return nil
}

func parseSHA256(value string) (string, error) {
	value = strings.TrimSpace(value)
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return "", errors.New("the official VS Code download did not include a valid SHA-256")
	}
	return strings.ToLower(value), nil
}

func extractVSCodeArchive(archive, destination string) error {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("the downloaded VS Code archive is not a valid ZIP: %w", err)
	}
	defer reader.Close()
	if len(reader.File) > managedVSCodeFileLimit {
		return errors.New("the VS Code archive contains too many files")
	}
	var declared uint64
	for _, entry := range reader.File {
		if entry.UncompressedSize64 > managedVSCodeExtractLimit-declared {
			return errors.New("VS Code exceeds the extraction limit")
		}
		declared += entry.UncompressedSize64
	}

	var extracted int64
	for _, entry := range reader.File {
		name := strings.ReplaceAll(entry.Name, "\\", "/")
		clean := filepath.Clean(filepath.FromSlash(name))
		first := strings.Split(name, "/")[0]
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || strings.Contains(first, ":") {
			return errors.New("the VS Code archive contains an unsafe path")
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return errors.New("the VS Code archive contains links that are not allowed")
		}
		target := filepath.Join(destination, clean)
		relative, relErr := filepath.Rel(destination, target)
		if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("the VS Code archive attempts to escape Seizen")
		}
		if entry.FileInfo().IsDir() {
			if err = os.MkdirAll(target, 0o700); err != nil {
				return fmt.Errorf("could not extract VS Code: %w", err)
			}
			continue
		}
		if !entry.Mode().IsRegular() {
			return errors.New("the VS Code archive contains a type that is not allowed")
		}
		if err = os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return fmt.Errorf("could not extract VS Code: %w", err)
		}
		source, openErr := entry.Open()
		if openErr != nil {
			return fmt.Errorf("could not read VS Code: %w", openErr)
		}
		destinationFile, createErr := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if createErr != nil {
			source.Close()
			return fmt.Errorf("could not extract VS Code: %w", createErr)
		}
		remaining := int64(managedVSCodeExtractLimit) - extracted
		written, copyErr := io.Copy(destinationFile, io.LimitReader(source, remaining+1))
		closeErr := destinationFile.Close()
		source.Close()
		extracted += written
		if copyErr != nil || closeErr != nil {
			return errors.New("could not complete the VS Code extraction")
		}
		if extracted > managedVSCodeExtractLimit {
			return errors.New("VS Code exceeds the extraction limit")
		}
	}
	return nil
}
