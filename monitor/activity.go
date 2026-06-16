package monitor

import (
	"log"
	"my-monitor/settings"
	"my-monitor/storage"
	"sync"
	"time"
)

type Status struct {
	IsActive    bool
	IdleSeconds int64
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
	var prevIdle  int64
	var prevMouse MousePos
	prevMouse = getMousePosition()

	t := time.NewTicker(interval)
	defer t.Stop()

	for range t.C {
		idleSec := getIdleSeconds()
		active   := time.Duration(idleSec)*time.Second < settings.IdleThreshold()

		curMouse  := getMousePosition()
		mouseMoved := settings.Get().MouseTrackingEnabled &&
			(curMouse.X != prevMouse.X || curMouse.Y != prevMouse.Y)

		// Keyboard activity: idle timer reset without mouse movement
		keyboardActive := idleSec < prevIdle && !mouseMoved

		prevIdle  = idleSec
		prevMouse = curMouse

		mu.Lock()
		current = Status{IsActive: active, IdleSeconds: idleSec, UpdatedAt: time.Now()}
		mu.Unlock()

		// Only record activity data when the employee is clocked in.
		// This keeps daily summaries and logs tied to actual work sessions.
		if !IsClockedIn() {
			continue
		}

		mouseInt := 0
		if mouseMoved {
			mouseInt = 1
		}
		keyboardInt := 0
		if keyboardActive {
			keyboardInt = 1
		}

		if err := db.SaveActivity(active, idleSec); err != nil {
			log.Printf("monitor: db save activity failed: %v", err)
		}

		workDate := time.Now().Format("2006-01-02")
		activeSec := int64(0)
		if active {
			activeSec = int64(interval.Seconds())
		}
		idleInterval := int64(0)
		if !active {
			idleInterval = int64(interval.Seconds())
		}

		if err := db.UpsertDailySummary(workDate, active, activeSec, idleInterval, mouseInt, keyboardInt); err != nil {
			log.Printf("monitor: db upsert summary failed: %v", err)
		}
	}
}
