package core

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectThumbnailSelectionFormatAndSize(t *testing.T) {
	t.Run("seizen convention and bounded output", func(t *testing.T) {
		app, _, project, root := thumbnailTestProject(t)
		writePNG(t, filepath.Join(root, "thumbnail.png"), 320, 200, color.RGBA{B: 255, A: 255})
		writePNG(t, filepath.Join(root, ".seizen", "thumbnail.png"), 640, 480, color.RGBA{R: 255, A: 255})

		dataURL, err := app.GetProjectThumbnail(project.ID, project.Path)
		if err != nil {
			t.Fatal(err)
		}
		thumbnail := decodeThumbnailDataURL(t, dataURL, "image/png", "png")
		if thumbnail.Bounds().Dx() > thumbnailWidth || thumbnail.Bounds().Dy() > thumbnailHeight {
			t.Fatalf("thumbnail is too large: %v", thumbnail.Bounds())
		}
		assertDominantColor(t, thumbnail, "red")
	})

	t.Run("preferred name beats directory", func(t *testing.T) {
		app, _, project, root := thumbnailTestProject(t)
		writeJPEG(t, filepath.Join(root, "cover.jpg"), 80, 60, color.RGBA{R: 255, A: 255})
		writePNG(t, filepath.Join(root, "public", "thumbnail.png"), 80, 60, color.RGBA{G: 255, A: 255})

		dataURL, err := app.GetProjectThumbnail(project.ID, project.Path)
		if err != nil {
			t.Fatal(err)
		}
		assertDominantColor(t, decodeThumbnailDataURL(t, dataURL, "image/png", "png"), "green")
	})

	t.Run("fallback is alphabetical and preserves jpeg", func(t *testing.T) {
		app, _, project, root := thumbnailTestProject(t)
		writePNG(t, filepath.Join(root, "zebra.png"), 80, 60, color.RGBA{R: 255, A: 255})
		writeJPEG(t, filepath.Join(root, "assets", "app.jpeg"), 80, 60, color.RGBA{B: 255, A: 255})

		dataURL, err := app.GetProjectThumbnail(project.ID, project.Path)
		if err != nil {
			t.Fatal(err)
		}
		assertDominantColor(t, decodeThumbnailDataURL(t, dataURL, "image/jpeg", "jpeg"), "blue")
	})
}

func TestProjectThumbnailSkipsInvalidImages(t *testing.T) {
	t.Run("corrupt preferred image", func(t *testing.T) {
		app, _, project, root := thumbnailTestProject(t)
		writeThumbnailTestFile(t, filepath.Join(root, ".seizen", "thumbnail.png"), []byte("not an image"))
		writeJPEG(t, filepath.Join(root, "preview.jpg"), 80, 60, color.RGBA{G: 255, A: 255})

		dataURL, err := app.GetProjectThumbnail(project.ID, project.Path)
		if err != nil {
			t.Fatal(err)
		}
		assertDominantColor(t, decodeThumbnailDataURL(t, dataURL, "image/jpeg", "jpeg"), "green")
	})

	t.Run("format does not match extension", func(t *testing.T) {
		app, _, project, root := thumbnailTestProject(t)
		writeJPEG(t, filepath.Join(root, "thumbnail.png"), 80, 60, color.RGBA{R: 255, A: 255})
		writePNG(t, filepath.Join(root, "preview.png"), 80, 60, color.RGBA{B: 255, A: 255})

		dataURL, err := app.GetProjectThumbnail(project.ID, project.Path)
		if err != nil {
			t.Fatal(err)
		}
		assertDominantColor(t, decodeThumbnailDataURL(t, dataURL, "image/png", "png"), "blue")
	})

	t.Run("excessive dimensions", func(t *testing.T) {
		app, _, project, root := thumbnailTestProject(t)
		writePNG(t, filepath.Join(root, "thumbnail.png"), maxThumbnailDimension+1, 1, color.RGBA{R: 255, A: 255})
		writePNG(t, filepath.Join(root, "preview.png"), 100, 50, color.RGBA{G: 255, A: 255})

		dataURL, err := app.GetProjectThumbnail(project.ID, project.Path)
		if err != nil {
			t.Fatal(err)
		}
		assertDominantColor(t, decodeThumbnailDataURL(t, dataURL, "image/png", "png"), "green")
	})
}

func TestProjectThumbnailRejectsOversizeSymlinkAndMismatch(t *testing.T) {
	t.Run("oversize", func(t *testing.T) {
		app, _, project, root := thumbnailTestProject(t)
		path := filepath.Join(root, "thumbnail.png")
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err = file.Truncate(maxThumbnailSize + 1); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err = file.Close(); err != nil {
			t.Fatal(err)
		}

		dataURL, err := app.GetProjectThumbnail(project.ID, project.Path)
		if err != nil || dataURL != "" {
			t.Fatalf("expected oversized image to be ignored, got %q, %v", dataURL, err)
		}
	})

	t.Run("symlink outside project", func(t *testing.T) {
		app, _, project, root := thumbnailTestProject(t)
		outside := filepath.Join(t.TempDir(), "outside")
		writePNG(t, filepath.Join(outside, "thumbnail.png"), 80, 60, color.RGBA{R: 255, A: 255})
		if err := os.Symlink(outside, filepath.Join(root, "public")); err != nil {
			t.Skipf("symlinks are unavailable: %v", err)
		}

		dataURL, err := app.GetProjectThumbnail(project.ID, project.Path)
		if err != nil || dataURL != "" {
			t.Fatalf("expected outside symlink to be ignored, got %q, %v", dataURL, err)
		}
	})

	t.Run("path mismatch", func(t *testing.T) {
		app, _, project, root := thumbnailTestProject(t)
		writePNG(t, filepath.Join(root, "thumbnail.png"), 80, 60, color.RGBA{R: 255, A: 255})
		if _, err := app.GetProjectThumbnail(project.ID, filepath.Join(filepath.Dir(root), "Other")); err == nil {
			t.Fatal("expected a mismatched path to be rejected")
		}
	})
}

func thumbnailTestProject(t *testing.T) (*App, *sql.DB, Project, string) {
	t.Helper()
	ctx := context.Background()
	base := t.TempDir()
	root := filepath.Join(base, "projects", "Demo")
	database := newDatabase(filepath.Join(base, "config", "seizen.db"), filepath.Dir(root))
	if err := database.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := database.Pool(ctx)
	if err != nil {
		t.Fatal(err)
	}
	project, err := upsertProject(ctx, db, FSProjectInfo{Name: "Demo", Path: root}, ProjectCreated)
	if err != nil {
		t.Fatal(err)
	}
	return &App{database: database}, db, project, root
}

func writePNG(t *testing.T, path string, width, height int, fill color.Color) {
	t.Helper()
	writeTestImage(t, path, width, height, fill, func(file *os.File, source image.Image) error {
		return png.Encode(file, source)
	})
}

func writeJPEG(t *testing.T, path string, width, height int, fill color.Color) {
	t.Helper()
	writeTestImage(t, path, width, height, fill, func(file *os.File, source image.Image) error {
		return jpeg.Encode(file, source, &jpeg.Options{Quality: 90})
	})
}

func writeTestImage(t *testing.T, path string, width, height int, fill color.Color, encode func(*os.File, image.Image) error) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	source := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(source, source.Bounds(), &image.Uniform{C: fill}, image.Point{}, draw.Src)
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = encode(file, source); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeThumbnailTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func decodeThumbnailDataURL(t *testing.T, value, mimeType, wantFormat string) image.Image {
	t.Helper()
	prefix := "data:" + mimeType + ";base64,"
	if !strings.HasPrefix(value, prefix) {
		t.Fatalf("expected %q data URL, got %q", mimeType, value)
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, prefix))
	if err != nil {
		t.Fatal(err)
	}
	decoded, format, err := image.Decode(bytes.NewReader(data))
	if err != nil || format != wantFormat {
		t.Fatalf("expected valid %s output, got %s, %v", wantFormat, format, err)
	}
	return decoded
}

func assertDominantColor(t *testing.T, source image.Image, want string) {
	t.Helper()
	red, green, blue, _ := source.At(source.Bounds().Min.X, source.Bounds().Min.Y).RGBA()
	values := map[string]uint32{"red": red, "green": green, "blue": blue}
	if values[want] == 0 || values[want] < red || values[want] < green || values[want] < blue {
		t.Fatalf("expected dominant %s, got r=%d g=%d b=%d", want, red, green, blue)
	}
}
