package web

import (
	"fmt"
	"os/exec"
	"strings"
)

const winAutoStartName = "MyMonitor"

func autoStartStatus() map[string]interface{} {
	out, err := exec.Command("powershell", "-NoProfile", "-Command",
		fmt.Sprintf(
			`(Get-ItemProperty -Path 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run' -Name '%s' -ErrorAction SilentlyContinue).%s`,
			winAutoStartName, winAutoStartName,
		),
	).Output()
	val := ""
	if err == nil {
		val = strings.TrimSpace(string(out))
	}
	return map[string]interface{}{
		"installed": val != "",
		"path":      val,
		"plistPath": val,
	}
}

func autoStartInstall(exe, workDir string) (map[string]interface{}, error) {
	cmd := fmt.Sprintf(
		`Set-ItemProperty -Path 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run' -Name '%s' -Value '%s'`,
		winAutoStartName, exe,
	)
	if err := exec.Command("powershell", "-NoProfile", "-Command", cmd).Run(); err != nil {
		return nil, fmt.Errorf("failed to set startup registry entry: %w", err)
	}
	return map[string]interface{}{
		"ok":        true,
		"path":      exe,
		"plistPath": exe,
		"message":   "Startup entry added — My Monitor will start on next login",
	}, nil
}

func autoStartRemove() (map[string]interface{}, error) {
	cmd := fmt.Sprintf(
		`Remove-ItemProperty -Path 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run' -Name '%s' -ErrorAction SilentlyContinue`,
		winAutoStartName,
	)
	if err := exec.Command("powershell", "-NoProfile", "-Command", cmd).Run(); err != nil {
		return nil, fmt.Errorf("failed to remove startup entry: %w", err)
	}
	return map[string]interface{}{"ok": true, "message": "Startup entry removed"}, nil
}
