package core

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestZipProjectDirectory(t *testing.T) {
	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "src", "app.txt"), []byte("Seizen"), 0o644); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "project.zip")
	if err := zipProjectDirectory(source, destination); err != nil {
		t.Fatal(err)
	}

	reader, err := zip.OpenReader(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if len(reader.File) != 2 || reader.File[0].Name != "src/" || reader.File[1].Name != "src/app.txt" {
		t.Fatalf("unexpected ZIP contents: %#v", reader.File)
	}

	if err = zipProjectDirectory(source, filepath.Join(source, "project.zip")); err == nil {
		t.Fatal("the ZIP was allowed to save inside the project")
	}

	existing := filepath.Join(t.TempDir(), "existing.zip")
	if err = os.WriteFile(existing, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = zipProjectDirectory(source, existing); err == nil {
		t.Fatal("an existing file was overwritten")
	}
	if value, readErr := os.ReadFile(existing); readErr != nil || string(value) != "keep" {
		t.Fatalf("the existing file changed: %q, %v", value, readErr)
	}
}
