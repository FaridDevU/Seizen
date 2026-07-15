package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestWSLDistributionDefaultsAndPersists(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	path := filepath.Join(base, "config", "seizen.db")
	database := newDatabase(path, filepath.Join(base, "projects"))

	distribution, err := database.WSLDistribution(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if distribution.ID != "debian" || distribution.Version != "13" {
		t.Fatalf("expected Debian 13 by default, got %#v", distribution)
	}
	if err = database.SetWSLDistribution(ctx, "arch"); err != nil {
		t.Fatal(err)
	}
	if err = database.SetWSLDistribution(ctx, "kali"); err == nil {
		t.Fatal("expected an unsupported distribution to be rejected")
	}
	database.Close()

	reopened := newDatabase(path, filepath.Join(base, "unused"))
	defer reopened.Close()
	distribution, err = reopened.WSLDistribution(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if distribution.ID != "arch" {
		t.Fatalf("expected Arch to persist, got %#v", distribution)
	}
}

func TestWSLResourcesAreRestrictedToSupportedManagedDistributions(t *testing.T) {
	ids := make([]string, 0, len(wslDistributionDefinitions))
	for _, definition := range wslDistributionDefinitions {
		ids = append(ids, definition.ID)
		arguments := strings.Join(wslInstallArguments(definition, filepath.Join("C:\\Seizen", definition.ID)), " ")
		if !strings.Contains(arguments, "--name "+definition.RuntimeName) || !strings.Contains(arguments, "--location") {
			t.Fatalf("installation is not managed for %#v: %q", definition, arguments)
		}
	}
	if !reflect.DeepEqual(ids, []string{"debian", "ubuntu", "fedora", "arch"}) {
		t.Fatalf("unexpected WSL resources: %#v", ids)
	}
	if _, ok := wslDistributionByRuntime("Ubuntu"); ok {
		t.Fatal("an external Ubuntu distribution must not be treated as managed")
	}
}

func TestDecodeWSLOutputAcceptsUTF16LE(t *testing.T) {
	words := utf16.Encode([]rune("Seizen-Ubuntu\r\nSeizen-Debian\r\n"))
	data := []byte{0xff, 0xfe}
	for _, word := range words {
		data = append(data, byte(word), byte(word>>8))
	}
	if got := decodeWSLOutput(data); got != "Seizen-Ubuntu\r\nSeizen-Debian" {
		t.Fatalf("unexpected decoded WSL output %q", got)
	}
}

func TestWSLRestartStateIsActionable(t *testing.T) {
	for _, input := range []struct {
		installStatus  string
		windowsPending bool
	}{
		{installStatus: "VirtualMachinePlatform"},
		{windowsPending: true},
	} {
		required, message := wslRestartState(input.installStatus, input.windowsPending)
		if !required || !strings.Contains(message, "Restart Windows") || !strings.Contains(message, "without deleting") {
			t.Fatalf("expected an actionable restart state, got %v, %q", required, message)
		}
	}
	if required, message := wslRestartState("", false); required || message != "" {
		t.Fatalf("unexpected restart state: %v, %q", required, message)
	}
}

func TestWSLResourceStatePrioritizesRestartButKeepsInstalledRuntime(t *testing.T) {
	status, message := wslResourceState(false, nil, errors.New("list failed"), true, "restart")
	if status != "restart_required" || message != "restart" {
		t.Fatalf("unexpected pending resource state %q, %q", status, message)
	}
	status, message = wslResourceState(true, nil, errors.New("list failed"), true, "restart")
	if status != "installed" || message != "" {
		t.Fatalf("installed runtime was hidden by pending restart: %q, %q", status, message)
	}
	ownership := errors.New("not managed")
	status, message = wslResourceState(false, ownership, nil, true, "restart")
	if status != "restart_required" || message != "restart" {
		t.Fatalf("pending staged runtime was not preserved: %q, %q", status, message)
	}
}

func TestManagedWSLManifestOwnsOnlyItsSeizenDirectory(t *testing.T) {
	base := t.TempDir()
	t.Setenv("LOCALAPPDATA", base)
	definition, _ := wslDistributionByID("ubuntu")
	root, installPath, manifestPath, err := managedWSLPaths(definition)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(installPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err = writeManagedWSLManifest(definition, installPath); err != nil {
		t.Fatal(err)
	}
	if err = validateManagedWSLManifest(definition); err != nil {
		t.Fatalf("expected a managed manifest to validate: %v", err)
	}
	outside := filepath.Join(base, "outside")
	if err = os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	bad, err := json.Marshal(wslResourceManifest{Version: 1, ID: "ubuntu", RuntimeName: "Seizen-Ubuntu", InstallPath: outside})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(manifestPath, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = validateManagedWSLManifest(definition); err == nil {
		t.Fatal("expected a manifest outside Seizen to be rejected")
	}
	if !pathInside(root, installPath) {
		t.Fatal("test installation escaped the managed root")
	}
}

func TestManagedWSLRetryCleansOnlyItsPartialDirectory(t *testing.T) {
	base := t.TempDir()
	t.Setenv("LOCALAPPDATA", base)
	definition, _ := wslDistributionByID("debian")
	root, installPath, _, err := managedWSLPaths(definition)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(installPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(installPath, "partial.vhdx"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "keep.txt")
	if err = os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = removeManagedWSLFiles(definition); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(installPath); !os.IsNotExist(err) {
		t.Fatalf("partial install was not removed: %v", err)
	}
	if _, err = os.Stat(outside); err != nil {
		t.Fatalf("cleanup escaped %s: %v", root, err)
	}
}
