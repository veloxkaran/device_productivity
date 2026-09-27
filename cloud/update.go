package cloud

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"my-monitor/monitor"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// latestManifest is the JSON the hub serves at /downloads/latest.json.
// Assets are keyed by "<goos>/<goarch>" and point at RAW binaries (not the
// human installers), because the updater replaces the running binary in place.
type latestManifest struct {
	Version string                 `json:"version"`
	Notes   string                 `json:"notes"`
	Assets  map[string]latestAsset `json:"assets"`
}

type latestAsset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// StartAutoUpdate launches a background loop that keeps this device on the
// latest published version. It is safe to call once at startup. Updates are
// only applied when the device is idle (not actively clocked in) so tracking
// is never interrupted mid-session.
func StartAutoUpdate(cfg Config) {
	if cfg.URL == "" {
		return
	}
	go func() {
		// Small delay so startup/sync settles first.
		time.Sleep(45 * time.Second)
		for {
			if err := checkAndApply(cfg); err != nil {
				log.Printf("autoupdate: %v", err)
			}
			time.Sleep(6 * time.Hour)
		}
	}()
}

func checkAndApply(cfg Config) error {
	man, err := fetchManifest(cfg.URL)
	if err != nil {
		return err
	}
	if !isNewer(man.Version, AppVersion) {
		return nil // already up to date
	}
	asset, ok := man.Assets[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok || asset.URL == "" {
		return nil // nothing published for this platform
	}
	// Don't interrupt an active tracking session; try again next cycle.
	if monitor.IsClockedIn() {
		log.Printf("autoupdate: %s available, deferring (tracking in progress)", man.Version)
		return nil
	}
	log.Printf("autoupdate: updating %s -> %s", AppVersion, man.Version)

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.EvalSymlinks(exe)

	tmp := exe + ".new"
	if err := download(asset.URL, tmp, asset.SHA256); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0755); err != nil {
		os.Remove(tmp)
		return err
	}
	// Platform-specific swap + restart (see update_unix.go / update_windows.go).
	return applyAndRestart(exe, tmp)
}

func fetchManifest(hub string) (*latestManifest, error) {
	url := strings.TrimRight(hub, "/") + "/downloads/latest.json"
	c := &http.Client{Timeout: 20 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil2err(resp.StatusCode)
	}
	var m latestManifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

type statusErr int

func (e statusErr) Error() string { return "latest.json HTTP " + strconv.Itoa(int(e)) }
func nil2err(code int) error      { return statusErr(code) }

func download(url, dest, wantSHA string) error {
	c := &http.Client{Timeout: 5 * time.Minute}
	resp, err := c.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return statusErr(resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if wantSHA != "" && !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), wantSHA) {
		return statusErr(-1) // checksum mismatch
	}
	return nil
}

// isNewer reports whether version a is strictly greater than b (dotted ints,
// e.g. "2.1.0" > "2.0.3"). Non-numeric parts are treated as 0.
func isNewer(a, b string) bool {
	pa, pb := splitVer(a), splitVer(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func splitVer(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.Split(v, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, _ := strconv.Atoi(strings.TrimFunc(p, func(r rune) bool { return r < '0' || r > '9' }))
		out[i] = n
	}
	return out
}
