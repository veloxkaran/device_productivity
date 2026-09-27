package hub

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Server struct {
	cfg      Config
	store    *Store
	verifier EmployerVerifier
	broker   *Broker
	tickets  *ticketScopes
}

func Run() {
	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("hub: config: %v", err)
	}
	store, err := OpenStore(filepath.Join(cfg.DataDir, "hub.db"))
	if err != nil {
		log.Fatalf("hub: store: %v", err)
	}
	defer store.Close()

	displayLocation = cfg.Location
	s := &Server{cfg: cfg, store: store, verifier: NewVerifier(cfg.HajirAPIURL), broker: NewBroker(), tickets: newTicketScopes()}
	go s.pruneLoop()

	srv := &http.Server{Addr: cfg.Addr, Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Printf("hub: listening on %s (hajir api %s, tz %s)", cfg.Addr, cfg.HajirAPIURL, cfg.Location)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("hub: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]any{"status": "ok"}) })

	mux.Handle("GET /api/sync/config", s.device(s.handleDeviceMe))
	mux.Handle("POST /api/heartbeat", s.device(s.handleHeartbeat))
	mux.Handle("POST /api/sync/time-entries", s.device(s.handleSyncTimeEntries))
	mux.Handle("POST /api/sync/activity", s.device(s.handleSyncActivity))
	mux.Handle("POST /api/sync/screenshots", s.device(s.handleSyncScreenshot))
	mux.Handle("POST /api/sync/breaks", s.device(s.handleSyncBreaks))
	mux.Handle("POST /api/sync/idle-reports", s.device(s.handleSyncIdleReports))
	mux.HandleFunc("POST /api/device/register", s.handleDeviceRegister)

	mux.Handle("GET /api/employer/{company}/devices", s.employer(s.handleDevices))
	mux.Handle("POST /api/employer/{company}/devices", s.owner("manage devices", s.handleCreateDevice))
	mux.Handle("DELETE /api/employer/{company}/devices/{id}", s.owner("manage devices", s.handleRevokeDevice))
	mux.Handle("GET /api/employer/{company}/time-entries", s.employer(s.handleTimeEntries))
	mux.Handle("GET /api/employer/{company}/timeline", s.employer(s.handleTimeline))
	mux.Handle("GET /api/employer/{company}/screenshots", s.employer(s.handleScreenshots))
	mux.Handle("DELETE /api/employer/{company}/screenshots/{id}", s.owner("delete screenshots", s.handleDeleteScreenshot))
	mux.Handle("GET /api/employer/{company}/overview", s.employer(s.handleOverview))
	mux.Handle("GET /api/employer/{company}/members/{user}/day", s.employer(s.handleMemberDay))
	mux.Handle("GET /api/employer/{company}/monthly", s.employer(s.handleMonthly))
	mux.Handle("GET /api/employer/{company}/monthly/members", s.employer(s.handleMonthlyMembers))
	mux.Handle("POST /api/employer/{company}/members/{user}/manual", s.owner("change manual time", s.handleAddManual))
	mux.Handle("DELETE /api/employer/{company}/manual/{id}", s.owner("change manual time", s.handleDeleteManual))
	mux.Handle("GET /api/employer/{company}/clock", s.employer(s.handleClock))
	mux.Handle("POST /api/employer/{company}/ws-ticket", s.employer(s.handleWSTicket))
	mux.Handle("GET /api/employer/{company}/settings", s.employer(s.handleGetSettings))
	mux.Handle("PUT /api/employer/{company}/settings", s.owner("change company settings", s.handleSaveSettings))
	mux.Handle("GET /api/employer/{company}/members/{user}/settings", s.employer(s.handleGetMemberSettings))
	mux.Handle("PUT /api/employer/{company}/members/{user}/settings", s.owner("change member settings", s.handleSaveMemberSettings))

	mux.Handle("GET /api/employer/{company}/downloads", s.employer(s.handleDownloads))
	mux.HandleFunc("GET /downloads/{name}", s.handleDownloadFile)
	mux.HandleFunc("PUT /api/internal/companies/{company}/modules", s.handleInternalModules)

	mux.HandleFunc("GET /files/screenshots/{id}", s.handleScreenshotFile)
	mux.HandleFunc("GET /ws", s.handleWS)

	return s.cors(mux)
}

func (s *Server) originAllowed(origin string) bool {
	if origin == "" {
		return true
	}
	for _, o := range s.cfg.AllowedOrigins {
		if o == "*" || strings.EqualFold(o, origin) {
			return true
		}
	}
	return false
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && s.originAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func (s *Server) device(h func(http.ResponseWriter, *http.Request, *Device)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d, err := s.store.DeviceByToken(bearer(r))
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "invalid or revoked device token")
			return
		}
		h(w, r, d)
	})
}

func (s *Server) employer(h func(http.ResponseWriter, *http.Request, *Employer, int64)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		companyID, err := strconv.ParseInt(r.PathValue("company"), 10, 64)
		if err != nil || companyID <= 0 {
			writeErr(w, http.StatusBadRequest, "invalid company id")
			return
		}
		emp, err := s.verifier.Verify(bearer(r))
		if errors.Is(err, errSessionRejected) {
			writeErr(w, http.StatusUnauthorized, "invalid or expired session")
			return
		}
		if err != nil {
			log.Printf("hub: employer verify: %v", err)
			writeErr(w, http.StatusServiceUnavailable, "activity service temporarily unavailable")
			return
		}
		sc, ok := emp.Access(companyID)
		if !ok {
			writeErr(w, http.StatusForbidden, "you do not have access to device activity for this company")
			return
		}
		h(w, withScope(r, sc), emp, companyID)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"status": "error", "code": code, "message": msg})
}

func writeOK(w http.ResponseWriter, msg string, data any) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "message": msg, "data": data})
}

func (s *Server) dayBounds(date string) (time.Time, time.Time, error) {
	day := time.Now().In(s.cfg.Location)
	if date != "" {
		t, err := time.ParseInLocation("2006-01-02", date, s.cfg.Location)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("date must be YYYY-MM-DD")
		}
		day = t
	}
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, s.cfg.Location)
	return start, start.AddDate(0, 0, 1), nil
}

func (s *Server) sign(id int64, exp int64) string {
	m := hmac.New(sha256.New, s.cfg.Secret)
	fmt.Fprintf(m, "%d:%d", id, exp)
	return hex.EncodeToString(m.Sum(nil))
}

const screenshotURLTTL = 15 * time.Minute

func (s *Server) screenshotURL(id int64) string {
	exp := time.Now().Add(screenshotURLTTL).Unix()
	return fmt.Sprintf("%s/files/screenshots/%d?exp=%d&sig=%s", s.cfg.PublicURL, id, exp, s.sign(id, exp))
}

func (s *Server) handleScreenshotFile(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	exp, _ := strconv.ParseInt(r.URL.Query().Get("exp"), 10, 64)
	if id == 0 || time.Now().Unix() > exp || !hmac.Equal([]byte(s.sign(id, exp)), []byte(r.URL.Query().Get("sig"))) {
		http.Error(w, "link expired or invalid", http.StatusForbidden)
		return
	}
	sh, err := s.store.ScreenshotByID(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeFile(w, r, filepath.Join(s.cfg.DataDir, "screenshots", sh.Path))
}

func (s *Server) pruneLoop() {
	var lastPurge time.Time
	for {
		shots := 0
		if time.Since(lastPurge) >= 24*time.Hour {
			n, err := s.purgeScreenshots(time.Now())
			if err != nil {
				log.Printf("hub: screenshot purge: %v", err)
			} else {
				lastPurge = time.Now()
			}
			shots = n
		}
		n, _ := s.store.PruneSamples(time.Now().AddDate(0, 0, -s.cfg.SampleRetainDays))
		if shots > 0 || n > 0 {
			log.Printf("hub: pruned %d screenshots, %d samples", shots, n)
		}
		time.Sleep(6 * time.Hour)
	}
}

func (s *Server) removeScreenshotFile(path string) {
	os.Remove(filepath.Join(s.cfg.DataDir, "screenshots", path))
}

func (s *Server) purgeScreenshots(now time.Time) (int, error) {
	companies, err := s.store.ScreenshotCompanies()
	if err != nil {
		return 0, err
	}
	total := 0
	for _, companyID := range companies {
		cutoff := now.AddDate(0, 0, -s.store.GetSettings(companyID).ScreenshotRetentionDays)
		var afterID int64
		for {
			shots, err := s.store.OldCompanyScreenshots(companyID, afterID, cutoff)
			if err != nil {
				log.Printf("hub: screenshot purge company=%d: %v", companyID, err)
				break
			}
			for _, sh := range shots {
				afterID = sh.ID
				if err := s.store.DeleteScreenshot(sh.ID); err != nil {
					log.Printf("hub: screenshot purge id=%d: %v", sh.ID, err)
					continue
				}
				s.removeScreenshotFile(sh.Path)
				total++
			}
			if len(shots) < oldScreenshotBatch {
				break
			}
		}
	}
	return total, nil
}

func readLimited(r *http.Request, max int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r.Body, max))
}

// downloadsDir is where desktop-app installers are placed for employers to grab.
func (s *Server) downloadsDir() string { return filepath.Join(s.cfg.DataDir, "downloads") }

// verRE extracts a semver-ish version from an installer filename, e.g.
// "MyMonitor-Setup-windows-amd64-v2.1.0.exe" -> "2.1.0".
var verRE = regexp.MustCompile(`(?i)v?(\d+\.\d+(?:\.\d+)?)`)

// platformOf maps an installer filename to a platform label, an
// architecture label (correct for that platform), and a version ("" if none).
func platformOf(name string) (platform, arch, version string) {
	n := strings.ToLower(name)

	switch {
	case strings.HasSuffix(n, ".dmg") || strings.Contains(n, "darwin") || strings.Contains(n, "macos") || strings.Contains(n, "-mac"):
		platform = "macOS"
	case strings.HasSuffix(n, ".exe") || strings.Contains(n, "windows") || strings.Contains(n, "win"):
		platform = "Windows"
	case strings.Contains(n, "linux"):
		platform = "Linux"
	default:
		platform = "Other"
	}

	isARM := strings.Contains(n, "arm64") || strings.Contains(n, "aarch64")
	isX64 := strings.Contains(n, "amd64") || strings.Contains(n, "x64") || strings.Contains(n, "x86_64") || strings.Contains(n, "intel")
	isUniv := strings.Contains(n, "universal")
	switch {
	case isUniv:
		arch = "Universal"
	case platform == "macOS" && isARM:
		arch = "Apple Silicon"
	case platform == "macOS" && isX64:
		arch = "Intel"
	case isARM:
		arch = "ARM64"
	case isX64:
		arch = "x64 (Intel/AMD)"
	}

	// A macOS .dmg with no explicit arch is our universal build.
	if platform == "macOS" && arch == "" {
		arch = "Universal"
	}

	if m := verRE.FindStringSubmatch(name); m != nil {
		version = m[1]
	}
	return platform, arch, version
}

// handleDownloads lists the desktop-app installers available for download.
func (s *Server) handleDownloads(w http.ResponseWriter, r *http.Request, e *Employer, companyID int64) {
	dir := s.downloadsDir()
	entries, _ := os.ReadDir(dir)
	files := []map[string]any{}
	for _, en := range entries {
		if en.IsDir() {
			continue
		}
		name := en.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		// Hide auto-update assets: the raw update binaries (my-monitor-*) and
		// the update manifest are downloaded by the app itself, not by people.
		low := strings.ToLower(name)
		if strings.HasPrefix(low, "my-monitor-") || strings.HasSuffix(low, ".json") {
			continue
		}
		info, err := en.Info()
		if err != nil {
			continue
		}
		plat, arch, version := platformOf(name)
		files = append(files, map[string]any{
			"name":        name,
			"platform":    plat,
			"arch":        arch,
			"version":     version,
			"size":        info.Size(),
			"modified_at": info.ModTime().UTC().Format(time.RFC3339),
			"url":         s.cfg.PublicURL + "/downloads/" + url.PathEscape(name),
		})
	}
	writeOK(w, "downloads", map[string]any{"files": files})
}

// handleDownloadFile serves an installer. Public (binaries are not secret) but
// strictly limited to files inside the downloads directory.
func (s *Server) handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	// Reject any path traversal — only a bare filename is allowed.
	if name == "" || name != filepath.Base(name) || strings.Contains(name, "..") {
		http.Error(w, "invalid file", http.StatusBadRequest)
		return
	}
	full := filepath.Join(s.downloadsDir(), name)
	if fi, err := os.Stat(full); err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"")
	http.ServeFile(w, r, full)
}
