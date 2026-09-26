package hub

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Server struct {
	cfg      Config
	store    *Store
	verifier *Verifier
	broker   *Broker
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
	s := &Server{cfg: cfg, store: store, verifier: NewVerifier(cfg.HajirAPIURL), broker: NewBroker()}
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
	mux.Handle("POST /api/employer/{company}/devices", s.employer(s.handleCreateDevice))
	mux.Handle("DELETE /api/employer/{company}/devices/{id}", s.employer(s.handleRevokeDevice))
	mux.Handle("GET /api/employer/{company}/time-entries", s.employer(s.handleTimeEntries))
	mux.Handle("GET /api/employer/{company}/timeline", s.employer(s.handleTimeline))
	mux.Handle("GET /api/employer/{company}/screenshots", s.employer(s.handleScreenshots))
	mux.Handle("GET /api/employer/{company}/overview", s.employer(s.handleOverview))
	mux.Handle("GET /api/employer/{company}/members/{user}/day", s.employer(s.handleMemberDay))
	mux.Handle("GET /api/employer/{company}/monthly", s.employer(s.handleMonthly))
	mux.Handle("POST /api/employer/{company}/members/{user}/manual", s.employer(s.handleAddManual))
	mux.Handle("DELETE /api/employer/{company}/manual/{id}", s.employer(s.handleDeleteManual))
	mux.Handle("GET /api/employer/{company}/settings", s.employer(s.handleGetSettings))
	mux.Handle("PUT /api/employer/{company}/settings", s.employer(s.handleSaveSettings))
	mux.Handle("GET /api/employer/{company}/members/{user}/settings", s.employer(s.handleGetMemberSettings))
	mux.Handle("PUT /api/employer/{company}/members/{user}/settings", s.employer(s.handleSaveMemberSettings))

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
		if err != nil {
			writeErr(w, http.StatusUnauthorized, err.Error())
			return
		}
		if !emp.Owns(companyID) {
			writeErr(w, http.StatusForbidden, "only the company owner can view device activity")
			return
		}
		h(w, r, emp, companyID)
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
	return start, start.Add(24*time.Hour - time.Second), nil
}

func (s *Server) sign(id int64, exp int64) string {
	m := hmac.New(sha256.New, s.cfg.Secret)
	fmt.Fprintf(m, "%d:%d", id, exp)
	return hex.EncodeToString(m.Sum(nil))
}

func (s *Server) screenshotURL(id int64) string {
	exp := time.Now().Add(30 * time.Minute).Unix()
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
	w.Header().Set("Cache-Control", "private, max-age=600")
	http.ServeFile(w, r, filepath.Join(s.cfg.DataDir, "screenshots", sh.Path))
}

func (s *Server) pruneLoop() {
	for {
		shots, _ := s.store.OldScreenshots(time.Now().AddDate(0, 0, -s.cfg.ScreenshotRetainDy))
		for _, sh := range shots {
			os.Remove(filepath.Join(s.cfg.DataDir, "screenshots", sh.Path))
			s.store.DeleteScreenshot(sh.ID)
		}
		n, _ := s.store.PruneSamples(time.Now().AddDate(0, 0, -s.cfg.SampleRetainDays))
		if len(shots) > 0 || n > 0 {
			log.Printf("hub: pruned %d screenshots, %d samples", len(shots), n)
		}
		time.Sleep(6 * time.Hour)
	}
}

func readLimited(r *http.Request, max int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r.Body, max))
}
