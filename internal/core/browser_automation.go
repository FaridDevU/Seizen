package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultAutomationTimeout = 20 * time.Second
	playwrightProbeTimeout   = 2 * time.Second
)

// BrowserAutomationProvider keeps browser automation optional. Every operation
// returns a structured result so missing Playwright never blocks App execution.
type BrowserAutomationProvider interface {
	Status() BrowserAutomationStatus
	OpenPreview(context.Context, string) BrowserAutomationResult
	WaitForHealthcheck(context.Context, string, time.Duration) BrowserAutomationResult
	RunSmokeTest(context.Context, string) BrowserAutomationResult
	CaptureScreenshot(context.Context, string, string) BrowserAutomationResult
	GetConsoleErrors(context.Context, string) BrowserAutomationResult
	TestRoute(context.Context, string, string) BrowserAutomationResult
}

type BrowserAutomationStatus struct {
	Provider        string `json:"provider"`
	Available       bool   `json:"available"`
	BrowserFeatures bool   `json:"browserFeatures"`
	Message         string `json:"message"`
}

type BrowserAutomationResult struct {
	Operation      string   `json:"operation"`
	Provider       string   `json:"provider"`
	Available      bool     `json:"available"`
	Success        bool     `json:"success"`
	URL            string   `json:"url,omitempty"`
	StatusCode     int      `json:"statusCode,omitempty"`
	DurationMS     int64    `json:"durationMs"`
	ScreenshotPath string   `json:"screenshotPath,omitempty"`
	ConsoleErrors  []string `json:"consoleErrors,omitempty"`
	Message        string   `json:"message,omitempty"`
	ErrorMessage   string   `json:"errorMessage,omitempty"`
}

type httpBrowserAutomationProvider struct {
	client  *http.Client
	message string
}

type playwrightBrowserAutomationProvider struct {
	http             *httpBrowserAutomationProvider
	node             string
	workingDirectory string
	timeout          time.Duration
}

// NewBrowserAutomationProvider uses Playwright only when the current App
// already has both Node and the playwright module. It never invokes npx or an
// installer; otherwise HTTP health/smoke checks remain available.
func NewBrowserAutomationProvider(workingDirectory string) BrowserAutomationProvider {
	httpProvider := newHTTPBrowserAutomationProvider()
	node, err := exec.LookPath("node")
	if err != nil {
		return httpProvider
	}

	ctx, cancel := context.WithTimeout(context.Background(), playwrightProbeTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", `require.resolve("playwright")`)
	if strings.TrimSpace(workingDirectory) != "" {
		command.Dir = workingDirectory
	}
	hideWindow(command)
	if err := command.Run(); err != nil {
		return httpProvider
	}
	return &playwrightBrowserAutomationProvider{
		http:             httpProvider,
		node:             node,
		workingDirectory: workingDirectory,
		timeout:          defaultAutomationTimeout,
	}
}

func newHTTPBrowserAutomationProvider() *httpBrowserAutomationProvider {
	client := &http.Client{Timeout: 10 * time.Second}
	client.CheckRedirect = func(request *http.Request, _ []*http.Request) error {
		return validateAutomationURL(request.URL.String())
	}
	return &httpBrowserAutomationProvider{
		client:  client,
		message: "Playwright is not installed in the App; HTTP healthchecks and smoke tests are still available.",
	}
}

func (provider *httpBrowserAutomationProvider) Status() BrowserAutomationStatus {
	return BrowserAutomationStatus{Provider: "http", Available: true, Message: provider.message}
}

func (provider *httpBrowserAutomationProvider) OpenPreview(ctx context.Context, rawURL string) BrowserAutomationResult {
	return provider.request(ctx, "open_preview", rawURL)
}

func (provider *httpBrowserAutomationProvider) RunSmokeTest(ctx context.Context, rawURL string) BrowserAutomationResult {
	return provider.request(ctx, "smoke_test", rawURL)
}

func (provider *httpBrowserAutomationProvider) WaitForHealthcheck(ctx context.Context, rawURL string, timeout time.Duration) BrowserAutomationResult {
	started := time.Now()
	if err := validateAutomationURL(rawURL); err != nil {
		return failedAutomationResult("wait_healthcheck", "http", rawURL, started, err)
	}
	if timeout <= 0 {
		timeout = defaultAutomationTimeout
	}
	waitContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var last BrowserAutomationResult
	for {
		last = provider.request(waitContext, "wait_healthcheck", rawURL)
		if last.Success {
			last.DurationMS = time.Since(started).Milliseconds()
			return last
		}
		select {
		case <-waitContext.Done():
			last.DurationMS = time.Since(started).Milliseconds()
			last.ErrorMessage = fmt.Sprintf("healthcheck not available: %v", waitContext.Err())
			return last
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (provider *httpBrowserAutomationProvider) CaptureScreenshot(_ context.Context, rawURL, _ string) BrowserAutomationResult {
	return unavailableBrowserResult("capture_screenshot", rawURL, provider.message)
}

func (provider *httpBrowserAutomationProvider) GetConsoleErrors(_ context.Context, rawURL string) BrowserAutomationResult {
	return unavailableBrowserResult("console_errors", rawURL, provider.message)
}

func (provider *httpBrowserAutomationProvider) TestRoute(ctx context.Context, baseURL, route string) BrowserAutomationResult {
	resolved, err := resolveAutomationRoute(baseURL, route)
	if err != nil {
		return failedAutomationResult("test_route", "http", baseURL, time.Now(), err)
	}
	return provider.request(ctx, "test_route", resolved)
}

func (provider *httpBrowserAutomationProvider) request(ctx context.Context, operation, rawURL string) BrowserAutomationResult {
	started := time.Now()
	if err := validateAutomationURL(rawURL); err != nil {
		return failedAutomationResult(operation, "http", rawURL, started, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return failedAutomationResult(operation, "http", rawURL, started, err)
	}
	response, err := provider.client.Do(request)
	if err != nil {
		return failedAutomationResult(operation, "http", rawURL, started, err)
	}
	response.Body.Close()
	success := response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusBadRequest
	result := BrowserAutomationResult{
		Operation:  operation,
		Provider:   "http",
		Available:  true,
		Success:    success,
		URL:        response.Request.URL.String(),
		StatusCode: response.StatusCode,
		DurationMS: time.Since(started).Milliseconds(),
	}
	if !success {
		result.ErrorMessage = fmt.Sprintf("HTTP response %d", response.StatusCode)
	}
	return result
}

func (provider *playwrightBrowserAutomationProvider) Status() BrowserAutomationStatus {
	return BrowserAutomationStatus{
		Provider: "playwright", Available: true, BrowserFeatures: true,
		Message: "Playwright available in the App.",
	}
}

func (provider *playwrightBrowserAutomationProvider) OpenPreview(ctx context.Context, rawURL string) BrowserAutomationResult {
	return provider.run(ctx, "open_preview", rawURL, "")
}

func (provider *playwrightBrowserAutomationProvider) WaitForHealthcheck(ctx context.Context, rawURL string, timeout time.Duration) BrowserAutomationResult {
	return provider.http.WaitForHealthcheck(ctx, rawURL, timeout)
}

func (provider *playwrightBrowserAutomationProvider) RunSmokeTest(ctx context.Context, rawURL string) BrowserAutomationResult {
	return provider.run(ctx, "smoke_test", rawURL, "")
}

func (provider *playwrightBrowserAutomationProvider) CaptureScreenshot(ctx context.Context, rawURL, outputPath string) BrowserAutomationResult {
	if strings.TrimSpace(outputPath) == "" || !filepath.IsAbs(outputPath) {
		return failedAutomationResult("capture_screenshot", "playwright", rawURL, time.Now(), errors.New("the screenshot needs an absolute path"))
	}
	return provider.run(ctx, "capture_screenshot", rawURL, filepath.Clean(outputPath))
}

func (provider *playwrightBrowserAutomationProvider) GetConsoleErrors(ctx context.Context, rawURL string) BrowserAutomationResult {
	return provider.run(ctx, "console_errors", rawURL, "")
}

func (provider *playwrightBrowserAutomationProvider) TestRoute(ctx context.Context, baseURL, route string) BrowserAutomationResult {
	resolved, err := resolveAutomationRoute(baseURL, route)
	if err != nil {
		return failedAutomationResult("test_route", "playwright", baseURL, time.Now(), err)
	}
	return provider.run(ctx, "test_route", resolved, "")
}

func (provider *playwrightBrowserAutomationProvider) run(ctx context.Context, operation, rawURL, outputPath string) BrowserAutomationResult {
	started := time.Now()
	if err := validateAutomationURL(rawURL); err != nil {
		return failedAutomationResult(operation, "playwright", rawURL, started, err)
	}
	commandContext, cancel := context.WithTimeout(ctx, provider.timeout)
	defer cancel()
	var output bytes.Buffer
	process, err := startPlatformManagedProcess(managedProcessSpec{
		Path: provider.node,
		Args: []string{"-e", playwrightScript, operation, rawURL, outputPath},
		Dir:  provider.workingDirectory,
		Env:  os.Environ(), HideWindow: true,
	}, &output)
	if err != nil {
		return failedAutomationResult(operation, "playwright", rawURL, started, err)
	}
	type processResult struct {
		exitCode int
		err      error
	}
	done := make(chan processResult, 1)
	go func() {
		exitCode, waitErr := process.Wait()
		done <- processResult{exitCode: exitCode, err: waitErr}
	}()
	var processOutcome processResult
	select {
	case processOutcome = <-done:
	case <-commandContext.Done():
		_ = process.Stop()
		processOutcome = <-done
		processOutcome.err = commandContext.Err()
	}
	if processOutcome.err != nil || processOutcome.exitCode != 0 {
		message := strings.TrimSpace(output.String())
		if message == "" {
			message = fmt.Sprintf("Playwright finished with code %d: %v", processOutcome.exitCode, processOutcome.err)
		}
		return failedAutomationResult(operation, "playwright", rawURL, started, errors.New(message))
	}
	var result BrowserAutomationResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		return failedAutomationResult(operation, "playwright", rawURL, started, fmt.Errorf("invalid response from Playwright: %w", err))
	}
	result.Operation = operation
	result.Provider = "playwright"
	result.Available = true
	result.DurationMS = time.Since(started).Milliseconds()
	if err := validateAutomationURL(result.URL); err != nil {
		return failedAutomationResult(operation, "playwright", rawURL, started, fmt.Errorf("Playwright navigated to a URL that is not allowed: %w", err))
	}
	return result
}

func validateAutomationURL(rawURL string) error {
	if strings.TrimSpace(rawURL) == "" {
		return errors.New("the URL is required")
	}
	return validateLocalAppURL(rawURL)
}

func resolveAutomationRoute(baseURL, route string) (string, error) {
	if err := validateAutomationURL(baseURL); err != nil {
		return "", err
	}
	base, _ := url.Parse(baseURL)
	reference, err := url.Parse(strings.TrimSpace(route))
	if err != nil || reference.IsAbs() || reference.Host != "" {
		return "", errors.New("the route must belong to the preview")
	}
	resolved := base.ResolveReference(reference).String()
	return resolved, validateAutomationURL(resolved)
}

func failedAutomationResult(operation, provider, rawURL string, started time.Time, err error) BrowserAutomationResult {
	return BrowserAutomationResult{
		Operation: operation, Provider: provider, Available: true, URL: rawURL,
		DurationMS: time.Since(started).Milliseconds(), ErrorMessage: err.Error(),
	}
}

func unavailableBrowserResult(operation, rawURL, message string) BrowserAutomationResult {
	return BrowserAutomationResult{
		Operation: operation, Provider: "http", URL: rawURL,
		Message: message, ErrorMessage: "this operation requires Playwright",
	}
}

const playwrightScript = `
const { chromium } = require("playwright");
const [operation, target, output] = process.argv.slice(1);
(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage();
    const consoleErrors = [];
    page.on("console", message => { if (message.type() === "error") consoleErrors.push(message.text()); });
    page.on("pageerror", error => consoleErrors.push(error.message));
    await page.route("**/*", route => {
      const requestURL = new URL(route.request().url());
      const hostname = requestURL.hostname.toLowerCase().replace(/\.$/, "");
      const safe = (requestURL.protocol === "http:" || requestURL.protocol === "https:") &&
        !requestURL.username && !requestURL.password &&
        hostname !== "wails.localhost" && !hostname.endsWith(".wails.localhost");
      if (!safe) return route.abort();
      return route.continue();
    });
    const response = await page.goto(target, { waitUntil: "domcontentloaded", timeout: 15000 });
    if (operation === "capture_screenshot") await page.screenshot({ path: output, fullPage: true });
    const statusCode = response ? response.status() : 0;
    const success = statusCode >= 200 && statusCode < 400 && (operation !== "smoke_test" || consoleErrors.length === 0);
    process.stdout.write(JSON.stringify({
      success, url: page.url(), statusCode, consoleErrors,
      screenshotPath: operation === "capture_screenshot" ? output : "",
      errorMessage: success ? "" : (consoleErrors[0] || "HTTP response " + statusCode)
    }));
  } finally {
    await browser.close();
  }
})().catch(error => { process.stderr.write(error.stack || error.message); process.exit(1); });
`
