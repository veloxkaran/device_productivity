package capture

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	_ "image/png"
	"log"
	"my-monitor/monitor"
	"my-monitor/storage"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ErrScreenUnavailable means there is no interactive screen to capture right
// now (locked, secure desktop, display off, disconnected session). It is a
// normal state, so it is skipped silently and not reported as a failure.
var ErrScreenUnavailable = errors.New("screen unavailable")

const (
	screenshotsDir     = "data/screenshots"
	defaultMaxWidth    = 1920
	defaultJPEGQuality = 60
)

// Screenshot quality and max width are tunable at runtime from employer
// settings (pushed on each heartbeat). Guarded by atomics.
var (
	jpegQualityV atomic.Int32
	maxWidthV    atomic.Int32
)

func curQuality() int {
	if v := jpegQualityV.Load(); v > 0 {
		return int(v)
	}
	return defaultJPEGQuality
}

func curMaxWidth() int {
	if v := maxWidthV.Load(); v > 0 {
		return int(v)
	}
	return defaultMaxWidth
}

// SetQuality sets the JPEG quality (clamped 30-95). SetMaxWidth sets the longest
// edge in pixels (clamped 640-3840). No-op for out-of-range or unchanged values.
func SetQuality(q int) {
	if q < 30 || q > 95 {
		return
	}
	if int(jpegQualityV.Swap(int32(q))) != q {
		log.Printf("capture: JPEG quality set to %d", q)
	}
}

func SetMaxWidth(px int) {
	if px < 640 || px > 3840 {
		return
	}
	if int(maxWidthV.Swap(int32(px))) != px {
		log.Printf("capture: max width set to %d px", px)
	}
}

type Status struct {
	LastSuccess *time.Time `json:"last_success"`
	LastError   string     `json:"last_error"`
	LastErrorAt *time.Time `json:"last_error_at"`
	Count       int        `json:"count"`
}

var (
	statusMu sync.Mutex
	status   Status
	nowCh    = make(chan struct{}, 1)

	intervalMu sync.Mutex
	interval   time.Duration
	intervalCh = make(chan struct{}, 1)
)

const (
	minInterval = 30 * time.Second
	maxInterval = time.Hour
	// Last interval the employer configured, so a restart uses it straight
	// away instead of the built-in default until the first heartbeat.
	intervalFile = "data/screenshot_interval"
)

func loadSavedInterval() time.Duration {
	b, err := os.ReadFile(intervalFile)
	if err != nil {
		return 0
	}
	secs, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0
	}
	d := time.Duration(secs) * time.Second
	if d < minInterval || d > maxInterval {
		return 0
	}
	return d
}

func saveInterval(d time.Duration) {
	if err := os.WriteFile(intervalFile, []byte(strconv.Itoa(int(d/time.Second))), 0644); err != nil {
		log.Printf("capture: cannot persist interval: %v", err)
	}
}

// SetInterval changes the screenshot interval at runtime (e.g. from employer settings).
func SetInterval(d time.Duration) {
	if d < minInterval {
		d = minInterval
	}
	if d > maxInterval {
		d = maxInterval
	}
	intervalMu.Lock()
	changed := d != interval
	interval = d
	intervalMu.Unlock()
	if changed {
		log.Printf("capture: interval set to %s", d)
		saveInterval(d)
		select {
		case intervalCh <- struct{}{}:
		default:
		}
	}
}

// OnCaptured, when set, is called after each successful screenshot (used to upload promptly).
var OnCaptured func()

// CurrentInterval is the interval the capture loop is using right now.
func CurrentInterval() time.Duration { return currentInterval() }

func currentInterval() time.Duration {
	intervalMu.Lock()
	defer intervalMu.Unlock()
	return interval
}

func CurrentStatus() Status {
	statusMu.Lock()
	defer statusMu.Unlock()
	return status
}

func TakeNow() {
	select {
	case nowCh <- struct{}{}:
	default:
	}
}

func StartCapture(db *storage.DB, initial time.Duration) {
	intervalMu.Lock()
	if interval == 0 {
		if saved := loadSavedInterval(); saved > 0 {
			interval = saved
		} else {
			interval = initial
		}
	}
	intervalMu.Unlock()
	if err := os.MkdirAll(screenshotsDir, 0755); err != nil {
		log.Printf("capture: cannot create screenshots dir: %v", err)
		return
	}
	prepare()
	// Schedule from the last attempt rather than a fixed ticker, so an interval
	// change applies to the wait already in progress: shortening it past the
	// time already elapsed captures right away, lengthening it extends the wait.
	last := time.Now()
	timer := time.NewTimer(currentInterval())
	defer timer.Stop()
	rearm := func(d time.Duration) {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		if d < 0 {
			d = 0
		}
		timer.Reset(d)
	}
	for {
		select {
		case <-timer.C:
		case <-intervalCh:
			rearm(time.Until(last.Add(currentInterval())))
			continue
		case <-nowCh:
			time.Sleep(2 * time.Second)
		}
		last = time.Now()
		rearm(currentInterval())
		if !monitor.IsClockedIn() || monitor.IsOnBreak() || !monitor.TrackingEnabled() {
			continue
		}
		// Capture on every tick so the timeline matches the configured interval,
		// even when the member is idle in the same app.
		take(db)
	}
}

func take(db *storage.DB) bool {
	now := time.Now()
	final := filepath.Join(screenshotsDir, fmt.Sprintf("screenshot_%s.jpg", now.Format("20060102_150405")))
	if err := captureTo(final); err != nil {
		if errors.Is(err, ErrScreenUnavailable) {
			clearError()
			return false
		}
		setError(err)
		return false
	}
	if err := db.SaveScreenshot(final); err != nil {
		os.Remove(final)
		setError(fmt.Errorf("save: %w", err))
		return false
	}
	statusMu.Lock()
	t := time.Now()
	status.LastSuccess = &t
	status.LastError = ""
	status.LastErrorAt = nil
	status.Count++
	statusMu.Unlock()
	if cb := OnCaptured; cb != nil {
		go cb()
	}
	return true
}

func clearError() {
	statusMu.Lock()
	status.LastError = ""
	status.LastErrorAt = nil
	statusMu.Unlock()
}

func setError(err error) {
	statusMu.Lock()
	changed := status.LastError != err.Error()
	t := time.Now()
	status.LastError = err.Error()
	status.LastErrorAt = &t
	statusMu.Unlock()
	if changed {
		log.Printf("capture: %v", err)
	}
}

func captureTo(final string) error {
	img, err := grab()
	if err != nil {
		return err
	}
	img = downscale(img, curMaxWidth())
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: curQuality()}); err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	tmp := final + ".part"
	if err := os.WriteFile(tmp, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return os.Rename(tmp, final)
}

func grabViaFile(run func(path string) error) (image.Image, error) {
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("hajir-shot-%d.png", time.Now().UnixNano()))
	defer os.Remove(tmp)
	if err := run(tmp); err != nil {
		return nil, err
	}
	f, err := os.Open(tmp)
	if err != nil {
		return nil, fmt.Errorf("no image produced: %w", err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return img, nil
}

func toRGBA(src image.Image) *image.RGBA {
	if r, ok := src.(*image.RGBA); ok {
		return r
	}
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst
}

func downscale(src image.Image, maxW int) image.Image {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= maxW || sw == 0 || sh == 0 {
		return src
	}
	dw := maxW
	dh := sh * dw / sw
	s := toRGBA(src)
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		sy0 := y * sh / dh
		sy1 := (y + 1) * sh / dh
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for x := 0; x < dw; x++ {
			sx0 := x * sw / dw
			sx1 := (x + 1) * sw / dw
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			var r, g, bl, n uint32
			for yy := sy0; yy < sy1; yy++ {
				off := yy*s.Stride + sx0*4
				for xx := sx0; xx < sx1; xx++ {
					r += uint32(s.Pix[off])
					g += uint32(s.Pix[off+1])
					bl += uint32(s.Pix[off+2])
					off += 4
					n++
				}
			}
			d := y*dst.Stride + x*4
			dst.Pix[d] = uint8(r / n)
			dst.Pix[d+1] = uint8(g / n)
			dst.Pix[d+2] = uint8(bl / n)
			dst.Pix[d+3] = 255
		}
	}
	return dst
}
