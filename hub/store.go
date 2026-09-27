package hub

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const tsLayout = "2006-01-02T15:04:05Z"

func ts(t time.Time) string { return t.UTC().Format(tsLayout) }

func parseTS(s string) time.Time {
	t, _ := time.Parse(tsLayout, s)
	return t
}

type Store struct{ db *sql.DB }

type Device struct {
	ID              int64
	CompanyID       int64
	UserID          int64
	EmployeeName    string
	Name            string
	Platform        string
	AppVersion      string
	LastSeenAt      *time.Time
	LastStatus      string
	LastIdleSeconds int64
	LastApp         string
	IsClockedIn     bool
	RevokedAt       *time.Time
	CreatedAt       time.Time
}

type TimeEntry struct {
	ID       int64
	DeviceID int64
	UserID   int64
	ClockIn  time.Time
	ClockOut *time.Time
}

type Sample struct {
	CapturedAt  time.Time
	IsActive    bool
	IdleSeconds int64
	AppName     string
}

type Screenshot struct {
	ID         int64
	DeviceID   int64
	CompanyID  int64
	UserID     int64
	CapturedAt time.Time
	Path       string
	SizeBytes  int64
}

func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, s.migrateSettings()
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS devices (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	company_id INTEGER NOT NULL,
	user_id INTEGER NOT NULL,
	employee_name TEXT NOT NULL DEFAULT '',
	name TEXT NOT NULL DEFAULT '',
	token_hash TEXT NOT NULL UNIQUE,
	platform TEXT NOT NULL DEFAULT '',
	app_version TEXT NOT NULL DEFAULT '',
	last_seen_at TEXT,
	last_status TEXT NOT NULL DEFAULT '',
	last_idle_seconds INTEGER NOT NULL DEFAULT 0,
	last_app TEXT NOT NULL DEFAULT '',
	is_clocked_in INTEGER NOT NULL DEFAULT 0,
	created_by INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	revoked_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_devices_company ON devices(company_id, user_id);
CREATE TABLE IF NOT EXISTS time_entries (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	device_id INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
	company_id INTEGER NOT NULL,
	user_id INTEGER NOT NULL,
	clock_in TEXT NOT NULL,
	clock_out TEXT,
	UNIQUE(device_id, clock_in)
);
CREATE INDEX IF NOT EXISTS idx_te_company ON time_entries(company_id, user_id, clock_in);
CREATE TABLE IF NOT EXISTS samples (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	device_id INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
	company_id INTEGER NOT NULL,
	user_id INTEGER NOT NULL,
	captured_at TEXT NOT NULL,
	is_active INTEGER NOT NULL,
	idle_seconds INTEGER NOT NULL DEFAULT 0,
	app_name TEXT NOT NULL DEFAULT '',
	UNIQUE(device_id, captured_at)
);
CREATE INDEX IF NOT EXISTS idx_samples_company ON samples(company_id, user_id, captured_at);
CREATE TABLE IF NOT EXISTS screenshots (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	device_id INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
	company_id INTEGER NOT NULL,
	user_id INTEGER NOT NULL,
	captured_at TEXT NOT NULL,
	path TEXT NOT NULL,
	size_bytes INTEGER NOT NULL DEFAULT 0,
	UNIQUE(device_id, captured_at)
);
CREATE INDEX IF NOT EXISTS idx_shots_company ON screenshots(company_id, user_id, captured_at);
`)
	return err
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "hdv_" + hex.EncodeToString(b), nil
}

const deviceCols = `id, company_id, user_id, employee_name, name, platform, app_version, last_seen_at, last_status, last_idle_seconds, last_app, is_clocked_in, revoked_at, created_at`

func scanDevice(sc interface{ Scan(...any) error }) (*Device, error) {
	var d Device
	var seen, revoked sql.NullString
	var created string
	var clocked int
	if err := sc.Scan(&d.ID, &d.CompanyID, &d.UserID, &d.EmployeeName, &d.Name, &d.Platform, &d.AppVersion, &seen, &d.LastStatus, &d.LastIdleSeconds, &d.LastApp, &clocked, &revoked, &created); err != nil {
		return nil, err
	}
	d.IsClockedIn = clocked == 1
	d.CreatedAt = parseTS(created)
	if seen.Valid {
		t := parseTS(seen.String)
		d.LastSeenAt = &t
	}
	if revoked.Valid {
		t := parseTS(revoked.String)
		d.RevokedAt = &t
	}
	return &d, nil
}

func (s *Store) CreateDevice(companyID, userID, createdBy int64, employeeName, name string) (*Device, string, error) {
	token, err := newToken()
	if err != nil {
		return nil, "", err
	}
	res, err := s.db.Exec(`INSERT INTO devices (company_id, user_id, employee_name, name, token_hash, created_by, created_at) VALUES (?,?,?,?,?,?,?)`,
		companyID, userID, employeeName, name, hashToken(token), createdBy, ts(time.Now()))
	if err != nil {
		return nil, "", err
	}
	id, _ := res.LastInsertId()
	d, err := s.DeviceByID(id)
	return d, token, err
}

func (s *Store) DeviceByID(id int64) (*Device, error) {
	return scanDevice(s.db.QueryRow(`SELECT `+deviceCols+` FROM devices WHERE id = ?`, id))
}

func (s *Store) DeviceByToken(token string) (*Device, error) {
	if token == "" {
		return nil, errors.New("empty token")
	}
	return scanDevice(s.db.QueryRow(`SELECT `+deviceCols+` FROM devices WHERE token_hash = ? AND revoked_at IS NULL`, hashToken(token)))
}

func (s *Store) Devices(companyID, userID int64, includeRevoked bool) ([]*Device, error) {
	q := `SELECT ` + deviceCols + ` FROM devices WHERE company_id = ?`
	args := []any{companyID}
	if userID > 0 {
		q += ` AND user_id = ?`
		args = append(args, userID)
	}
	if !includeRevoked {
		q += ` AND revoked_at IS NULL`
	}
	q += ` ORDER BY last_seen_at DESC, id DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, d)
	}
	return list, rows.Err()
}

func (s *Store) RevokeDevice(companyID, id int64) (bool, error) {
	res, err := s.db.Exec(`UPDATE devices SET revoked_at = ?, last_status = 'offline' WHERE id = ? AND company_id = ? AND revoked_at IS NULL`, ts(time.Now()), id, companyID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) Heartbeat(id int64, status string, idle int64, app, platform, version string, clockedIn *bool) error {
	q := `UPDATE devices SET last_seen_at = ?, last_status = ?, last_idle_seconds = ?, last_app = ?,
		platform = CASE WHEN ? = '' THEN platform ELSE ? END,
		app_version = CASE WHEN ? = '' THEN app_version ELSE ? END`
	args := []any{ts(time.Now()), status, idle, app, platform, platform, version, version}
	if clockedIn != nil {
		q += `, is_clocked_in = ?`
		args = append(args, boolInt(*clockedIn))
	}
	q += ` WHERE id = ?`
	args = append(args, id)
	_, err := s.db.Exec(q, args...)
	return err
}

func (s *Store) Touch(id int64) error {
	_, err := s.db.Exec(`UPDATE devices SET last_seen_at = ? WHERE id = ?`, ts(time.Now()), id)
	return err
}

func (s *Store) SetClockedIn(id int64, v bool) error {
	_, err := s.db.Exec(`UPDATE devices SET is_clocked_in = ? WHERE id = ?`, boolInt(v), id)
	return err
}

func boolWord(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Store) UpsertTimeEntry(d *Device, in time.Time, out *time.Time) error {
	var outVal any
	if out != nil {
		outVal = ts(*out)
	}
	_, err := s.db.Exec(`INSERT INTO time_entries (device_id, company_id, user_id, clock_in, clock_out) VALUES (?,?,?,?,?)
		ON CONFLICT(device_id, clock_in) DO UPDATE SET clock_out = excluded.clock_out`,
		d.ID, d.CompanyID, d.UserID, ts(in), outVal)
	return err
}

func (s *Store) InsertSamples(d *Device, samples []Sample) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO samples (device_id, company_id, user_id, captured_at, is_active, idle_seconds, app_name) VALUES (?,?,?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, sm := range samples {
		if _, err := stmt.Exec(d.ID, d.CompanyID, d.UserID, ts(sm.CapturedAt), boolInt(sm.IsActive), sm.IdleSeconds, sm.AppName); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ScreenshotExists(deviceID int64, at time.Time) (int64, bool) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM screenshots WHERE device_id = ? AND captured_at = ?`, deviceID, ts(at)).Scan(&id)
	return id, err == nil
}

func (s *Store) InsertScreenshot(d *Device, at time.Time, path string, size int64) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO screenshots (device_id, company_id, user_id, captured_at, path, size_bytes) VALUES (?,?,?,?,?,?)`,
		d.ID, d.CompanyID, d.UserID, ts(at), path, size)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ScreenshotByID(id int64) (*Screenshot, error) {
	var sh Screenshot
	var at string
	err := s.db.QueryRow(`SELECT id, device_id, company_id, user_id, captured_at, path, size_bytes FROM screenshots WHERE id = ?`, id).
		Scan(&sh.ID, &sh.DeviceID, &sh.CompanyID, &sh.UserID, &at, &sh.Path, &sh.SizeBytes)
	sh.CapturedAt = parseTS(at)
	return &sh, err
}

func (s *Store) TimeEntries(companyID, userID int64, deviceIDs []int64, from, to time.Time) ([]TimeEntry, error) {
	q := `SELECT id, device_id, user_id, clock_in, clock_out FROM time_entries WHERE company_id = ? AND clock_in < ? AND (clock_out IS NULL OR clock_out >= ?)`
	args := []any{companyID, ts(to), ts(from)}
	if userID > 0 {
		q += ` AND user_id = ?`
		args = append(args, userID)
	}
	if len(deviceIDs) > 0 {
		q += ` AND device_id IN (?` + strings.Repeat(`,?`, len(deviceIDs)-1) + `)`
		for _, id := range deviceIDs {
			args = append(args, id)
		}
	}
	q += ` ORDER BY clock_in DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []TimeEntry
	for rows.Next() {
		var e TimeEntry
		var in string
		var out sql.NullString
		if err := rows.Scan(&e.ID, &e.DeviceID, &e.UserID, &in, &out); err != nil {
			return nil, err
		}
		e.ClockIn = parseTS(in)
		if out.Valid {
			t := parseTS(out.String)
			e.ClockOut = &t
		}
		list = append(list, e)
	}
	return list, rows.Err()
}

func (s *Store) Samples(companyID, userID, deviceID int64, from, to time.Time) ([]Sample, error) {
	q := `SELECT captured_at, is_active, idle_seconds, app_name FROM samples WHERE company_id = ? AND captured_at >= ? AND captured_at < ?`
	args := []any{companyID, ts(from), ts(to)}
	if userID > 0 {
		q += ` AND user_id = ?`
		args = append(args, userID)
	}
	if deviceID > 0 {
		q += ` AND device_id = ?`
		args = append(args, deviceID)
	}
	q += ` ORDER BY captured_at`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Sample
	for rows.Next() {
		var sm Sample
		var at string
		var active int
		if err := rows.Scan(&at, &active, &sm.IdleSeconds, &sm.AppName); err != nil {
			return nil, err
		}
		sm.CapturedAt = parseTS(at)
		sm.IsActive = active == 1
		list = append(list, sm)
	}
	return list, rows.Err()
}

func (s *Store) Screenshots(companyID, userID int64, from, to time.Time) ([]Screenshot, error) {
	rows, err := s.db.Query(`SELECT id, device_id, company_id, user_id, captured_at, path, size_bytes FROM screenshots
		WHERE company_id = ? AND user_id = ? AND captured_at >= ? AND captured_at < ? ORDER BY captured_at DESC`, companyID, userID, ts(from), ts(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Screenshot
	for rows.Next() {
		var sh Screenshot
		var at string
		if err := rows.Scan(&sh.ID, &sh.DeviceID, &sh.CompanyID, &sh.UserID, &at, &sh.Path, &sh.SizeBytes); err != nil {
			return nil, err
		}
		sh.CapturedAt = parseTS(at)
		list = append(list, sh)
	}
	return list, rows.Err()
}

func (s *Store) ScreenshotStats(deviceID int64, from, to time.Time) (int64, *time.Time) {
	var n int64
	var last sql.NullString
	s.db.QueryRow(`SELECT COUNT(*), MAX(captured_at) FROM screenshots WHERE device_id = ? AND captured_at >= ? AND captured_at < ?`, deviceID, ts(from), ts(to)).Scan(&n, &last)
	if last.Valid {
		t := parseTS(last.String)
		return n, &t
	}
	return n, nil
}

const oldScreenshotBatch = 1000

func (s *Store) ScreenshotCompanies() ([]int64, error) {
	rows, err := s.db.Query(`SELECT DISTINCT company_id FROM screenshots`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		list = append(list, id)
	}
	return list, rows.Err()
}

func (s *Store) OldCompanyScreenshots(companyID, afterID int64, before time.Time) ([]Screenshot, error) {
	rows, err := s.db.Query(`SELECT id, path FROM screenshots WHERE company_id = ? AND id > ? AND captured_at < ? ORDER BY id LIMIT ?`, companyID, afterID, ts(before), oldScreenshotBatch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Screenshot
	for rows.Next() {
		var sh Screenshot
		if err := rows.Scan(&sh.ID, &sh.Path); err != nil {
			return nil, err
		}
		list = append(list, sh)
	}
	return list, rows.Err()
}

func (s *Store) ManualByID(companyID, id int64) (ManualEntry, error) {
	var m ManualEntry
	err := s.db.QueryRow(`SELECT id, user_id, day, seconds, note, created_by, created_at FROM manual_entries WHERE id = ? AND company_id = ?`, id, companyID).
		Scan(&m.ID, &m.UserID, &m.Day, &m.Seconds, &m.Note, &m.CreatedBy, &m.CreatedAt)
	return m, err
}

func (s *Store) DeleteScreenshot(id int64) error {
	_, err := s.db.Exec(`DELETE FROM screenshots WHERE id = ?`, id)
	return err
}

func (s *Store) PruneSamples(before time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM samples WHERE captured_at < ?`, ts(before))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

type SampleRow struct {
	DeviceID   int64
	UserID     int64
	CapturedAt time.Time
	IsActive   bool
}

type Settings struct {
	WorkStart    string   `json:"work_start"`
	WorkEnd      string   `json:"work_end"`
	GraceMinutes int      `json:"grace_minutes"`
	WeeklyOff    []string `json:"weekly_off"`
	// ScreenshotIntervalSeconds controls how often desktop agents capture the screen.
	ScreenshotIntervalSeconds int `json:"screenshot_interval_seconds"`
	// ScreenshotQuality is the JPEG quality (30-95). ScreenshotMaxWidth is the
	// longest edge in pixels images are downscaled to (640-3840). Both control
	// screenshot file size and are pushed to agents on each heartbeat.
	ScreenshotQuality       int `json:"screenshot_quality"`
	ScreenshotMaxWidth      int `json:"screenshot_max_width"`
	ScreenshotRetentionDays int `json:"screenshot_retention_days"`
}

func DefaultSettings() Settings {
	return Settings{WorkStart: "09:00", WorkEnd: "18:00", GraceMinutes: 15, WeeklyOff: []string{"sat"}, ScreenshotIntervalSeconds: 180, ScreenshotQuality: 60, ScreenshotMaxWidth: 1920, ScreenshotRetentionDays: 90}
}

func (s *Store) migrateSettings() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS company_settings (company_id INTEGER PRIMARY KEY, data TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS company_modules (company_id INTEGER PRIMARY KEY, activity_enabled INTEGER NOT NULL DEFAULT 1, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS device_runtime (device_id INTEGER PRIMARY KEY, applied_interval INTEGER NOT NULL DEFAULT 0, capture_error TEXT NOT NULL DEFAULT '', last_capture_at TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS member_settings (company_id INTEGER NOT NULL, user_id INTEGER NOT NULL, screenshot_interval_seconds INTEGER, updated_at TEXT NOT NULL, PRIMARY KEY (company_id, user_id));
CREATE TABLE IF NOT EXISTS breaks (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	device_id INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
	company_id INTEGER NOT NULL,
	user_id INTEGER NOT NULL,
	start_at TEXT NOT NULL,
	end_at TEXT,
	UNIQUE(device_id, start_at)
);
CREATE INDEX IF NOT EXISTS idx_breaks_company ON breaks(company_id, user_id, start_at);
CREATE TABLE IF NOT EXISTS idle_reports (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	device_id INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
	company_id INTEGER NOT NULL,
	user_id INTEGER NOT NULL,
	start_at TEXT NOT NULL,
	end_at TEXT NOT NULL,
	reason TEXT NOT NULL,
	note TEXT NOT NULL DEFAULT '',
	UNIQUE(device_id, start_at)
);
CREATE INDEX IF NOT EXISTS idx_idle_company ON idle_reports(company_id, user_id, start_at);
CREATE TABLE IF NOT EXISTS manual_entries (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	company_id INTEGER NOT NULL,
	user_id INTEGER NOT NULL,
	day TEXT NOT NULL,
	seconds INTEGER NOT NULL,
	note TEXT NOT NULL DEFAULT '',
	created_by INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_manual_company ON manual_entries(company_id, user_id, day);
CREATE TABLE IF NOT EXISTS member_setting_audit (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	company_id INTEGER NOT NULL,
	user_id INTEGER NOT NULL,
	field TEXT NOT NULL,
	old_value TEXT NOT NULL DEFAULT '',
	new_value TEXT NOT NULL DEFAULT '',
	actor_id INTEGER NOT NULL DEFAULT 0,
	actor_name TEXT NOT NULL DEFAULT '',
	at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_msaudit ON member_setting_audit(company_id, user_id, at);
`)
	if err != nil {
		return err
	}
	// tracking_mode column: 'manual' (default) or 'auto'. ALTER is idempotent-guarded.
	if !s.hasColumn("member_settings", "tracking_mode") {
		if _, err := s.db.Exec(`ALTER TABLE member_settings ADD COLUMN tracking_mode TEXT NOT NULL DEFAULT 'manual'`); err != nil {
			return err
		}
	}
	// tracking_enabled column: per-member on/off. DEFAULT 1 (on) so adding it
	// never pauses anyone. A super admin / employer pauses one member here.
	if !s.hasColumn("member_settings", "tracking_enabled") {
		if _, err := s.db.Exec(`ALTER TABLE member_settings ADD COLUMN tracking_enabled INTEGER NOT NULL DEFAULT 1`); err != nil {
			return err
		}
	}
	return nil
}

// hasColumn reports whether a table already has a named column (for guarded migrations).
func (s *Store) hasColumn(table, col string) bool {
	rows, err := s.db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil && n == col {
			return true
		}
	}
	return false
}

type BreakRow struct {
	UserID int64
	Start  time.Time
	End    *time.Time
}

type ManualEntry struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"user_id"`
	Day       string `json:"date"`
	Seconds   int64  `json:"seconds"`
	Note      string `json:"note"`
	CreatedBy int64  `json:"created_by"`
	CreatedAt string `json:"created_at"`
}

func (s *Store) UpsertBreak(d *Device, start time.Time, end *time.Time) error {
	var endVal any
	if end != nil {
		endVal = ts(*end)
	}
	_, err := s.db.Exec(`INSERT INTO breaks (device_id, company_id, user_id, start_at, end_at) VALUES (?,?,?,?,?)
		ON CONFLICT(device_id, start_at) DO UPDATE SET end_at = excluded.end_at`, d.ID, d.CompanyID, d.UserID, ts(start), endVal)
	return err
}

func (s *Store) Breaks(companyID, userID int64, from, to time.Time) ([]BreakRow, error) {
	q := `SELECT user_id, start_at, end_at FROM breaks WHERE company_id = ? AND start_at < ? AND (end_at IS NULL OR end_at >= ?)`
	args := []any{companyID, ts(to), ts(from)}
	if userID > 0 {
		q += ` AND user_id = ?`
		args = append(args, userID)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []BreakRow
	for rows.Next() {
		var b BreakRow
		var st string
		var en sql.NullString
		if err := rows.Scan(&b.UserID, &st, &en); err != nil {
			return nil, err
		}
		b.Start = parseTS(st)
		if en.Valid {
			t := parseTS(en.String)
			b.End = &t
		}
		list = append(list, b)
	}
	return list, rows.Err()
}

func (s *Store) AddManual(companyID, userID, createdBy int64, day string, seconds int64, note string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO manual_entries (company_id, user_id, day, seconds, note, created_by, created_at) VALUES (?,?,?,?,?,?,?)`,
		companyID, userID, day, seconds, note, createdBy, ts(time.Now()))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) DeleteManual(companyID, id int64) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM manual_entries WHERE id = ? AND company_id = ?`, id, companyID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) Manual(companyID, userID int64, fromDay, toDay string) ([]ManualEntry, error) {
	q := `SELECT id, user_id, day, seconds, note, created_by, created_at FROM manual_entries WHERE company_id = ? AND day BETWEEN ? AND ?`
	args := []any{companyID, fromDay, toDay}
	if userID > 0 {
		q += ` AND user_id = ?`
		args = append(args, userID)
	}
	q += ` ORDER BY day, id`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []ManualEntry{}
	for rows.Next() {
		var m ManualEntry
		if err := rows.Scan(&m.ID, &m.UserID, &m.Day, &m.Seconds, &m.Note, &m.CreatedBy, &m.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

func (s *Store) GetSettings(companyID int64) Settings {
	cfg := DefaultSettings()
	var raw string
	if err := s.db.QueryRow(`SELECT data FROM company_settings WHERE company_id = ?`, companyID).Scan(&raw); err == nil {
		json.Unmarshal([]byte(raw), &cfg)
	}
	if cfg.ScreenshotRetentionDays < 7 || cfg.ScreenshotRetentionDays > 365 {
		cfg.ScreenshotRetentionDays = DefaultSettings().ScreenshotRetentionDays
	}
	return cfg
}

func (s *Store) SaveSettings(companyID int64, cfg Settings) error {
	b, _ := json.Marshal(cfg)
	_, err := s.db.Exec(`INSERT INTO company_settings (company_id, data, updated_at) VALUES (?,?,?)
		ON CONFLICT(company_id) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at`, companyID, string(b), ts(time.Now()))
	return err
}

// ActivityEnabled reports whether the company's Activity module is on.
// Default ON: a company Laravel never told us about is enabled.
func (s *Store) ActivityEnabled(companyID int64) bool {
	var v int
	if err := s.db.QueryRow(`SELECT activity_enabled FROM company_modules WHERE company_id = ?`, companyID).Scan(&v); err != nil {
		return true
	}
	return v != 0
}

func (s *Store) SetActivityEnabled(companyID int64, on bool) error {
	_, err := s.db.Exec(`INSERT INTO company_modules (company_id, activity_enabled, updated_at) VALUES (?,?,?)
		ON CONFLICT(company_id) DO UPDATE SET activity_enabled = excluded.activity_enabled, updated_at = excluded.updated_at`, companyID, boolInt(on), ts(time.Now()))
	return err
}

// SaveDeviceRuntime records what the desktop app reports it is currently doing.
func (s *Store) SaveDeviceRuntime(deviceID int64, applied int, captureErr, lastCapture string) error {
	_, err := s.db.Exec(`INSERT INTO device_runtime (device_id, applied_interval, capture_error, last_capture_at, updated_at) VALUES (?,?,?,?,?)
		ON CONFLICT(device_id) DO UPDATE SET applied_interval = excluded.applied_interval, capture_error = excluded.capture_error,
		last_capture_at = excluded.last_capture_at, updated_at = excluded.updated_at`, deviceID, applied, captureErr, lastCapture, ts(time.Now()))
	return err
}

type DeviceRuntime struct {
	AppliedInterval int
	CaptureError    string
	LastCaptureAt   string
}

func (s *Store) GetDeviceRuntime(deviceID int64) DeviceRuntime {
	var r DeviceRuntime
	s.db.QueryRow(`SELECT applied_interval, capture_error, last_capture_at FROM device_runtime WHERE device_id = ?`, deviceID).Scan(&r.AppliedInterval, &r.CaptureError, &r.LastCaptureAt)
	return r
}

// MemberScreenshotInterval returns the member's own interval override, or 0 when none is set.
func (s *Store) MemberScreenshotInterval(companyID, userID int64) int {
	var v sql.NullInt64
	if err := s.db.QueryRow(`SELECT screenshot_interval_seconds FROM member_settings WHERE company_id = ? AND user_id = ?`, companyID, userID).Scan(&v); err != nil || !v.Valid {
		return 0
	}
	return int(v.Int64)
}

// SetMemberScreenshotInterval stores a member override; secs <= 0 clears the
// override (falling back to the company default) without touching tracking_mode.
func (s *Store) SetMemberScreenshotInterval(companyID, userID int64, secs int) error {
	if secs <= 0 {
		// Clear just the interval; keep the row so tracking_mode survives.
		_, err := s.db.Exec(`UPDATE member_settings SET screenshot_interval_seconds = NULL, updated_at = ? WHERE company_id = ? AND user_id = ?`, ts(time.Now()), companyID, userID)
		return err
	}
	_, err := s.db.Exec(`INSERT INTO member_settings (company_id, user_id, screenshot_interval_seconds, updated_at) VALUES (?,?,?,?)
		ON CONFLICT(company_id, user_id) DO UPDATE SET screenshot_interval_seconds = excluded.screenshot_interval_seconds, updated_at = excluded.updated_at`, companyID, userID, secs, ts(time.Now()))
	return err
}

// MemberTrackingEnabled reports whether tracking is on for this member.
// Default ON: a member with no row, or a row from before this column existed, is enabled.
func (s *Store) MemberTrackingEnabled(companyID, userID int64) bool {
	var v sql.NullInt64
	if err := s.db.QueryRow(`SELECT tracking_enabled FROM member_settings WHERE company_id = ? AND user_id = ?`, companyID, userID).Scan(&v); err != nil || !v.Valid {
		return true
	}
	return v.Int64 != 0
}

// SetMemberTrackingEnabled turns tracking on/off for one member and records an
// audit row (who changed it, when).
func (s *Store) SetMemberTrackingEnabled(companyID, userID int64, on bool, actorID int64, actorName string) error {
	old := s.MemberTrackingEnabled(companyID, userID)
	_, err := s.db.Exec(`INSERT INTO member_settings (company_id, user_id, tracking_enabled, updated_at) VALUES (?,?,?,?)
		ON CONFLICT(company_id, user_id) DO UPDATE SET tracking_enabled = excluded.tracking_enabled, updated_at = excluded.updated_at`, companyID, userID, boolInt(on), ts(time.Now()))
	if err != nil {
		return err
	}
	if old != on {
		s.db.Exec(`INSERT INTO member_setting_audit (company_id, user_id, field, old_value, new_value, actor_id, actor_name, at) VALUES (?,?,?,?,?,?,?,?)`,
			companyID, userID, "tracking_enabled", boolWord(old), boolWord(on), actorID, actorName, ts(time.Now()))
	}
	return nil
}

// MemberTrackingMode returns "auto" or "manual" (the default) for a member.
func (s *Store) MemberTrackingMode(companyID, userID int64) string {
	var m sql.NullString
	if err := s.db.QueryRow(`SELECT tracking_mode FROM member_settings WHERE company_id = ? AND user_id = ?`, companyID, userID).Scan(&m); err != nil || !m.Valid || m.String == "" {
		return "manual"
	}
	if m.String == "auto" {
		return "auto"
	}
	return "manual"
}

// SetMemberTrackingMode sets a member's tracking mode and records an audit row
// (who changed it, from what, to what, when). mode must be "auto" or "manual".
func (s *Store) SetMemberTrackingMode(companyID, userID int64, mode string, actorID int64, actorName string) error {
	if mode != "auto" {
		mode = "manual"
	}
	old := s.MemberTrackingMode(companyID, userID)
	_, err := s.db.Exec(`INSERT INTO member_settings (company_id, user_id, tracking_mode, updated_at) VALUES (?,?,?,?)
		ON CONFLICT(company_id, user_id) DO UPDATE SET tracking_mode = excluded.tracking_mode, updated_at = excluded.updated_at`, companyID, userID, mode, ts(time.Now()))
	if err != nil {
		return err
	}
	if old != mode {
		s.db.Exec(`INSERT INTO member_setting_audit (company_id, user_id, field, old_value, new_value, actor_id, actor_name, at) VALUES (?,?,?,?,?,?,?,?)`,
			companyID, userID, "tracking_mode", old, mode, actorID, actorName, ts(time.Now()))
	}
	return nil
}

// MemberModeAudit returns recent tracking-mode changes for a member, newest first.
func (s *Store) MemberModeAudit(companyID, userID int64, limit int) []map[string]any {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	out := []map[string]any{}
	rows, err := s.db.Query(`SELECT old_value, new_value, actor_id, actor_name, at FROM member_setting_audit WHERE company_id = ? AND user_id = ? AND field = 'tracking_mode' ORDER BY id DESC LIMIT ?`, companyID, userID, limit)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var ov, nv, an, at string
		var aid int64
		if rows.Scan(&ov, &nv, &aid, &an, &at) == nil {
			out = append(out, map[string]any{"from": ov, "to": nv, "actor_id": aid, "actor_name": an, "at": at})
		}
	}
	return out
}

// EffectiveScreenshotInterval is the member override if set, else the company setting.
func (s *Store) EffectiveScreenshotInterval(companyID, userID int64) int {
	if v := s.MemberScreenshotInterval(companyID, userID); v > 0 {
		return v
	}
	return s.GetSettings(companyID).ScreenshotIntervalSeconds
}

func (s *Store) EachSample(companyID, userID int64, from, to time.Time, fn func(SampleRow)) error {
	q := `SELECT device_id, user_id, captured_at, is_active FROM samples WHERE company_id = ? AND captured_at >= ? AND captured_at < ?`
	args := []any{companyID, ts(from), ts(to)}
	if userID > 0 {
		q += ` AND user_id = ?`
		args = append(args, userID)
	}
	q += ` ORDER BY device_id, captured_at`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r SampleRow
		var at string
		var active int
		if err := rows.Scan(&r.DeviceID, &r.UserID, &at, &active); err != nil {
			return err
		}
		r.CapturedAt = parseTS(at)
		r.IsActive = active == 1
		fn(r)
	}
	return rows.Err()
}

func (s *Store) CompanyScreenshots(companyID, userID int64, from, to time.Time) ([]Screenshot, error) {
	q := `SELECT id, device_id, company_id, user_id, captured_at, path, size_bytes FROM screenshots WHERE company_id = ? AND captured_at >= ? AND captured_at < ?`
	args := []any{companyID, ts(from), ts(to)}
	if userID > 0 {
		q += ` AND user_id = ?`
		args = append(args, userID)
	}
	q += ` ORDER BY captured_at`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Screenshot
	for rows.Next() {
		var sh Screenshot
		var at string
		if err := rows.Scan(&sh.ID, &sh.DeviceID, &sh.CompanyID, &sh.UserID, &at, &sh.Path, &sh.SizeBytes); err != nil {
			return nil, err
		}
		sh.CapturedAt = parseTS(at)
		list = append(list, sh)
	}
	return list, rows.Err()
}

type IdleReport struct {
	ID     int64  `json:"id"`
	Start  string `json:"start"`
	End    string `json:"end"`
	Reason string `json:"reason"`
	Note   string `json:"note"`
	Secs   int64  `json:"seconds"`
}

func (s *Store) InsertIdleReport(d *Device, start, end time.Time, reason, note string) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO idle_reports (device_id, company_id, user_id, start_at, end_at, reason, note) VALUES (?,?,?,?,?,?,?)`,
		d.ID, d.CompanyID, d.UserID, ts(start), ts(end), reason, note)
	return err
}

func (s *Store) IdleReports(companyID, userID int64, from, to time.Time) ([]IdleReport, error) {
	rows, err := s.db.Query(`SELECT id, start_at, end_at, reason, note FROM idle_reports WHERE company_id = ? AND user_id = ? AND start_at >= ? AND start_at < ? ORDER BY start_at`, companyID, userID, ts(from), ts(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []IdleReport{}
	for rows.Next() {
		var r IdleReport
		if err := rows.Scan(&r.ID, &r.Start, &r.End, &r.Reason, &r.Note); err != nil {
			return nil, err
		}
		st, en := parseTS(r.Start), parseTS(r.End)
		r.Secs = int64(en.Sub(st).Seconds())
		r.Start, r.End = st.Format(time.RFC3339), en.Format(time.RFC3339)
		list = append(list, r)
	}
	return list, rows.Err()
}

func (s *Store) RotateDeviceToken(companyID, userID int64, name, employeeName, platform string) (*Device, string, error) {
	token, err := newToken()
	if err != nil {
		return nil, "", err
	}
	var id int64
	err = s.db.QueryRow(`SELECT id FROM devices WHERE company_id = ? AND user_id = ? AND name = ? AND revoked_at IS NULL ORDER BY id DESC LIMIT 1`, companyID, userID, name).Scan(&id)
	if err == nil {
		if _, err := s.db.Exec(`UPDATE devices SET token_hash = ?, employee_name = ?, platform = ? WHERE id = ?`, hashToken(token), employeeName, platform, id); err != nil {
			return nil, "", err
		}
		d, err := s.DeviceByID(id)
		return d, token, err
	}
	res, err := s.db.Exec(`INSERT INTO devices (company_id, user_id, employee_name, name, token_hash, platform, created_by, created_at) VALUES (?,?,?,?,?,?,?,?)`,
		companyID, userID, employeeName, name, hashToken(token), platform, userID, ts(time.Now()))
	if err != nil {
		return nil, "", err
	}
	id, _ = res.LastInsertId()
	d, err := s.DeviceByID(id)
	return d, token, err
}
