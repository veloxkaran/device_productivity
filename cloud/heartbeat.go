package cloud

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"my-monitor/capture"
	"my-monitor/monitor"
	"net/http"
	"runtime"
	"time"
)

const AppVersion = "2.1.6"

func (s *Syncer) heartbeatLoop(interval time.Duration, stop <-chan struct{}) {
	t := time.NewTicker(interval)
	defer t.Stop()
	s.sendHeartbeat()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			s.sendHeartbeat()
		}
	}
}

func (s *Syncer) SendOffline() {
	body, _ := json.Marshal(map[string]any{"status": "offline", "is_clocked_in": monitor.IsClockedIn(), "platform": runtime.GOOS, "app_version": AppVersion})
	req, _ := http.NewRequest(http.MethodPost, s.cfg.URL+"/api/heartbeat", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+s.cfg.SyncToken)
	req.Header.Set("Content-Type", "application/json")
	if resp, err := s.client.Do(req); err == nil {
		resp.Body.Close()
	}
}

func (s *Syncer) sendHeartbeat() {
	st := monitor.CurrentStatus()
	status := "active"
	if !st.IsActive {
		status = "idle"
	}
	if monitor.IsOnBreak() {
		status = "break"
	}
	cs := capture.CurrentStatus()
	var lastCapture string
	if cs.LastSuccess != nil {
		lastCapture = cs.LastSuccess.UTC().Format(time.RFC3339)
	}
	body, _ := json.Marshal(map[string]any{
		"screenshot_interval_seconds": int(capture.CurrentInterval() / time.Second),
		"capture_error":               cs.LastError,
		"last_capture_at":             lastCapture,
		"status":                      status,
		"idle_seconds":                st.IdleSeconds,
		"is_clocked_in":               monitor.IsClockedIn(),
		"app_name":                    st.AppName,
		"platform":                    runtime.GOOS,
		"app_version":                 AppVersion,
	})
	req, _ := http.NewRequest(http.MethodPost, s.cfg.URL+"/api/heartbeat", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+s.cfg.SyncToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		log.Printf("cloud: heartbeat: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("cloud: heartbeat: server %d", resp.StatusCode)
		return
	}
	var out struct {
		Data struct {
			ScreenshotIntervalSeconds int     `json:"screenshot_interval_seconds"`
			ScreenshotQuality         int     `json:"screenshot_quality"`
			ScreenshotMaxWidth        int     `json:"screenshot_max_width"`
			TrackingEnabled           *bool   `json:"tracking_enabled"`
			TrackingMode              *string `json:"tracking_mode"`
			ModePolicy                *string `json:"mode_policy"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<14)).Decode(&out); err != nil {
		return
	}
	if out.Data.ScreenshotIntervalSeconds > 0 {
		capture.SetInterval(time.Duration(out.Data.ScreenshotIntervalSeconds) * time.Second)
	}
	if out.Data.ScreenshotQuality > 0 {
		capture.SetQuality(out.Data.ScreenshotQuality)
	}
	if out.Data.ScreenshotMaxWidth > 0 {
		capture.SetMaxWidth(out.Data.ScreenshotMaxWidth)
	}
	if out.Data.TrackingEnabled != nil && *out.Data.TrackingEnabled != monitor.TrackingEnabled() {
		monitor.SetTrackingEnabled(*out.Data.TrackingEnabled)
		if *out.Data.TrackingEnabled {
			log.Printf("cloud: activity module enabled by employer — tracking resumed")
		} else {
			log.Printf("cloud: activity module disabled by employer — screenshots and activity paused")
		}
	}
	if out.Data.ModePolicy != nil {
		monitor.SetModePolicy(*out.Data.ModePolicy)
	}
	mode := ""
	if out.Data.TrackingMode != nil {
		mode = *out.Data.TrackingMode
	}
	s.applyTrackingMode(mode)
}

// applyTrackingMode sets the effective tracking mode. Precedence:
//  1. Employer policy lock (lock_auto / lock_manual) wins over everything.
//  2. Otherwise (flexible): a MANAGED (token-provisioned) device always tracks
//     automatically; an INTERACTIVE sign-in defaults to MANUAL — the person must
//     press Start — unless they have chosen Automatic themselves in the app.
// The hub's per-member tracking_mode is no longer used to force auto on an
// interactive device; that is what the employer policy is for.
func (s *Syncer) applyTrackingMode(mode string) {
	_ = mode // retained for API compatibility; policy + device type now decide
	switch monitor.ModePolicy() {
	case "lock_auto":
		monitor.SetAutoMode(true)
	case "lock_manual":
		monitor.SetAutoMode(false)
	default: // flexible
		if s.cfg.Managed {
			monitor.SetAutoMode(true)
		} else if !monitor.UserModeLocked() {
			monitor.SetAutoMode(false)
		}
	}
	// Auto-clock-in follows the effective mode regardless of who set it.
	if monitor.AutoMode() && monitor.TrackingEnabled() && !monitor.IsClockedIn() && !monitor.IsOnBreak() {
		uid := s.db.FirstUserID()
		if uid <= 0 {
			return
		}
		if _, err := s.db.ClockIn(uid); err != nil {
			return // already open or transient; heartbeat will retry next tick
		}
		monitor.SetClockedIn(true)
		capture.TakeNow()
		log.Printf("cloud: auto mode — clocked in automatically")
	}
}
