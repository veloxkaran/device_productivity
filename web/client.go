package web

import (
	"bytes"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"my-monitor/capture"
	"my-monitor/cloud"
	"my-monitor/monitor"
	"my-monitor/storage"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

//go:embed assets/hajirlogo.svg
var hajirLogo []byte

const clientConfigPath = "data/client.json"

var (
	DefaultHajirAPIURL = "http://localhost:8001/api/v2"
	DefaultHubURL      = "http://localhost:4010"
	// DefaultProvisionToken can be baked at build time (-ldflags -X) to image a
	// fleet of managed devices. Empty by default; env HAJIR_DEVICE_TOKEN or a
	// provision.json file override it at runtime.
	DefaultProvisionToken = ""
)

type clientConfig struct {
	HajirAPIURL string `json:"-"`
	HubURL      string `json:"-"`
	Login       string `json:"login"`
	Remember    bool   `json:"remember"`
	UserName    string `json:"user_name"`
	CompanyID   int64  `json:"company_id"`
	CompanyName string `json:"company_name"`
	IdleMinutes int    `json:"idle_minutes"`
	DeviceToken string `json:"device_token"`
}

type idleState struct {
	Start   time.Time
	End     *time.Time
	Pending bool
}

type Client struct {
	db   *storage.DB
	mgr  *cloud.Manager
	mu   sync.Mutex
	cfg  clientConfig
	idle idleState
	http *http.Client
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

var currentClient *Client

// OnBecameManaged is set by main on platforms with a menu-bar item. It is called
// once when a device is switched to managed/automatic mode at runtime, so the
// native layer can hide the menu-bar icon and any open window (go covert).
var OnBecameManaged func()

func Quit() {
	if currentClient != nil {
		currentClient.shutdown()
	}
	os.Exit(0)
}

func (c *Client) shutdown() {
	uid := c.uid()
	if cur, _ := c.db.GetCurrentEntry(uid); cur != nil {
		c.db.EndBreak(uid)
		c.db.ClockOut(uid)
		monitor.SetOnBreak(false)
		monitor.SetClockedIn(false)
	}
	c.mgr.Flush()
	log.Println("client: quit")
}

// provisionFile is the drop location a managed installer / MDM can place a
// device token in, as {"token":"hdv_..."}. It is consumed and deleted on first
// launch so the raw token is not left on disk (the manager persists the session
// in cloud.json afterwards).
const provisionFile = "data/provision.json"

// Provision silently authenticates a managed device using an employer-issued
// device token, so no login screen is ever shown. It looks for a token from, in
// order: an existing signed-in session, the HAJIR_DEVICE_TOKEN env var, a
// provision.json drop file, then a build-time baked token. It returns whether
// this install is managed (headless, no window). Call it once at startup before
// building any UI.
func Provision(mgr *cloud.Manager) (managed bool) {
	if cur := mgr.Config(); cur.SyncToken != "" {
		return cur.Managed // already provisioned in a previous launch
	}
	hub := strings.TrimRight(envOr("HUB_URL", DefaultHubURL), "/")

	token := strings.TrimSpace(os.Getenv("HAJIR_DEVICE_TOKEN"))
	fromFile := false
	if token == "" {
		if b, err := os.ReadFile(provisionFile); err == nil {
			var pf struct {
				Token string `json:"token"`
				Hub   string `json:"hub_url"`
			}
			if json.Unmarshal(b, &pf) == nil && strings.TrimSpace(pf.Token) != "" {
				token = strings.TrimSpace(pf.Token)
				fromFile = true
				if strings.TrimSpace(pf.Hub) != "" {
					hub = strings.TrimRight(strings.TrimSpace(pf.Hub), "/")
				}
			}
		}
	}
	if token == "" {
		token = strings.TrimSpace(DefaultProvisionToken)
	}
	if token == "" {
		return false // interactive install — normal login flow
	}
	if err := mgr.Apply(cloud.Config{URL: hub, SyncToken: token, Managed: true}, true); err != nil {
		log.Printf("provision: could not apply device token: %v", err)
		return false
	}
	if fromFile {
		os.Remove(provisionFile) // token now lives only in cloud.json (0600)
	}
	log.Printf("provision: managed device authenticated silently")
	return true
}

func NewClient(db *storage.DB, mgr *cloud.Manager) *Client {
	c := &Client{db: db, mgr: mgr, http: &http.Client{Timeout: 15 * time.Second}}
	currentClient = c
	c.cfg = clientConfig{Remember: true, IdleMinutes: 5}
	if b, err := os.ReadFile(clientConfigPath); err == nil {
		json.Unmarshal(b, &c.cfg)
	}
	c.cfg.HajirAPIURL = strings.TrimRight(envOr("HAJIR_API_URL", DefaultHajirAPIURL), "/")
	c.cfg.HubURL = strings.TrimRight(envOr("HUB_URL", DefaultHubURL), "/")
	if c.cfg.IdleMinutes <= 0 {
		c.cfg.IdleMinutes = 5
	}
	if c.cfg.DeviceToken == "" {
		b := make([]byte, 16)
		rand.Read(b)
		c.cfg.DeviceToken = "hajir-desktop-" + hex.EncodeToString(b)
		c.save()
	}
	go c.watchIdle()
	return c
}

func (c *Client) save() {
	b, _ := json.MarshalIndent(c.cfg, "", "  ")
	os.WriteFile(clientConfigPath, b, 0600)
}

func (c *Client) uid() int64 { return c.db.FirstUserID() }

func (c *Client) watchIdle() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for range t.C {
		st := monitor.CurrentStatus()
		threshold := int64(c.cfg.IdleMinutes * 60)
		c.mu.Lock()
		tracking := monitor.IsClockedIn() && !monitor.IsOnBreak()
		switch {
		case !tracking:
			if !c.idle.Pending {
				c.idle = idleState{}
			}
		case st.IdleSeconds >= threshold && c.idle.Start.IsZero():
			c.idle = idleState{Start: time.Now().Add(-time.Duration(st.IdleSeconds) * time.Second)}
		case st.IdleSeconds < 30 && !c.idle.Start.IsZero() && c.idle.End == nil:
			now := time.Now()
			c.idle.End = &now
			c.idle.Pending = true
		}
		c.mu.Unlock()
	}
}

func localOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "localhost" && host != "127.0.0.1" && host != "::1" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Header.Get("X-Hajir-Client") != "1" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (c *Client) Register(mux *http.ServeMux) {
	mux.HandleFunc("/app", localOnly(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		tmpl.ExecuteTemplate(w, "app.html", nil)
	}))
	mux.HandleFunc("/app/logo.svg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "max-age=86400")
		w.Write(hajirLogo)
	})
	mux.HandleFunc("/app/api/state", localOnly(c.handleState))
	mux.HandleFunc("/app/api/provision", localOnly(c.handleProvision))
	mux.HandleFunc("/app/api/login", localOnly(c.handleLogin))
	mux.HandleFunc("/app/api/logout", localOnly(c.handleLogout))
	mux.HandleFunc("/app/api/start", localOnly(c.handleStart))
	mux.HandleFunc("/app/api/stop", localOnly(c.handleStop))
	mux.HandleFunc("/app/api/break", localOnly(c.handleBreak))
	mux.HandleFunc("/app/api/idle", localOnly(c.handleIdle))
	mux.HandleFunc("/app/api/quit", localOnly(c.handleQuit))
	mux.HandleFunc("/app/api/open-permissions", localOnly(func(w http.ResponseWriter, r *http.Request) {
		capture.RequestScreenPermission()
		capture.OpenScreenPermissionSettings()
		jsonOut(w, http.StatusOK, map[string]any{"ok": true})
	}))
}

func jsonOut(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	jsonOut(w, code, map[string]any{"error": msg})
}

func (c *Client) handleState(w http.ResponseWriter, r *http.Request) {
	uid := c.uid()
	cfg := c.mgr.Config()
	signed := cfg.URL != "" && cfg.SyncToken != ""
	todaySec, _, _ := c.db.TodayStats(uid)
	cur, _ := c.db.GetCurrentEntry(uid)
	brk, _ := c.db.CurrentBreak(uid)
	c.mu.Lock()
	idle := c.idle
	ccfg := c.cfg
	c.mu.Unlock()

	out := map[string]any{
		"signed_in":         signed,
		"login":             ccfg.Login,
		"remember":          ccfg.Remember,
		"user_name":         ccfg.UserName,
		"company_name":      ccfg.CompanyName,
		"version":           cloud.AppVersion,
		"now":               time.Now().Format(time.RFC3339),
		"clocked_in":        cur != nil,
		"on_break":          brk != nil,
		"today_seconds":     todaySec,
		"break_seconds":     c.db.TodayBreakSeconds(uid),
		"current_start":     nil,
		"break_start":       nil,
		"idle":              nil,
		"screen_permission": capture.ScreenPermission(),
		"capture":           capture.CurrentStatus(),
		"auto_mode":         monitor.AutoMode(),
		"managed":           cfg.Managed,
	}
	if cur != nil {
		out["current_start"] = cur.ClockIn.Format(time.RFC3339)
	}
	if brk != nil {
		out["break_start"] = brk.Start.Format(time.RFC3339)
	}
	if !idle.Start.IsZero() {
		end := time.Now()
		if idle.End != nil {
			end = *idle.End
		}
		out["idle"] = map[string]any{"last_active": idle.Start.Format(time.RFC3339), "seconds": int64(end.Sub(idle.Start).Seconds()), "returned": idle.Pending}
	}
	jsonOut(w, http.StatusOK, out)
}

func (c *Client) postJSON(url, token string, body any, out any) (int, error) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if out != nil {
		json.Unmarshal(raw, out)
	}
	return resp.StatusCode, nil
}

// handleProvision authenticates the device silently with an employer-issued
// device token (the Automatic / covert install path). No username or password.
// On success the device becomes managed: it runs headless and the menu-bar icon
// is removed.
func (c *Client) handleProvision(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&in); err != nil || strings.TrimSpace(in.Token) == "" {
		jsonErr(w, http.StatusUnprocessableEntity, "Enter the device token.")
		return
	}
	token := strings.TrimSpace(in.Token)
	c.mu.Lock()
	hub := strings.TrimRight(c.cfg.HubURL, "/")
	c.mu.Unlock()

	// Verify the token against the hub before committing it.
	code, err := c.postJSON(hub+"/api/heartbeat", token, map[string]any{"status": "offline", "platform": runtime.GOOS, "app_version": cloud.AppVersion}, nil)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, "Can't reach the activity server. Check the connection.")
		return
	}
	if code == http.StatusUnauthorized {
		jsonErr(w, http.StatusUnauthorized, "Invalid or revoked device token.")
		return
	}
	if code != http.StatusOK {
		jsonErr(w, http.StatusBadGateway, "Server rejected the token. Try again.")
		return
	}
	if err := c.mgr.Apply(cloud.Config{URL: hub, SyncToken: token, Managed: true}, true); err != nil {
		jsonErr(w, http.StatusInternalServerError, "Could not save the connection.")
		return
	}
	capture.TakeNow()
	if OnBecameManaged != nil {
		OnBecameManaged()
	}
	jsonOut(w, http.StatusOK, map[string]any{"ok": true, "managed": true})
}

func (c *Client) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var in struct {
		Login     string `json:"login"`
		Password  string `json:"password"`
		Remember  bool   `json:"remember"`
		CompanyID int64  `json:"company_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&in); err != nil || strings.TrimSpace(in.Login) == "" || in.Password == "" {
		jsonErr(w, http.StatusUnprocessableEntity, "Enter your email or phone and password.")
		return
	}
	c.mu.Lock()
	api := strings.TrimRight(c.cfg.HajirAPIURL, "/")
	hub := strings.TrimRight(c.cfg.HubURL, "/")
	c.mu.Unlock()

	var login struct {
		Message string `json:"message"`
		Data    struct {
			User struct {
				Name string `json:"name"`
			} `json:"user"`
			Token struct {
				AccessToken string `json:"access_token"`
			} `json:"token"`
		} `json:"data"`
	}
	host, _ := os.Hostname()
	code, err := c.postJSON(api+"/candidate/login", "", map[string]string{
		"phone":        strings.TrimSpace(in.Login),
		"password":     in.Password,
		"device_token": c.cfg.DeviceToken,
		"name":         host,
		"type":         "desktop-" + runtime.GOOS,
		"app_version":  cloud.AppVersion,
	}, &login)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, "Can't reach Hajir. Check your internet connection.")
		return
	}
	if code != http.StatusOK || login.Data.Token.AccessToken == "" {
		msg := login.Message
		if msg == "" {
			msg = "Incorrect email or password."
		}
		jsonErr(w, http.StatusUnauthorized, msg)
		return
	}

	var reg struct {
		Message   string `json:"message"`
		Companies []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"companies"`
		Data struct {
			Token        string `json:"token"`
			CompanyID    int64  `json:"company_id"`
			CompanyName  string `json:"company_name"`
			EmployeeName string `json:"employee_name"`
		} `json:"data"`
	}
	code, err = c.postJSON(hub+"/api/device/register", login.Data.Token.AccessToken, map[string]any{"company_id": in.CompanyID, "device_name": host, "platform": runtime.GOOS}, &reg)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, "Can't reach the activity server.")
		return
	}
	if code == http.StatusConflict {
		jsonOut(w, http.StatusConflict, map[string]any{"error": "Choose the company you're working for.", "companies": reg.Companies})
		return
	}
	if code != http.StatusOK && code != http.StatusCreated {
		msg := reg.Message
		if msg == "" {
			msg = fmt.Sprintf("Activity server error (%d).", code)
		}
		jsonErr(w, code, msg)
		return
	}
	if err := c.mgr.Apply(cloud.Config{URL: hub, SyncToken: reg.Data.Token}, true); err != nil {
		jsonErr(w, http.StatusInternalServerError, "Could not save sign-in.")
		return
	}
	c.mu.Lock()
	c.cfg.Remember = in.Remember
	if in.Remember {
		c.cfg.Login = strings.TrimSpace(in.Login)
	} else {
		c.cfg.Login = ""
	}
	c.cfg.UserName = reg.Data.EmployeeName
	if c.cfg.UserName == "" {
		c.cfg.UserName = login.Data.User.Name
	}
	c.cfg.CompanyID = reg.Data.CompanyID
	c.cfg.CompanyName = reg.Data.CompanyName
	c.save()
	c.mu.Unlock()
	log.Printf("client: signed in as %s (%s)", c.cfg.UserName, c.cfg.CompanyName)
	jsonOut(w, http.StatusOK, map[string]any{"ok": true})
}

func (c *Client) handleLogout(w http.ResponseWriter, r *http.Request) {
	uid := c.uid()
	if cur, _ := c.db.GetCurrentEntry(uid); cur != nil {
		c.db.EndBreak(uid)
		c.db.ClockOut(uid)
		monitor.SetOnBreak(false)
		monitor.SetClockedIn(false)
	}
	c.mgr.Disconnect()
	c.mu.Lock()
	c.cfg.UserName, c.cfg.CompanyName, c.cfg.CompanyID = "", "", 0
	c.idle = idleState{}
	c.save()
	c.mu.Unlock()
	jsonOut(w, http.StatusOK, map[string]any{"ok": true})
}

func (c *Client) handleStart(w http.ResponseWriter, r *http.Request) {
	if monitor.AutoMode() {
		jsonErr(w, http.StatusConflict, "Tracking is managed automatically by your employer.")
		return
	}
	if _, err := c.db.ClockIn(c.uid()); err != nil {
		jsonErr(w, http.StatusConflict, err.Error())
		return
	}
	monitor.SetClockedIn(true)
	capture.TakeNow()
	c.mgr.SyncNow()
	jsonOut(w, http.StatusOK, map[string]any{"ok": true})
}

func (c *Client) handleStop(w http.ResponseWriter, r *http.Request) {
	if monitor.AutoMode() {
		jsonErr(w, http.StatusConflict, "Tracking is managed automatically by your employer.")
		return
	}
	uid := c.uid()
	if b, _ := c.db.CurrentBreak(uid); b != nil {
		c.db.EndBreak(uid)
		monitor.SetOnBreak(false)
	}
	if _, err := c.db.ClockOut(uid); err != nil {
		jsonErr(w, http.StatusConflict, err.Error())
		return
	}
	monitor.SetClockedIn(false)
	c.mu.Lock()
	c.idle = idleState{}
	c.mu.Unlock()
	c.mgr.SyncNow()
	jsonOut(w, http.StatusOK, map[string]any{"ok": true})
}

func (c *Client) handleBreak(w http.ResponseWriter, r *http.Request) {
	uid := c.uid()
	var err error
	onBreak := false
	if b, _ := c.db.CurrentBreak(uid); b != nil {
		_, err = c.db.EndBreak(uid)
	} else {
		_, err = c.db.StartBreak(uid)
		onBreak = true
	}
	if err != nil {
		jsonErr(w, http.StatusConflict, err.Error())
		return
	}
	monitor.SetOnBreak(onBreak)
	if !onBreak {
		capture.TakeNow()
	}
	c.mgr.SyncNow()
	jsonOut(w, http.StatusOK, map[string]any{"ok": true, "on_break": onBreak})
}

func (c *Client) handleIdle(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Reason string `json:"reason"`
		Note   string `json:"note"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&in); err != nil || strings.TrimSpace(in.Reason) == "" {
		jsonErr(w, http.StatusUnprocessableEntity, "Choose a reason.")
		return
	}
	c.mu.Lock()
	idle := c.idle
	c.idle = idleState{}
	c.mu.Unlock()
	if !idle.Start.IsZero() {
		end := time.Now()
		if idle.End != nil {
			end = *idle.End
		}
		c.db.SaveIdleReport(idle.Start, end, strings.TrimSpace(in.Reason), strings.TrimSpace(in.Note))
		c.mgr.SyncNow()
	}
	jsonOut(w, http.StatusOK, map[string]any{"ok": true})
}

func (c *Client) handleQuit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	jsonOut(w, http.StatusOK, map[string]any{"ok": true})
	go func() {
		time.Sleep(300 * time.Millisecond)
		Quit()
	}()
}
