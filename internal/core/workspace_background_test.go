package core

import (
	"errors"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectWorkspaceBackgroundPersistsClearsAndStaysScoped(t *testing.T) {
	app, db, root := deletionTestApp(t)
	project := deletionTestProject(t, db, filepath.Join(root, "Background"), ProjectCreated)
	other := deletionTestProject(t, db, filepath.Join(root, "Other"), ProjectCreated)
	source := filepath.Join(t.TempDir(), "wallpaper.png")
	writePNG(t, source, 320, 180, color.RGBA{R: 30, G: 60, B: 90, A: 255})

	written, err := app.setProjectWorkspaceBackground(project.ID, project.Path, source)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(written, "data:image/png;base64,") {
		t.Fatalf("expected a PNG data URL, got %q", written)
	}
	loaded, err := app.GetProjectWorkspaceBackground(project.ID, project.Path)
	if err != nil || loaded != written {
		t.Fatalf("expected the stored background, got %q, %v", loaded, err)
	}
	if otherBackground, err := app.GetProjectWorkspaceBackground(other.ID, other.Path); err != nil || otherBackground != "" {
		t.Fatalf("expected the other project to keep its default, got %q, %v", otherBackground, err)
	}
	if _, err = app.GetProjectWorkspaceBackground(project.ID, other.Path); err == nil {
		t.Fatal("expected a mismatched project path to be rejected")
	}

	if err = app.ClearProjectWorkspaceBackground(project.ID, project.Path); err != nil {
		t.Fatal(err)
	}
	loaded, err = app.GetProjectWorkspaceBackground(project.ID, project.Path)
	if err != nil || loaded != "" {
		t.Fatalf("expected the default background after clearing, got %q, %v", loaded, err)
	}
}

func TestProjectWorkspaceBackgroundRejectsUnsafeFiles(t *testing.T) {
	app, db, root := deletionTestApp(t)
	project := deletionTestProject(t, db, filepath.Join(root, "Background"), ProjectCreated)

	t.Run("corrupt image", func(t *testing.T) {
		source := filepath.Join(t.TempDir(), "wallpaper.png")
		if err := os.WriteFile(source, []byte("not an image"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := app.setProjectWorkspaceBackground(project.ID, project.Path, source); err == nil {
			t.Fatal("expected a corrupt image to be rejected")
		}
	})

	t.Run("format mismatch", func(t *testing.T) {
		source := filepath.Join(t.TempDir(), "wallpaper.png")
		writeJPEG(t, source, 80, 60, color.RGBA{G: 255, A: 255})
		if _, err := app.setProjectWorkspaceBackground(project.ID, project.Path, source); err == nil {
			t.Fatal("expected content that does not match its extension to be rejected")
		}
	})

	t.Run("oversize", func(t *testing.T) {
		source := filepath.Join(t.TempDir(), "wallpaper.jpg")
		file, err := os.Create(source)
		if err != nil {
			t.Fatal(err)
		}
		if err = file.Truncate(maxWorkspaceBackgroundSize + 1); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err = file.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err = app.setProjectWorkspaceBackground(project.ID, project.Path, source); err == nil {
			t.Fatal("expected an oversized image to be rejected")
		}
	})

	t.Run("symlink", func(t *testing.T) {
		directory := t.TempDir()
		target := filepath.Join(directory, "target.png")
		link := filepath.Join(directory, "wallpaper.png")
		writePNG(t, target, 80, 60, color.RGBA{B: 255, A: 255})
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlinks are unavailable: %v", err)
		}
		if _, err := app.setProjectWorkspaceBackground(project.ID, project.Path, link); err == nil {
			t.Fatal("expected a symlink to be rejected")
		}
	})
}

func TestDeleteProjectRemovesWorkspaceBackground(t *testing.T) {
	app, db, root := deletionTestApp(t)
	project := deletionTestProject(t, db, filepath.Join(root, "Background"), ProjectCreated)
	source := filepath.Join(t.TempDir(), "wallpaper.jpg")
	writeJPEG(t, source, 160, 90, color.RGBA{R: 100, G: 80, B: 40, A: 255})
	if _, err := app.setProjectWorkspaceBackground(project.ID, project.Path, source); err != nil {
		t.Fatal(err)
	}
	stored, err := app.workspaceBackgroundPath(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = app.DeleteProject(project.ID, project.Path); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(stored); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected the managed background to be removed, got %v", err)
	}
}
