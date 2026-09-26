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
	// Managed is true for silently provisioned devices (employer pushed a device
	// token; no interactive login). Managed devices run headless with no window.
	Managed bool `json:"managed,omitempty"`
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

func (s *Syncer) loop(interval time.Duration, stop <-chan struct{}, kick <-chan struct{}) {
	log.Printf("cloud: sync enabled → %s (interval %s)", s.cfg.URL, interval)
	t := time.NewTicker(interval)
	defer t.Stop()
	s.run()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			s.run()
		case <-kick:
			s.run()
		}
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
	if err := s.syncBreaks(); err != nil {
		log.Printf("cloud: breaks: %v", err)
	}
	if err := s.syncIdleReports(); err != nil {
		log.Printf("cloud: idle reports: %v", err)
	}
}

// ── Screenshots ───────────────────────────────────────────────────

type permanentError struct{ msg string }

func (e permanentError) Error() string { return e.msg }

func (s *Syncer) syncScreenshots() error {
	rows, err := s.db.GetUnsyncedScreenshots(20)
	if err != nil {
		return err
	}
	for _, ss := range rows {
		err := s.uploadScreenshot(ss)
		if err == nil {
			s.db.MarkScreenshotSynced(ss.ID)
			continue
		}
		if _, ok := err.(permanentError); ok {
			log.Printf("cloud: screenshot %d skipped: %v", ss.ID, err)
			s.db.MarkScreenshotSynced(ss.ID)
			continue
		}
		return fmt.Errorf("screenshot %d: %w", ss.ID, err)
	}
	return nil
}

func (s *Syncer) uploadScreenshot(ss storage.Screenshot) error {
	f, err := os.Open(ss.FilePath)
	if err != nil {
		return permanentError{fmt.Sprintf("file missing: %s", ss.FilePath)}
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() > 11<<20 {
		return permanentError{fmt.Sprintf("file too large (%d bytes)", st.Size())}
	}

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
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		msg := fmt.Sprintf("server %d: %s", resp.StatusCode, body)
		if resp.StatusCode == http.StatusRequestEntityTooLarge || resp.StatusCode == http.StatusUnprocessableEntity || resp.StatusCode == http.StatusBadRequest {
			return permanentError{msg}
		}
		return fmt.Errorf("%s", msg)
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
		AppName     string `json:"app_name,omitempty"`
		CapturedAt  string `json:"captured_at"`
	}
	var payload []row
	for _, l := range logs {
		payload = append(payload, row{
			IsActive:    l.IsActive,
			IdleSeconds: l.IdleSeconds,
			AppName:     l.AppName,
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

func (s *Syncer) syncBreaks() error {
	lastSync, err := s.db.GetLastSyncTime("breaks")
	if err != nil {
		return err
	}
	list, err := s.db.GetBreaksSince(lastSync)
	if err != nil || len(list) == 0 {
		return err
	}
	type row struct {
		Start string  `json:"start"`
		End   *string `json:"end"`
	}
	payload := make([]row, 0, len(list))
	for _, b := range list {
		r := row{Start: b.Start.Format(time.RFC3339)}
		if b.End != nil {
			t := b.End.Format(time.RFC3339)
			r.End = &t
		}
		payload = append(payload, r)
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", s.cfg.URL+"/api/sync/breaks", bytes.NewReader(body))
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
	return s.db.SetLastSyncTime("breaks", time.Now())
}

func (s *Syncer) syncIdleReports() error {
	list, err := s.db.UnsyncedIdleReports()
	if err != nil || len(list) == 0 {
		return err
	}
	type row struct {
		Start  string `json:"start"`
		End    string `json:"end"`
		Reason string `json:"reason"`
		Note   string `json:"note"`
	}
	payload := make([]row, 0, len(list))
	for _, r := range list {
		payload = append(payload, row{Start: r.Start.Format(time.RFC3339), End: r.End.Format(time.RFC3339), Reason: r.Reason, Note: r.Note})
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", s.cfg.URL+"/api/sync/idle-reports", bytes.NewReader(body))
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
	ids := make([]int64, 0, len(list))
	for _, r := range list {
		ids = append(ids, r.ID)
	}
	return s.db.MarkIdleReportsSynced(ids)
}
