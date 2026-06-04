package cloud

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"my-monitor/storage"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	URL       string `json:"url"`
	SyncToken string `json:"sync_token"`
}

type Syncer struct {
	cfg    Config
	db     *storage.DB
	client *http.Client
}

func NewSyncer(cfg Config, db *storage.DB) *Syncer {
	return &Syncer{
		cfg:    cfg,
		db:     db,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// Start runs a sync loop every interval. Call in a goroutine.
func (s *Syncer) Start(interval time.Duration) {
	log.Printf("cloud: sync enabled → %s (interval %s)", s.cfg.URL, interval)
	s.run()
	for range time.Tick(interval) {
		s.run()
	}
}

func (s *Syncer) run() {
	if err := s.syncScreenshots(); err != nil {
		log.Printf("cloud: screenshots: %v", err)
	}
	if err := s.syncTimeEntries(); err != nil {
		log.Printf("cloud: time entries: %v", err)
	}
	if err := s.syncActivity(); err != nil {
		log.Printf("cloud: activity: %v", err)
	}
}

// ── Screenshots ───────────────────────────────────────────────────

func (s *Syncer) syncScreenshots() error {
	// Get all unsynced screenshots from local DB
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

	req, _ := http.NewRequest("POST", s.cfg.URL+"/api/sync/screenshots", &buf)
	req.Header.Set("Authorization", "Bearer "+s.cfg.SyncToken)
	req.Header.Set("Content-Type", mw.FormDataContentType())

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

func (s *Syncer) syncTimeEntries() error {
	lastSync, err := s.db.GetLastSyncTime("time_entries")
	if err != nil {
		return err
	}

	entries, err := s.db.GetTimeEntriesSince(lastSync)
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
	req, _ := http.NewRequest("POST", s.cfg.URL+"/api/sync/time-entries", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+s.cfg.SyncToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server %d: %s", resp.StatusCode, b)
	}

	return s.db.SetLastSyncTime("time_entries", time.Now())
}

// ── Activity ──────────────────────────────────────────────────────

func (s *Syncer) syncActivity() error {
	lastSync, err := s.db.GetLastSyncTime("activity_logs")
	if err != nil {
		return err
	}

	logs, err := s.db.GetActivityLogsSince(lastSync)
	if err != nil {
		return err
	}
	if len(logs) == 0 {
		return nil
	}

	type row struct {
		IsActive    bool   `json:"is_active"`
		IdleSeconds int64  `json:"idle_seconds"`
		CapturedAt  string `json:"captured_at"`
	}
	var payload []row
	for _, l := range logs {
		payload = append(payload, row{
			IsActive:    l.IsActive,
			IdleSeconds: l.IdleSeconds,
			CapturedAt:  l.CreatedAt.Format(time.RFC3339),
		})
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", s.cfg.URL+"/api/sync/activity", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+s.cfg.SyncToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server %d: %s", resp.StatusCode, b)
	}

	return s.db.SetLastSyncTime("activity_logs", time.Now())
}
