//go:build windows

package core

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type terminalManagedProcessBackend interface {
	managedProcessIDs() ([]int, error)
}

type terminalJobProcessList struct {
	Assigned uint32
	Count    uint32
	IDs      [terminalJobActiveProcessLimit]uintptr
}

func (backend *windowsTerminalBackend) managedProcessIDs() ([]int, error) {
	return managedJobProcessIDs(backend.job)
}

func managedJobProcessIDs(job *windowsTerminalJob) ([]int, error) {
	if job == nil || job.handle == 0 {
		return nil, errors.New("the terminal does not have an active Job Object")
	}
	var list terminalJobProcessList
	if err := windows.QueryInformationJobObject(job.handle, windows.JobObjectBasicProcessIdList,
		uintptr(unsafe.Pointer(&list)), uint32(unsafe.Sizeof(list)), nil); err != nil {
		return nil, fmt.Errorf("could not query the managed process tree: %w", err)
	}
	count := int(list.Count)
	if count > len(list.IDs) {
		count = len(list.IDs)
	}
	result := make([]int, 0, count)
	for _, id := range list.IDs[:count] {
		if id > 0 {
			result = append(result, int(id))
		}
	}
	return result, nil
}

func platformManagedTerminalPorts(session *terminalSession) ([]DetectedTerminalEndpoint, error) {
	backend, ok := session.backend.(terminalManagedProcessBackend)
	if !ok {
		return nil, errors.New("the terminal backend does not expose its managed process tree")
	}
	pids, err := backend.managedProcessIDs()
	if err != nil {
		return nil, err
	}
	return windowsManagedPorts(pids)
}

func windowsManagedPorts(pids []int) ([]DetectedTerminalEndpoint, error) {
	allowed := make(map[int]bool, len(pids))
	for _, pid := range pids {
		allowed[pid] = true
	}
	command := exec.Command("netstat.exe", "-ano", "-p", "tcp")
	hideWindow(command)
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("could not query TCP ports: %w", err)
	}
	seen := map[int]bool{}
	result := make([]DetectedTerminalEndpoint, 0)
	for _, line := range bytes.Split(output, []byte{'\n'}) {
		fields := strings.Fields(string(line))
		if len(fields) < 5 || !strings.EqualFold(fields[0], "TCP") {
			continue
		}
		pid, parseErr := strconv.Atoi(fields[len(fields)-1])
		state := strings.ToUpper(fields[len(fields)-2])
		if parseErr != nil || !allowed[pid] || (state != "LISTENING" && state != "ESCUCHANDO") {
			continue
		}
		_, rawPort, found := strings.Cut(fields[1], ":")
		if strings.HasPrefix(fields[1], "[") {
			_, rawPort, found = strings.Cut(fields[1], "]:")
		}
		port, parseErr := strconv.Atoi(rawPort)
		if !found || parseErr != nil || port < 1 || port > 65535 || seen[port] {
			continue
		}
		seen[port] = true
		result = append(result, DetectedTerminalEndpoint{PID: pid, Port: port, URL: fmt.Sprintf("http://127.0.0.1:%d", port), Managed: true})
	}
	return result, nil
}

func platformManagedAppOwnsPort(process managedProcess, port int) (bool, bool) {
	managed, ok := process.(*windowsManagedProcess)
	if !ok || managed.job == nil {
		return false, false
	}
	pids, err := managedJobProcessIDs(managed.job)
	if err != nil {
		return true, false
	}
	ports, err := windowsManagedPorts(pids)
	if err != nil {
		return true, false
	}
	for _, endpoint := range ports {
		if endpoint.Port == port {
			return true, true
		}
	}
	return true, false
}

func platformManagedAppDetectedPort(process managedProcess) (bool, int) {
	managed, ok := process.(*windowsManagedProcess)
	if !ok || managed.job == nil {
		return false, 0
	}
	pids, err := managedJobProcessIDs(managed.job)
	if err != nil {
		return true, 0
	}
	ports, err := windowsManagedPorts(pids)
	if err != nil || len(ports) == 0 {
		return true, 0
	}
	port := ports[0].Port
	for _, endpoint := range ports[1:] {
		if endpoint.Port < port {
			port = endpoint.Port
		}
	}
	return true, port
}
