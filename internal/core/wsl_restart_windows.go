//go:build windows

package core

import "golang.org/x/sys/windows/registry"

const (
	wslInstallStatusRegistryPath = `Software\Microsoft\Windows\CurrentVersion\Lxss\InstallStatus`
	windowsRebootRegistryPath    = `SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending`
)

func wslRestartRequired() (bool, string) {
	installStatus := ""
	if key, err := registry.OpenKey(registry.CURRENT_USER, wslInstallStatusRegistryPath, registry.QUERY_VALUE); err == nil {
		installStatus, _, _ = key.GetStringValue("")
		_ = key.Close()
	}
	restartPending := false
	if key, err := registry.OpenKey(registry.LOCAL_MACHINE, windowsRebootRegistryPath, registry.QUERY_VALUE); err == nil {
		restartPending = true
		_ = key.Close()
	}
	return wslRestartState(installStatus, restartPending)
}
