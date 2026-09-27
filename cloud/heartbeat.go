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

const AppVersion = "2.1.3"

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
	if out.Data.TrackingMode != nil {
		s.applyTrackingMode(*out.Data.TrackingMode)
	}
}

// applyTrackingMode reacts to the per-member mode reported by the hub. In "auto"
// mode the agent clocks in on its own whenever it is running, signed in and the
// employer's Activity module is enabled, so tracking follows the computer being
// on rather than a Clock In press. Switching back to "manual" hands control to
// the user again (a session already open is left running until stopped).
func (s *Syncer) applyTrackingMode(mode string) {
	auto := mode == "auto"
	was := monitor.AutoMode()
	if auto != was {
		monitor.SetAutoMode(auto)
		if auto {
			log.Printf("cloud: tracking mode set to AUTO by employer — tracking follows device power")
		} else {
			log.Printf("cloud: tracking mode set to MANUAL by employer — Clock In required")
		}
	}
	if auto && monitor.TrackingEnabled() && !monitor.IsClockedIn() && !monitor.IsOnBreak() {
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
