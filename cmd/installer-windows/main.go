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
	if len(os.Args) > 1 {
		switch strings.ToLower(os.Args[1]) {
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
	installDir := installDirectory()
	binPath := filepath.Join(installDir, "my-monitor.exe")
	dataDir := filepath.Join(installDir, "data")

	printBanner()
	fmt.Printf("  Install directory : %s\n", installDir)
	fmt.Println()

	mustDo(os.MkdirAll(installDir, 0755), "create install dir")
	mustDo(os.MkdirAll(dataDir, 0755), "create data dir")
	mustDo(os.MkdirAll(filepath.Join(dataDir, "screenshots"), 0755), "create screenshots dir")
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
	fmt.Print("\n  Press Enter to close this window...")
	fmt.Scanln()
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
