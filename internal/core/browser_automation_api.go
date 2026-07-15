package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func (a *App) GetBrowserAutomationStatus(appID string) (BrowserAutomationStatus, error) {
	app, err := a.browserAutomationApp(appID)
	if err != nil {
		return BrowserAutomationStatus{}, err
	}
	return NewBrowserAutomationProvider(app.WorkingDirectory).Status(), nil
}

func (a *App) SmokeTestApp(appID string) (BrowserAutomationResult, error) {
	app, err := a.browserAutomationApp(appID)
	if err != nil {
		return BrowserAutomationResult{}, err
	}
	return NewBrowserAutomationProvider(app.WorkingDirectory).RunSmokeTest(a.context(), app.PreviewURL), nil
}

func (a *App) CaptureAppPreview(appID string) (BrowserAutomationResult, error) {
	app, err := a.browserAutomationApp(appID)
	if err != nil {
		return BrowserAutomationResult{}, err
	}
	databasePath, err := a.database.databasePath()
	if err != nil {
		return BrowserAutomationResult{}, err
	}
	directory := filepath.Join(filepath.Dir(databasePath), "screenshots", app.ID)
	if err = os.MkdirAll(directory, 0o700); err != nil {
		return BrowserAutomationResult{}, fmt.Errorf("could not prepare the screenshots folder: %w", err)
	}
	name, err := newUUID()
	if err != nil {
		return BrowserAutomationResult{}, err
	}
	result := NewBrowserAutomationProvider(app.WorkingDirectory).CaptureScreenshot(
		a.context(), app.PreviewURL, filepath.Join(directory, name+".png"),
	)
	return result, nil
}

func (a *App) GetAppConsoleErrors(appID string) (BrowserAutomationResult, error) {
	app, err := a.browserAutomationApp(appID)
	if err != nil {
		return BrowserAutomationResult{}, err
	}
	return NewBrowserAutomationProvider(app.WorkingDirectory).GetConsoleErrors(a.context(), app.PreviewURL), nil
}

func (a *App) TestAppRoute(appID, route string) (BrowserAutomationResult, error) {
	app, err := a.browserAutomationApp(appID)
	if err != nil {
		return BrowserAutomationResult{}, err
	}
	return NewBrowserAutomationProvider(app.WorkingDirectory).TestRoute(a.context(), app.PreviewURL, route), nil
}

func (a *App) WaitForAppHealthcheck(appID string, timeoutMS int) (BrowserAutomationResult, error) {
	app, err := a.browserAutomationApp(appID)
	if err != nil {
		return BrowserAutomationResult{}, err
	}
	target := app.HealthcheckURL
	if target == "" {
		target = app.PreviewURL
	}
	timeout := time.Duration(timeoutMS) * time.Millisecond
	if timeoutMS < 1 || timeout > time.Minute {
		timeout = 20 * time.Second
	}
	return NewBrowserAutomationProvider(app.WorkingDirectory).WaitForHealthcheck(a.context(), target, timeout), nil
}

func (a *App) browserAutomationApp(appID string) (ProjectApp, error) {
	db, err := a.database.Pool(a.context())
	if err != nil {
		return ProjectApp{}, err
	}
	app, err := getProjectApp(context.Background(), db, appID)
	if err != nil {
		return ProjectApp{}, err
	}
	if app.Kind != "web" {
		return ProjectApp{}, errors.New("browser automation is only available for web Apps")
	}
	if app.PreviewURL == "" {
		return ProjectApp{}, errors.New("the App does not have a preview URL yet")
	}
	if err = validateAutomationURL(app.PreviewURL); err != nil {
		return ProjectApp{}, err
	}
	return app, nil
}
