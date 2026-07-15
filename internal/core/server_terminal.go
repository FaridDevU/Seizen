package core

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

type serverRootTerminalProvider interface {
	RootTerminalCommand(Server) (string, []string, error)
}

func (a *App) StartServerTerminal(serverID string) (string, error) {
	ctx := a.context()
	db, err := a.database.Pool(ctx)
	if err != nil {
		return "", err
	}
	server, projectPath, err := serverProjectPath(ctx, db, serverID)
	if err != nil {
		return "", err
	}
	if server.Status != "running" && server.Status != "degraded" {
		return "", errors.New("start the server before opening its root terminal")
	}
	manager := a.projectServerManager()
	_, _, provider, err := manager.load(ctx, serverID)
	if err != nil {
		return "", err
	}
	rootProvider, ok := provider.(serverRootTerminalProvider)
	if !ok {
		return "", errors.New("this provider does not offer a root terminal")
	}
	path, arguments, err := rootProvider.RootTerminalCommand(server)
	if err != nil {
		return "", err
	}
	path, err = exec.LookPath(path)
	if err != nil {
		return "", errors.New("WSL is not installed or wsl.exe is not available")
	}
	projectPath, err = existingDirectory(projectPath)
	if err != nil {
		return "", err
	}
	sessionID, err := newUUID()
	if err != nil {
		return "", err
	}
	command := &terminalCommand{
		path: path,
		args: append([]string{filepath.Base(path)}, arguments...),
		dir:  projectPath,
		env:  os.Environ(),
	}
	if err = a.projectTerminalManager().startScoped(sessionID, server.ProjectID, server.ID, command); err != nil {
		return "", err
	}
	return sessionID, nil
}

func (a *App) WriteServerTerminal(sessionID, input string) error {
	return a.projectTerminalManager().write(sessionID, input)
}

func (a *App) ResizeServerTerminal(sessionID string, columns, rows int) error {
	return a.projectTerminalManager().resize(sessionID, columns, rows)
}

func (a *App) StopServerTerminal(sessionID string) error {
	return a.projectTerminalManager().stop(sessionID)
}
