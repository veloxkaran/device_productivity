package web

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"log"
	"my-monitor/auth"
	"my-monitor/capture"
	"my-monitor/cloud"
	"my-monitor/monitor"
	"my-monitor/registration"
	"my-monitor/settings"
	"my-monitor/storage"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed templates/*.html
var templateFS embed.FS

var tmpl = template.Must(template.ParseFS(templateFS, "templates/*.html"))

// ── Session store ─────────────────────────────────────────────────

type sessionStore struct {
	mu   sync.RWMutex
	data map[string]int64
}

var sessions = &sessionStore{data: make(map[string]int64)}

func (s *sessionStore) create(userID int64) string {
	b := make([]byte, 16)
	rand.Read(b)
	id := hex.EncodeToString(b)
	s.mu.Lock()
	s.data[id] = userID
	s.mu.Unlock()
	return id
}

func (s *sessionStore) get(id string) (int64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	uid, ok := s.data[id]
	return uid, ok
}

func (s *sessionStore) delete(id string) {
	s.mu.Lock()
	delete(s.data, id)
	s.mu.Unlock()
}

func getUserID(r *http.Request) (int64, bool) {
	cookie, err := r.Cookie("session")
	if err != nil || cookie.Value == "" {
		return 0, false
	}
	return sessions.get(cookie.Value)
}

// ── Setup state ───────────────────────────────────────────────────

const setupCompleteFile = "data/.setup_complete"

func isSetupComplete() bool {
	_, err := os.Stat(setupCompleteFile)
	return err == nil
}

// ── Routes ────────────────────────────────────────────────────────

func Start(db *storage.DB, addr string) {
	mux := http.NewServeMux()

	// Static files
	mux.Handle("/screenshots/", http.StripPrefix("/screenshots/",
		http.FileServer(http.Dir("data/screenshots"))))

	// Auth
	mux.HandleFunc("/login", loginHandler(db))
	mux.HandleFunc("/logout", logoutHandler)

	// Setup wizard
	mux.HandleFunc("/setup", requireAuth(setupHandler))

	// API — setup
	mux.HandleFunc("/api/permissions", requireAuth(apiPermissionsHandler))
	mux.HandleFunc("/api/test-screenshot", requireAuth(apiTestScreenshotHandler))
	mux.HandleFunc("/api/launchagent/status", requireAuth(apiAutoStartStatusHandler))
	mux.HandleFunc("/api/launchagent/install", requireAuth(apiAutoStartInstallHandler))
	mux.HandleFunc("/api/launchagent/remove", requireAuth(apiAutoStartRemoveHandler))
	mux.HandleFunc("/api/change-password", requireAuth(apiChangePasswordHandler(db)))
	mux.HandleFunc("/api/setup/complete", requireAuth(apiSetupCompleteHandler))

	// Time tracker page + API
	mux.HandleFunc("/time", requireAuth(timePageHandler))
	mux.HandleFunc("/api/time/status", requireAuth(apiTimeStatusHandler(db)))
	mux.HandleFunc("/api/time/clockin", requireAuth(apiClockInHandler(db)))
	mux.HandleFunc("/api/time/clockout", requireAuth(apiClockOutHandler(db)))
	mux.HandleFunc("/api/time/entries", requireAuth(apiTimeEntriesHandler(db)))

	// Screenshot interval settings
	mux.HandleFunc("/api/settings/screenshot-interval", requireAuth(apiScreenshotIntervalHandler))

	// Sync status + manual trigger
	mux.HandleFunc("/api/sync/status", requireAuth(apiSyncStatusHandler(db)))
	mux.HandleFunc("/api/sync/trigger", requireAuth(apiSyncTriggerHandler))

	// Device approval status check
	mux.HandleFunc("/api/device/check-status", requireAuth(apiDeviceCheckStatusHandler(db)))

	// Cloud setup
	mux.HandleFunc("/cloud", requireAuth(cloudSetupHandler(db)))

	// Dashboard (root)
	mux.HandleFunc("/", requireAuth(dashboardHandler(db)))

	log.Printf("web: listening on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("web: %v", err)
	}
}

// ── Middleware ────────────────────────────────────────────────────

func requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session")
		if err != nil || cookie.Value == "" {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		if _, ok := sessions.get(cookie.Value); !ok {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next(w, r)
	}
}

// ── Auth handlers ─────────────────────────────────────────────────

func loginHandler(db *storage.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			tmpl.ExecuteTemplate(w, "login.html", nil)
			return
		}
		username := r.FormValue("username")
		password := r.FormValue("password")

		user, err := db.GetUserByUsername(username)
		if err != nil || user == nil || !auth.CheckPassword(user.PasswordHash, password) {
			tmpl.ExecuteTemplate(w, "login.html", map[string]string{"Error": "Invalid username or password"})
			return
		}

		sid := sessions.create(user.ID)
		http.SetCookie(w, &http.Cookie{
			Name:     "session",
			Value:    sid,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		next := r.FormValue("next")
		if next == "" || !strings.HasPrefix(next, "/") {
			next = "/"
		}
		if !isSetupComplete() && next == "/" {
			next = "/setup"
		}
		http.Redirect(w, r, next, http.StatusFound)
	}
}

func logoutHandler(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("session"); err == nil {
		sessions.delete(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "session", Value: "", MaxAge: -1, Path: "/"})
	http.Redirect(w, r, "/login", http.StatusFound)
}

// ── Dashboard ─────────────────────────────────────────────────────

type shotView struct {
	URL       string
	CreatedAt time.Time
}

func dashboardHandler(db *storage.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		shots, err := db.GetRecentScreenshots(20)
		if err != nil {
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		var views []shotView
		for _, s := range shots {
			views = append(views, shotView{
				URL:       "/screenshots/" + filepath.Base(s.FilePath),
				CreatedAt: s.CreatedAt,
			})
		}

		activity, err := db.GetRecentActivity(50)
		if err != nil {
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}

		reg, _ := db.GetDeviceRegistration()
		cloudCfg := loadCloudConfig()

		tmpl.ExecuteTemplate(w, "dashboard.html", map[string]interface{}{
			"Screenshots":      views,
			"Activity":         activity,
			"Status":           monitor.CurrentStatus(),
			"SetupComplete":    isSetupComplete(),
			"ClockedIn":        monitor.IsClockedIn(),
			"Registration":     reg,
			"CloudConfigured":  cloudCfg.URL != "",
		})
	}
}

// ── Setup wizard handler ──────────────────────────────────────────

func setupHandler(w http.ResponseWriter, r *http.Request) {
	tmpl.ExecuteTemplate(w, "setup.html", map[string]interface{}{
		"SetupComplete": isSetupComplete(),
		"Platform":      runtime.GOOS,
	})
}

// ── API helpers ───────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ── API: permissions ──────────────────────────────────────────────

func apiPermissionsHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, monitor.CheckPermissions())
}

// ── API: test screenshot ──────────────────────────────────────────

func apiTestScreenshotHandler(w http.ResponseWriter, r *http.Request) {
	const testFile = "data/screenshots/test_permission.png"
	if err := os.MkdirAll("data/screenshots", 0755); err != nil {
		writeJSONError(w, "cannot create screenshots dir", http.StatusInternalServerError)
		return
	}
	if err := monitor.TakeTestScreenshot(testFile); err != nil {
		writeJSONError(w, "screenshot failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	info, _ := os.Stat(testFile)
	sizeKB := int64(0)
	if info != nil {
		sizeKB = info.Size() / 1024
	}
	writeJSON(w, map[string]interface{}{
		"url":    "/screenshots/test_permission.png",
		"sizeKB": sizeKB,
		"ok":     sizeKB > 10,
	})
}

// ── API: auto-start (LaunchAgent / systemd / registry) ────────────

func apiAutoStartStatusHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, autoStartStatus())
}

func apiAutoStartInstallHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	exe, err := os.Executable()
	if err != nil {
		writeJSONError(w, "cannot determine executable path: "+err.Error(), http.StatusInternalServerError)
		return
	}
	exe, _ = filepath.EvalSymlinks(exe)
	result, err := autoStartInstall(exe, filepath.Dir(exe))
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, result)
}

func apiAutoStartRemoveHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	result, err := autoStartRemove()
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, result)
}

// ── API: change password ──────────────────────────────────────────

func apiChangePasswordHandler(db *storage.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		uid, ok := getUserID(r)
		if !ok {
			writeJSONError(w, "not authenticated", http.StatusUnauthorized)
			return
		}

		var body struct {
			Current string `json:"current"`
			New     string `json:"new"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if len(body.New) < 6 {
			writeJSONError(w, "new password must be at least 6 characters", http.StatusBadRequest)
			return
		}

		user, err := db.GetUserByID(uid)
		if err != nil || user == nil {
			writeJSONError(w, "user not found", http.StatusInternalServerError)
			return
		}
		if !auth.CheckPassword(user.PasswordHash, body.Current) {
			writeJSONError(w, "current password is incorrect", http.StatusUnauthorized)
			return
		}

		hash, err := auth.HashPassword(body.New)
		if err != nil {
			writeJSONError(w, "hash error", http.StatusInternalServerError)
			return
		}
		if err := db.UpdateUserPassword(uid, hash); err != nil {
			writeJSONError(w, "db error: "+err.Error(), http.StatusInternalServerError)
			return
		}

		writeJSON(w, map[string]interface{}{"ok": true, "message": "Password updated"})
	}
}

// ── API: complete setup ───────────────────────────────────────────

func apiSetupCompleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := os.MkdirAll("data", 0755); err != nil {
		writeJSONError(w, "cannot create data dir", http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(setupCompleteFile, []byte(time.Now().Format(time.RFC3339)), 0644); err != nil {
		writeJSONError(w, "write error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true})
}

// ── Time Tracker page ─────────────────────────────────────────────

func timePageHandler(w http.ResponseWriter, r *http.Request) {
	tmpl.ExecuteTemplate(w, "time.html", nil)
}

// ── API: time status ──────────────────────────────────────────────

type timeStatusResp struct {
	ClockedIn     bool       `json:"clockedIn"`
	CurrentEntry  *entryJSON `json:"currentEntry,omitempty"`
	TodaySec      int64      `json:"todaySec"`
	TodaySessions int        `json:"todaySessions"`
}

type entryJSON struct {
	ID          int64  `json:"id"`
	ClockIn     string `json:"clockIn"`
	ClockOut    string `json:"clockOut,omitempty"`
	DurationSec int64  `json:"durationSec"`
	IsActive    bool   `json:"isActive"`
}

func toEntryJSON(e storage.TimeEntry) entryJSON {
	ej := entryJSON{
		ID:          e.ID,
		ClockIn:     e.ClockIn.Format(time.RFC3339),
		DurationSec: e.DurationSec(),
		IsActive:    e.IsActive(),
	}
	if e.ClockOut != nil {
		ej.ClockOut = e.ClockOut.Format(time.RFC3339)
	}
	return ej
}

func apiTimeStatusHandler(db *storage.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, _ := getUserID(r)
		todaySec, sessions, _ := db.TodayStats(uid)
		current, _ := db.GetCurrentEntry(uid)

		resp := timeStatusResp{
			ClockedIn:     monitor.IsClockedIn(),
			TodaySec:      todaySec,
			TodaySessions: sessions,
		}
		if current != nil {
			ej := toEntryJSON(*current)
			resp.CurrentEntry = &ej
		}
		writeJSON(w, resp)
	}
}

// ── API: clock in ─────────────────────────────────────────────────

func apiClockInHandler(db *storage.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		uid, _ := getUserID(r)
		entry, err := db.ClockIn(uid)
		if err != nil {
			writeJSONError(w, err.Error(), http.StatusConflict)
			return
		}
		monitor.SetClockedIn(true)
		capture.TriggerNow() // take first screenshot immediately
		cloud.SetDeviceSessionActive(true)
		go cloud.WebClockIn() // mirror to Hajir if connected (no-op when standalone)
		ej := toEntryJSON(*entry)
		writeJSON(w, map[string]interface{}{"ok": true, "entry": ej})
	}
}

// ── API: clock out ────────────────────────────────────────────────

func apiClockOutHandler(db *storage.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		uid, _ := getUserID(r)
		entry, err := db.ClockOut(uid)
		if err != nil {
			writeJSONError(w, err.Error(), http.StatusConflict)
			return
		}
		monitor.SetClockedIn(false)
		cloud.SetDeviceSessionActive(false)
		go cloud.WebClockOut() // mirror to Hajir if connected (no-op when standalone)
		ej := toEntryJSON(*entry)
		writeJSON(w, map[string]interface{}{"ok": true, "entry": ej})
	}
}

// ── API: time entries (with filters) ──────────────────────────────

func apiTimeEntriesHandler(db *storage.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, _ := getUserID(r)
		q := r.URL.Query()

		now := time.Now()
		loc := now.Location()

		fromStr := q.Get("from")
		toStr := q.Get("to")

		var from, to time.Time
		if fromStr != "" {
			from = parseLocalDate(fromStr, loc)
		} else {
			from = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
		}
		if toStr != "" {
			to = parseLocalDate(toStr, loc).Add(24 * time.Hour)
		} else {
			to = from.Add(24 * time.Hour)
		}

		entries, err := db.GetTimeEntries(uid, from, to)
		if err != nil {
			writeJSONError(w, "db error: "+err.Error(), http.StatusInternalServerError)
			return
		}

		var totalSec int64
		out := make([]entryJSON, 0, len(entries))
		for _, e := range entries {
			totalSec += e.DurationSec()
			out = append(out, toEntryJSON(e))
		}

		writeJSON(w, map[string]interface{}{
			"entries":  out,
			"totalSec": totalSec,
			"from":     from.Format("2006-01-02"),
			"to":       to.Add(-time.Second).Format("2006-01-02"),
		})
	}
}

func parseLocalDate(s string, loc *time.Location) time.Time {
	parts := strings.Split(s, "-")
	if len(parts) != 3 {
		return time.Now()
	}
	y, _ := strconv.Atoi(parts[0])
	m, _ := strconv.Atoi(parts[1])
	d, _ := strconv.Atoi(parts[2])
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, loc)
}

// ── Cloud setup handler ───────────────────────────────────────────

const cloudConfigPath = "data/cloud.json"

type cloudConfig struct {
	URL       string `json:"url"`
	SyncToken string `json:"sync_token"`
	CompanyID int64  `json:"company_id"`
}

func loadCloudConfig() cloudConfig {
	f, err := os.ReadFile(cloudConfigPath)
	if err != nil {
		return cloudConfig{}
	}
	var cfg cloudConfig
	json.Unmarshal(f, &cfg)
	return cfg
}

func saveCloudConfig(cfg cloudConfig) error {
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(cloudConfigPath, b, 0600)
}

func cloudSetupHandler(db *storage.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := loadCloudConfig()
		reg, _ := db.GetDeviceRegistration()

		if r.Method == http.MethodGet {
			connected := false
			if cfg.URL != "" && cfg.SyncToken != "" {
				connected = pingCloud(cfg)
			}
			tmpl.ExecuteTemplate(w, "cloud.html", map[string]interface{}{
				"Config":       cfg,
				"Connected":    connected,
				"Registration": reg,
			})
			return
		}

		action := r.FormValue("action")
		if action == "disconnect" {
			saveCloudConfig(cloudConfig{})
			tmpl.ExecuteTemplate(w, "cloud.html", map[string]interface{}{
				"Config":  cloudConfig{},
				"Success": "Disconnected from Hajir.",
			})
			return
		}

		companyID, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue("company_id")), 10, 64)
		newCfg := cloudConfig{
			URL:       strings.TrimRight(r.FormValue("url"), "/"),
			SyncToken: strings.TrimSpace(r.FormValue("sync_token")),
			CompanyID: companyID,
		}
		if newCfg.URL == "" || newCfg.SyncToken == "" || newCfg.CompanyID == 0 {
			tmpl.ExecuteTemplate(w, "cloud.html", map[string]interface{}{
				"Config":       newCfg,
				"Registration": reg,
				"Error":        "Server URL, Access Token, and Company ID are all required.",
			})
			return
		}

		if !pingCloud(newCfg) {
			tmpl.ExecuteTemplate(w, "cloud.html", map[string]interface{}{
				"Config":       newCfg,
				"Registration": reg,
				"Error":        "Could not reach the Hajir server. Check the URL and token.",
			})
			return
		}

		if err := saveCloudConfig(newCfg); err != nil {
			tmpl.ExecuteTemplate(w, "cloud.html", map[string]interface{}{
				"Config":       newCfg,
				"Registration": reg,
				"Error":        "Failed to save config: " + err.Error(),
			})
			return
		}

		tmpl.ExecuteTemplate(w, "cloud.html", map[string]interface{}{
			"Config":       newCfg,
			"Connected":    true,
			"Registration": reg,
			"Success":      "Connected to Hajir! Device registration is in progress.",
		})
	}
}

// ── API: screenshot interval settings ────────────────────────────

func apiScreenshotIntervalHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s := settings.Get()
		writeJSON(w, map[string]interface{}{
			"interval_min":   s.CaptureIntervalMin,
			"valid_intervals": settings.ValidIntervals,
		})
		return
	}

	if r.Method != http.MethodPost {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		IntervalMin int `json:"interval_min"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.IntervalMin <= 0 || body.IntervalMin > 120 {
		writeJSONError(w, "interval_min must be between 1 and 120", http.StatusBadRequest)
		return
	}

	if err := settings.SetIntervalMin(body.IntervalMin); err != nil {
		writeJSONError(w, "failed to save settings: "+err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]interface{}{"ok": true, "interval_min": body.IntervalMin})
}

// ── API: sync status ──────────────────────────────────────────────

func apiSyncStatusHandler(db *storage.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		counts, _ := db.GetPendingSyncCounts()
		cfg := loadCloudConfig()
		configured := cfg.URL != ""

		lastSync := cloud.LastSyncAt()
		lastSyncStr := ""
		if !lastSync.IsZero() {
			lastSyncStr = lastSync.Format(time.RFC3339)
		}

		writeJSON(w, map[string]interface{}{
			"configured": configured,
			"online":     configured && cloud.IsOnline(),
			"last_sync":  lastSyncStr,
			"pending":    counts,
		})
	}
}

// ── API: trigger immediate sync ───────────────────────────────────

func apiSyncTriggerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cloud.TriggerSync()
	writeJSON(w, map[string]interface{}{"ok": true, "message": "Sync triggered"})
}

// ── API: check device approval status immediately ─────────────────

func apiDeviceCheckStatusHandler(db *storage.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		cfg := loadCloudConfig()
		if cfg.URL == "" {
			writeJSONError(w, "cloud not configured", http.StatusBadRequest)
			return
		}
		reg, err := db.GetDeviceRegistration()
		if err != nil || reg == nil {
			writeJSONError(w, "device not registered", http.StatusNotFound)
			return
		}
		regCfg := registration.Config{
			URL:       cfg.URL,
			Token:     cfg.SyncToken,
			CompanyID: cfg.CompanyID,
		}
		status, err := registration.PollStatus(regCfg, db, reg.MachineID)
		if err != nil {
			writeJSONError(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, map[string]interface{}{"status": status})
	}
}

func pingCloud(cfg cloudConfig) bool {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", cfg.URL+"/api/v2/productivity/device/status", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+cfg.SyncToken)
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	// 401/403 means server is reachable but token may be wrong — still counts as reachable
	return resp.StatusCode != 0
}

