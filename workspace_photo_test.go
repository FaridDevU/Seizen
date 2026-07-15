package main

import (
	"errors"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectWorkspacePhotosPersistAndStayScoped(t *testing.T) {
	app, db, root := deletionTestApp(t)
	project := deletionTestProject(t, db, filepath.Join(root, "Photos"), ProjectCreated)
	other := deletionTestProject(t, db, filepath.Join(root, "Other"), ProjectCreated)
	firstSource := filepath.Join(t.TempDir(), "first.png")
	secondSource := filepath.Join(t.TempDir(), "second.jpg")
	writePNG(t, firstSource, 320, 180, color.RGBA{R: 30, G: 60, B: 90, A: 255})
	writeJPEG(t, secondSource, 160, 90, color.RGBA{R: 100, G: 80, B: 40, A: 255})

	first, err := app.setProjectWorkspacePhoto(project.ID, project.Path, firstSource)
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.setProjectWorkspacePhoto(project.ID, project.Path, secondSource)
	if err != nil {
		t.Fatal(err)
	}
	if first.AssetID == second.AssetID || !strings.HasPrefix(first.DataURL, "data:image/png;base64,") || !strings.HasPrefix(second.DataURL, "data:image/jpeg;base64,") {
		t.Fatalf("unexpected photo assets: %#v %#v", first, second)
	}
	loaded, err := app.GetProjectWorkspacePhoto(project.ID, project.Path, first.AssetID)
	if err != nil || loaded != first.DataURL {
		t.Fatalf("expected the stored photo, got %q, %v", loaded, err)
	}
	if _, err = app.GetProjectWorkspacePhoto(other.ID, other.Path, first.AssetID); err == nil {
		t.Fatal("expected another project to be unable to read the photo")
	}
	if _, err = app.GetProjectWorkspacePhoto(project.ID, other.Path, first.AssetID); err == nil {
		t.Fatal("expected a mismatched project path to be rejected")
	}
	if err = app.DeleteProjectWorkspacePhoto(other.ID, other.Path, first.AssetID); err != nil {
		t.Fatal(err)
	}
	if loaded, err = app.GetProjectWorkspacePhoto(project.ID, project.Path, first.AssetID); err != nil || loaded != first.DataURL {
		t.Fatalf("another project deleted this project's photo: %q, %v", loaded, err)
	}
	if err = app.DeleteProjectWorkspacePhoto(project.ID, project.Path, first.AssetID); err != nil {
		t.Fatal(err)
	}
	if _, err = app.GetProjectWorkspacePhoto(project.ID, project.Path, first.AssetID); err == nil {
		t.Fatal("expected the deleted photo to be unavailable")
	}
	if loaded, err = app.GetProjectWorkspacePhoto(project.ID, project.Path, second.AssetID); err != nil || loaded != second.DataURL {
		t.Fatalf("deleting one photo affected another: %q, %v", loaded, err)
	}
}

func TestProjectWorkspacePhotoRejectsInvalidAssetIDs(t *testing.T) {
	app, db, root := deletionTestApp(t)
	project := deletionTestProject(t, db, filepath.Join(root, "Photos"), ProjectCreated)
	invalid := []string{
		"",
		"../outside",
		"123e4567-e89b-12d3-a456-426614174000.image",
		"123E4567-E89B-12D3-A456-426614174000",
		"123e4567e89b12d3a456426614174000",
		"123e4567-e89b-12d3-a456-42661417400g",
	}
	for _, assetID := range invalid {
		if _, err := app.GetProjectWorkspacePhoto(project.ID, project.Path, assetID); err == nil {
			t.Errorf("expected %q to be rejected", assetID)
		}
		if err := app.DeleteProjectWorkspacePhoto(project.ID, project.Path, assetID); err == nil {
			t.Errorf("expected deletion with %q to be rejected", assetID)
		}
	}
}

func TestDeleteProjectRemovesWorkspacePhotos(t *testing.T) {
	app, db, root := deletionTestApp(t)
	project := deletionTestProject(t, db, filepath.Join(root, "Photos"), ProjectCreated)
	source := filepath.Join(t.TempDir(), "photo.png")
	writePNG(t, source, 160, 90, color.RGBA{R: 30, G: 80, B: 120, A: 255})
	photo, err := app.setProjectWorkspacePhoto(project.ID, project.Path, source)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := app.workspacePhotoPath(project.ID, photo.AssetID)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Dir(stored)
	if err = app.DeleteProject(project.ID, project.Path); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected the managed workspace photos to be removed, got %v", err)
	}
}
