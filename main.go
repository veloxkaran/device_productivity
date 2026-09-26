package main

import (
	"fmt"
	"log"
	"my-monitor/auth"
	"my-monitor/capture"
	"my-monitor/cloud"
	"my-monitor/hub"
	"my-monitor/monitor"
	"my-monitor/storage"
	"my-monitor/web"
	"os"
	"time"
)

func main() {
	if hasFlag("--open-window-only") {
		openAppWindow("http://127.0.0.1:8090/app")
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "hub" {
		hub.Run()
		return
	}

	setupAppBundle()

	if serverRunning("127.0.0.1:8090") {
		log.Println("already running, opening window")
		openAppWindow("http://127.0.0.1:8090/app")
		return
	}

	if err := os.MkdirAll("data", 0755); err != nil {
		log.Fatalf("cannot create data dir: %v", err)
	}

	db, err := storage.New("data/monitor.db")
	if err != nil {
		log.Fatalf("storage: %v", err)
	}
	defer db.Close()

	firstRun := auth.EnsureDefaultUser(db)

	// Restore clocked-in state if a session was left open before restart
	if open, err := db.HasOpenTimeEntry(); err == nil && open {
		monitor.SetClockedIn(true)
		log.Println("clock: restored active session from database")
	}
	if db.HasOpenBreak() {
		monitor.SetOnBreak(true)
	}

	go capture.StartCapture(db, 3*time.Minute)
	go monitor.StartActivityMonitor(db, 30*time.Second)
	go monitor.StartInputMonitor(db, time.Second)

	// Purge data older than 30 days once at startup, then daily
	go func() {
		purge := func() {
			if err := db.PurgeOldData(30); err != nil {
				log.Printf("purge: %v", err)
			}
		}
		purge()
		for range time.NewTicker(24 * time.Hour).C {
			purge()
		}
	}()

	mgr := cloud.NewManager(db)

	// When a device is switched to managed/automatic at runtime (via the in-app
	// Automatic setup), hide the menu-bar icon so it goes covert immediately.
	web.OnBecameManaged = func() { hideMenuBar() }

	// Managed devices are provisioned silently with an employer-issued device
	// token (no login screen) and run headless — no auto-opened window and no
	// menu-bar item. Detect this before building any UI.
	managed := web.Provision(mgr)

	// Start automatically at every login (essential for the hidden/managed app,
	// which has no visible way to relaunch after a restart).
	ensureAutostart(managed)

	printStartBanner(firstRun)

	if !managed && !nativeWindow && !hasFlag("--no-window") && os.Getenv("MM_NO_WINDOW") == "" {
		go openAppWindow("http://127.0.0.1:8090/app")
	}

	go web.Start(db, mgr, "127.0.0.1:8090")
	runUI(func() { go openAppWindow("http://127.0.0.1:8090/app") }, web.Quit, managed)
}

func hasFlag(name string) bool {
	for _, a := range os.Args[1:] {
		if a == name {
			return true
		}
	}
	return false
}

func printStartBanner(firstRun bool) {
	fmt.Println("")
	fmt.Println("  ┌──────────────────────────────────────────────┐")
	fmt.Println("  │             My Monitor is running            │")
	fmt.Println("  ├──────────────────────────────────────────────┤")
	fmt.Printf("  │  Desktop app   →  http://localhost:8090/app  │\n")
	fmt.Printf("  │  Dashboard     →  http://localhost:8090      │\n")
	fmt.Printf("  │  Time Tracker  →  http://localhost:8090/time │\n")
	fmt.Printf("  │  Cloud Setup   →  http://localhost:8090/cloud│\n")
	if firstRun {
		fmt.Println("  │  Setup         →  http://localhost:8090/setup│")
		fmt.Println("  │  Login         →  admin / admin              │")
		fmt.Println("  ├──────────────────────────────────────────────┤")
		fmt.Println("  │  ⚠  Change default password in Setup!        │")
	}
	fmt.Println("  └──────────────────────────────────────────────┘")
	fmt.Println("")
}
