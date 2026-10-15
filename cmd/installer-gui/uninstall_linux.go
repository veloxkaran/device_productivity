//go:build packaging && linux

package main

import (
	"os"
	"os/exec"
	"path/filepath"
)

func linuxInstallDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "my-monitor")
}

// uninstall removes the app natively (no embedded payload): stops/disables the
// systemd user service and deletes the service file, launcher entries, symlink,
// and install folder (app + data).
func uninstall() {
	home, _ := os.UserHomeDir()
	_ = exec.Command("systemctl", "--user", "stop", "my-monitor").Run()
	_ = exec.Command("systemctl", "--user", "disable", "my-monitor").Run()
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	for _, p := range []string{
		filepath.Join(home, ".config", "systemd", "user", "my-monitor.service"),
		filepath.Join(home, ".local", "bin", "my-monitor"),
		filepath.Join(home, ".local", "share", "applications", "my-monitor.desktop"),
		filepath.Join(home, ".local", "share", "applications", "my-monitor-uninstall.desktop"),
	} {
		_ = os.Remove(p)
	}
	_ = os.RemoveAll(linuxInstallDir())
}
