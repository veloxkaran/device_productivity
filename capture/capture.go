package capture

import (
	"fmt"
	"log"
	"my-monitor/monitor"
	"my-monitor/settings"
	"my-monitor/storage"
	"os"
	"path/filepath"
	"time"
)

const screenshotsDir = "data/screenshots"

// triggerCh lets callers request an immediate screenshot (e.g. on clock-in).
var triggerCh = make(chan struct{}, 1)

// TriggerNow requests an immediate screenshot outside the normal interval.
// Non-blocking — if a trigger is already queued the extra signal is dropped.
func TriggerNow() {
	select {
	case triggerCh <- struct{}{}:
	default:
	}
}

// StartCapture runs indefinitely. It takes a screenshot every configured
// interval while clocked in, and also immediately when TriggerNow is called.
func StartCapture(db *storage.DB) {
	if err := os.MkdirAll(screenshotsDir, 0755); err != nil {
		log.Fatalf("capture: cannot create screenshots dir: %v", err)
	}

	for {
		interval := settings.CaptureInterval()
		timer := time.NewTimer(interval)

		select {
		case <-timer.C:
			// Normal scheduled capture
		case <-triggerCh:
			// Immediate capture requested (e.g. clock-in)
			timer.Stop()
		}

		if !monitor.IsClockedIn() {
			continue
		}
		if !settings.Get().ScreenshotsEnabled {
			continue
		}
		take(db)
	}
}

func take(db *storage.DB) {
	filename := fmt.Sprintf("screenshot_%s.jpg", time.Now().Format("20060102_150405"))
	path := filepath.Join(screenshotsDir, filename)

	if err := takeScreenshot(path); err != nil {
		log.Printf("capture: screenshot failed: %v", err)
		return
	}

	if err := db.SaveScreenshot(path); err != nil {
		log.Printf("capture: db save failed: %v", err)
	}
}
