package cloud

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"my-monitor/capture"
	"my-monitor/monitor"
	"my-monitor/settings"
	"my-monitor/storage"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Config struct {
	URL       string `json:"url"`
	SyncToken string `json:"sync_token"` // Candidate Passport Bearer token
	CompanyID int64  `json:"company_id"`
}

// ── Package-level status (read by web UI) ─────────────────────────

var (
	statusMu   sync.RWMutex
	isOnline   bool
	lastSyncAt time.Time
)

// IsOnline reports whether the last connectivity probe succeeded.
func IsOnline() bool {
	statusMu.RLock()
	defer statusMu.RUnlock()
	return isOnline
}

// LastSyncAt returns the time of the last completed sync cycle.
func LastSyncAt() time.Time {
	statusMu.RLock()
	defer statusMu.RUnlock()
	return lastSyncAt
}

func setOnline(v bool) {
	statusMu.Lock()
	isOnline = v
	statusMu.Unlock()
}

func markSynced() {
	statusMu.Lock()
	lastSyncAt = time.Now()
	statusMu.Unlock()
}

// ─────────────────────────────────────────────────────────────────

type Syncer struct {
	cfg                 Config
	db                  *storage.DB
	machineID           string
	client              *http.Client
	triggerCh           chan struct{}
	webMu               sync.Mutex // serialises pullWebEntries + device-session flag writes
	webSessionActive    bool       // true when a Hajir-dashboard-initiated web session is active
	deviceSessionActive bool       // true when device clock-in (localhost:8080) is active
}

func NewSyncer(cfg Config, db *storage.DB, machineID string) *Syncer {
	s := &Syncer{
		cfg:       cfg,
		db:        db,
		machineID: machineID,
		client:    &http.Client{Timeout: 30 * time.Second},
		triggerCh: make(chan struct{}, 1),
	}
	activeSyncer = s
	return s
}

// activeSyncer is the running instance — used by TriggerSync.
var activeSyncer *Syncer

// TriggerSync requests an immediate sync cycle. Safe to call from any goroutine.
// Non-blocking: if a trigger is already queued the extra signal is dropped.
func TriggerSync() {
	if activeSyncer == nil {
		return
	}
	select {
	case activeSyncer.triggerCh <- struct{}{}:
	default:
	}
}

// SetDeviceSessionActive tells the syncer whether a device-local clock-in is active.
// When true, web clock-out detection from Hajir will not stop monitoring.
func SetDeviceSessionActive(active bool) {
	if activeSyncer == nil {
		return
	}
	activeSyncer.webMu.Lock()
	activeSyncer.deviceSessionActive = active
	activeSyncer.webMu.Unlock()
}

// WebClockIn mirrors a device clock-in to the Hajir web attendance API.
// Safe to call from any goroutine; returns immediately if not configured.
func WebClockIn() {
	if activeSyncer == nil {
		return
	}
	s := activeSyncer
	body, _ := json.Marshal(map[string]interface{}{"company_id": s.cfg.CompanyID})
	req, _ := http.NewRequest("POST", s.cfg.URL+"/api/v2/productivity/attendance/web/clock-in", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+s.cfg.SyncToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		log.Printf("cloud: mirror clock-in: %v", err)
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		log.Printf("cloud: mirror clock-in: server %d: %s", resp.StatusCode, b)
		return
	}
	log.Printf("cloud: device clock-in mirrored to Hajir")
}

// WebClockOut mirrors a device clock-out to the Hajir web attendance API.
// Safe to call from any goroutine; returns immediately if not configured.
func WebClockOut() {
	if activeSyncer == nil {
		return
	}
	s := activeSyncer
	body, _ := json.Marshal(map[string]interface{}{"company_id": s.cfg.CompanyID})
	req, _ := http.NewRequest("POST", s.cfg.URL+"/api/v2/productivity/attendance/web/clock-out", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+s.cfg.SyncToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		log.Printf("cloud: mirror clock-out: %v", err)
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 && resp.StatusCode != 404 {
		log.Printf("cloud: mirror clock-out: server %d: %s", resp.StatusCode, b)
		return
	}
	log.Printf("cloud: device clock-out mirrored to Hajir")
}

// Start runs a sync loop every interval. Call in a goroutine.
func (s *Syncer) Start(interval time.Duration) {
	log.Printf("cloud: sync enabled → %s (interval %s)", s.cfg.URL, interval)
	s.run()
	go s.webSessionLoop() // fast-poll web clock state every 30s
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.run()
		case <-s.triggerCh:
			log.Printf("cloud: manual sync triggered")
			s.run()
			// drain any queued extra trigger
			select {
			case <-s.triggerCh:
			default:
			}
		}
	}
}

// webSessionLoop polls web clock-in/out state every 30 seconds so that a
// clock-out (or clock-in) done on the Hajir dashboard reflects in the agent
// promptly instead of waiting for the full 5-minute sync cycle.
func (s *Syncer) webSessionLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if err := s.pullWebEntries(); err != nil {
			log.Printf("cloud: web session poll: %v", err)
		}
	}
}

// probe does a fast HEAD request to check server reachability.
// Any HTTP response (even 4xx) means the server is up.
func (s *Syncer) probe() bool {
	probe := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("HEAD", s.cfg.URL, nil)
	if err != nil {
		return false
	}
	resp, err := probe.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

func (s *Syncer) run() {
	if !s.probe() {
		log.Printf("cloud: offline — data queued locally, will sync when connectivity is restored")
		setOnline(false)
		// Still enforce retention locally even when offline.
		s.enforceRetention()
		return
	}
	setOnline(true)

	// Fetch latest settings from Hajir first so everything below uses them.
	if err := s.syncSettings(); err != nil {
		log.Printf("cloud: settings: %v", err)
	}

	// Enforce retention before uploading so we don't re-upload deleted data.
	s.enforceRetention()

	if err := s.syncScreenshots(); err != nil {
		log.Printf("cloud: screenshots: %v", err)
	}
	if err := s.syncTimeEntries(); err != nil {
		log.Printf("cloud: time entries: %v", err)
	}
	if err := s.pullWebEntries(); err != nil {
		log.Printf("cloud: pull web entries: %v", err)
	}
	if err := s.syncActivitySummaries(); err != nil {
		log.Printf("cloud: activity summaries: %v", err)
	}

	markSynced()
}

// ── Screenshots ───────────────────────────────────────────────────

func (s *Syncer) syncScreenshots() error {
	rows, err := s.db.GetUnsyncedScreenshots(20)
	if err != nil {
		return err
	}
	for _, ss := range rows {
		if err := s.uploadScreenshot(ss); err != nil {
			log.Printf("cloud: screenshot %d: %v", ss.ID, err)
			continue
		}
		if err := s.db.MarkScreenshotSynced(ss.ID); err != nil {
			log.Printf("cloud: mark synced %d: %v", ss.ID, err)
		}
	}
	return nil
}

func (s *Syncer) uploadScreenshot(ss storage.Screenshot) error {
	f, err := os.Open(ss.FilePath)
	if err != nil {
		return fmt.Errorf("open %s: %w", ss.FilePath, err)
	}
	defer f.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("captured_at", ss.CreatedAt.Format(time.RFC3339))

	fw, err := mw.CreateFormFile("file", filepath.Base(ss.FilePath))
	if err != nil {
		return err
	}
	if _, err := io.Copy(fw, f); err != nil {
		return err
	}
	mw.Close()

	req, _ := http.NewRequest("POST", s.cfg.URL+"/api/v2/productivity/screenshots/sync", &buf)
	req.Header.Set("Authorization", "Bearer "+s.cfg.SyncToken)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Machine-ID", s.machineID)

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server %d: %s", resp.StatusCode, body)
	}
	return nil
}

// ── Time entries ──────────────────────────────────────────────────
//
// Each time entry is tracked individually in time_entries_sync.
// An entry is re-synced if it was previously uploaded with clock_out=NULL
// (session still open) but now has a clock_out (session closed offline).
// The server uses upsert on clock_in so duplicate uploads are safe.

func (s *Syncer) syncTimeEntries() error {
	entries, err := s.db.GetUnsyncedTimeEntries()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}

	type row struct {
		ClockIn  string  `json:"clock_in"`
		ClockOut *string `json:"clock_out"`
	}
	var payload []row
	for _, e := range entries {
		r := row{ClockIn: e.ClockIn.Format(time.RFC3339)}
		if e.ClockOut != nil {
			t := e.ClockOut.Format(time.RFC3339)
			r.ClockOut = &t
		}
		payload = append(payload, r)
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", s.cfg.URL+"/api/v2/productivity/attendance/sync", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+s.cfg.SyncToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Machine-ID", s.machineID)

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server %d: %s", resp.StatusCode, b)
	}

	for _, e := range entries {
		if err := s.db.MarkTimeEntrySynced(e.ID, e.ClockOut); err != nil {
			log.Printf("cloud: mark time entry %d synced: %v", e.ID, err)
		}
	}
	return nil
}

// ── Settings sync ─────────────────────────────────────────────────

func (s *Syncer) syncSettings() error {
	req, err := http.NewRequest("GET", s.cfg.URL+"/api/v2/productivity/settings/effective", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.SyncToken)
	req.Header.Set("X-Machine-ID", s.machineID)

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil // server has no settings yet — keep local defaults
	}

	var fetched settings.Settings
	if err := json.NewDecoder(resp.Body).Decode(&fetched); err != nil {
		return err
	}
	return settings.Save(fetched)
}

// ── Retention enforcement ─────────────────────────────────────────

func (s *Syncer) enforceRetention() {
	days := settings.RetentionDays()
	deleted, err := s.db.EnforceRetention(days)
	if err != nil {
		log.Printf("cloud: retention: db: %v", err)
	}
	for _, path := range deleted {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			log.Printf("cloud: retention: remove %s: %v", path, err)
		}
	}
	if len(deleted) > 0 {
		log.Printf("cloud: retention: purged %d screenshot(s) older than %d days", len(deleted), days)
	}
}

// ── Web entries pull ──────────────────────────────────────────────

// parseFlexTime parses the datetime formats Laravel may return:
//   "2026-06-15T08:42:18.000000Z" (RFC3339Nano, most common with Carbon)
//   "2026-06-15T08:42:18Z"        (RFC3339)
//   "2026-06-15 08:42:18"         (MySQL raw)
func parseFlexTime(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse time %q", s)
}

func (s *Syncer) pullWebEntries() error {
	s.webMu.Lock()
	defer s.webMu.Unlock()
	lastPull, err := s.db.GetLastWebPullTime()
	if err != nil {
		return err
	}

	endpoint := s.cfg.URL + "/api/v2/productivity/attendance/web/entries"
	if !lastPull.IsZero() {
		endpoint += "?since=" + url.QueryEscape(lastPull.Format(time.RFC3339))
	}

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.SyncToken)
	req.Header.Set("X-Machine-ID", s.machineID)

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil // device not approved or no entries yet
	}

	var result struct {
		Entries []struct {
			ClockIn  string  `json:"clock_in"`
			ClockOut *string `json:"clock_out"`
		} `json:"entries"`
		HasOpenSession bool `json:"has_open_session"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	for _, e := range result.Entries {
		clockIn, err := parseFlexTime(e.ClockIn)
		if err != nil {
			log.Printf("cloud: web entry: bad clock_in %q: %v", e.ClockIn, err)
			continue
		}
		var clockOut *time.Time
		if e.ClockOut != nil {
			if t, err := parseFlexTime(*e.ClockOut); err == nil {
				clockOut = &t
			}
		}
		if err := s.db.UpsertWebEntry(clockIn, clockOut); err != nil {
			log.Printf("cloud: web entry upsert: %v", err)
		}
	}

	// Sync local clock-in state with web session state.
	// webSessionActive is only set when the Hajir dashboard initiated the session;
	// deviceSessionActive is set when the local device clock-in (localhost:8080) is active.
	// This way, a web clock-out from the dashboard stops monitoring only when the
	// device itself didn't initiate the session.
	if result.HasOpenSession {
		if !s.deviceSessionActive {
			s.webSessionActive = true
		}
		if !monitor.IsClockedIn() {
			monitor.SetClockedIn(true)
			capture.TriggerNow()
			log.Printf("cloud: web clock-in detected — productivity monitoring started")
		}
	} else if s.webSessionActive {
		s.webSessionActive = false
		monitor.SetClockedIn(false)
		log.Printf("cloud: web clock-out detected — productivity monitoring stopped")
	}

	return s.db.SetLastWebPullTime(time.Now())
}

// ── Activity daily summaries ──────────────────────────────────────

func (s *Syncer) syncActivitySummaries() error {
	lastSync, err := s.db.GetLastSyncTime("activity_daily_summaries")
	if err != nil {
		return err
	}

	summaries, err := s.db.GetDailySummariesSince(lastSync)
	if err != nil {
		return err
	}
	if len(summaries) == 0 {
		return nil
	}

	type row struct {
		WorkDate          string  `json:"work_date"`
		TotalIntervals    int64   `json:"total_intervals"`
		ActiveIntervals   int64   `json:"active_intervals"`
		IdleIntervals     int64   `json:"idle_intervals"`
		MouseIntervals    int64   `json:"mouse_intervals"`
		KeyboardIntervals int64   `json:"keyboard_intervals"`
		TotalActiveSec    int64   `json:"total_active_sec"`
		TotalIdleSec      int64   `json:"total_idle_sec"`
		ProductivityPct   float64 `json:"productivity_pct"`
	}
	var payload []row
	for _, s := range summaries {
		pct := 0.0
		if s.TotalIntervals > 0 {
			pct = float64(s.ActiveIntervals) / float64(s.TotalIntervals) * 100
		}
		payload = append(payload, row{
			WorkDate:          s.WorkDate,
			TotalIntervals:    s.TotalIntervals,
			ActiveIntervals:   s.ActiveIntervals,
			IdleIntervals:     s.IdleIntervals,
			MouseIntervals:    s.MouseIntervals,
			KeyboardIntervals: s.KeyboardIntervals,
			TotalActiveSec:    s.TotalActiveSec,
			TotalIdleSec:      s.TotalIdleSec,
			ProductivityPct:   pct,
		})
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", s.cfg.URL+"/api/v2/productivity/activity/sync", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+s.cfg.SyncToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Machine-ID", s.machineID)

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server %d: %s", resp.StatusCode, b)
	}

	return s.db.SetLastSyncTime("activity_daily_summaries", time.Now())
}
