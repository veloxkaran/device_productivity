package web

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const autoStartLabel = "com.mymonitor.app"

func autoStartConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", autoStartLabel+".plist")
}

func autoStartStatus() map[string]interface{} {
	path := autoStartConfigPath()
	_, err := os.Stat(path)
	return map[string]interface{}{
		"installed": err == nil,
		"path":      path,
		"plistPath": path,
	}
}

func autoStartInstall(exe, workDir string) (map[string]interface{}, error) {
	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, "Library", "Logs")
	plistPath := autoStartConfigPath()

	if err := os.MkdirAll(filepath.Dir(plistPath), 0755); err != nil {
		return nil, fmt.Errorf("cannot create LaunchAgents dir: %w", err)
	}

	content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
    </array>
    <key>WorkingDirectory</key>
    <string>%s</string>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <dict>
        <key>SuccessfulExit</key>
        <false/>
    </dict>
    <key>StandardOutPath</key>
    <string>%s/my-monitor.log</string>
    <key>StandardErrorPath</key>
    <string>%s/my-monitor-error.log</string>
    <key>ThrottleInterval</key>
    <integer>30</integer>
</dict>
</plist>
`, autoStartLabel, exe, workDir, logDir, logDir)

	if err := os.WriteFile(plistPath, []byte(content), 0644); err != nil {
		return nil, fmt.Errorf("write plist failed: %w", err)
	}
	exec.Command("launchctl", "load", plistPath).Run()

	return map[string]interface{}{
		"ok":        true,
		"path":      plistPath,
		"plistPath": plistPath,
		"message":   "LaunchAgent installed — My Monitor will start on next login",
	}, nil
}

func autoStartRemove() (map[string]interface{}, error) {
	path := autoStartConfigPath()
	exec.Command("launchctl", "unload", path).Run()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove failed: %w", err)
	}
	return map[string]interface{}{"ok": true, "message": "LaunchAgent removed"}, nil
}
