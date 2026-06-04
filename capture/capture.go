package capture

import (
	"fmt"
	"log"
	"my-monitor/monitor"
	"my-monitor/storage"
	"os"
	"path/filepath"
	"time"
)

const screenshotsDir = "data/screenshots"

func StartCapture(db *storage.DB, interval time.Duration) {
	if err := os.MkdirAll(screenshotsDir, 0755); err != nil {
		log.Fatalf("capture: cannot create screenshots dir: %v", err)
	}

	t := time.NewTicker(interval)
	defer t.Stop()
	for range t.C {
		if !monitor.IsClockedIn() {
			continue
		}
		take(db)
	}
}

func take(db *storage.DB) {
	filename := fmt.Sprintf("screenshot_%s.png", time.Now().Format("20060102_150405"))
	path := filepath.Join(screenshotsDir, filename)

	if err := takeScreenshot(path); err != nil {
		log.Printf("capture: screenshot failed: %v", err)
		return
	}

	if err := db.SaveScreenshot(path); err != nil {
		log.Printf("capture: db save failed: %v", err)
	}
}
