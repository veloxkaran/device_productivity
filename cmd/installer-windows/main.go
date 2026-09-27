//go:build packaging

// My Monitor — Windows self-installing .exe
// Build with: GOOS=windows GOARCH=amd64 go build -tags packaging -o MyMonitor-Setup-windows-amd64.exe ./cmd/installer-windows/
// The Makefile 'package-windows-amd64' target handles the full build workflow.

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
	appName  = "MyMonitor"
	port     = 8090
	regPath  = `HKCU:\Software\Microsoft\Windows\CurrentVersion\Run`
	regValue = "MyMonitor"
)

func main() {
	var token, hubURL string
	silent := false
	for i := 1; i < len(os.Args); i++ {
		a := os.Args[i]
		switch strings.ToLower(a) {
		case "--uninstall", "uninstall":
			runUninstall()
			return
		case "--help", "-h":
			printHelp()
			return
		case "--silent", "/silent", "-s":
			silent = true
		case "--token", "/token":
			if i+1 < len(os.Args) {
				i++
				token = strings.TrimSpace(os.Args[i])
			}
		case "--hub", "/hub":
			if i+1 < len(os.Args) {
				i++
				hubURL = strings.TrimSpace(os.Args[i])
			}
		default:
			// also accept --token=xxx / --hub=xxx
			if strings.HasPrefix(a, "--token=") {
				token = strings.TrimSpace(a[len("--token="):])
			} else if strings.HasPrefix(a, "--hub=") {
				hubURL = strings.TrimSpace(a[len("--hub="):])
			}
		}
	}
	// A device token implies a silent, managed (covert) install.
	if token != "" {
		silent = true
	}
	runInstall(token, hubURL, silent)
}

// ── Install ────────────────────────────────────────────────────────

func runInstall(token, hubURL string, silent bool) {
	installDir := installDirectory()
	binPath := filepath.Join(installDir, "my-monitor.exe")
	dataDir := filepath.Join(installDir, "data")

	printBanner()
	fmt.Printf("  Install directory : %s\n", installDir)
	fmt.Println()

	mustDo(os.MkdirAll(installDir, 0755), "create install dir")
	mustDo(os.MkdirAll(dataDir, 0755), "create data dir")
	mustDo(os.MkdirAll(filepath.Join(dataDir, "screenshots"), 0755), "create screenshots dir")
	// Managed provisioning: drop the employer-issued device token so the app
	// comes up managed (covert) on first launch — no login page, no dashboard.
	if token != "" {
		pf := fmt.Sprintf("{\"token\":%q,\"hub_url\":%q}", token, hubURL)
		if err := os.WriteFile(filepath.Join(dataDir, "provision.json"), []byte(pf), 0600); err != nil {
			warn("Could not write provision file: " + err.Error())
		} else {
			ok("Provisioned as managed device (no local login page)")
		}
	}
	mustDo(os.WriteFile(binPath, appBinary, 0755), "write binary")
	ok("Binary installed: " + binPath)

	// Windows startup registry
	if err := psRun(fmt.Sprintf(
		`Set-ItemProperty -Path '%s' -Name '%s' -Value '%s'`, regPath, regValue, binPath,
	)); err == nil {
		ok("Added to Windows startup (HKCU Run)")
	} else {
		warn("Could not write startup registry entry: " + err.Error())
	}

	// Start Menu shortcut
	createShortcut(binPath, installDir)

	// Install a click-to-uninstall: copy this setup exe and make a Start Menu shortcut.
	uninstallExe := filepath.Join(installDir, "MyMonitor-Uninstall.exe")
	if self, err := os.Executable(); err == nil {
		if data, err := os.ReadFile(self); err == nil {
			if os.WriteFile(uninstallExe, data, 0755) == nil {
				createUninstallShortcut(uninstallExe)
			}
		}
	}

	// Launch app
	fmt.Println()
	info("Starting My Monitor in background...")
	cmd := exec.Command(binPath)
	cmd.Dir = installDir
	if err := cmd.Start(); err != nil {
		warn("Could not start automatically: " + err.Error())
		warn("Run manually: " + binPath)
	} else if silent {
		// Managed/silent install: no local web UI, so don't probe the port or
		// open a browser.
		ok("Started in managed mode")
	} else {
		for i := 0; i < 20; i++ {
			time.Sleep(300 * time.Millisecond)
			if portReady() {
				break
			}
		}
		if portReady() {
			ok(fmt.Sprintf("Running at http://localhost:%d", port))
			exec.Command("cmd", "/c", "start",
				fmt.Sprintf("http://localhost:%d/setup", port)).Start()
		} else {
			warn("App started but port not yet open — may still be loading.")
		}
	}

	printDone()
	if !silent {
		fmt.Print("\n  Press Enter to close this window...")
		fmt.Scanln()
	}
}

func createShortcut(binPath, workDir string) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return
	}
	shortcutDir := filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs")
	os.MkdirAll(shortcutDir, 0755)
	shortcutPath := filepath.Join(shortcutDir, "My Monitor.lnk")
	ps := fmt.Sprintf(`
$ws = New-Object -ComObject WScript.Shell
$s  = $ws.CreateShortcut('%s')
$s.TargetPath       = '%s'
$s.WorkingDirectory = '%s'
$s.Description      = 'My Monitor - Activity & Screenshot Tracker'
$s.Save()`, shortcutPath, binPath, workDir)
	if err := psRun(ps); err == nil {
		ok("Start Menu shortcut: " + shortcutPath)
	}
}

func createUninstallShortcut(uninstallExe string) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return
	}
	shortcutDir := filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs")
	os.MkdirAll(shortcutDir, 0755)
	shortcutPath := filepath.Join(shortcutDir, "Uninstall My Monitor.lnk")
	ps := fmt.Sprintf(`
$ws = New-Object -ComObject WScript.Shell
$s  = $ws.CreateShortcut('%s')
$s.TargetPath       = '%s'
$s.Arguments        = '--uninstall'
$s.Description      = 'Uninstall My Monitor'
$s.Save()`, shortcutPath, uninstallExe)
	if err := psRun(ps); err == nil {
		ok("Start Menu shortcut: " + shortcutPath)
	}
}

// ── Uninstall ──────────────────────────────────────────────────────

func runUninstall() {
	fmt.Println("\n  Uninstalling My Monitor...\n")
	exec.Command("taskkill", "/F", "/IM", "my-monitor.exe").Run()

	psRun(fmt.Sprintf(
		`Remove-ItemProperty -Path '%s' -Name '%s' -ErrorAction SilentlyContinue`,
		regPath, regValue,
	))
	ok("Registry startup entry removed")

	if appData := os.Getenv("APPDATA"); appData != "" {
		progs := filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs")
		os.Remove(filepath.Join(progs, "My Monitor.lnk"))
		os.Remove(filepath.Join(progs, "Uninstall My Monitor.lnk"))
		ok("Start Menu shortcuts removed")
	}

	installDir := installDirectory()
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
	fmt.Print("\n  Press Enter to close...")
	fmt.Scanln()
}

// ── Helpers ────────────────────────────────────────────────────────

func installDirectory() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
	}
	return filepath.Join(base, appName)
}

func portReady() bool {
	err := exec.Command("powershell", "-NoProfile", "-Command",
		fmt.Sprintf(`(New-Object Net.Sockets.TcpClient).Connect('127.0.0.1', %d)`, port),
	).Run()
	return err == nil
}

func psRun(script string) error {
	return exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Run()
}

func mustDo(err error, context string) {
	if err != nil {
		fmt.Printf("\n  Press Enter to close...")
		fmt.Scanln()
		log.Fatalf("✗  Failed to %s: %v", context, err)
	}
}

func ok(msg string)   { fmt.Printf("  ✓  %s\n", msg) }
func warn(msg string) { fmt.Printf("  ⚠  %s\n", msg) }
func info(msg string) { fmt.Printf("  ℹ  %s\n", msg) }

func printBanner() {
	fmt.Println()
	fmt.Println("  ┌──────────────────────────────────────────────┐")
	fmt.Println("  │       My Monitor — Windows Installer         │")
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
	fmt.Println("  │  Uninstall:  MyMonitor-Setup.exe --uninstall  │")
	fmt.Println("  └──────────────────────────────────────────────┘")
}

func printHelp() {
	fmt.Println("My Monitor Installer")
	fmt.Println("Usage:")
	fmt.Println("  MyMonitor-Setup-windows-amd64.exe             Install")
	fmt.Println("  MyMonitor-Setup-windows-amd64.exe --uninstall  Uninstall")
}
