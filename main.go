package main

import (
	"encoding/json"
	"fmt"
	"log"
	"my-monitor/auth"
	"my-monitor/capture"
	"my-monitor/cloud"
	"my-monitor/machine"
	"my-monitor/monitor"
	"my-monitor/registration"
	"my-monitor/settings"
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

	// Load persisted settings (capture interval, etc.)
	settings.Load()

	// Identify this machine
	machInfo := machine.GetInfo()
	log.Printf("machine: id=%s name=%s os=%s/%s", machInfo.MachineID, machInfo.MachineName, machInfo.OS, machInfo.OSVersion)

	// Restore clocked-in state if a session was left open before restart
	if open, err := db.HasOpenTimeEntry(); err == nil && open {
		monitor.SetClockedIn(true)
		log.Println("clock: restored active session from database")
	}

	go capture.StartCapture(db)
	go monitor.StartActivityMonitor(db, 30*time.Second)

	// Start cloud sync + device registration if configured
	go startCloudSync(db, machInfo)

	printStartBanner(firstRun)

	web.Start(db, ":8080")
}

func startCloudSync(db *storage.DB, info machine.Info) {
	cfgPath := "data/cloud.json"
	f, err := os.ReadFile(cfgPath)
	if err != nil {
		return
	}
	var cfg cloud.Config
	if err := json.Unmarshal(f, &cfg); err != nil || cfg.URL == "" || cfg.SyncToken == "" {
		return
	}

	regCfg := registration.Config{
		URL:       cfg.URL,
		Token:     cfg.SyncToken,
		CompanyID: cfg.CompanyID,
	}

	// Register (or re-register) this device with Hajir
	if err := registration.Register(regCfg, db, info); err != nil {
		log.Printf("registration: %v", err)
	}

	// Poll status; only sync when approved
	go func() {
		for {
			status, err := registration.PollStatus(regCfg, db, info.MachineID)
			if err != nil {
				log.Printf("registration: status poll: %v", err)
			} else {
				switch status {
				case "approved":
					log.Println("registration: device approved — sync active")
				case "blocked":
					log.Println("registration: device blocked — sync paused")
				default:
					log.Printf("registration: status=%s — waiting for admin approval", status)
				}
			}
			time.Sleep(5 * time.Minute)
		}
	}()

	syncer := cloud.NewSyncer(cfg, db, info.MachineID)
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
