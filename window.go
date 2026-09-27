package main

import (
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func serverRunning(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// setupAppBundle points the working directory at a stable per-user data folder
// and redirects logs there, so an installed app (started at login from an
// unpredictable working directory) always reads/writes the same place. It is a
// no-op for a dev build run from the source tree, where ./data is used instead.
func setupAppBundle() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	// Dev build: exe sits next to go.mod, or was produced by `go run`.
	if _, e := os.Stat(filepath.Join(filepath.Dir(exe), "go.mod")); e == nil {
		return
	}
	if strings.Contains(exe, "go-build") {
		return
	}

	home, _ := os.UserHomeDir()
	var data, logDir string
	switch runtime.GOOS {
	case "darwin":
		// Only relocate for a real .app bundle; a loose binary stays in place.
		if !strings.Contains(exe, ".app/Contents/MacOS/") {
			return
		}
		data = filepath.Join(home, "Library", "Application Support", "MyMonitor")
		logDir = filepath.Join(home, "Library", "Logs")
	default: // windows, linux and others: keep data next to the installed binary
		data = filepath.Dir(exe)
		logDir = filepath.Join(data, "logs")
	}
	_ = home

	os.MkdirAll(filepath.Join(data, "data", "screenshots"), 0755)
	os.MkdirAll(logDir, 0755)
	os.Chdir(data)
	logName := "MyMonitor.log"
	if f, err := os.OpenFile(filepath.Join(logDir, logName), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); err == nil {
		os.Stdout = f
		os.Stderr = f
		log.SetOutput(f)
	}
	log.Printf("=== launch %s (%s) data=%s ===", exe, runtime.GOOS, data)
}

func waitForServer(addr string) {
	for i := 0; i < 50; i++ {
		if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			c.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func openAppWindow(url string) {
	waitForServer("127.0.0.1:8090")
	profile, _ := filepath.Abs(filepath.Join("data", "window-profile"))
	args := []string{"--app=" + url, "--window-size=380,620", "--user-data-dir=" + profile, "--no-first-run", "--no-default-browser-check"}

	var candidates [][]string
	switch runtime.GOOS {
	case "darwin":
		for _, app := range []string{"Google Chrome", "Microsoft Edge", "Brave Browser", "Chromium"} {
			candidates = append(candidates, append([]string{"open", "-na", app, "--args"}, args...))
		}
		candidates = append(candidates, []string{"open", url})
	case "windows":
		for _, p := range []string{
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("LocalAppData"), "Google", "Chrome", "Application", "chrome.exe"),
		} {
			if _, err := os.Stat(p); err == nil {
				candidates = append(candidates, append([]string{p}, args...))
			}
		}
		candidates = append(candidates, []string{"rundll32", "url.dll,FileProtocolHandler", url})
	default:
		for _, b := range []string{"google-chrome", "chromium", "chromium-browser", "microsoft-edge", "brave-browser"} {
			if p, err := exec.LookPath(b); err == nil {
				candidates = append(candidates, append([]string{p}, args...))
			}
		}
		candidates = append(candidates, []string{"xdg-open", url})
	}

	for _, c := range candidates {
		cmd := exec.Command(c[0], c[1:]...)
		var err error
		if c[0] == "open" || c[0] == "rundll32" || c[0] == "xdg-open" {
			err = cmd.Run()
		} else {
			err = cmd.Start()
		}
		if err == nil {
			return
		}
	}
	log.Printf("window: could not open a window, visit %s", url)
}
