//go:build packaging

// My Monitor — Linux self-extracting installer.
// Build with: GOOS=linux GOARCH=amd64 go build -tags packaging -o MyMonitor-Setup-linux-amd64 ./cmd/installer-linux/
// The Makefile 'package-linux-*' targets handle the full build workflow.

package main

import (
	_ "embed"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

//go:embed asset.bin
var appBinary []byte

const (
	installBase = ".local/share/my-monitor"
	binLinkDir  = ".local/bin"
	serviceDir  = ".config/systemd/user"
	serviceName = "my-monitor"
	port        = 8080
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--uninstall", "uninstall":
			runUninstall()
			return
		case "--help", "-h":
			printHelp()
			return
		}
	}
	runInstall()
}

// ── Install ────────────────────────────────────────────────────────

func runInstall() {
	home := mustHome()
	installDir := filepath.Join(home, installBase)
	binPath := filepath.Join(installDir, "my-monitor")
	dataDir := filepath.Join(installDir, "data")

	printBanner()
	fmt.Printf("  Install directory : %s\n", installDir)
	fmt.Printf("  Data directory    : %s\n", dataDir)
	fmt.Println()

	mustDo(os.MkdirAll(installDir, 0755), "create install dir")
	mustDo(os.MkdirAll(dataDir, 0755), "create data dir")
	mustDo(os.MkdirAll(filepath.Join(dataDir, "screenshots"), 0755), "create screenshots dir")

	mustDo(os.WriteFile(binPath, appBinary, 0755), "write binary")
	ok("Binary installed: " + binPath)

	// Symlink ~/.local/bin/my-monitor → binary
	linkDir := filepath.Join(home, binLinkDir)
	os.MkdirAll(linkDir, 0755)
	linkPath := filepath.Join(linkDir, serviceName)
	os.Remove(linkPath)
	if err := os.Symlink(binPath, linkPath); err == nil {
		ok("Symlink: " + linkPath)
	}

	installSystemd(home, binPath, installDir)

	fmt.Println()
	info("Starting My Monitor in background...")
	cmd := exec.Command(binPath)
	cmd.Dir = installDir
	if err := cmd.Start(); err != nil {
		warn("Could not start automatically: " + err.Error())
		warn("Run manually: " + binPath)
	} else {
		for i := 0; i < 20; i++ {
			time.Sleep(300 * time.Millisecond)
			if tcpReady() {
				break
			}
		}
		if tcpReady() {
			ok(fmt.Sprintf("Running at http://localhost:%d", port))
			exec.Command("xdg-open", fmt.Sprintf("http://localhost:%d/setup", port)).Start()
		} else {
			warn("App started but port not yet open — may still be loading.")
		}
	}

	printDone()
}

func installSystemd(home, binPath, workDir string) {
	svcPath := filepath.Join(home, serviceDir, serviceName+".service")
	mustDo(os.MkdirAll(filepath.Dir(svcPath), 0755), "create systemd dir")

	content := fmt.Sprintf(`[Unit]
Description=My Monitor — Activity & Screenshot Tracker
After=graphical-session.target

[Service]
Type=simple
ExecStart=%s
WorkingDirectory=%s
Restart=on-failure
RestartSec=30
Environment=HOME=%s

[Install]
WantedBy=default.target
`, binPath, workDir, home)

	mustDo(os.WriteFile(svcPath, []byte(content), 0644), "write service file")
	exec.Command("systemctl", "--user", "daemon-reload").Run()
	if exec.Command("systemctl", "--user", "enable", serviceName).Run() == nil {
		ok("Systemd user service enabled: " + svcPath)
		info("Auto-start: My Monitor will launch on every login.")
	} else {
		warn("Could not enable systemd service (run from a desktop session).")
		info("Service file written: " + svcPath)
	}
}

// ── Uninstall ──────────────────────────────────────────────────────

func runUninstall() {
	home := mustHome()
	fmt.Println("\n  Uninstalling My Monitor...\n")

	exec.Command("systemctl", "--user", "stop", serviceName).Run()
	exec.Command("systemctl", "--user", "disable", serviceName).Run()
	exec.Command("systemctl", "--user", "daemon-reload").Run()

	for _, p := range []string{
		filepath.Join(home, serviceDir, serviceName+".service"),
		filepath.Join(home, binLinkDir, serviceName),
	} {
		if err := os.Remove(p); err == nil {
			ok("Removed: " + p)
		}
	}

	installDir := filepath.Join(home, installBase)
	fmt.Printf("\n  App files at: %s\n", installDir)
	fmt.Print("  Remove data (database + screenshots) too? [y/N] ")
	var ans string
	fmt.Scanln(&ans)
	if strings.ToLower(strings.TrimSpace(ans)) == "y" {
		os.RemoveAll(installDir)
		ok("Data removed.")
	} else {
		info("Data kept at: " + installDir)
	}
	fmt.Println()
}

// ── Helpers ────────────────────────────────────────────────────────

func tcpReady() bool {
	out, _ := exec.Command("bash", "-c",
		fmt.Sprintf("(echo >/dev/tcp/127.0.0.1/%d) 2>/dev/null && echo ok", port)).Output()
	return strings.TrimSpace(string(out)) == "ok"
}

func mustHome() string {
	h, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("cannot determine home directory: %v", err)
	}
	return h
}

func mustDo(err error, context string) {
	if err != nil {
		log.Fatalf("✗  Failed to %s: %v", context, err)
	}
}

func ok(msg string)   { fmt.Printf("  ✓  %s\n", msg) }
func warn(msg string) { fmt.Printf("  ⚠  %s\n", msg) }
func info(msg string) { fmt.Printf("  ℹ  %s\n", msg) }

func printBanner() {
	fmt.Println()
	fmt.Println("  ┌──────────────────────────────────────────────┐")
	fmt.Println("  │         My Monitor — Linux Installer         │")
	fmt.Println("  │  Activity & Screenshot Tracker               │")
	fmt.Println("  └──────────────────────────────────────────────┘")
	fmt.Println()
}

func printDone() {
	fmt.Println()
	fmt.Println("  ┌──────────────────────────────────────────────┐")
	fmt.Println("  │  Installation complete!                       │")
	fmt.Printf("  │  Dashboard:  http://localhost:%d             │\n", port)
	fmt.Println("  │  Login:      admin / admin                    │")
	fmt.Println("  ├──────────────────────────────────────────────┤")
	fmt.Println("  │  ⚠  Change your password in the Setup wizard! │")
	fmt.Println("  ├──────────────────────────────────────────────┤")
	fmt.Println("  │  Uninstall:  ./MyMonitor-Setup --uninstall    │")
	fmt.Println("  └──────────────────────────────────────────────┘")
	fmt.Println()
}

func printHelp() {
	fmt.Println("My Monitor Installer")
	fmt.Println("Usage:")
	fmt.Println("  ./MyMonitor-Setup-linux-amd64             Install")
	fmt.Println("  ./MyMonitor-Setup-linux-amd64 --uninstall  Uninstall")
}
