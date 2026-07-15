//go:build !windows

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
)

type unixManagedProcess struct {
	command    *exec.Cmd
	outputDone <-chan struct{}
}

func startPlatformManagedProcess(spec managedProcessSpec, output io.Writer) (managedProcess, error) {
	if output == nil {
		output = io.Discard
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("could not prepare the App's output: %w", err)
	}
	command := exec.Command(spec.Path, spec.Args...)
	command.Dir = spec.Dir
	command.Env = spec.Env
	command.Stdout = writer
	command.Stderr = writer
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return nil, fmt.Errorf("could not start the App's process: %w", err)
	}
	_ = writer.Close()
	outputDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(output, reader)
		_ = reader.Close()
		close(outputDone)
	}()
	return &unixManagedProcess{command: command, outputDone: outputDone}, nil
}

func (process *unixManagedProcess) PID() int { return process.command.Process.Pid }

func (process *unixManagedProcess) Wait() (int, error) {
	err := process.command.Wait()
	groupErr := syscall.Kill(-process.command.Process.Pid, syscall.SIGKILL)
	if !errors.Is(groupErr, syscall.ESRCH) {
		err = errors.Join(err, groupErr)
	}
	<-process.outputDone
	if process.command.ProcessState == nil {
		return -1, err
	}
	return process.command.ProcessState.ExitCode(), err
}

func (process *unixManagedProcess) Stop() error {
	if process == nil || process.command.Process == nil {
		return nil
	}
	err := syscall.Kill(-process.command.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
