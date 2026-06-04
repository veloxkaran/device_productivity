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
			clock_out TEXT
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

		CREATE TABLE IF NOT EXISTS input_logs (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			recorded_at    TEXT NOT NULL,
			key_events     INTEGER NOT NULL DEFAULT 0,
			mouse_clicks   INTEGER NOT NULL DEFAULT 0,
			mouse_distance REAL    NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_input_time ON input_logs(recorded_at DESC);
	`)
	return err
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
		 WHERE user_id = ? AND clock_out IS NULL
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
	err := d.db.QueryRow("SELECT COUNT(*) FROM time_entries WHERE clock_out IS NULL").Scan(&n)
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

// ── Input log methods ─────────────────────────────────────────────

// InputLog is one 1-minute bucket of aggregated keyboard/mouse activity.
type InputLog struct {
	ID            int64
	RecordedAt    time.Time
	KeyEvents     int64
	MouseClicks   int64
	MouseDistance float64
}

func (d *DB) SaveInputLog(recordedAt time.Time, keyEvents, mouseClicks int64, mouseDist float64) error {
	_, err := d.db.Exec(
		`INSERT INTO input_logs (recorded_at, key_events, mouse_clicks, mouse_distance)
		 VALUES (?, ?, ?, ?)`,
		recordedAt.Format(time.RFC3339), keyEvents, mouseClicks, mouseDist,
	)
	return err
}

func (d *DB) GetRecentInputLogs(limit int) ([]InputLog, error) {
	rows, err := d.db.Query(
		`SELECT id, recorded_at, key_events, mouse_clicks, mouse_distance
		 FROM input_logs ORDER BY recorded_at DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []InputLog
	for rows.Next() {
		var l InputLog
		var ts string
		if err := rows.Scan(&l.ID, &ts, &l.KeyEvents, &l.MouseClicks, &l.MouseDistance); err != nil {
			return nil, err
		}
		l.RecordedAt, _ = time.Parse(time.RFC3339, ts)
		list = append(list, l)
	}
	return list, rows.Err()
}

// PurgeOldData deletes rows older than retainDays from screenshots, activity_logs, and input_logs.
func (d *DB) PurgeOldData(retainDays int) error {
	cutoff := time.Now().AddDate(0, 0, -retainDays).Format(time.RFC3339)
	tables := []string{"screenshots", "activity_logs", "input_logs"}
	for _, t := range tables {
		col := "created_at"
		if t == "input_logs" {
			col = "recorded_at"
		}
		if _, err := d.db.Exec(
			fmt.Sprintf("DELETE FROM %s WHERE %s < ?", t, col), cutoff,
		); err != nil {
			return fmt.Errorf("purge %s: %w", t, err)
		}
	}
	return nil
}

// DBStats returns row counts for the main tables.
func (d *DB) DBStats() (map[string]int64, error) {
	stats := map[string]int64{}
	tables := []string{"screenshots", "activity_logs", "input_logs", "time_entries"}
	for _, t := range tables {
		var n int64
		if err := d.db.QueryRow("SELECT COUNT(*) FROM " + t).Scan(&n); err != nil {
			return nil, err
		}
		stats[t] = n
	}
	return stats, nil
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
