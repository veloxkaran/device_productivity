//go:build packaging && darwin

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const bundleID = "com.hajir.tracker"

func appBundlePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Applications", "MyMonitor.app")
}

// uninstall removes the app, its login item, and (optionally) its local data,
// using native macOS dialogs. No embedded payload needed.
func uninstall() {
	confirm := exec.Command("osascript", "-e",
		`display dialog "This will remove Hajir Tracker from this Mac, including its background login item." with title "Uninstall Hajir Tracker" buttons {"Cancel","Uninstall"} default button "Uninstall" cancel button "Cancel" with icon caution`)
	if confirm.Run() != nil {
		return
	}
	home, _ := os.UserHomeDir()
	plist := filepath.Join(home, "Library", "LaunchAgents", bundleID+".plist")
	_ = exec.Command("launchctl", "unload", plist).Run()
	_ = os.Remove(plist)
	_ = exec.Command("pkill", "-f", "MyMonitor.app/Contents/MacOS/MyMonitor").Run()
	_ = os.RemoveAll(appBundlePath())

	out, _ := exec.Command("osascript",
		"-e", `display dialog "Also delete local data (screenshots, database and settings)?" with title "Uninstall Hajir Tracker" buttons {"Keep Data","Delete Data"} default button "Keep Data" with icon caution`,
		"-e", `button returned of result`).Output()
	msg := "Hajir Tracker has been removed."
	if strings.Contains(string(out), "Delete Data") {
		_ = os.RemoveAll(filepath.Join(home, "Library", "Application Support", "MyMonitor"))
		_ = os.Remove(filepath.Join(home, "Library", "Logs", "MyMonitor.log"))
		msg = "Hajir Tracker and its local data have been removed."
	}
	_ = exec.Command("osascript", "-e",
		`display dialog "`+msg+`\\nIf this device was managed, remember to revoke it in the dashboard." with title "Uninstall Hajir Tracker" buttons {"OK"} default button "OK"`).Run()
}
