package web

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func autoStartConfigPath() string {
	cfg, _ := os.UserConfigDir()
	return filepath.Join(cfg, "systemd", "user", "my-monitor.service")
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
	servicePath := autoStartConfigPath()
	if err := os.MkdirAll(filepath.Dir(servicePath), 0755); err != nil {
		return nil, fmt.Errorf("cannot create systemd user dir: %w", err)
	}

	content := fmt.Sprintf(`[Unit]
Description=My Monitor — Activity & Screenshot Tracker
After=graphical-session.target

[Service]
Type=simple
ExecStart=%s
WorkingDirectory=%s
Restart=on-failure
RestartSec=30

[Install]
WantedBy=default.target
`, exe, workDir)

	if err := os.WriteFile(servicePath, []byte(content), 0644); err != nil {
		return nil, fmt.Errorf("write service file failed: %w", err)
	}

	exec.Command("systemctl", "--user", "daemon-reload").Run()
	exec.Command("systemctl", "--user", "enable", "my-monitor").Run()
	exec.Command("systemctl", "--user", "start", "my-monitor").Run()

	return map[string]interface{}{
		"ok":        true,
		"path":      servicePath,
		"plistPath": servicePath,
		"message":   "Systemd user service installed — My Monitor will start on next login",
	}, nil
}

func autoStartRemove() (map[string]interface{}, error) {
	path := autoStartConfigPath()
	exec.Command("systemctl", "--user", "stop", "my-monitor").Run()
	exec.Command("systemctl", "--user", "disable", "my-monitor").Run()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove service file failed: %w", err)
	}
	exec.Command("systemctl", "--user", "daemon-reload").Run()
	return map[string]interface{}{"ok": true, "message": "Systemd service removed"}, nil
}
