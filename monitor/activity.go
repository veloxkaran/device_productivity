package monitor

import (
	"log"
	"my-monitor/storage"
	"sync"
	"time"
)

const idleThreshold = 5 * time.Minute

type Status struct {
	IsActive    bool
	IdleSeconds int64
	AppName     string
	UpdatedAt   time.Time
}

var (
	mu      sync.RWMutex
	current = Status{IsActive: true, UpdatedAt: time.Now()}
)

func CurrentStatus() Status {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

func StartActivityMonitor(db *storage.DB, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for range t.C {
		idle := getIdleSeconds() // implemented per-platform in idle_<os>.go
		active := time.Duration(idle)*time.Second < idleThreshold

		app := getActiveApp()

		mu.Lock()
		current = Status{IsActive: active, IdleSeconds: idle, AppName: app, UpdatedAt: time.Now()}
		mu.Unlock()

		if !TrackingEnabled() {
			continue
		}
		if err := db.SaveActivity(active, idle, app); err != nil {
			log.Printf("monitor: db save failed: %v", err)
		}
	}
}
