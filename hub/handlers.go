package hub

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const maxSampleGap = 5 * time.Minute

func parseClientTime(v string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(v))
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid time %q", v)
	}
	return t.UTC().Truncate(time.Second), nil
}

func decodeList[T any](r *http.Request, key string) ([]T, error) {
	body, err := readLimited(r, 8<<20)
	if err != nil {
		return nil, err
	}
	var list []T
	if err := json.Unmarshal(body, &list); err == nil {
		return list, nil
	}
	var wrapped map[string][]T
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, fmt.Errorf("body must be a JSON array")
	}
	return wrapped[key], nil
}

func (s *Server) publish(d *Device, kind string) {
	fresh, err := s.store.DeviceByID(d.ID)
	if err != nil {
		return
	}
	start, end, _ := s.dayBounds("")
	s.broker.Publish(d.CompanyID, d.UserID, "device-activity-updated", map[string]any{"kind": kind, "summary": s.summary(fresh, start, end)})
}

func (s *Server) handleDeviceMe(w http.ResponseWriter, r *http.Request, d *Device) {
	writeOK(w, "device authenticated", map[string]any{
		"device_id": d.ID, "device_name": d.Name, "company_id": d.CompanyID, "user_id": d.UserID, "employee_name": d.EmployeeName,
	})
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request, d *Device) {
	var in struct {
		Status      string `json:"status"`
		IdleSeconds int64  `json:"idle_seconds"`
		AppName     string `json:"app_name"`
		IsClockedIn *bool  `json:"is_clocked_in"`
		Platform    string `json:"platform"`
		AppVersion  string `json:"app_version"`
		// Reported by newer desktop apps; absent (nil) on old builds.
		AppliedInterval *int   `json:"screenshot_interval_seconds"`
		CaptureError    string `json:"capture_error"`
		LastCaptureAt   string `json:"last_capture_at"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&in); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "invalid JSON")
		return
	}
	if in.AppliedInterval != nil {
		s.store.SaveDeviceRuntime(d.ID, *in.AppliedInterval, truncate(in.CaptureError, 300), truncate(in.LastCaptureAt, 40))
	}
	switch in.Status {
	case "active", "idle", "break", "offline":
	default:
		writeErr(w, http.StatusUnprocessableEntity, "status must be active, idle, break or offline")
		return
	}
	if err := s.store.Heartbeat(d.ID, in.Status, in.IdleSeconds, truncate(in.AppName, 190), truncate(in.Platform, 30), truncate(in.AppVersion, 30), in.IsClockedIn); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save heartbeat")
		return
	}
	s.publish(d, "heartbeat")
	writeOK(w, "heartbeat received", map[string]any{
		"screenshot_interval_seconds": s.store.EffectiveScreenshotInterval(d.CompanyID, d.UserID),
		"tracking_enabled":            s.store.ActivityEnabled(d.CompanyID) && s.store.MemberTrackingEnabled(d.CompanyID, d.UserID),
		"tracking_mode":               s.store.MemberTrackingMode(d.CompanyID, d.UserID),
		"screenshot_quality":          s.store.GetSettings(d.CompanyID).ScreenshotQuality,
		"screenshot_max_width":        s.store.GetSettings(d.CompanyID).ScreenshotMaxWidth,
	})
}

func (s *Server) handleSyncTimeEntries(w http.ResponseWriter, r *http.Request, d *Device) {
	rows, err := decodeList[struct {
		ClockIn  string  `json:"clock_in"`
		ClockOut *string `json:"clock_out"`
	}](r, "entries")
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	open := false
	for _, row := range rows {
		in, err := parseClientTime(row.ClockIn)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		var out *time.Time
		if row.ClockOut != nil && *row.ClockOut != "" {
			t, err := parseClientTime(*row.ClockOut)
			if err != nil {
				writeErr(w, http.StatusUnprocessableEntity, err.Error())
				return
			}
			if t.Before(in) {
				continue
			}
			out = &t
		} else {
			open = true
		}
		if err := s.store.UpsertTimeEntry(d, in, out); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not save time entry")
			return
		}
	}
	s.store.SetClockedIn(d.ID, open)
	s.store.Touch(d.ID)
	s.publish(d, "time-entries")
	writeOK(w, fmt.Sprintf("%d time entries synced", len(rows)), nil)
}

func (s *Server) handleSyncActivity(w http.ResponseWriter, r *http.Request, d *Device) {
	rows, err := decodeList[struct {
		CapturedAt  string `json:"captured_at"`
		IsActive    bool   `json:"is_active"`
		IdleSeconds int64  `json:"idle_seconds"`
		AppName     string `json:"app_name"`
	}](r, "samples")
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	samples := make([]Sample, 0, len(rows))
	for _, row := range rows {
		t, err := parseClientTime(row.CapturedAt)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		samples = append(samples, Sample{CapturedAt: t, IsActive: row.IsActive, IdleSeconds: row.IdleSeconds, AppName: truncate(row.AppName, 190)})
	}
	if err := s.store.InsertSamples(d, samples); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save activity")
		return
	}
	s.store.Touch(d.ID)
	s.publish(d, "activity")
	writeOK(w, fmt.Sprintf("%d samples synced", len(samples)), nil)
}

func (s *Server) handleSyncScreenshot(w http.ResponseWriter, r *http.Request, d *Device) {
	if !s.store.ActivityEnabled(d.CompanyID) {
		// Module off: accept and discard so old desktop builds don't retry forever.
		io.Copy(io.Discard, io.LimitReader(r.Body, 32<<20))
		writeOK(w, "activity module disabled; screenshot discarded", nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
	if err := r.ParseMultipartForm(12 << 20); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "invalid upload (max 12MB)")
		return
	}
	at, err := parseClientTime(r.FormValue("captured_at"))
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if id, ok := s.store.ScreenshotExists(d.ID, at); ok {
		writeOK(w, "screenshot already synced", map[string]any{"id": id})
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "file is required")
		return
	}
	defer file.Close()
	ext := strings.ToLower(filepath.Ext(header.Filename))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".webp":
	default:
		writeErr(w, http.StatusUnprocessableEntity, "file must be png, jpg or webp")
		return
	}
	rel := filepath.Join(strconv.FormatInt(d.CompanyID, 10), strconv.FormatInt(d.UserID, 10), at.In(s.cfg.Location).Format("2006-01-02"),
		fmt.Sprintf("%d-%d%s", d.ID, at.Unix(), ext))
	abs := filepath.Join(s.cfg.DataDir, "screenshots", rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		writeErr(w, http.StatusInternalServerError, "storage error")
		return
	}
	out, err := os.Create(abs)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "storage error")
		return
	}
	size, err := io.Copy(out, file)
	out.Close()
	if err != nil {
		os.Remove(abs)
		writeErr(w, http.StatusInternalServerError, "storage error")
		return
	}
	id, err := s.store.InsertScreenshot(d, at, rel, size)
	if err != nil {
		os.Remove(abs)
		writeErr(w, http.StatusInternalServerError, "could not save screenshot")
		return
	}
	s.store.Touch(d.ID)
	s.publish(d, "screenshot")
	writeOK(w, "screenshot synced", map[string]any{"id": id})
}

func (s *Server) summary(d *Device, start, end time.Time) map[string]any {
	online := d.RevokedAt == nil && d.LastSeenAt != nil && d.LastStatus != "offline" && time.Since(*d.LastSeenAt) < s.cfg.OnlineThreshold
	status := "offline"
	if online {
		status = d.LastStatus
	}

	entries, _ := s.store.TimeEntries(d.CompanyID, d.UserID, []int64{d.ID}, start, end)
	var worked time.Duration
	now := time.Now()
	for _, e := range entries {
		from := maxTime(e.ClockIn, start)
		to := now
		if e.ClockOut != nil {
			to = *e.ClockOut
		}
		to = minTime(to, end)
		if to.After(from) {
			worked += to.Sub(from)
		}
	}

	samples, _ := s.store.Samples(d.CompanyID, 0, d.ID, start, end)
	var active, idle time.Duration
	for i := 0; i+1 < len(samples); i++ {
		gap := samples[i+1].CapturedAt.Sub(samples[i].CapturedAt)
		if gap > maxSampleGap {
			gap = maxSampleGap
		}
		if samples[i].IsActive {
			active += gap
		} else {
			idle += gap
		}
	}

	shots, lastShot := s.store.ScreenshotStats(d.ID, start, end)

	return map[string]any{
		"device_id":            d.ID,
		"device_name":          d.Name,
		"user_id":              d.UserID,
		"employee_name":        d.EmployeeName,
		"platform":             d.Platform,
		"app_version":          d.AppVersion,
		"online":               online,
		"status":               status,
		"is_clocked_in":        d.IsClockedIn,
		"current_idle_seconds": d.LastIdleSeconds,
		"current_app":          d.LastApp,
		"last_seen_at":         isoPtr(d.LastSeenAt),
		"worked_seconds":       int64(worked.Seconds()),
		"active_seconds":       int64(active.Seconds()),
		"idle_seconds":         int64(idle.Seconds()),
		"screenshots_count":    shots,
		"last_screenshot_at":   isoPtr(lastShot),
		"revoked":              d.RevokedAt != nil,
	}
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request, e *Employer, companyID int64) {
	start, end, err := s.dayBounds(r.URL.Query().Get("date"))
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	userID, _ := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	devices, err := s.store.Devices(companyID, userID, r.URL.Query().Get("include_revoked") == "1")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load devices")
		return
	}
	sc := scopeOf(r)
	list := make([]map[string]any, 0, len(devices))
	for _, d := range devices {
		if !sc.Allows(d.UserID) {
			continue
		}
		list = append(list, s.summary(d, start, end))
	}
	writeOK(w, "successfully fetched", list)
}

func (s *Server) handleCreateDevice(w http.ResponseWriter, r *http.Request, e *Employer, companyID int64) {
	var in struct {
		UserID       int64  `json:"user_id"`
		EmployeeName string `json:"employee_name"`
		Name         string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&in); err != nil || in.UserID <= 0 {
		writeErr(w, http.StatusUnprocessableEntity, "user_id is required")
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = strings.TrimSpace(in.EmployeeName + " device")
	}
	d, token, err := s.store.CreateDevice(companyID, in.UserID, e.UserID, truncate(in.EmployeeName, 150), truncate(name, 150))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not create device")
		return
	}
	s.publish(d, "created")
	writeJSON(w, http.StatusCreated, map[string]any{"status": "success", "message": "device token created, it is shown only once", "data": map[string]any{
		"device_id": d.ID, "device_name": d.Name, "user_id": d.UserID, "employee_name": d.EmployeeName, "token": token, "server_url": s.cfg.PublicURL,
	}})
}

func (s *Server) handleRevokeDevice(w http.ResponseWriter, r *http.Request, e *Employer, companyID int64) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	ok, err := s.store.RevokeDevice(companyID, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not revoke device")
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "device not found")
		return
	}
	if d, err := s.store.DeviceByID(id); err == nil {
		s.publish(d, "revoked")
	}
	writeOK(w, "device revoked", nil)
}

func (s *Server) handleTimeEntries(w http.ResponseWriter, r *http.Request, e *Employer, companyID int64) {
	q := r.URL.Query()
	start, _, err := s.dayBounds(q.Get("date_from"))
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	to := q.Get("date_to")
	if to == "" {
		to = q.Get("date_from")
	}
	_, end, err := s.dayBounds(to)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	userID, _ := strconv.ParseInt(q.Get("user_id"), 10, 64)
	if userID > 0 && denyMember(w, r, userID) {
		return
	}
	sc := scopeOf(r)
	entries, err := s.store.TimeEntries(companyID, userID, nil, start, end)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load time entries")
		return
	}
	list := make([]map[string]any, 0, len(entries))
	for _, en := range entries {
		if !sc.Allows(en.UserID) {
			continue
		}
		dur := time.Since(en.ClockIn)
		if en.ClockOut != nil {
			dur = en.ClockOut.Sub(en.ClockIn)
		}
		list = append(list, map[string]any{
			"id": en.ID, "device_id": en.DeviceID, "user_id": en.UserID,
			"clock_in": en.ClockIn.In(s.cfg.Location).Format(time.RFC3339), "clock_out": isoPtr(en.ClockOut),
			"duration_seconds": int64(dur.Seconds()), "is_open": en.ClockOut == nil,
		})
	}
	writeOK(w, "successfully fetched", list)
}

func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request, e *Employer, companyID int64) {
	userID, _ := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	if userID <= 0 {
		writeErr(w, http.StatusUnprocessableEntity, "user_id is required")
		return
	}
	if denyMember(w, r, userID) {
		return
	}
	start, end, err := s.dayBounds(r.URL.Query().Get("date"))
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	samples, err := s.store.Samples(companyID, userID, 0, start, end)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load timeline")
		return
	}
	list := make([]map[string]any, 0, len(samples))
	for _, sm := range samples {
		list = append(list, map[string]any{
			"captured_at": sm.CapturedAt.In(s.cfg.Location).Format(time.RFC3339), "is_active": sm.IsActive, "idle_seconds": sm.IdleSeconds, "app_name": sm.AppName,
		})
	}
	writeOK(w, "successfully fetched", list)
}

func (s *Server) handleScreenshots(w http.ResponseWriter, r *http.Request, e *Employer, companyID int64) {
	userID, _ := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	if userID <= 0 {
		writeErr(w, http.StatusUnprocessableEntity, "user_id is required")
		return
	}
	if denyMember(w, r, userID) {
		return
	}
	start, end, err := s.dayBounds(r.URL.Query().Get("date"))
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	shots, err := s.store.Screenshots(companyID, userID, start, end)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load screenshots")
		return
	}
	list := make([]map[string]any, 0, len(shots))
	for _, sh := range shots {
		list = append(list, map[string]any{
			"id": sh.ID, "device_id": sh.DeviceID, "captured_at": sh.CapturedAt.In(s.cfg.Location).Format(time.RFC3339), "size_bytes": sh.SizeBytes, "url": s.screenshotURL(sh.ID),
		})
	}
	writeOK(w, "successfully fetched", list)
}

func truncate(v string, n int) string {
	v = strings.TrimSpace(v)
	r := []rune(v)
	if len(r) > n {
		return string(r[:n])
	}
	return v
}

var displayLocation = time.UTC

func isoPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.In(displayLocation).Format(time.RFC3339)
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (s *Server) handleSyncBreaks(w http.ResponseWriter, r *http.Request, d *Device) {
	rows, err := decodeList[struct {
		Start string  `json:"start"`
		End   *string `json:"end"`
	}](r, "breaks")
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	for _, row := range rows {
		start, err := parseClientTime(row.Start)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		var end *time.Time
		if row.End != nil && *row.End != "" {
			t, err := parseClientTime(*row.End)
			if err != nil {
				writeErr(w, http.StatusUnprocessableEntity, err.Error())
				return
			}
			if t.Before(start) {
				continue
			}
			end = &t
		}
		if err := s.store.UpsertBreak(d, start, end); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not save break")
			return
		}
	}
	s.store.Touch(d.ID)
	s.publish(d, "breaks")
	writeOK(w, fmt.Sprintf("%d breaks synced", len(rows)), nil)
}

func (s *Server) handleDeviceRegister(w http.ResponseWriter, r *http.Request) {
	emp, err := s.verifier.Verify(bearer(r))
	if errors.Is(err, errActivityUnavailable) {
		log.Printf("hub: device register: %v", err)
		writeErr(w, http.StatusServiceUnavailable, "Hajir is temporarily unavailable. Please try again shortly.")
		return
	}
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "Your Hajir session is not valid. Please sign in again.")
		return
	}
	var in struct {
		CompanyID  int64  `json:"company_id"`
		DeviceName string `json:"device_name"`
		Platform   string `json:"platform"`
	}
	json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&in)
	var company *EmployerCompany
	if in.CompanyID > 0 {
		if c, ok := emp.Companies[in.CompanyID]; ok {
			company = &c
		}
	} else if len(emp.Companies) == 1 {
		for _, c := range emp.Companies {
			cc := c
			company = &cc
		}
	}
	if company == nil {
		if len(emp.Companies) == 0 {
			writeErr(w, http.StatusForbidden, "Your account is not an active employee of any company.")
			return
		}
		list := []EmployerCompany{}
		for _, c := range emp.Companies {
			list = append(list, c)
		}
		writeJSON(w, http.StatusConflict, map[string]any{"status": "error", "message": "choose a company", "companies": list})
		return
	}
	name := truncate(in.DeviceName, 150)
	if name == "" {
		name = "Desktop"
	}
	d, token, err := s.store.RotateDeviceToken(company.ID, emp.UserID, name, truncate(emp.Name, 150), truncate(in.Platform, 30))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not register device")
		return
	}
	s.publish(d, "created")
	writeOK(w, "device registered", map[string]any{
		"token": token, "device_id": d.ID, "company_id": company.ID, "company_name": company.Name, "user_id": emp.UserID, "employee_name": emp.Name,
	})
}

func (s *Server) handleSyncIdleReports(w http.ResponseWriter, r *http.Request, d *Device) {
	rows, err := decodeList[struct {
		Start  string `json:"start"`
		End    string `json:"end"`
		Reason string `json:"reason"`
		Note   string `json:"note"`
	}](r, "reports")
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	for _, row := range rows {
		st, err1 := parseClientTime(row.Start)
		en, err2 := parseClientTime(row.End)
		if err1 != nil || err2 != nil || en.Before(st) {
			continue
		}
		if err := s.store.InsertIdleReport(d, st, en, truncate(row.Reason, 60), truncate(row.Note, 255)); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not save idle report")
			return
		}
	}
	s.store.Touch(d.ID)
	s.publish(d, "idle-report")
	writeOK(w, fmt.Sprintf("%d idle reports synced", len(rows)), nil)
}

// handleInternalModules is called by Laravel (server-to-server) when a super
// admin switches a company's Activity module on or off. Authenticated with the
// shared HUB_INTERNAL_KEY; disabled entirely when no key is configured.
func (s *Server) handleInternalModules(w http.ResponseWriter, r *http.Request) {
	if s.cfg.InternalKey == "" {
		writeErr(w, http.StatusServiceUnavailable, "HUB_INTERNAL_KEY is not configured on the hub")
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Hub-Key")), []byte(s.cfg.InternalKey)) != 1 {
		writeErr(w, http.StatusUnauthorized, "invalid hub key")
		return
	}
	companyID, _ := strconv.ParseInt(r.PathValue("company"), 10, 64)
	if companyID <= 0 {
		writeErr(w, http.StatusUnprocessableEntity, "invalid company")
		return
	}
	var in struct {
		ActivityEnabled *bool `json:"activity_enabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<12)).Decode(&in); err != nil || in.ActivityEnabled == nil {
		writeErr(w, http.StatusUnprocessableEntity, "activity_enabled (bool) is required")
		return
	}
	if err := s.store.SetActivityEnabled(companyID, *in.ActivityEnabled); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save module state")
		return
	}
	log.Printf("hub: company %d activity module → %v", companyID, *in.ActivityEnabled)
	writeOK(w, "module state saved", map[string]any{"company_id": companyID, "activity_enabled": *in.ActivityEnabled})
}

func (s *Server) handleDeleteScreenshot(w http.ResponseWriter, r *http.Request, e *Employer, companyID int64) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	sh, err := s.store.ScreenshotByID(id)
	if id <= 0 || err != nil || sh.CompanyID != companyID {
		writeErr(w, http.StatusNotFound, "screenshot not found")
		return
	}
	if err := s.store.DeleteScreenshot(sh.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not delete screenshot")
		return
	}
	s.removeScreenshotFile(sh.Path)
	s.broker.Publish(companyID, sh.UserID, "device-activity-updated", map[string]any{"kind": "screenshot-deleted", "summary": map[string]any{"user_id": sh.UserID, "screenshot_id": sh.ID}})
	writeOK(w, "screenshot deleted", map[string]any{"id": sh.ID})
}
