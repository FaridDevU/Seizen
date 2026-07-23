package core

import (
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifyWorkspaceAsset(t *testing.T) {
	cases := []struct {
		mime, name, want string
	}{
		{"application/pdf", "contract.pdf", "pdf"},
		{"text/plain; charset=utf-8", "contract.pdf", "pdf"},
		{"application/zip", "report.docx", "docx"},
		{"application/zip", "archive.zip", ""},
		{"image/png", "photo.png", "image"},
		{"video/mp4", "clip.mp4", "video"},
		{"audio/mpeg", "song.mp3", "audio"},
		{"text/plain; charset=utf-8", "readme.md", "text"},
		{"application/octet-stream", "notes.md", "text"},
		{"application/octet-stream", "tool.exe", ""},
	}
	for _, testCase := range cases {
		if got := classifyWorkspaceAsset(testCase.mime, testCase.name); got != testCase.want {
			t.Errorf("classifyWorkspaceAsset(%q, %q) = %q, want %q",
				testCase.mime, testCase.name, got, testCase.want)
		}
	}
}

func TestWorkspaceAssetImportServeAndDelete(t *testing.T) {
	app, db, root := deletionTestApp(t)
	project := deletionTestProject(t, db, filepath.Join(root, "Docs"), ProjectCreated)
	other := deletionTestProject(t, db, filepath.Join(root, "Other"), ProjectCreated)

	source := filepath.Join(t.TempDir(), "spec.pdf")
	content := "%PDF-1.4\n" + strings.Repeat("workspace asset body ", 100)
	if err := os.WriteFile(source, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	asset, err := app.ImportProjectWorkspaceAsset(project.ID, project.Path, source)
	if err != nil {
		t.Fatal(err)
	}
	if asset.Kind != "pdf" || asset.Name != "spec.pdf" || asset.AssetID == "" {
		t.Fatalf("unexpected asset: %#v", asset)
	}
	if _, err = app.ImportProjectWorkspaceAsset(other.ID, project.Path, source); err == nil {
		t.Fatal("expected a mismatched project path to be rejected")
	}

	handler := app.workspaceAssetHandler()
	serve := func(path string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest("GET", path, nil))
		return recorder
	}

	response := serve(workspaceAssetURLPrefix + project.ID + "/" + asset.AssetID)
	body, _ := io.ReadAll(response.Body)
	if response.Code != 200 || string(body) != content {
		t.Fatalf("expected the asset to stream back, got %d with %d bytes", response.Code, len(body))
	}
	if got := response.Header().Get("Content-Type"); got != "application/pdf" {
		t.Fatalf("expected application/pdf, got %q", got)
	}

	// Range requests keep video/PDF streaming cheap.
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", workspaceAssetURLPrefix+project.ID+"/"+asset.AssetID, nil)
	request.Header.Set("Range", "bytes=0-7")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != 206 || recorder.Body.String() != "%PDF-1.4" {
		t.Fatalf("expected a partial response, got %d %q", recorder.Code, recorder.Body.String())
	}

	if response = serve(workspaceAssetURLPrefix + other.ID + "/" + asset.AssetID); response.Code != 404 {
		t.Fatalf("expected another project's URL to miss, got %d", response.Code)
	}
	if response = serve(workspaceAssetURLPrefix + project.ID + "/../escape"); response.Code != 404 {
		t.Fatalf("expected traversal to be rejected, got %d", response.Code)
	}

	if err = app.DeleteProjectWorkspaceAsset(project.ID, project.Path, asset.AssetID); err != nil {
		t.Fatal(err)
	}
	if response = serve(workspaceAssetURLPrefix + project.ID + "/" + asset.AssetID); response.Code != 404 {
		t.Fatalf("expected the deleted asset to be gone, got %d", response.Code)
	}
}

func TestWorkspaceAssetRejectsUnsupportedAndIrregularFiles(t *testing.T) {
	app, db, root := deletionTestApp(t)
	project := deletionTestProject(t, db, filepath.Join(root, "Docs"), ProjectCreated)

	binary := filepath.Join(t.TempDir(), "tool.exe")
	if err := os.WriteFile(binary, []byte{0x4d, 0x5a, 0x90, 0x00, 0x03}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ImportProjectWorkspaceAsset(project.ID, project.Path, binary); err == nil {
		t.Fatal("expected an unsupported binary to be rejected")
	}
	if _, err := app.ImportProjectWorkspaceAsset(project.ID, project.Path, t.TempDir()); err == nil {
		t.Fatal("expected a directory to be rejected")
	}
}
