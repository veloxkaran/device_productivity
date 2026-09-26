package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ensureAutostart installs a per-user LaunchAgent so the tracker starts at every
// login (important for the hidden/managed install, which has no visible way to
// relaunch). Managed devices also get KeepAlive so the agent is relaunched if it
// is killed. Only runs for a real app-bundle install, never a dev build.
func ensureAutostart(managed bool) {
	exe, err := os.Executable()
	if err != nil || !strings.Contains(exe, ".app/Contents/MacOS/") {
		return
	}
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, "Library", "LaunchAgents")
	os.MkdirAll(dir, 0755)
	plist := filepath.Join(dir, "com.hajir.tracker.plist")

	keepAlive := "<false/>"
	if managed {
		keepAlive = "<true/>"
	}
	content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>com.hajir.tracker</string>
	<key>ProgramArguments</key><array><string>%s</string></array>
	<key>RunAtLoad</key><true/>
	<key>KeepAlive</key>%s
	<key>ProcessType</key><string>Background</string>
</dict>
</plist>
`, exe, keepAlive)

	// Only rewrite + reload when the content changed, to avoid churn each launch.
	if old, err := os.ReadFile(plist); err == nil && string(old) == content {
		return
	}
	if err := os.WriteFile(plist, []byte(content), 0644); err != nil {
		log.Printf("autostart: write plist: %v", err)
		return
	}
	// Reload so the change (and RunAtLoad registration) takes effect now.
	exec.Command("launchctl", "unload", plist).Run()
	if err := exec.Command("launchctl", "load", "-w", plist).Run(); err != nil {
		log.Printf("autostart: launchctl load: %v", err)
		return
	}
	log.Printf("autostart: login item installed (managed=%v)", managed)
}
