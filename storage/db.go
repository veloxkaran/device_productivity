package storage

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// TimeEntry represents a single clock-in / clock-out session.
type TimeEntry struct {
	ID       int64
	UserID   int64
	ClockIn  time.Time
	ClockOut *time.Time // nil while session is active
	Source   string     // 'device' or 'web'
}

func (e *TimeEntry) IsActive() bool { return e.ClockOut == nil }

func (e *TimeEntry) DurationSec() int64 {
	end := time.Now()
	if e.ClockOut != nil {
		end = *e.ClockOut
	}
	d := end.Sub(e.ClockIn)
	if d < 0 {
		return 0
	}
	return int64(d.Seconds())
}

type DB struct {
	db *sql.DB
}

type User struct {
	ID           int64
	Username     string
	PasswordHash string
}

type Screenshot struct {
	ID        int64
	FilePath  string
	CreatedAt time.Time
}

type ActivityLog struct {
	ID          int64
	IsActive    bool
	IdleSeconds int64
	CreatedAt   time.Time
}

func New(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		return nil, err
	}
	if err := initSchema(db); err != nil {
		return nil, err
	}
	return &DB{db: db}, nil
}

func initSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			username      TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS screenshots (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			file_path  TEXT NOT NULL,
			created_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS activity_logs (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			is_active    INTEGER NOT NULL,
			idle_seconds INTEGER NOT NULL DEFAULT 0,
			created_at   TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS time_entries (
			id        INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id   INTEGER NOT NULL,
			clock_in  TEXT NOT NULL,
			clock_out TEXT,
			source    TEXT NOT NULL DEFAULT 'device'
		);
		CREATE INDEX IF NOT EXISTS idx_te_user_clock ON time_entries(user_id, clock_in);

		CREATE TABLE IF NOT EXISTS screenshots_sync (
			screenshot_id INTEGER PRIMARY KEY REFERENCES screenshots(id) ON DELETE CASCADE,
			synced_at     TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS cloud_sync_state (
			table_name TEXT PRIMARY KEY,
			last_sync  TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS device_registration (
			id          INTEGER PRIMARY KEY,
			device_uuid TEXT,
			machine_id  TEXT NOT NULL,
			company_id  INTEGER,
			status      TEXT NOT NULL DEFAULT 'pending',
			registered_at TEXT,
			updated_at    TEXT
		);

		CREATE TABLE IF NOT EXISTS activity_daily_summaries (
			id                  INTEGER PRIMARY KEY AUTOINCREMENT,
			work_date           TEXT NOT NULL UNIQUE,
			total_intervals     INTEGER NOT NULL DEFAULT 0,
			active_intervals    INTEGER NOT NULL DEFAULT 0,
			idle_intervals      INTEGER NOT NULL DEFAULT 0,
			mouse_intervals     INTEGER NOT NULL DEFAULT 0,
			keyboard_intervals  INTEGER NOT NULL DEFAULT 0,
			total_active_sec    INTEGER NOT NULL DEFAULT 0,
			total_idle_sec      INTEGER NOT NULL DEFAULT 0,
			updated_at          TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS time_entries_sync (
			entry_id         INTEGER PRIMARY KEY REFERENCES time_entries(id) ON DELETE CASCADE,
			synced_clock_out TEXT,
			synced_at        TEXT NOT NULL
		);
	`)
	if err != nil {
		return err
	}
	// Add source column to existing DBs — no-op if already present (new DBs have it from CREATE TABLE).
	db.Exec(`ALTER TABLE time_entries ADD COLUMN source TEXT NOT NULL DEFAULT 'device'`)
	return nil
}

// ── Device registration ───────────────────────────────────────────

type DeviceRegistration struct {
	DeviceUUID   string
	MachineID    string
	CompanyID    int64
	Status       string
	RegisteredAt time.Time
}

func (d *DB) SaveDeviceRegistration(reg DeviceRegistration) error {
	_, err := d.db.Exec(`
		INSERT INTO device_registration (id, device_uuid, machine_id, company_id, status, registered_at, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			device_uuid   = excluded.device_uuid,
			machine_id    = excluded.machine_id,
			company_id    = excluded.company_id,
			status        = excluded.status,
			registered_at = excluded.registered_at,
			updated_at    = excluded.updated_at
	`,
		reg.DeviceUUID,
		reg.MachineID,
		reg.CompanyID,
		reg.Status,
		reg.RegisteredAt.Format(time.RFC3339),
		time.Now().Format(time.RFC3339),
	)
	return err
}

func (d *DB) GetDeviceRegistration() (*DeviceRegistration, error) {
	row := d.db.QueryRow(
		`SELECT device_uuid, machine_id, company_id, status, registered_at FROM device_registration WHERE id = 1`,
	)
	var r DeviceRegistration
	var ts string
	err := row.Scan(&r.DeviceUUID, &r.MachineID, &r.CompanyID, &r.Status, &ts)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.RegisteredAt, _ = time.Parse(time.RFC3339, ts)
	return &r, nil
}

// ── Time entry methods ────────────────────────────────────────────

func (d *DB) ClockIn(userID int64) (*TimeEntry, error) {
	existing, err := d.GetCurrentEntry(userID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("already clocked in")
	}
	now := time.Now()
	res, err := d.db.Exec(
		"INSERT INTO time_entries (user_id, clock_in) VALUES (?, ?)",
		userID, now.Format(time.RFC3339),
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &TimeEntry{ID: id, UserID: userID, ClockIn: now}, nil
}

func (d *DB) ClockOut(userID int64) (*TimeEntry, error) {
	existing, err := d.GetCurrentEntry(userID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, fmt.Errorf("not clocked in")
	}
	now := time.Now()
	_, err = d.db.Exec(
		"UPDATE time_entries SET clock_out = ? WHERE id = ?",
		now.Format(time.RFC3339), existing.ID,
	)
	if err != nil {
		return nil, err
	}
	existing.ClockOut = &now
	return existing, nil
}

func (d *DB) GetCurrentEntry(userID int64) (*TimeEntry, error) {
	row := d.db.QueryRow(
		`SELECT id, user_id, clock_in FROM time_entries
		 WHERE user_id = ? AND clock_out IS NULL AND source = 'device'
		 ORDER BY clock_in DESC LIMIT 1`, userID,
	)
	var e TimeEntry
	var ts string
	if err := row.Scan(&e.ID, &e.UserID, &ts); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	e.ClockIn, _ = time.Parse(time.RFC3339, ts)
	return &e, nil
}

// HasOpenTimeEntry returns true if any user has an unclosed time entry.
// Used at startup to restore the clocked-in state.
func (d *DB) HasOpenTimeEntry() (bool, error) {
	var n int
	err := d.db.QueryRow("SELECT COUNT(*) FROM time_entries WHERE clock_out IS NULL AND source = 'device'").Scan(&n)
	return n > 0, err
}

// GetTimeEntries returns all entries for userID whose clock_in falls in [from, to).
func (d *DB) GetTimeEntries(userID int64, from, to time.Time) ([]TimeEntry, error) {
	rows, err := d.db.Query(
		`SELECT id, user_id, clock_in, clock_out FROM time_entries
		 WHERE user_id = ? AND clock_in >= ? AND clock_in < ?
		 ORDER BY clock_in ASC`,
		userID, from.Format(time.RFC3339), to.Format(time.RFC3339),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []TimeEntry
	for rows.Next() {
		var e TimeEntry
		var tin string
		var toutPtr *string
		if err := rows.Scan(&e.ID, &e.UserID, &tin, &toutPtr); err != nil {
			return nil, err
		}
		e.ClockIn, _ = time.Parse(time.RFC3339, tin)
		if toutPtr != nil {
			t, _ := time.Parse(time.RFC3339, *toutPtr)
			e.ClockOut = &t
		}
		list = append(list, e)
	}
	return list, rows.Err()
}

// TodayStats returns total worked seconds and session count for today.
func (d *DB) TodayStats(userID int64) (totalSec int64, sessions int, err error) {
	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	endOfDay := startOfDay.Add(24 * time.Hour)

	entries, err := d.GetTimeEntries(userID, startOfDay, endOfDay)
	if err != nil {
		return 0, 0, err
	}
	for _, e := range entries {
		totalSec += e.DurationSec()
		sessions++
	}
	return totalSec, sessions, nil
}

// EnforceRetention deletes activity logs, time entries, and screenshots
// older than the given number of days. It returns the file paths of
// screenshot files that were removed from the database so the caller
// can delete them from disk.
func (d *DB) EnforceRetention(days int) (deletedFiles []string, err error) {
	if days <= 0 {
		days = 30
	}
	cutoff := time.Now().AddDate(0, 0, -days).Format(time.RFC3339)

	// Collect screenshot file paths before deletion
	rows, err := d.db.Query(
		`SELECT s.file_path FROM screenshots s
		 LEFT JOIN screenshots_sync ss ON ss.screenshot_id = s.id
		 WHERE s.created_at < ?`, cutoff)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var p string
			if rows.Scan(&p) == nil {
				deletedFiles = append(deletedFiles, p)
			}
		}
	}

	_, err = d.db.Exec(`DELETE FROM screenshots WHERE created_at < ?`, cutoff)
	if err != nil {
		return deletedFiles, err
	}
	_, err = d.db.Exec(`DELETE FROM activity_logs WHERE created_at < ?`, cutoff)
	if err != nil {
		return deletedFiles, err
	}
	_, err = d.db.Exec(`DELETE FROM time_entries WHERE clock_in < ? AND clock_out IS NOT NULL`, cutoff)
	if err != nil {
		return deletedFiles, err
	}
	_, err = d.db.Exec(
		`DELETE FROM activity_daily_summaries WHERE work_date < date(?, 'unixepoch')`,
		time.Now().AddDate(0, 0, -days).Unix(),
	)
	return deletedFiles, err
}

func (d *DB) Close() error { return d.db.Close() }

func (d *DB) CreateUser(username, passwordHash string) error {
	_, err := d.db.Exec(
		"INSERT INTO users (username, password_hash) VALUES (?, ?)",
		username, passwordHash,
	)
	return err
}

func (d *DB) GetUserByUsername(username string) (*User, error) {
	row := d.db.QueryRow(
		"SELECT id, username, password_hash FROM users WHERE username = ?", username,
	)
	u := &User{}
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func (d *DB) GetUserByID(id int64) (*User, error) {
	row := d.db.QueryRow(
		"SELECT id, username, password_hash FROM users WHERE id = ?", id,
	)
	u := &User{}
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func (d *DB) UpdateUserPassword(id int64, passwordHash string) error {
	_, err := d.db.Exec(
		"UPDATE users SET password_hash = ? WHERE id = ?", passwordHash, id,
	)
	return err
}

func (d *DB) UserCount() (int, error) {
	var n int
	err := d.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&n)
	return n, err
}

func (d *DB) SaveScreenshot(filePath string) error {
	_, err := d.db.Exec(
		"INSERT INTO screenshots (file_path, created_at) VALUES (?, ?)",
		filePath, time.Now().Format(time.RFC3339),
	)
	return err
}

func (d *DB) GetRecentScreenshots(limit int) ([]Screenshot, error) {
	rows, err := d.db.Query(
		"SELECT id, file_path, created_at FROM screenshots ORDER BY created_at DESC LIMIT ?", limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Screenshot
	for rows.Next() {
		var s Screenshot
		var ts string
		if err := rows.Scan(&s.ID, &s.FilePath, &ts); err != nil {
			return nil, err
		}
		s.CreatedAt, _ = time.Parse(time.RFC3339, ts)
		list = append(list, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return list, nil
}

func (d *DB) SaveActivity(isActive bool, idleSeconds int64) error {
	active := 0
	if isActive {
		active = 1
	}
	_, err := d.db.Exec(
		"INSERT INTO activity_logs (is_active, idle_seconds, created_at) VALUES (?, ?, ?)",
		active, idleSeconds, time.Now().Format(time.RFC3339),
	)
	return err
}

func (d *DB) GetRecentActivity(limit int) ([]ActivityLog, error) {
	rows, err := d.db.Query(
		"SELECT id, is_active, idle_seconds, created_at FROM activity_logs ORDER BY created_at DESC LIMIT ?", limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []ActivityLog
	for rows.Next() {
		var a ActivityLog
		var active int
		var ts string
		if err := rows.Scan(&a.ID, &active, &a.IdleSeconds, &ts); err != nil {
			return nil, err
		}
		a.IsActive = active == 1
		a.CreatedAt, _ = time.Parse(time.RFC3339, ts)
		list = append(list, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return list, nil
}

// ── Cloud sync helpers ────────────────────────────────────────────

func (d *DB) GetUnsyncedScreenshots(limit int) ([]Screenshot, error) {
	rows, err := d.db.Query(`
		SELECT s.id, s.file_path, s.created_at
		FROM screenshots s
		LEFT JOIN screenshots_sync ss ON ss.screenshot_id = s.id
		WHERE ss.screenshot_id IS NULL
		ORDER BY s.created_at ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Screenshot
	for rows.Next() {
		var s Screenshot
		var ts string
		if err := rows.Scan(&s.ID, &s.FilePath, &ts); err != nil {
			return nil, err
		}
		s.CreatedAt, _ = time.Parse(time.RFC3339, ts)
		list = append(list, s)
	}
	return list, rows.Err()
}

func (d *DB) MarkScreenshotSynced(id int64) error {
	_, err := d.db.Exec(
		"INSERT OR REPLACE INTO screenshots_sync (screenshot_id, synced_at) VALUES (?, ?)",
		id, time.Now().Format(time.RFC3339),
	)
	return err
}

func (d *DB) GetLastSyncTime(table string) (time.Time, error) {
	var ts string
	err := d.db.QueryRow(
		"SELECT last_sync FROM cloud_sync_state WHERE table_name=?", table,
	).Scan(&ts)
	if err == sql.ErrNoRows {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	t, _ := time.Parse(time.RFC3339, ts)
	return t, nil
}

func (d *DB) SetLastSyncTime(table string, t time.Time) error {
	_, err := d.db.Exec(
		`INSERT OR REPLACE INTO cloud_sync_state (table_name, last_sync) VALUES (?, ?)`,
		table, t.Format(time.RFC3339),
	)
	return err
}

// ── Activity daily summaries ──────────────────────────────────────

type DailySummary struct {
	WorkDate          string
	TotalIntervals    int64
	ActiveIntervals   int64
	IdleIntervals     int64
	MouseIntervals    int64
	KeyboardIntervals int64
	TotalActiveSec    int64
	TotalIdleSec      int64
}

func (s DailySummary) ProductivityPercent() float64 {
	if s.TotalIntervals == 0 {
		return 0
	}
	return float64(s.ActiveIntervals) / float64(s.TotalIntervals) * 100
}

// UpsertDailySummary increments the counters for today's summary row.
func (d *DB) UpsertDailySummary(workDate string, active bool, activeSec, idleSec int64, mouseEvent, keyboardEvent int) error {
	activeInt  := int64(0)
	if active { activeInt = 1 }
	idleInt    := int64(1) - activeInt
	now := time.Now().Format(time.RFC3339)

	_, err := d.db.Exec(`
		INSERT INTO activity_daily_summaries
			(work_date, total_intervals, active_intervals, idle_intervals, mouse_intervals, keyboard_intervals, total_active_sec, total_idle_sec, updated_at)
		VALUES (?, 1, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(work_date) DO UPDATE SET
			total_intervals    = total_intervals + 1,
			active_intervals   = active_intervals + excluded.active_intervals,
			idle_intervals     = idle_intervals + excluded.idle_intervals,
			mouse_intervals    = mouse_intervals + ?,
			keyboard_intervals = keyboard_intervals + ?,
			total_active_sec   = total_active_sec + excluded.total_active_sec,
			total_idle_sec     = total_idle_sec + excluded.total_idle_sec,
			updated_at         = excluded.updated_at
	`, workDate, activeInt, idleInt, mouseEvent, keyboardEvent, activeSec, idleSec, now,
		mouseEvent, keyboardEvent)
	return err
}

func (d *DB) GetDailySummariesSince(since time.Time) ([]DailySummary, error) {
	var rows *sql.Rows
	var err error
	if since.IsZero() {
		rows, err = d.db.Query(
			`SELECT work_date, total_intervals, active_intervals, idle_intervals, mouse_intervals, keyboard_intervals, total_active_sec, total_idle_sec
			 FROM activity_daily_summaries ORDER BY work_date`)
	} else {
		rows, err = d.db.Query(
			`SELECT work_date, total_intervals, active_intervals, idle_intervals, mouse_intervals, keyboard_intervals, total_active_sec, total_idle_sec
			 FROM activity_daily_summaries WHERE updated_at > ? ORDER BY work_date`,
			since.Format(time.RFC3339))
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []DailySummary
	for rows.Next() {
		var s DailySummary
		if err := rows.Scan(&s.WorkDate, &s.TotalIntervals, &s.ActiveIntervals, &s.IdleIntervals,
			&s.MouseIntervals, &s.KeyboardIntervals, &s.TotalActiveSec, &s.TotalIdleSec); err != nil {
			return nil, err
		}
		list = append(list, s)
	}
	return list, rows.Err()
}

func (d *DB) GetTimeEntriesSince(since time.Time) ([]TimeEntry, error) {
	var rs *sql.Rows
	var err error
	if since.IsZero() {
		rs, err = d.db.Query(
			`SELECT id, user_id, clock_in, clock_out FROM time_entries ORDER BY clock_in`,
		)
	} else {
		rs, err = d.db.Query(
			`SELECT id, user_id, clock_in, clock_out FROM time_entries
			 WHERE clock_in > ? ORDER BY clock_in`,
			since.Format(time.RFC3339),
		)
	}
	if err != nil {
		return nil, err
	}
	defer rs.Close()

	var list []TimeEntry
	for rs.Next() {
		var e TimeEntry
		var tin string
		var tout *string
		if err := rs.Scan(&e.ID, &e.UserID, &tin, &tout); err != nil {
			return nil, err
		}
		e.ClockIn, _ = time.Parse(time.RFC3339, tin)
		if tout != nil {
			t, _ := time.Parse(time.RFC3339, *tout)
			e.ClockOut = &t
		}
		list = append(list, e)
	}
	return list, rs.Err()
}

// ── Offline sync helpers ──────────────────────────────────────────

// PendingSyncCounts holds how many local records are waiting to be uploaded.
type PendingSyncCounts struct {
	Screenshots int `json:"screenshots"`
	TimeEntries int `json:"time_entries"`
	Summaries   int `json:"summaries"`
}

// GetUnsyncedTimeEntries returns device-originated entries that have never been
// synced, or were synced while still in-progress (clock_out = NULL) but now
// have a clock_out. Web entries (source='web') are excluded — they originated
// from Hajir and must not be pushed back.
func (d *DB) GetUnsyncedTimeEntries() ([]TimeEntry, error) {
	rs, err := d.db.Query(`
		SELECT te.id, te.user_id, te.clock_in, te.clock_out
		FROM time_entries te
		LEFT JOIN time_entries_sync ts ON ts.entry_id = te.id
		WHERE te.source = 'device'
		  AND (ts.entry_id IS NULL
		   OR (ts.synced_clock_out IS NULL AND te.clock_out IS NOT NULL))
		ORDER BY te.clock_in ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rs.Close()

	var list []TimeEntry
	for rs.Next() {
		var e TimeEntry
		var tin string
		var tout *string
		if err := rs.Scan(&e.ID, &e.UserID, &tin, &tout); err != nil {
			return nil, err
		}
		e.ClockIn, _ = time.Parse(time.RFC3339, tin)
		if tout != nil {
			t, _ := time.Parse(time.RFC3339, *tout)
			e.ClockOut = &t
		}
		list = append(list, e)
	}
	return list, rs.Err()
}

// MarkTimeEntrySynced records what clock_out value was present when this entry
// was last uploaded. Call again after clock_out changes to re-sync the update.
func (d *DB) MarkTimeEntrySynced(id int64, clockOut *time.Time) error {
	var clockOutStr *string
	if clockOut != nil {
		s := clockOut.Format(time.RFC3339)
		clockOutStr = &s
	}
	_, err := d.db.Exec(`
		INSERT OR REPLACE INTO time_entries_sync (entry_id, synced_clock_out, synced_at)
		VALUES (?, ?, ?)
	`, id, clockOutStr, time.Now().Format(time.RFC3339))
	return err
}

// GetPendingSyncCounts returns how many records are queued for upload.
func (d *DB) GetPendingSyncCounts() (PendingSyncCounts, error) {
	var c PendingSyncCounts

	d.db.QueryRow(`
		SELECT COUNT(*) FROM screenshots s
		LEFT JOIN screenshots_sync ss ON ss.screenshot_id = s.id
		WHERE ss.screenshot_id IS NULL
	`).Scan(&c.Screenshots)

	d.db.QueryRow(`
		SELECT COUNT(*) FROM time_entries te
		LEFT JOIN time_entries_sync ts ON ts.entry_id = te.id
		WHERE ts.entry_id IS NULL
		   OR (ts.synced_clock_out IS NULL AND te.clock_out IS NOT NULL)
	`).Scan(&c.TimeEntries)

	var lastSyncStr string
	d.db.QueryRow(
		`SELECT last_sync FROM cloud_sync_state WHERE table_name = 'activity_daily_summaries'`,
	).Scan(&lastSyncStr)
	if lastSyncStr == "" {
		d.db.QueryRow(`SELECT COUNT(*) FROM activity_daily_summaries`).Scan(&c.Summaries)
	} else {
		d.db.QueryRow(
			`SELECT COUNT(*) FROM activity_daily_summaries WHERE updated_at > ?`, lastSyncStr,
		).Scan(&c.Summaries)
	}

	return c, nil
}

// UpsertWebEntry stores a web-originated clock-in/out pulled from Hajir.
// source is always 'web'. Idempotent on clock_in value.
func (d *DB) UpsertWebEntry(clockIn time.Time, clockOut *time.Time) error {
	clockInStr := clockIn.Format(time.RFC3339)
	var clockOutStr *string
	if clockOut != nil {
		s := clockOut.Format(time.RFC3339)
		clockOutStr = &s
	}
	_, err := d.db.Exec(`
		INSERT INTO time_entries (user_id, clock_in, clock_out, source)
		VALUES (1, ?, ?, 'web')
		ON CONFLICT DO NOTHING
	`, clockInStr, clockOutStr)
	// Also update clock_out if it changed (was null, now set).
	if clockOut != nil {
		d.db.Exec(`
			UPDATE time_entries SET clock_out = ?
			WHERE clock_in = ? AND source = 'web' AND clock_out IS NULL
		`, *clockOutStr, clockInStr)
	}
	return err
}

// HasOpenWebEntry returns true if there is a locally-stored web-sourced
// time entry that has no clock_out (i.e. an open web session).
func (d *DB) HasOpenWebEntry() (bool, error) {
	var n int
	err := d.db.QueryRow(
		`SELECT COUNT(*) FROM time_entries WHERE source = 'web' AND clock_out IS NULL`,
	).Scan(&n)
	return n > 0, err
}

// GetLastWebPullTime returns the time of the last successful web-entries pull.
func (d *DB) GetLastWebPullTime() (time.Time, error) {
	return d.GetLastSyncTime("web_entries_pull")
}

// SetLastWebPullTime records the time of the most recent web-entries pull.
func (d *DB) SetLastWebPullTime(t time.Time) error {
	return d.SetLastSyncTime("web_entries_pull", t)
}

func (d *DB) GetActivityLogsSince(since time.Time) ([]ActivityLog, error) {
	var rs *sql.Rows
	var err error
	if since.IsZero() {
		rs, err = d.db.Query(
			`SELECT id, is_active, idle_seconds, created_at FROM activity_logs ORDER BY created_at`,
		)
	} else {
		rs, err = d.db.Query(
			`SELECT id, is_active, idle_seconds, created_at FROM activity_logs
			 WHERE created_at > ? ORDER BY created_at`,
			since.Format(time.RFC3339),
		)
	}
	if err != nil {
		return nil, err
	}
	defer rs.Close()

	var list []ActivityLog
	for rs.Next() {
		var a ActivityLog
		var active int
		var ts string
		if err := rs.Scan(&a.ID, &active, &a.IdleSeconds, &ts); err != nil {
			return nil, err
		}
		a.IsActive = active == 1
		a.CreatedAt, _ = time.Parse(time.RFC3339, ts)
		list = append(list, a)
	}
	return list, rs.Err()
}
