package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedVSCodeInstallsVerifiedPortableArchive(t *testing.T) {
	archive := vscodeTestArchive(t, map[string]string{
		"Code.exe": "binary", "bin/code-tunnel.exe": "server", "resources/app/LICENSE.txt": "license",
	})
	installer := vscodeTestInstaller(t, archive, fmt.Sprintf("%x", sha256.Sum256(archive)))
	if err := installer.install(context.Background()); err != nil {
		t.Fatal(err)
	}
	available, status, message := installer.status()
	if !available || status != "installed" || message != "" {
		t.Fatalf("unexpected status: %v %q %q", available, status, message)
	}
	for _, path := range []string{"current/Code.exe", "current/bin/code-tunnel.exe", "current/data/tmp", "current/resources/app/LICENSE.txt"} {
		if _, err := os.Stat(filepath.Join(installer.root, filepath.FromSlash(path))); err != nil {
			t.Fatalf("missing %s: %v", path, err)
		}
	}
}

func TestManagedVSCodeRejectsBadHashAndZipTraversal(t *testing.T) {
	valid := vscodeTestArchive(t, map[string]string{"Code.exe": "binary", "bin/code-tunnel.exe": "server"})
	installer := vscodeTestInstaller(t, valid, strings.Repeat("0", 64))
	if err := installer.install(context.Background()); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("expected hash rejection, got %v", err)
	}

	malicious := vscodeTestArchive(t, map[string]string{"Code.exe": "binary", "bin/code-tunnel.exe": "server", "../outside.txt": "escape"})
	installer = vscodeTestInstaller(t, malicious, fmt.Sprintf("%x", sha256.Sum256(malicious)))
	if err := installer.install(context.Background()); err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("expected traversal rejection, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(installer.root), "outside.txt")); !os.IsNotExist(err) {
		t.Fatalf("archive escaped the managed directory: %v", err)
	}
}

func vscodeTestInstaller(t *testing.T, archive []byte, hash string) *managedVSCodeInstaller {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/latest":
			response.Header().Set("Location", server.URL+"/archive")
			response.Header().Set("X-SHA256", hash)
			response.WriteHeader(http.StatusFound)
		case "/archive":
			response.Header().Set("Content-Length", fmt.Sprint(len(archive)))
			_, _ = response.Write(archive)
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return &managedVSCodeInstaller{
		root:         filepath.Join(t.TempDir(), "vscode"),
		sourceURL:    server.URL + "/latest",
		downloadHost: serverURL.Hostname(),
		client:       server.Client(),
		allowHTTP:    true,
	}
}

func vscodeTestArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, contents := range files {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = file.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
