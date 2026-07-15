package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

const (
	wslDistributionSetting = "terminal_wsl_distribution"
	defaultWSLDistribution = "debian"
	wslCommandTimeout      = 10 * time.Second
	wslInstallationTimeout = 20 * time.Minute
	maximumWSLErrorLength  = 4096
)

type WSLDistributionResource struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	RuntimeName  string `json:"runtimeName"`
	Selected     bool   `json:"selected"`
	Installed    bool   `json:"installed"`
	Status       string `json:"status"`
	ErrorMessage string `json:"errorMessage"`
}

type wslDistributionDefinition struct {
	ID          string
	Name        string
	OnlineName  string
	RuntimeName string
	SystemID    string
	Version     string
}

type wslResourceManifest struct {
	Version     int    `json:"version"`
	ID          string `json:"id"`
	RuntimeName string `json:"runtimeName"`
	InstallPath string `json:"installPath"`
}

var wslDistributionDefinitions = []wslDistributionDefinition{
	{ID: "debian", Name: "Debian 13", OnlineName: "Debian", RuntimeName: "Seizen-Debian", SystemID: "debian", Version: "13"},
	{ID: "ubuntu", Name: "Ubuntu", OnlineName: "Ubuntu", RuntimeName: "Seizen-Ubuntu", SystemID: "ubuntu"},
	{ID: "fedora", Name: "Fedora 44", OnlineName: "FedoraLinux-44", RuntimeName: "Seizen-Fedora", SystemID: "fedora"},
	{ID: "arch", Name: "Arch", OnlineName: "archlinux", RuntimeName: "Seizen-Arch", SystemID: "arch"},
}

// ponytail: installs are rare desktop actions; one lock prevents conflicting
// WSL registrations without adding an installer queue.
var wslInstallMu sync.Mutex

func (a *App) GetWSLDistributions() ([]WSLDistributionResource, error) {
	selected, err := a.database.WSLDistribution(a.context())
	if err != nil {
		return nil, err
	}
	names, listErr := installedWSLRuntimeNames(a.context())
	restartRequired, restartMessage := wslRestartRequired()
	resources := make([]WSLDistributionResource, 0, len(wslDistributionDefinitions))
	for _, definition := range wslDistributionDefinitions {
		_, registered := names[strings.ToLower(definition.RuntimeName)]
		installed := false
		ownershipErr := error(nil)
		if registered {
			ownershipErr = validateManagedWSLManifest(definition)
			installed = ownershipErr == nil
		}
		status, errorMessage := wslResourceState(installed, ownershipErr, listErr, restartRequired, restartMessage)
		resource := WSLDistributionResource{
			ID: definition.ID, Name: definition.Name, RuntimeName: definition.RuntimeName,
			Selected: definition.ID == selected.ID, Installed: installed,
			Status: status, ErrorMessage: errorMessage,
		}
		resources = append(resources, resource)
	}
	return resources, nil
}

func (a *App) SetDefaultWSLDistribution(id string) ([]WSLDistributionResource, error) {
	if err := a.database.SetWSLDistribution(a.context(), id); err != nil {
		return nil, err
	}
	return a.GetWSLDistributions()
}

func (a *App) InstallWSLDistribution(id string) ([]WSLDistributionResource, error) {
	definition, ok := wslDistributionByID(id)
	if !ok {
		return nil, errors.New("the WSL distribution is not valid")
	}
	wslInstallMu.Lock()
	defer wslInstallMu.Unlock()

	registered, err := isWSLRuntimeInstalled(a.context(), definition.RuntimeName)
	if restartRequired, _ := wslRestartRequired(); restartRequired {
		return a.GetWSLDistributions()
	}
	if registered {
		if err = validateManagedWSLManifest(definition); err != nil {
			return nil, err
		}
		return a.GetWSLDistributions()
	}
	if err != nil {
		return nil, err
	}
	wslPath, err := systemWSLPath()
	if err != nil {
		return nil, err
	}
	root, err := managedWSLRoot()
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("could not prepare the WSL folder: %w", err)
	}
	installPath := filepath.Join(root, definition.ID)
	if err = removeManagedWSLFiles(definition); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(a.context(), wslInstallationTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, wslPath, wslInstallArguments(definition, installPath)...)
	hideWindow(command)
	output, runErr := command.CombinedOutput()
	registered, checkErr := isWSLRuntimeInstalled(context.WithoutCancel(a.context()), definition.RuntimeName)
	// Windows may successfully stage VirtualMachinePlatform before it can
	// register the distro. Keep staged files intact until the required reboot.
	if restartRequired, _ := wslRestartRequired(); restartRequired {
		return a.GetWSLDistributions()
	}
	if runErr != nil {
		rollbackErr := rollbackManagedWSLInstall(a.context(), wslPath, definition, registered)
		message := conciseWSLOutput(output)
		if message != "" {
			return nil, errors.Join(fmt.Errorf("could not install %s: %s", definition.Name, message), rollbackErr)
		}
		return nil, errors.Join(fmt.Errorf("could not install %s: %w", definition.Name, runErr), rollbackErr)
	}
	if checkErr != nil {
		return nil, errors.Join(checkErr, rollbackManagedWSLInstall(a.context(), wslPath, definition, registered))
	}
	if !registered {
		return nil, errors.Join(
			fmt.Errorf("WSL did not register %s after installing it", definition.Name),
			rollbackManagedWSLInstall(a.context(), wslPath, definition, false),
		)
	}
	if err = checkWSLRuntime(a.context(), wslPath, definition); err != nil {
		return nil, errors.Join(err, rollbackManagedWSLInstall(a.context(), wslPath, definition, true))
	}
	if err = writeManagedWSLManifest(definition, installPath); err != nil {
		return nil, errors.Join(err, rollbackManagedWSLInstall(a.context(), wslPath, definition, true))
	}
	return a.GetWSLDistributions()
}

func wslResourceState(installed bool, ownershipErr, listErr error, restartRequired bool, restartMessage string) (string, string) {
	if installed {
		return "installed", ""
	}
	if restartRequired {
		return "restart_required", restartMessage
	}
	if ownershipErr != nil {
		return "unavailable", ownershipErr.Error()
	}
	if listErr != nil {
		return "unavailable", listErr.Error()
	}
	return "not_installed", ""
}

func wslRestartState(installStatus string, windowsRestartPending bool) (bool, string) {
	if strings.TrimSpace(installStatus) == "" && !windowsRestartPending {
		return false, ""
	}
	return true, "Restart Windows to finish activating WSL. Then go back to Resources; Seizen will verify the installation without deleting its files."
}

func (d *Database) WSLDistribution(ctx context.Context) (wslDistributionDefinition, error) {
	db, err := d.Pool(ctx)
	if err != nil {
		return wslDistributionDefinition{}, err
	}
	id := defaultWSLDistribution
	err = db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, wslDistributionSetting).Scan(&id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return wslDistributionDefinition{}, fmt.Errorf("could not load the WSL environment: %w", err)
	}
	definition, ok := wslDistributionByID(id)
	if !ok {
		return wslDistributionDefinition{}, errors.New("the saved WSL configuration is not valid")
	}
	return definition, nil
}

func (d *Database) SetWSLDistribution(ctx context.Context, id string) error {
	if _, ok := wslDistributionByID(id); !ok {
		return errors.New("the WSL distribution is not valid")
	}
	db, err := d.Pool(ctx)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value`, wslDistributionSetting, id)
	if err != nil {
		return fmt.Errorf("could not save the WSL environment: %w", err)
	}
	return nil
}

func wslDistributionByID(id string) (wslDistributionDefinition, bool) {
	for _, definition := range wslDistributionDefinitions {
		if definition.ID == id {
			return definition, true
		}
	}
	return wslDistributionDefinition{}, false
}

func wslDistributionByRuntime(runtimeName string) (wslDistributionDefinition, bool) {
	for _, definition := range wslDistributionDefinitions {
		if strings.EqualFold(definition.RuntimeName, runtimeName) {
			return definition, true
		}
	}
	return wslDistributionDefinition{}, false
}

func installedWSLRuntimeNames(ctx context.Context) (map[string]struct{}, error) {
	wslPath, err := systemWSLPath()
	if err != nil {
		return nil, err
	}
	queryContext, cancel := context.WithTimeout(ctx, wslCommandTimeout)
	defer cancel()
	command := exec.CommandContext(queryContext, wslPath, "--list", "--quiet")
	hideWindow(command)
	output, err := command.CombinedOutput()
	if err != nil {
		message := conciseWSLOutput(output)
		if message != "" {
			return nil, errors.New(message)
		}
		return nil, fmt.Errorf("could not query the WSL distributions: %w", err)
	}
	names := map[string]struct{}{}
	for _, line := range strings.Split(decodeWSLOutput(output), "\n") {
		if name := strings.ToLower(strings.TrimSpace(line)); name != "" {
			names[name] = struct{}{}
		}
	}
	return names, nil
}

func isWSLRuntimeInstalled(ctx context.Context, runtimeName string) (bool, error) {
	names, err := installedWSLRuntimeNames(ctx)
	if err != nil {
		return false, err
	}
	_, installed := names[strings.ToLower(runtimeName)]
	return installed, nil
}

func managedWSLRoot() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("could not find Seizen's local folder: %w", err)
	}
	return filepath.Join(cache, "Seizen", "wsl"), nil
}

func systemWSLPath() (string, error) {
	if runtime.GOOS != "windows" {
		return "", errors.New("WSL is only available on Windows")
	}
	windowsRoot := strings.TrimSpace(os.Getenv("SystemRoot"))
	if windowsRoot == "" || !filepath.IsAbs(windowsRoot) {
		return "", errors.New("Windows did not report a valid system folder")
	}
	path := filepath.Join(filepath.Clean(windowsRoot), "System32", "wsl.exe")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("WSL is not installed on Windows")
	}
	return path, nil
}

func managedWSLPaths(definition wslDistributionDefinition) (string, string, string, error) {
	root, err := managedWSLRoot()
	if err != nil {
		return "", "", "", err
	}
	return root, filepath.Join(root, definition.ID), filepath.Join(root, definition.ID+".json"), nil
}

func validateManagedWSLManifest(definition wslDistributionDefinition) error {
	root, installPath, manifestPath, err := managedWSLPaths(definition)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("%s already exists, but it does not belong to Seizen", definition.RuntimeName)
	}
	var manifest wslResourceManifest
	if json.Unmarshal(data, &manifest) != nil || manifest.Version != 1 ||
		manifest.ID != definition.ID || manifest.RuntimeName != definition.RuntimeName {
		return fmt.Errorf("the manifest for %s is not valid", definition.Name)
	}
	resolvedRoot, rootErr := canonicalPath(root)
	resolvedInstall, installErr := canonicalPath(installPath)
	resolvedManifest, manifestErr := canonicalPath(manifest.InstallPath)
	if rootErr != nil || installErr != nil || manifestErr != nil ||
		!samePath(resolvedInstall, resolvedManifest) || !pathInside(resolvedRoot, resolvedInstall) {
		return fmt.Errorf("the installation of %s is outside Seizen", definition.Name)
	}
	return nil
}

func writeManagedWSLManifest(definition wslDistributionDefinition, installPath string) error {
	root, expectedInstall, manifestPath, err := managedWSLPaths(definition)
	if err != nil {
		return err
	}
	resolvedRoot, err := canonicalPath(root)
	if err != nil {
		return err
	}
	resolvedInstall, err := canonicalPath(installPath)
	if err != nil || !samePath(expectedInstall, installPath) || !pathInside(resolvedRoot, resolvedInstall) {
		return fmt.Errorf("could not validate the managed folder for %s", definition.Name)
	}
	data, err := json.Marshal(wslResourceManifest{
		Version: 1, ID: definition.ID, RuntimeName: definition.RuntimeName, InstallPath: resolvedInstall,
	})
	if err != nil {
		return err
	}
	temporary := manifestPath + ".tmp"
	_ = os.Remove(temporary)
	if err = os.WriteFile(temporary, data, 0o600); err == nil {
		err = os.Rename(temporary, manifestPath)
	}
	if err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("could not register the installation of %s: %w", definition.Name, err)
	}
	return nil
}

func removeManagedWSLFiles(definition wslDistributionDefinition) error {
	root, installPath, manifestPath, err := managedWSLPaths(definition)
	if err != nil {
		return err
	}
	resolvedRoot, err := canonicalPath(root)
	if err != nil {
		return err
	}
	if info, statErr := os.Lstat(installPath); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("the managed folder for %s is a disallowed link", definition.Name)
		}
		resolvedInstall, resolveErr := canonicalPath(installPath)
		if resolveErr != nil || !pathInside(resolvedRoot, resolvedInstall) {
			return fmt.Errorf("the managed folder for %s is outside Seizen", definition.Name)
		}
		if err = os.RemoveAll(installPath); err != nil {
			return fmt.Errorf("could not clean up the incomplete installation of %s: %w", definition.Name, err)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	if err = os.Remove(manifestPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("could not clean up the manifest for %s: %w", definition.Name, err)
	}
	return nil
}

func checkWSLRuntime(ctx context.Context, wslPath string, definition wslDistributionDefinition) error {
	checkContext, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	command := exec.CommandContext(checkContext, wslPath, "--distribution", definition.RuntimeName, "--user", "root", "--exec", "/bin/sh", "-lc", `. /etc/os-release && printf '%s\n%s' "$ID" "$VERSION_ID"`)
	hideWindow(command)
	output, err := command.CombinedOutput()
	if err != nil {
		message := conciseWSLOutput(output)
		if message != "" {
			return fmt.Errorf("%s installed, but could not start: %s", definition.Name, message)
		}
		return fmt.Errorf("%s installed, but could not start: %w", definition.Name, err)
	}
	identity := strings.Fields(decodeWSLOutput(output))
	if len(identity) < 1 || identity[0] != definition.SystemID ||
		(definition.Version != "" && (len(identity) < 2 || identity[1] != definition.Version)) {
		return fmt.Errorf("the installed distribution does not match %s", definition.Name)
	}
	terminateContext, terminateCancel := context.WithTimeout(context.WithoutCancel(ctx), wslCommandTimeout)
	defer terminateCancel()
	terminate := exec.CommandContext(terminateContext, wslPath, "--terminate", definition.RuntimeName)
	hideWindow(terminate)
	if terminateOutput, terminateErr := terminate.CombinedOutput(); terminateErr != nil {
		return fmt.Errorf("could not shut down %s after verifying it: %s", definition.Name, conciseWSLOutput(terminateOutput))
	}
	return nil
}

func rollbackManagedWSLInstall(ctx context.Context, wslPath string, definition wslDistributionDefinition, registered bool) error {
	if registered {
		rollbackContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		command := exec.CommandContext(rollbackContext, wslPath, "--unregister", definition.RuntimeName)
		hideWindow(command)
		if output, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("could not roll back %s: %s", definition.Name, conciseWSLOutput(output))
		}
	}
	return removeManagedWSLFiles(definition)
}

func wslInstallArguments(definition wslDistributionDefinition, installPath string) []string {
	return []string{
		"--install", definition.OnlineName,
		"--name", definition.RuntimeName,
		"--location", installPath,
		"--version", "2",
		"--no-launch",
		"--web-download",
	}
}

func decodeWSLOutput(data []byte) string {
	utf16Text := len(data) >= 2 && (data[0] == 0xff && data[1] == 0xfe)
	if !utf16Text {
		for index := 1; index < len(data); index += 2 {
			if data[index] == 0 {
				utf16Text = true
				break
			}
		}
	}
	if !utf16Text {
		return strings.TrimSpace(strings.ToValidUTF8(string(data), "�"))
	}
	if len(data) >= 2 && data[0] == 0xff && data[1] == 0xfe {
		data = data[2:]
	}
	if len(data)%2 != 0 {
		data = data[:len(data)-1]
	}
	words := make([]uint16, 0, len(data)/2)
	for index := 0; index < len(data); index += 2 {
		words = append(words, uint16(data[index])|uint16(data[index+1])<<8)
	}
	return strings.TrimSpace(string(utf16.Decode(words)))
}

func conciseWSLOutput(data []byte) string {
	message := strings.Join(strings.Fields(decodeWSLOutput(data)), " ")
	if len(message) > maximumWSLErrorLength {
		message = message[len(message)-maximumWSLErrorLength:]
	}
	return message
}
