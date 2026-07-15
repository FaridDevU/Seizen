package main

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxThumbnailSize      = 3 * 1024 * 1024
	maxThumbnailDimension = 8192
	maxThumbnailPixels    = 24_000_000
	thumbnailWidth        = 160
	thumbnailHeight       = 112
)

var (
	thumbnailNames = []string{"thumbnail", "preview", "cover", "screenshot", "logo"}
	thumbnailMIMEs = map[string]string{
		".png":  "image/png",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
	}
)

type thumbnailCandidate struct {
	path      string
	name      string
	directory int
	rank      int
}

func (a *App) GetProjectThumbnail(id, path string) (string, error) {
	ctx := a.context()
	db, err := a.database.Pool(ctx)
	if err != nil {
		return "", err
	}
	var storedPath string
	err = db.QueryRowContext(ctx, `SELECT path FROM projects WHERE id = ?`, id).Scan(&storedPath)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("the project was not found")
	}
	if err != nil {
		return "", err
	}
	if !sameRequestedPath(storedPath, path) {
		return "", errors.New("the given path does not match the project's saved path")
	}

	root, ok := safeThumbnailRoot(storedPath)
	if !ok {
		return "", nil
	}
	for _, candidate := range findThumbnailCandidates(root) {
		if dataURL, ok := readThumbnail(root, candidate.path); ok {
			return dataURL, nil
		}
	}
	return "", nil
}

func findThumbnailCandidates(root string) []thumbnailCandidate {
	directories := []struct {
		path     string
		fallback bool
	}{
		{filepath.Join(root, ".seizen"), false},
		{root, true},
		{filepath.Join(root, "public"), true},
		{filepath.Join(root, "assets"), true},
		{filepath.Join(root, "src", "assets"), true},
	}
	candidates := make([]thumbnailCandidate, 0)
	for directoryIndex, directory := range directories {
		if !safeThumbnailDirectory(root, directory.path) {
			continue
		}
		entries, err := os.ReadDir(directory.path)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			extension := strings.ToLower(filepath.Ext(entry.Name()))
			if _, ok := thumbnailMIMEs[extension]; !ok {
				continue
			}
			base := strings.ToLower(strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())))
			rank := thumbnailNameRank(base)
			if !directory.fallback && rank != 0 {
				continue
			}
			if directoryIndex == 0 {
				rank = -1
			}
			candidates = append(candidates, thumbnailCandidate{
				path:      filepath.Join(directory.path, entry.Name()),
				name:      strings.ToLower(entry.Name()),
				directory: directoryIndex,
				rank:      rank,
			})
		}
	}
	sort.Slice(candidates, func(left, right int) bool {
		first, second := candidates[left], candidates[right]
		if first.rank != second.rank {
			return first.rank < second.rank
		}
		if first.rank >= len(thumbnailNames) && first.name != second.name {
			return first.name < second.name
		}
		if first.directory != second.directory {
			return first.directory < second.directory
		}
		return first.name < second.name
	})
	return candidates
}

func thumbnailNameRank(name string) int {
	for index, preferred := range thumbnailNames {
		if name == preferred {
			return index
		}
	}
	return len(thumbnailNames)
}

func safeThumbnailRoot(path string) (string, bool) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", false
	}
	root, err := canonicalPath(absolute)
	if err != nil || !samePath(displayPath(absolute), displayPath(root)) {
		return "", false
	}
	return root, true
}

func safeThumbnailDirectory(root, path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	resolved, err := canonicalPath(path)
	return err == nil && samePath(displayPath(path), displayPath(resolved)) && pathInside(root, resolved)
}

func readThumbnail(root, path string) (string, bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() <= 0 || info.Size() > maxThumbnailSize {
		return "", false
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	resolved, err := canonicalPath(absolute)
	if err != nil || !samePath(displayPath(absolute), displayPath(resolved)) || !pathInside(root, resolved) {
		return "", false
	}

	file, err := os.Open(resolved)
	if err != nil {
		return "", false
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || openedInfo.Size() <= 0 ||
		openedInfo.Size() > maxThumbnailSize || !os.SameFile(info, openedInfo) {
		return "", false
	}
	extension := strings.ToLower(filepath.Ext(resolved))
	expectedFormat := "png"
	if extension == ".jpg" || extension == ".jpeg" {
		expectedFormat = "jpeg"
	}
	config, format, err := image.DecodeConfig(io.LimitReader(file, maxThumbnailSize+1))
	if err != nil || format != expectedFormat || !validThumbnailDimensions(config.Width, config.Height) {
		return "", false
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return "", false
	}
	decoded, decodedFormat, err := image.Decode(io.LimitReader(file, maxThumbnailSize+1))
	if err != nil || decodedFormat != expectedFormat {
		return "", false
	}
	bounds := decoded.Bounds()
	if !validThumbnailDimensions(bounds.Dx(), bounds.Dy()) {
		return "", false
	}

	resized := resizeThumbnail(decoded, thumbnailWidth, thumbnailHeight)
	var output bytes.Buffer
	if expectedFormat == "jpeg" {
		err = jpeg.Encode(&output, resized, &jpeg.Options{Quality: 82})
	} else {
		err = (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&output, resized)
	}
	if err != nil || output.Len() == 0 {
		return "", false
	}
	mimeType := thumbnailMIMEs[extension]
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(output.Bytes()), true
}

func validThumbnailDimensions(width, height int) bool {
	return width > 0 && height > 0 && width <= maxThumbnailDimension && height <= maxThumbnailDimension &&
		int64(width)*int64(height) <= maxThumbnailPixels
}

func resizeThumbnail(source image.Image, maxWidth, maxHeight int) image.Image {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width > maxWidth {
		height = max(1, height*maxWidth/width)
		width = maxWidth
	}
	if height > maxHeight {
		width = max(1, width*maxHeight/height)
		height = maxHeight
	}
	if width == bounds.Dx() && height == bounds.Dy() {
		return source
	}

	result := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		sourceY := bounds.Min.Y + y*bounds.Dy()/height
		for x := 0; x < width; x++ {
			sourceX := bounds.Min.X + x*bounds.Dx()/width
			result.Set(x, y, source.At(sourceX, sourceY))
		}
	}
	return result
}

func pathInside(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !filepath.IsAbs(relative) &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
