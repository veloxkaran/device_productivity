package settings

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

const filePath = "data/settings.json"

var ValidIntervals = []int{1, 3, 6, 15}

type Settings struct {
	CaptureIntervalMin      int  `json:"capture_interval_min"`
	IdleThresholdMin        int  `json:"idle_threshold_min"`
	RetentionDays           int  `json:"retention_days"`
	ScreenshotsEnabled      bool `json:"screenshots_enabled"`
	KeyboardTrackingEnabled bool `json:"keyboard_tracking_enabled"`
	MouseTrackingEnabled    bool `json:"mouse_tracking_enabled"`
}

func defaults() Settings {
	return Settings{
		CaptureIntervalMin:      3,
		IdleThresholdMin:        5,
		RetentionDays:           30,
		ScreenshotsEnabled:      true,
		KeyboardTrackingEnabled: true,
		MouseTrackingEnabled:    true,
	}
}

var (
	mu      sync.RWMutex
	current = defaults()
)

func Load() {
	mu.Lock()
	defer mu.Unlock()
	f, err := os.ReadFile(filePath)
	if err != nil {
		return
	}
	s := defaults()
	if err := json.Unmarshal(f, &s); err != nil {
		return
	}
	current = sanitize(s)
}

func Save(s Settings) error {
	mu.Lock()
	defer mu.Unlock()
	s = sanitize(s)
	current = s
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(filePath, b, 0644)
}

// SetIntervalMin updates only the capture interval, preserving all other settings.
func SetIntervalMin(min int) error {
	mu.Lock()
	defer mu.Unlock()
	if min <= 0 {
		min = 3
	}
	current.CaptureIntervalMin = min
	b, _ := json.MarshalIndent(current, "", "  ")
	return os.WriteFile(filePath, b, 0644)
}

func Get() Settings {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

func CaptureInterval() time.Duration {
	mu.RLock()
	defer mu.RUnlock()
	return time.Duration(current.CaptureIntervalMin) * time.Minute
}

func IdleThreshold() time.Duration {
	mu.RLock()
	defer mu.RUnlock()
	return time.Duration(current.IdleThresholdMin) * time.Minute
}

func RetentionDays() int {
	mu.RLock()
	defer mu.RUnlock()
	return current.RetentionDays
}

func sanitize(s Settings) Settings {
	if s.CaptureIntervalMin <= 0 {
		s.CaptureIntervalMin = 3
	}
	if s.IdleThresholdMin <= 0 {
		s.IdleThresholdMin = 5
	}
	if s.RetentionDays <= 0 {
		s.RetentionDays = 30
	}
	if s.RetentionDays > 90 {
		s.RetentionDays = 90
	}
	return s
}
