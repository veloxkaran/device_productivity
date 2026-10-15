//go:build packaging

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

var (
	winMu  sync.Mutex
	winCmd *exec.Cmd // the browser process rendering the wizard, if trackable
)

// openWindow opens url in a Chrome/Edge app-window (chromeless, fixed size).
// Returns true if a window was launched. Mirrors the app's own window.go opener.
func openWindow(url string) bool {
	profile := filepath.Join(os.TempDir(), "hajir-installer-profile")
	args := []string{
		"--app=" + url,
		"--window-size=720,600",
		"--user-data-dir=" + profile,
		"--no-first-run",
		"--no-default-browser-check",
	}

	type cand struct {
		argv       []string
		trackable  bool // true = a dedicated browser process we can later kill
	}
	var cands []cand

	switch runtime.GOOS {
	case "darwin":
		for _, app := range []string{"Google Chrome", "Microsoft Edge", "Brave Browser", "Chromium"} {
			cands = append(cands, cand{append([]string{"open", "-na", app, "--args"}, args...), false})
		}
		cands = append(cands, cand{[]string{"open", url}, false})
	case "windows":
		for _, p := range []string{
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("LocalAppData"), "Google", "Chrome", "Application", "chrome.exe"),
		} {
			if _, err := os.Stat(p); err == nil {
				cands = append(cands, cand{append([]string{p}, args...), true})
			}
		}
		cands = append(cands, cand{[]string{"rundll32", "url.dll,FileProtocolHandler", url}, false})
	default:
		for _, b := range []string{"google-chrome", "chromium", "chromium-browser", "microsoft-edge", "brave-browser"} {
			if p, err := exec.LookPath(b); err == nil {
				cands = append(cands, cand{append([]string{p}, args...), true})
			}
		}
		cands = append(cands, cand{[]string{"xdg-open", url}, false})
	}

	for _, c := range cands {
		cmd := exec.Command(c.argv[0], c.argv[1:]...)
		if err := cmd.Start(); err != nil {
			continue
		}
		if c.trackable {
			winMu.Lock()
			winCmd = cmd
			winMu.Unlock()
		}
		return true
	}
	return false
}

// closeWindow best-effort closes the wizard window we launched.
func closeWindow() {
	winMu.Lock()
	cmd := winCmd
	winMu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
