package core

import "errors"

func (a *App) FocusApp(id string) error {
	status, err := a.GetAppStatus(id)
	if err != nil {
		return err
	}
	if status.App.Kind != "desktop" {
		return errors.New("only desktop Apps have a native window")
	}
	if !status.ProcessAlive || status.PID < 1 {
		return errors.New("the desktop App is not running")
	}
	return focusProcessWindow(status.PID)
}
