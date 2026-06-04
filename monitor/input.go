package monitor

import (
	"log"
	"math"
	"my-monitor/storage"
	"sync"
	"time"
)

// InputRate holds the current per-minute keyboard/mouse rates for live display.
type InputRate struct {
	KeysPerMin   int64   `json:"keysPerMin"`
	ClicksPerMin int64   `json:"clicksPerMin"`
	MousePixels  float64 `json:"mousePixels"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

var (
	inputMu   sync.RWMutex
	inputRate InputRate
)

// GetInputRate returns the latest aggregated input rate (safe for concurrent read).
func GetInputRate() InputRate {
	inputMu.RLock()
	defer inputMu.RUnlock()
	return inputRate
}

// inputBucket accumulates raw input samples for a 1-minute window.
type inputBucket struct {
	start         time.Time
	keyEvents     int64
	mouseClicks   int64
	mouseDistance float64
	lastX, lastY  float64
	lastIdleSec   int64
}

// StartInputMonitor samples mouse position and input activity every sampleInterval
// and flushes aggregated 1-minute buckets to the database.
func StartInputMonitor(db *storage.DB, sampleInterval time.Duration) {
	b := newBucket()

	sampleTick := time.NewTicker(sampleInterval)
	flushTick := time.NewTicker(time.Minute)
	defer sampleTick.Stop()
	defer flushTick.Stop()

	for {
		select {
		case <-sampleTick.C:
			takeSample(b)

		case <-flushTick.C:
			flushBucket(db, b)
			b = newBucket()
		}
	}
}

func newBucket() *inputBucket {
	x, y := getMousePos()
	return &inputBucket{
		start:       time.Now(),
		lastX:       x,
		lastY:       y,
		lastIdleSec: getIdleSeconds(),
	}
}

func takeSample(b *inputBucket) {
	// ── Mouse distance ─────────────────────────────────────────────
	x, y := getMousePos()
	dx := x - b.lastX
	dy := y - b.lastY
	dist := math.Sqrt(dx*dx + dy*dy)
	if dist > 2 { // ignore sub-pixel jitter
		b.mouseDistance += dist
	}
	b.lastX, b.lastY = x, y

	// ── Keyboard / input events via idle-time heuristic ────────────
	// If idle time dropped since last sample, at least one input event occurred.
	idle := getIdleSeconds()
	if b.lastIdleSec > 0 && idle < b.lastIdleSec {
		b.keyEvents++
	}
	b.lastIdleSec = idle

	// ── Platform-specific click counting ──────────────────────────
	b.mouseClicks += sampleClicks()

	// ── Update live rate ───────────────────────────────────────────
	elapsed := time.Since(b.start).Minutes()
	if elapsed > 0 {
		inputMu.Lock()
		inputRate = InputRate{
			KeysPerMin:   int64(float64(b.keyEvents) / elapsed),
			ClicksPerMin: int64(float64(b.mouseClicks) / elapsed),
			MousePixels:  b.mouseDistance,
			UpdatedAt:    time.Now(),
		}
		inputMu.Unlock()
	}
}

func flushBucket(db *storage.DB, b *inputBucket) {
	if b.keyEvents == 0 && b.mouseClicks == 0 && b.mouseDistance < 1 {
		return
	}
	if err := db.SaveInputLog(b.start, b.keyEvents, b.mouseClicks, b.mouseDistance); err != nil {
		log.Printf("input: db save failed: %v", err)
	}
}
