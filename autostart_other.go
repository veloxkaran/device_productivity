//go:build !darwin && !windows

package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// ensureAutostart writes an XDG autostart .desktop entry so the tracker starts at
// login on Linux desktops.
func ensureAutostart(managed bool) {
	_ = managed
	exe, err := os.Executable()
	if err != nil {
		return
	}
	home, _ := os.UserHomeDir()
	// If the systemd user service (created by the Linux installer) is present, it
	// already handles login start — don't add a duplicate .desktop entry.
	if _, e := os.Stat(filepath.Join(home, ".config", "systemd", "user", "my-monitor.service")); e == nil {
		return
	}
	dir := filepath.Join(home, ".config", "autostart")
	os.MkdirAll(dir, 0755)
	path := filepath.Join(dir, "hajir-tracker.desktop")
	content := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Hajir Tracker
Exec=%s
X-GNOME-Autostart-enabled=true
NoDisplay=true
Terminal=false
`, exe)
	if old, err := os.ReadFile(path); err == nil && string(old) == content {
		return
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		log.Printf("autostart: write desktop entry: %v", err)
		return
	}
	log.Printf("autostart: login item installed")
}
