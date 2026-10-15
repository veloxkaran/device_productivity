//go:build packaging && windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
)

func installDirectory() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
	}
	return filepath.Join(base, "MyMonitor")
}

func ps(script string) { _ = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Run() }

// uninstall removes the app natively (no embedded payload): stops it, clears the
// startup + Add/Remove Programs registry entries, deletes the Start-menu
// shortcuts, and removes the install folder (app + data).
func uninstall() {
	_ = exec.Command("taskkill", "/F", "/IM", "my-monitor.exe").Run()
	ps(`Remove-ItemProperty -Path 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run' -Name 'MyMonitor' -ErrorAction SilentlyContinue`)
	ps(`Remove-Item -Path 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\MyMonitor' -Recurse -Force -ErrorAction SilentlyContinue`)
	if appData := os.Getenv("APPDATA"); appData != "" {
		progs := filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs")
		_ = os.Remove(filepath.Join(progs, "My Monitor.lnk"))
		_ = os.Remove(filepath.Join(progs, "Uninstall My Monitor.lnk"))
	}
	_ = os.RemoveAll(installDirectory())
}
