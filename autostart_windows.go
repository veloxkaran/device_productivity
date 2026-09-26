package main

import (
	"log"
	"os"
	"os/exec"
)

// ensureAutostart adds an HKCU Run entry so the tracker starts at login. A GUI
// app with no console starts silently, which suits the hidden/managed install.
// Windows has no simple per-key "relaunch if killed"; managed is accepted for a
// consistent signature.
func ensureAutostart(managed bool) {
	_ = managed
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command("reg", "add", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`,
		"/v", "MyMonitor", "/t", "REG_SZ", "/d", exe, "/f")
	if err := cmd.Run(); err != nil {
		log.Printf("autostart: reg add: %v", err)
		return
	}
	log.Printf("autostart: login item installed")
}
