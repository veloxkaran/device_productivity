package main

import (
	"encoding/json"
	"fmt"
	"log"
	"my-monitor/auth"
	"my-monitor/capture"
	"my-monitor/cloud"
	"my-monitor/monitor"
	"my-monitor/storage"
	"my-monitor/web"
	"os"
	"time"
)

func main() {
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

	// Start cloud sync if configured
	go startCloudSync(db)

	printStartBanner(firstRun)

	web.Start(db, ":8080")
}

func startCloudSync(db *storage.DB) {
	cfgPath := "data/cloud.json"
	f, err := os.ReadFile(cfgPath)
	if err != nil {
		return
	}
	var cfg cloud.Config
	if err := json.Unmarshal(f, &cfg); err != nil || cfg.URL == "" || cfg.SyncToken == "" {
		return
	}
	syncer := cloud.NewSyncer(cfg, db)
	syncer.Start(5 * time.Minute)
}

func printStartBanner(firstRun bool) {
	fmt.Println("")
	fmt.Println("  ┌──────────────────────────────────────────────┐")
	fmt.Println("  │             My Monitor is running            │")
	fmt.Println("  ├──────────────────────────────────────────────┤")
	fmt.Printf("  │  Dashboard     →  http://localhost:8080      │\n")
	fmt.Printf("  │  Time Tracker  →  http://localhost:8080/time │\n")
	fmt.Printf("  │  Cloud Setup   →  http://localhost:8080/cloud│\n")
	if firstRun {
		fmt.Println("  │  Setup         →  http://localhost:8080/setup│")
		fmt.Println("  │  Login         →  admin / admin              │")
		fmt.Println("  ├──────────────────────────────────────────────┤")
		fmt.Println("  │  ⚠  Change default password in Setup!        │")
	}
	fmt.Println("  └──────────────────────────────────────────────┘")
	fmt.Println("")
}
