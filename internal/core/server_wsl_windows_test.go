//go:build windows

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const testServerID = "123e4567-e89b-12d3-a456-426614174000"

type recordedWslCommand struct {
	name string
	args []string
}

type recordingWslRunner struct {
	mu       sync.Mutex
	commands []recordedWslCommand
	result   wslCommandResult
	err      error
}

func (runner *recordingWslRunner) Run(_ context.Context, name string, args ...string) (wslCommandResult, error) {
	runner.mu.Lock()
	runner.commands = append(runner.commands, recordedWslCommand{name: name, args: append([]string(nil), args...)})
	runner.mu.Unlock()
	return runner.result, runner.err
}

func testWslProvider(t *testing.T) (*WslServerProvider, *recordingWslRunner) {
	t.Helper()
	payload := []byte("pinned Debian fixture")
	hash := sha256.Sum256(payload)
	runner := &recordingWslRunner{}
	provider := newWslServerProvider(t.TempDir(), runner, func(_ context.Context, _ string, destination string) error {
		return os.WriteFile(destination, payload, 0o600)
	})
	provider.rootfs = wslRootfsSpec{URL: "fixture://debian", SHA256: hex.EncodeToString(hash[:]), Size: int64(len(payload))}
	return provider, runner
}

func TestWslProviderCreatesPinnedManagedDistribution(t *testing.T) {
	provider, runner := testWslProvider(t)
	server := Server{ID: testServerID, Provider: "wsl", Distro: "Debian 12"}
	reference, err := provider.Create(context.Background(), server)
	if err != nil {
		t.Fatal(err)
	}
	if reference != "Seizen-Server-123e4567e89b12d3a456426614174000" {
		t.Fatalf("unexpected runtime reference: %s", reference)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("expected one WSL import, got %d", len(runner.commands))
	}
	command := runner.commands[0]
	if command.name != "wsl.exe" || len(command.args) != 6 || command.args[0] != "--import" || command.args[1] != reference || command.args[4] != "--version" || command.args[5] != "2" {
		t.Fatalf("unexpected import command: %s %v", command.name, command.args)
	}
	manifest, err := readWslManifest(filepath.Join(command.args[2], "seizen-server.json"))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ServerID != server.ID || manifest.RuntimeReference != reference || manifest.RootfsSHA256 != provider.rootfs.SHA256 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
}

func TestWslProviderRejectsExternalOrUnmanifestedUnregister(t *testing.T) {
	provider, runner := testWslProvider(t)
	server := Server{ID: testServerID, Provider: "wsl", RuntimeReference: "Ubuntu"}
	if err := provider.Destroy(context.Background(), server); err == nil {
		t.Fatal("expected an external distro to be rejected")
	}
	server.RuntimeReference = "Seizen-Server-123e4567e89b12d3a456426614174000"
	installPath := filepath.Join(provider.root, "123e4567e89b12d3a456426614174000")
	if err := os.MkdirAll(installPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := provider.Destroy(context.Background(), server); err == nil {
		t.Fatal("a matching prefix without a manifest must not be unregistered")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("unsafe unregister was attempted: %+v", runner.commands)
	}
}

func TestWslProviderRejectsTraversalManifest(t *testing.T) {
	provider, runner := testWslProvider(t)
	server := Server{ID: testServerID, Provider: "wsl", RuntimeReference: "Seizen-Server-123e4567e89b12d3a456426614174000"}
	installPath := filepath.Join(provider.root, "123e4567e89b12d3a456426614174000")
	if err := os.MkdirAll(installPath, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := wslManifest{
		Version:          1,
		ServerID:         server.ID,
		RuntimeReference: server.RuntimeReference,
		InstallPath:      filepath.Join(provider.root, "..", "external"),
		RootfsSHA256:     provider.rootfs.SHA256,
	}
	if err := writeWslManifest(filepath.Join(installPath, "seizen-server.json"), manifest); err != nil {
		t.Fatal(err)
	}
	if err := provider.Destroy(context.Background(), server); err == nil {
		t.Fatal("expected a traversal manifest to be rejected")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("unsafe unregister was attempted: %+v", runner.commands)
	}
}

func TestWslProviderUnregistersOnlyMatchingManifest(t *testing.T) {
	provider, runner := testWslProvider(t)
	server := Server{ID: testServerID, Provider: "wsl", Distro: "Debian 12"}
	reference, err := provider.Create(context.Background(), server)
	if err != nil {
		t.Fatal(err)
	}
	server.RuntimeReference = reference
	if err = provider.Destroy(context.Background(), server); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 2 || runner.commands[1].args[0] != "--unregister" || runner.commands[1].args[1] != reference {
		t.Fatalf("unexpected command sequence: %+v", runner.commands)
	}
	if _, err = os.Stat(filepath.Join(provider.root, "123e4567e89b12d3a456426614174000")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("managed directory still exists: %v", err)
	}
}

func TestWslProviderRootTerminalIsExplicitlyRoot(t *testing.T) {
	provider, _ := testWslProvider(t)
	server := Server{ID: testServerID, Provider: "wsl", Distro: "Debian 12"}
	reference, err := provider.Create(context.Background(), server)
	if err != nil {
		t.Fatal(err)
	}
	server.RuntimeReference = reference
	path, args, err := provider.RootTerminalCommand(server)
	if err != nil {
		t.Fatal(err)
	}
	if path != "wsl.exe" || strings.Join(args, " ") != "-d "+reference+" -u root" {
		t.Fatalf("unexpected terminal command: %s %v", path, args)
	}
}

type partialImportRunner struct {
	commands []recordedWslCommand
}

func (runner *partialImportRunner) Run(_ context.Context, name string, args ...string) (wslCommandResult, error) {
	runner.commands = append(runner.commands, recordedWslCommand{name: name, args: append([]string(nil), args...)})
	if len(args) > 2 && args[0] == "--import" {
		if err := os.WriteFile(filepath.Join(args[2], "ext4.vhdx"), []byte("partial"), 0o600); err != nil {
			return wslCommandResult{}, err
		}
		return wslCommandResult{ExitCode: 1}, errors.New("import failed")
	}
	if len(args) > 0 && args[0] == "--unregister" {
		return wslCommandResult{ExitCode: 1}, errors.New("cleanup failed")
	}
	return wslCommandResult{}, nil
}

func TestWslPartialImportKeepsManagedRecoveryReference(t *testing.T) {
	payload := []byte("pinned Debian fixture")
	hash := sha256.Sum256(payload)
	runner := &partialImportRunner{}
	provider := newWslServerProvider(t.TempDir(), runner, func(_ context.Context, _ string, destination string) error {
		return os.WriteFile(destination, payload, 0o600)
	})
	provider.rootfs = wslRootfsSpec{URL: "fixture://debian", SHA256: hex.EncodeToString(hash[:]), Size: int64(len(payload))}
	server := Server{ID: testServerID, Provider: "wsl", Distro: "Debian 12"}
	reference, err := provider.Create(context.Background(), server)
	if err == nil || reference != "Seizen-Server-123e4567e89b12d3a456426614174000" {
		t.Fatalf("partial import lost its recovery reference: ref=%q err=%v", reference, err)
	}
	installPath := filepath.Join(provider.root, "123e4567e89b12d3a456426614174000")
	manifest, manifestErr := readWslManifest(filepath.Join(installPath, "seizen-server.json"))
	if manifestErr != nil || manifest.RuntimeReference != reference {
		t.Fatalf("partial import is not safely recoverable: %+v, %v", manifest, manifestErr)
	}
	if len(runner.commands) != 2 || runner.commands[1].args[0] != "--unregister" {
		t.Fatalf("partial import cleanup was not attempted: %+v", runner.commands)
	}
}

func TestNormalizeServerUUIDRejectsPathsAndNonUUIDs(t *testing.T) {
	for _, value := range []string{"Ubuntu", "../server", "123e4567-e89b-12d3-a456-42661417400z", "Seizen-Server-123"} {
		if _, err := normalizeServerUUID(value); err == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
	if value, err := normalizeServerUUID(testServerID); err != nil || value != "123e4567e89b12d3a456426614174000" {
		t.Fatalf("unexpected normalized UUID: %q, %v", value, err)
	}
}
