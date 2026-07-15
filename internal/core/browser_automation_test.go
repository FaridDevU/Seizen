package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPBrowserAutomationSmokeHealthcheckAndRoute(t *testing.T) {
	var healthAttempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/health":
			if healthAttempts.Add(1) < 2 {
				http.Error(response, "starting", http.StatusServiceUnavailable)
				return
			}
			response.WriteHeader(http.StatusNoContent)
		case "/route":
			response.WriteHeader(http.StatusOK)
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	provider := newHTTPBrowserAutomationProvider()
	if result := provider.WaitForHealthcheck(context.Background(), server.URL+"/health", time.Second); !result.Success || result.StatusCode != http.StatusNoContent {
		t.Fatalf("unexpected healthcheck: %+v", result)
	}
	if result := provider.TestRoute(context.Background(), server.URL, "/route"); !result.Success || result.URL != server.URL+"/route" {
		t.Fatalf("unexpected route test: %+v", result)
	}
}

func TestBrowserAutomationRejectsUnsafeURLsAndExternalRoutes(t *testing.T) {
	provider := newHTTPBrowserAutomationProvider()
	for _, rawURL := range []string{
		"ftp://localhost/app",
		"http://user:secret@localhost/app",
		"http://wails.localhost/app",
	} {
		if result := provider.OpenPreview(context.Background(), rawURL); result.Success || result.ErrorMessage == "" {
			t.Fatalf("unsafe URL was accepted %q: %+v", rawURL, result)
		}
	}
	if result := provider.TestRoute(context.Background(), "http://localhost:3000", "//example.com/escape"); result.Success || !strings.Contains(result.ErrorMessage, "preview") {
		t.Fatalf("escaping the origin was allowed: %+v", result)
	}
}

func TestHTTPBrowserAutomationClearlyReportsOptionalFeatures(t *testing.T) {
	provider := newHTTPBrowserAutomationProvider()
	status := provider.Status()
	if !status.Available || status.BrowserFeatures || !strings.Contains(status.Message, "Playwright") {
		t.Fatalf("unexpected status: %+v", status)
	}
	result := provider.CaptureScreenshot(context.Background(), "http://localhost:3000", `C:\temp\preview.png`)
	if result.Available || result.Success || !strings.Contains(result.ErrorMessage, "Playwright") {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestBrowserAutomationProviderInterface(t *testing.T) {
	var _ BrowserAutomationProvider = newHTTPBrowserAutomationProvider()
	var _ BrowserAutomationProvider = (*playwrightBrowserAutomationProvider)(nil)
}
