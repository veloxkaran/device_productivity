//go:build packaging && linux && !uninstaller

package main

import (
	_ "embed"
	"os"
	"os/exec"
	"path/filepath"
)

//go:embed asset_installer.bin
var consoleInstaller []byte

func displayInstallDir() string { return linuxInstallDir() }

func doInstall(logf func(string)) error {
	tmp := filepath.Join(os.TempDir(), "hajir-setup")
	if err := os.WriteFile(tmp, consoleInstaller, 0755); err != nil {
		return err
	}
	cmd := exec.Command(tmp, "--silent")
	cmd.Env = append(os.Environ(), "MM_NO_WINDOW=1")
	out, err := cmd.CombinedOutput()
	logf(string(out))
	_ = os.Remove(tmp)
	return err
}

func openTracker() {
	bin := filepath.Join(linuxInstallDir(), "my-monitor")
	_ = exec.Command(bin, "--open-window-only").Start()
}
