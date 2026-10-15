//go:build packaging && darwin && !uninstaller

package main

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// The complete, already-signed MyMonitor.app, zipped at build time with ditto so
// its code signature is preserved. Only the INSTALLER embeds it; the uninstaller
// build excludes this file (via the `uninstaller` tag) so it stays small.
//go:embed app.zip
var appZip []byte

func displayInstallDir() string { return appBundlePath() }

func doInstall(logf func(string)) error {
	home, _ := os.UserHomeDir()
	appsDir := filepath.Join(home, "Applications")
	if err := os.MkdirAll(appsDir, 0755); err != nil {
		return err
	}
	_ = os.RemoveAll(appBundlePath())

	tmp := filepath.Join(os.TempDir(), "MyMonitor-app.zip")
	if err := os.WriteFile(tmp, appZip, 0644); err != nil {
		return err
	}
	defer os.Remove(tmp)

	if out, err := exec.Command("ditto", "-x", "-k", tmp, appsDir).CombinedOutput(); err != nil {
		return fmt.Errorf("could not install app: %v: %s", err, string(out))
	}

	binPath := filepath.Join(appBundlePath(), "Contents", "MacOS", "MyMonitor")
	if err := installLoginItem(binPath, logf); err != nil {
		logf("login item (non-fatal): " + err.Error())
	}
	return nil
}

func installLoginItem(binPath string, logf func(string)) error {
	home, _ := os.UserHomeDir()
	la := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(la, 0755); err != nil {
		return err
	}
	plistPath := filepath.Join(la, bundleID+".plist")
	content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key><array><string>%s</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><false/>
</dict></plist>`, bundleID, binPath)
	if err := os.WriteFile(plistPath, []byte(content), 0644); err != nil {
		return err
	}
	_ = exec.Command("launchctl", "unload", plistPath).Run()
	return exec.Command("launchctl", "load", plistPath).Run()
}

func openTracker() {
	_ = exec.Command("open", appBundlePath()).Start()
}
