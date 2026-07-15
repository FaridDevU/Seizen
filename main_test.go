package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestWebviewDataPersists(t *testing.T) {
	base := t.TempDir()
	databasePath := filepath.Join(base, "seizen.db")
	path, err := ensureWebviewData(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(path, "login-cookie")
	if err = os.WriteFile(marker, []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = ensureWebviewData(databasePath); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(marker); err != nil {
		t.Fatalf("the browser profile was not kept: %v", err)
	}
}

func TestDenyFraming(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	denyFraming(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(recorder, request)

	if got := recorder.Header().Get("Content-Security-Policy"); got != "frame-ancestors 'none'" {
		t.Fatalf("Content-Security-Policy = %q", got)
	}
	if got := recorder.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q", got)
	}
}

func TestCloseHandshake(t *testing.T) {
	base := t.TempDir()
	app := NewApp()
	app.database = newDatabase(filepath.Join(base, "seizen.db"), filepath.Join(base, "projects"))
	t.Cleanup(app.database.Close)
	ctx := context.Background()
	app.startup(ctx)

	var events atomic.Int32
	var quits atomic.Int32
	app.emitEvent = func(_ context.Context, name string, _ ...interface{}) {
		if name != beforeCloseEvent {
			t.Errorf("event = %q", name)
		}
		events.Add(1)
	}
	app.quit = func(got context.Context) {
		if got != ctx {
			t.Error("quit received the wrong context")
		}
		quits.Add(1)
	}

	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if !app.beforeClose(ctx) {
				t.Error("first close attempt was not prevented")
			}
		}()
	}
	wait.Wait()
	if got := events.Load(); got != 1 {
		t.Fatalf("close events = %d, want 1", got)
	}

	app.mu.RLock()
	attempt := app.closeAttempt
	app.mu.RUnlock()
	app.expireCloseAttempt(attempt)
	if !app.beforeClose(ctx) {
		t.Fatal("expired close attempt was not retried")
	}
	if got := events.Load(); got != 2 {
		t.Fatalf("close events after expiry = %d, want 2", got)
	}

	app.CancelClose()
	if !app.beforeClose(ctx) {
		t.Fatal("retried close attempt was not prevented")
	}
	if got := events.Load(); got != 3 {
		t.Fatalf("close events after retry = %d, want 3", got)
	}

	app.ConfirmClose()
	app.ConfirmClose()
	if got := quits.Load(); got != 1 {
		t.Fatalf("quit calls = %d, want 1", got)
	}
	if app.beforeClose(ctx) {
		t.Fatal("confirmed close was prevented")
	}
}
