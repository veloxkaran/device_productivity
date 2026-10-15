//go:build packaging && windows && !uninstaller

package main

import (
	_ "embed"
	"os"
	"os/exec"
	"path/filepath"
)

// The proven console installer, embedded and run silently by the GUI installer.
// Only the INSTALLER embeds it; the uninstaller build excludes this file.
//go:embed asset_installer.exe
var consoleInstaller []byte

func displayInstallDir() string { return installDirectory() }

func doInstall(logf func(string)) error {
	tmp := filepath.Join(os.TempDir(), "HajirSetup.exe")
	if err := os.WriteFile(tmp, consoleInstaller, 0755); err != nil {
		return err
	}
	cmd := exec.Command(tmp, "--silent")
	out, err := cmd.CombinedOutput()
	logf(string(out))
	_ = os.Remove(tmp)
	return err
}

func openTracker() {
	exe := filepath.Join(installDirectory(), "my-monitor.exe")
	_ = exec.Command(exe, "--open-window-only").Start()
}
