package web

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"my-monitor/auth"
	"my-monitor/monitor"
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

	// API — live data
	mux.HandleFunc("/api/events", requireAuth(apiEventsHandler(db)))
	mux.HandleFunc("/api/stats", requireAuth(apiStatsHandler(db)))
	mux.HandleFunc("/api/input/recent", requireAuth(apiInputRecentHandler(db)))
	mux.HandleFunc("/api/db/stats", requireAuth(apiDBStatsHandler(db)))

	// Time tracker page + API
	mux.HandleFunc("/time", requireAuth(timePageHandler))
	mux.HandleFunc("/api/time/status", requireAuth(apiTimeStatusHandler(db)))
	mux.HandleFunc("/api/time/clockin", requireAuth(apiClockInHandler(db)))
	mux.HandleFunc("/api/time/clockout", requireAuth(apiClockOutHandler(db)))
	mux.HandleFunc("/api/time/entries", requireAuth(apiTimeEntriesHandler(db)))

	// Cloud setup
	mux.HandleFunc("/cloud", requireAuth(cloudSetupHandler))

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

		tmpl.ExecuteTemplate(w, "dashboard.html", map[string]interface{}{
			"Screenshots":   views,
			"Activity":      activity,
			"Status":        monitor.CurrentStatus(),
			"SetupComplete": isSetupComplete(),
			"ClockedIn":     monitor.IsClockedIn(),
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

// ── API: SSE live events ──────────────────────────────────────────

func apiEventsHandler(db *storage.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		send := func() {
			b, _ := json.Marshal(buildLiveState(db))
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		}

		send()
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-tick.C:
				send()
			}
		}
	}
}

type liveState struct {
	Activity   map[string]interface{} `json:"activity"`
	Input      monitor.InputRate      `json:"input"`
	ClockedIn  bool                   `json:"clockedIn"`
	LatestShot string                 `json:"latestShot"`
}

func buildLiveState(db *storage.DB) liveState {
	st := monitor.CurrentStatus()
	shots, _ := db.GetRecentScreenshots(1)
	latest := ""
	if len(shots) > 0 {
		latest = "/screenshots/" + filepath.Base(shots[0].FilePath)
	}
	return liveState{
		Activity: map[string]interface{}{
			"isActive":    st.IsActive,
			"idleSeconds": st.IdleSeconds,
			"updatedAt":   st.UpdatedAt.Format(time.RFC3339),
		},
		Input:      monitor.GetInputRate(),
		ClockedIn:  monitor.IsClockedIn(),
		LatestShot: latest,
	}
}

// ── API: current stats snapshot ───────────────────────────────────

func apiStatsHandler(db *storage.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, buildLiveState(db))
	}
}

// ── API: recent input logs (last N 1-min buckets) ─────────────────

func apiInputRecentHandler(db *storage.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n := 15
		if v := r.URL.Query().Get("n"); v != "" {
			if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 && parsed <= 60 {
				n = parsed
			}
		}
		logs, err := db.GetRecentInputLogs(n)
		if err != nil {
			writeJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		type bucket struct {
			Time          string  `json:"time"`
			KeyEvents     int64   `json:"keyEvents"`
			MouseClicks   int64   `json:"mouseClicks"`
			MouseDistance float64 `json:"mouseDistance"`
		}
		out := make([]bucket, 0, len(logs))
		for _, l := range logs {
			out = append(out, bucket{
				Time:          l.RecordedAt.Format("15:04"),
				KeyEvents:     l.KeyEvents,
				MouseClicks:   l.MouseClicks,
				MouseDistance: l.MouseDistance,
			})
		}
		writeJSON(w, map[string]interface{}{"buckets": out})
	}
}

// ── API: DB row-count stats ───────────────────────────────────────

func apiDBStatsHandler(db *storage.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats, err := db.DBStats()
		if err != nil {
			writeJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, stats)
	}
}

// ── Cloud setup handler ───────────────────────────────────────────

const cloudConfigPath = "data/cloud.json"

type cloudConfig struct {
	URL       string `json:"url"`
	SyncToken string `json:"sync_token"`
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

var cloudSetupHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	cfg := loadCloudConfig()

	if r.Method == http.MethodGet {
		connected := false
		if cfg.URL != "" && cfg.SyncToken != "" {
			connected = pingCloud(cfg)
		}
		tmpl.ExecuteTemplate(w, "cloud.html", map[string]interface{}{
			"Config":    cfg,
			"Connected": connected,
		})
		return
	}

	action := r.FormValue("action")
	if action == "disconnect" {
		saveCloudConfig(cloudConfig{})
		tmpl.ExecuteTemplate(w, "cloud.html", map[string]interface{}{
			"Config":  cloudConfig{},
			"Success": "Disconnected from cloud.",
		})
		return
	}

	newCfg := cloudConfig{
		URL:       strings.TrimRight(r.FormValue("url"), "/"),
		SyncToken: strings.TrimSpace(r.FormValue("sync_token")),
	}
	if newCfg.URL == "" || newCfg.SyncToken == "" {
		tmpl.ExecuteTemplate(w, "cloud.html", map[string]interface{}{
			"Config": cfg,
			"Error":  "URL and sync token are required.",
		})
		return
	}

	if !pingCloud(newCfg) {
		tmpl.ExecuteTemplate(w, "cloud.html", map[string]interface{}{
			"Config": newCfg,
			"Error":  "Could not connect to the cloud server. Check the URL and token.",
		})
		return
	}

	if err := saveCloudConfig(newCfg); err != nil {
		tmpl.ExecuteTemplate(w, "cloud.html", map[string]interface{}{
			"Config": newCfg,
			"Error":  "Failed to save config: " + err.Error(),
		})
		return
	}

	tmpl.ExecuteTemplate(w, "cloud.html", map[string]interface{}{
		"Config":    newCfg,
		"Connected": true,
		"Success":   "Connected! Data will sync in the background.",
	})
})

func pingCloud(cfg cloudConfig) bool {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", cfg.URL+"/api/sync/config", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+cfg.SyncToken)
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

