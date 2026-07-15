//go:build windows

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	wslRootfsURL    = "https://raw.githubusercontent.com/debuerreotype/docker-debian-artifacts/3355451ec423321fe5ba232dc55c00f3216f6d87/bookworm/oci/blobs/rootfs.tar.gz"
	wslRootfsSHA256 = "425befdf76e52426879d2abe42093a00dca59a893e7b4fa2a7679b0180b71d4b"
	wslRootfsSize   = int64(48502210)
	wslDistroPrefix = "Seizen-Server-"
)

type wslRootfsSpec struct {
	URL    string
	SHA256 string
	Size   int64
}

type wslCommandResult struct {
	Output   string
	ExitCode int
}

type wslCommandRunner interface {
	Run(context.Context, string, ...string) (wslCommandResult, error)
}

type wslDownloader func(context.Context, string, string) error

type wslManifest struct {
	Version          int    `json:"version"`
	ServerID         string `json:"serverId"`
	RuntimeReference string `json:"runtimeReference"`
	InstallPath      string `json:"installPath"`
	RootfsSHA256     string `json:"rootfsSha256"`
}

type WslServerProvider struct {
	root       string
	rootfs     wslRootfsSpec
	runner     wslCommandRunner
	downloader wslDownloader
}

func NewWslServerProvider() *WslServerProvider {
	root := os.Getenv("LOCALAPPDATA")
	if root == "" {
		root, _ = os.UserConfigDir()
	}
	return newWslServerProvider(filepath.Join(root, "Seizen", "servers"), execWslRunner{}, downloadWslRootfs)
}

func newWslServerProvider(root string, runner wslCommandRunner, downloader wslDownloader) *WslServerProvider {
	return &WslServerProvider{
		root:       root,
		rootfs:     wslRootfsSpec{URL: wslRootfsURL, SHA256: wslRootfsSHA256, Size: wslRootfsSize},
		runner:     runner,
		downloader: downloader,
	}
}

func (provider *WslServerProvider) Create(ctx context.Context, server Server) (string, error) {
	name, installPath, err := provider.newServerIdentity(server)
	if err != nil {
		return "", err
	}
	if server.Provider != "wsl" || server.RuntimeReference != "" || !strings.EqualFold(strings.TrimSpace(server.Distro), "Debian 12") {
		return "", errors.New("the new WSL server is not valid")
	}
	if err = os.MkdirAll(provider.root, 0o700); err != nil {
		return "", fmt.Errorf("could not prepare the WSL servers directory: %w", err)
	}
	if entries, readErr := os.ReadDir(installPath); readErr == nil && len(entries) != 0 {
		return "", errors.New("the WSL server's managed directory already contains data")
	} else if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return "", readErr
	}
	if err = os.MkdirAll(installPath, 0o700); err != nil {
		return "", err
	}
	canonicalInstall, err := filepath.EvalSymlinks(installPath)
	if err != nil {
		_ = os.RemoveAll(installPath)
		return "", err
	}
	rootfsPath, err := provider.cachedRootfs(ctx)
	if err != nil {
		_ = os.RemoveAll(installPath)
		return "", err
	}
	_, err = provider.runner.Run(ctx, "wsl.exe", "--import", name, canonicalInstall, rootfsPath, "--version", "2")
	if err != nil {
		importErr := fmt.Errorf("could not import Debian 12 into WSL: %w", err)
		entries, readErr := os.ReadDir(canonicalInstall)
		if readErr != nil || len(entries) == 0 {
			_ = os.RemoveAll(installPath)
			return "", errors.Join(importErr, readErr)
		}
		manifest := wslManifest{Version: 1, ServerID: server.ID, RuntimeReference: name, InstallPath: canonicalInstall, RootfsSHA256: provider.rootfs.SHA256}
		manifestErr := writeWslManifest(filepath.Join(canonicalInstall, "seizen-server.json"), manifest)
		cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		_, unregisterErr := provider.runner.Run(cleanupContext, "wsl.exe", "--unregister", name)
		cancel()
		if unregisterErr == nil {
			removeErr := os.RemoveAll(canonicalInstall)
			return "", errors.Join(importErr, manifestErr, removeErr)
		}
		// Preserve the deterministic reference and manifest so the failed draft remains visible and safely destroyable.
		return name, errors.Join(importErr, manifestErr, fmt.Errorf("could not revert the partial import: %w", unregisterErr))
	}
	manifest := wslManifest{Version: 1, ServerID: server.ID, RuntimeReference: name, InstallPath: canonicalInstall, RootfsSHA256: provider.rootfs.SHA256}
	if err = writeWslManifest(filepath.Join(canonicalInstall, "seizen-server.json"), manifest); err != nil {
		_, unregisterErr := provider.runner.Run(context.WithoutCancel(ctx), "wsl.exe", "--unregister", name)
		_ = os.RemoveAll(canonicalInstall)
		return "", errors.Join(fmt.Errorf("could not register the WSL manifest: %w", err), unregisterErr)
	}
	return name, nil
}

func (provider *WslServerProvider) Start(ctx context.Context, server Server) error {
	if _, _, err := provider.validateManagedServer(server); err != nil {
		return err
	}
	_, err := provider.runner.Run(ctx, "wsl.exe", "-d", server.RuntimeReference, "-u", "root", "--exec", "/bin/true")
	return err
}

func (provider *WslServerProvider) Stop(ctx context.Context, server Server) error {
	if _, _, err := provider.validateManagedServer(server); err != nil {
		return err
	}
	_, err := provider.runner.Run(ctx, "wsl.exe", "--terminate", server.RuntimeReference)
	return err
}

func (provider *WslServerProvider) Restart(ctx context.Context, server Server) error {
	if err := provider.Stop(ctx, server); err != nil {
		return err
	}
	return provider.Start(ctx, server)
}

func (provider *WslServerProvider) Destroy(ctx context.Context, server Server) error {
	_, installPath, err := provider.validateManagedServer(server)
	if err != nil {
		return err
	}
	if _, err = provider.runner.Run(ctx, "wsl.exe", "--unregister", server.RuntimeReference); err != nil {
		return fmt.Errorf("could not unregister the WSL server: %w", err)
	}
	if err = os.RemoveAll(installPath); err != nil {
		return fmt.Errorf("the server was unregistered, but its directory could not be cleaned up: %w", err)
	}
	return nil
}

func (provider *WslServerProvider) Exec(ctx context.Context, server Server, command string) (ServerExecResult, error) {
	if _, _, err := provider.validateManagedServer(server); err != nil {
		return ServerExecResult{}, err
	}
	if strings.TrimSpace(command) == "" {
		return ServerExecResult{}, errors.New("the WSL command is empty")
	}
	result, err := provider.runner.Run(ctx, "wsl.exe", "-d", server.RuntimeReference, "-u", "root", "--exec", "/bin/sh", "-lc", command)
	return ServerExecResult{Output: result.Output, ExitCode: result.ExitCode}, err
}

func (provider *WslServerProvider) Stats(ctx context.Context, server Server) (ServerStats, error) {
	result, err := provider.Exec(ctx, server, `awk '/MemTotal:/{t=$2}/MemAvailable:/{a=$2}END{printf "%d %d", (t-a)/1024, t/1024}' /proc/meminfo`)
	if err != nil {
		return ServerStats{}, err
	}
	fields := strings.Fields(result.Output)
	if len(fields) != 2 {
		return ServerStats{}, errors.New("WSL returned invalid memory statistics")
	}
	used, usedErr := strconv.Atoi(fields[0])
	total, totalErr := strconv.Atoi(fields[1])
	if usedErr != nil || totalErr != nil {
		return ServerStats{}, errors.New("WSL returned invalid memory statistics")
	}
	return ServerStats{
		MemoryUsedMB:     used,
		MemoryLimitMB:    total,
		LimitsEnforced:   false,
		LimitDescription: fmt.Sprintf("CPU %.1f and RAM %d MB requested; WSL does not enforce strong per-server isolation.", server.CPULimit, server.MemoryMB),
	}, nil
}

func (provider *WslServerProvider) InspectServices(ctx context.Context, server Server) ([]ServerService, error) {
	result, err := provider.Exec(ctx, server, "ss -H -lnt")
	if err != nil {
		return nil, err
	}
	services := make([]ServerService, 0)
	seen := make(map[int]bool)
	for _, line := range strings.Split(result.Output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		host, port, ok := splitWslAddress(fields[3])
		if !ok || seen[port] {
			continue
		}
		seen[port] = true
		services = append(services, ServerService{
			ID:       fmt.Sprintf("observed-tcp-%d", port),
			ServerID: server.ID,
			Name:     fmt.Sprintf("TCP %d", port),
			Kind:     "service",
			Host:     host,
			Port:     &port,
			Protocol: "tcp",
			Status:   "running",
			Source:   "observed",
		})
	}
	return services, nil
}

func (provider *WslServerProvider) CheckHealth(ctx context.Context, server Server) (ServerHealth, error) {
	result, err := provider.Exec(ctx, server, "/bin/true")
	if err != nil || result.ExitCode != 0 {
		if err == nil {
			err = fmt.Errorf("the process exited with code %d", result.ExitCode)
		}
		return ServerHealth{Healthy: false, Message: err.Error()}, nil
	}
	return ServerHealth{Healthy: true, Message: "Debian 12 is responding"}, nil
}

// RootTerminalCommand returns the exact managed distro command used by the
// existing ConPTY terminal integration.
func (provider *WslServerProvider) RootTerminalCommand(server Server) (string, []string, error) {
	if _, _, err := provider.validateManagedServer(server); err != nil {
		return "", nil, err
	}
	return "wsl.exe", []string{"-d", server.RuntimeReference, "-u", "root"}, nil
}

func (provider *WslServerProvider) newServerIdentity(server Server) (string, string, error) {
	normalized, err := normalizeServerUUID(server.ID)
	if err != nil {
		return "", "", err
	}
	root, err := filepath.Abs(provider.root)
	if err != nil {
		return "", "", err
	}
	return wslDistroPrefix + normalized, filepath.Join(root, normalized), nil
}

func (provider *WslServerProvider) validateManagedServer(server Server) (wslManifest, string, error) {
	name, expectedPath, err := provider.newServerIdentity(server)
	if err != nil || server.Provider != "wsl" || server.RuntimeReference != name {
		return wslManifest{}, "", errors.New("the WSL reference does not belong to this Seizen server")
	}
	root, err := filepath.Abs(provider.root)
	if err != nil {
		return wslManifest{}, "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return wslManifest{}, "", fmt.Errorf("the WSL managed root was not found: %w", err)
	}
	installPath, err := filepath.EvalSymlinks(expectedPath)
	if err != nil {
		return wslManifest{}, "", fmt.Errorf("the server's managed directory was not found: %w", err)
	}
	if !pathInside(root, installPath) {
		return wslManifest{}, "", errors.New("the WSL directory is outside the root managed by Seizen")
	}
	manifest, err := readWslManifest(filepath.Join(installPath, "seizen-server.json"))
	if err != nil {
		return wslManifest{}, "", fmt.Errorf("the WSL server does not have a valid manifest: %w", err)
	}
	manifestPath, err := filepath.Abs(manifest.InstallPath)
	if err != nil {
		return wslManifest{}, "", err
	}
	manifestPath, err = filepath.EvalSymlinks(manifestPath)
	if err != nil || !samePath(manifestPath, installPath) || !pathInside(root, manifestPath) ||
		manifest.Version != 1 || manifest.ServerID != server.ID || manifest.RuntimeReference != server.RuntimeReference ||
		manifest.RootfsSHA256 != provider.rootfs.SHA256 {
		return wslManifest{}, "", errors.New("the WSL manifest does not match the server row")
	}
	return manifest, installPath, nil
}

func (provider *WslServerProvider) cachedRootfs(ctx context.Context) (string, error) {
	cacheDir := filepath.Join(provider.root, "cache")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return "", err
	}
	destination := filepath.Join(cacheDir, "debian-bookworm-rootfs.tar.gz")
	if err := verifyWslRootfs(destination, provider.rootfs); err == nil {
		return destination, nil
	}
	temporary, err := os.CreateTemp(cacheDir, "rootfs-*.part")
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	_ = temporary.Close()
	defer os.Remove(temporaryPath)
	if err = provider.downloader(ctx, provider.rootfs.URL, temporaryPath); err != nil {
		return "", fmt.Errorf("could not download Debian 12: %w", err)
	}
	if err = verifyWslRootfs(temporaryPath, provider.rootfs); err != nil {
		return "", err
	}
	_ = os.Remove(destination)
	if err = os.Rename(temporaryPath, destination); err != nil {
		return "", err
	}
	return destination, nil
}

type execWslRunner struct{}

func (execWslRunner) Run(ctx context.Context, name string, arguments ...string) (wslCommandResult, error) {
	command := exec.CommandContext(ctx, name, arguments...)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := command.CombinedOutput()
	result := wslCommandResult{Output: strings.ToValidUTF8(string(output), "\uFFFD"), ExitCode: 0}
	if err != nil {
		result.ExitCode = -1
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			result.ExitCode = exitError.ExitCode()
		}
	}
	return result, err
}

func downloadWslRootfs(ctx context.Context, rawURL, destination string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, io.LimitReader(response.Body, wslRootfsSize+1))
	return errors.Join(copyErr, file.Sync(), file.Close())
}

func verifyWslRootfs(path string, spec wslRootfsSpec) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() != spec.Size {
		return fmt.Errorf("the Debian 12 image has an unexpected size: %d", info.Size())
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), spec.SHA256) {
		return errors.New("the Debian 12 SHA-256 checksum does not match")
	}
	return nil
}

func writeWslManifest(path string, manifest wslManifest) error {
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "manifest-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err = temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(data)
	}
	if err == nil {
		err = temporary.Sync()
	}
	err = errors.Join(err, temporary.Close())
	if err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func readWslManifest(path string) (wslManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return wslManifest{}, err
	}
	var manifest wslManifest
	if err = json.Unmarshal(data, &manifest); err != nil {
		return wslManifest{}, err
	}
	return manifest, nil
}

func normalizeServerUUID(id string) (string, error) {
	value := strings.ToLower(strings.ReplaceAll(strings.Trim(strings.TrimSpace(id), "{}"), "-", ""))
	if len(value) != 32 {
		return "", errors.New("the server identifier is not a valid UUID")
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return "", errors.New("the server identifier is not a valid UUID")
		}
	}
	return value, nil
}

func splitWslAddress(address string) (string, int, bool) {
	separator := strings.LastIndex(address, ":")
	if separator < 0 {
		return "", 0, false
	}
	host := strings.Trim(address[:separator], "[]")
	port, err := strconv.Atoi(address[separator+1:])
	return host, port, err == nil && port > 0 && port <= 65535
}
