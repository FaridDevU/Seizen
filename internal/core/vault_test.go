package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCopyTreeFidelity(t *testing.T) {
	source := t.TempDir()
	files := map[string]string{
		"readme.md":                 "hello",
		filepath.Join("src", "a.go"): "package a",
		filepath.Join("src", "deep", "nested", "b.txt"): "deep",
		filepath.Join(".git", "config"):                 "[core]",
	}
	for name, content := range files {
		full := filepath.Join(source, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	target := filepath.Join(t.TempDir(), "copy")
	var progressCalls int
	if err := copyTree(context.Background(), source, target, func(int, int64) { progressCalls++ }); err != nil {
		t.Fatalf("copyTree: %v", err)
	}

	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(target, name))
		if err != nil {
			t.Fatalf("missing copied file %q: %v", name, err)
		}
		if string(got) != want {
			t.Errorf("file %q = %q, want %q", name, got, want)
		}
	}
}

func TestCopyTreeCancelled(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	target := filepath.Join(t.TempDir(), "copy")
	if err := copyTree(ctx, source, target, func(int, int64) {}); err == nil {
		t.Fatal("expected copyTree to stop on a cancelled context")
	}
}

func TestUniqueVaultTarget(t *testing.T) {
	root := t.TempDir()
	first, err := uniqueVaultTarget(root, "web")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(first) != "web" {
		t.Fatalf("first target = %q, want base web", first)
	}
	if err = os.Mkdir(first, 0o755); err != nil {
		t.Fatal(err)
	}
	second, err := uniqueVaultTarget(root, "web")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(second) != "web (2)" {
		t.Fatalf("second target = %q, want base %q", second, "web (2)")
	}
}

func TestSanitizeProjectName(t *testing.T) {
	cases := map[string]string{
		"clean":       "clean",
		"a/b:c":       "a-b-c",
		`<>:"/\|?*`:   "project", // only invalid characters remain
		"  spaced  ":  "spaced",
		"trailing.":   "trailing",
	}
	for input, want := range cases {
		if got := sanitizeProjectName(input); got != want {
			t.Errorf("sanitizeProjectName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestWithin(t *testing.T) {
	root := filepath.Join("C:", "vault")
	if runtime.GOOS != "windows" {
		root = "/vault"
	}
	inside := filepath.Join(root, "project")
	outside := filepath.Join(filepath.Dir(root), "elsewhere")
	if !within(root, inside) {
		t.Errorf("within(%q, %q) = false, want true", root, inside)
	}
	if within(root, outside) {
		t.Errorf("within(%q, %q) = true, want false", root, outside)
	}
}

func TestEstimateFolder(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.bin", "b.bin"} {
		if err := os.WriteFile(filepath.Join(root, name), make([]byte, 100), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if bytes, files, large := estimateFolder(root, 1000); bytes != 200 || files != 2 || large {
		t.Fatalf("estimateFolder = (%d,%d,%v), want (200,2,false)", bytes, files, large)
	}
	if _, _, large := estimateFolder(root, 150); !large {
		t.Fatal("expected large=true once the threshold is crossed")
	}
}

func TestHealVault(t *testing.T) {
	base := t.TempDir()
	app := &App{database: newDatabase(filepath.Join(base, "seizen.db"), "")}
	root, err := app.vaultRoot()
	if err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(root, ".seizen-import-abc")
	project := filepath.Join(root, "Keep")
	for _, dir := range []string{orphan, project} {
		if err = os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	app.healVault()

	if _, err = os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected the orphan import dir to be removed, got %v", err)
	}
	if info, statErr := os.Stat(project); statErr != nil || !info.IsDir() {
		t.Errorf("expected the project folder to be kept: %v", statErr)
	}
	if runtime.GOOS == "windows" {
		t.Cleanup(func() { _ = unprotectFolder(root); _ = unprotectFolder(project) })
		if err = os.Remove(project); err == nil {
			t.Error("expected the healed guard to block deletion")
		}
	}
}

// TestProtectBlocksDelete verifies the Windows guard: a protected folder resists deletion
// while its parent denies delete-child, and lifting the guard restores deletion.
func TestProtectBlocksDelete(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("folder deletion guards are Windows-only")
	}
	root := t.TempDir()
	if err := protectVaultRoot(root); err != nil {
		t.Fatalf("protectVaultRoot: %v", err)
	}
	t.Cleanup(func() { _ = unprotectFolder(root) }) // Let t.TempDir clean up afterward.

	project := filepath.Join(root, "guarded")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := protectFolder(project); err != nil {
		t.Fatalf("protectFolder: %v", err)
	}

	// A file inside stays writable even while the folder is guarded.
	if err := os.WriteFile(filepath.Join(project, "inside.txt"), []byte("ok"), 0o644); err != nil {
		t.Errorf("expected the guarded folder's contents to stay writable: %v", err)
	}
	if err := os.RemoveAll(project); err == nil {
		t.Error("expected deleting a guarded folder to fail")
	}

	if err := unprotectFolder(project); err != nil {
		t.Fatalf("unprotectFolder: %v", err)
	}
	if err := os.RemoveAll(project); err != nil {
		t.Errorf("expected deletion to succeed after lifting the guard: %v", err)
	}
}
