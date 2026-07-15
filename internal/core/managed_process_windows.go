//go:build windows

package core

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

type windowsManagedProcess struct {
	command    *exec.Cmd
	job        *windowsTerminalJob
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
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    spec.HideWindow,
	}
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

	job, err := attachTerminalProcess(command.Process)
	if err != nil {
		cleanupErr := terminateUnmanagedTerminalProcess(command.Process)
		_ = command.Wait()
		<-outputDone
		return nil, errors.Join(fmt.Errorf("could not isolate the App's process: %w", err), cleanupErr)
	}
	if err = releaseTerminalProcess(command.Process); err != nil {
		cleanupErr := job.close()
		if cleanupErr != nil {
			cleanupErr = errors.Join(cleanupErr, terminateUnmanagedTerminalProcess(command.Process))
		}
		_ = command.Wait()
		<-outputDone
		return nil, errors.Join(fmt.Errorf("could not resume the App's process: %w", err), cleanupErr)
	}
	return &windowsManagedProcess{command: command, job: job, outputDone: outputDone}, nil
}

func (process *windowsManagedProcess) PID() int { return process.command.Process.Pid }

func (process *windowsManagedProcess) Wait() (int, error) {
	err := process.command.Wait()
	err = errors.Join(err, process.job.close())
	<-process.outputDone
	if process.command.ProcessState == nil {
		return -1, err
	}
	return process.command.ProcessState.ExitCode(), err
}

func (process *windowsManagedProcess) Stop() error {
	if process == nil || process.job == nil {
		return nil
	}
	if err := process.job.close(); err != nil {
		fallback := terminateUnmanagedTerminalProcess(process.command.Process)
		return errors.Join(fmt.Errorf("the App's Job Object failed to close: %w", err), fallback)
	}
	return nil
}
