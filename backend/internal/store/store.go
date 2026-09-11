package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Hewbacca/CatchupDVR/backend/internal/guide"
	"github.com/Hewbacca/CatchupDVR/backend/internal/model"
)

var (
	ErrTunerConflict            = errors.New("all tuners are reserved for this time")
	ErrAuthenticationConfigured = errors.New("an account has already been configured")
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;
CREATE TABLE IF NOT EXISTS channels (
  id TEXT PRIMARY KEY,
  number TEXT NOT NULL,
  name TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS programs (
  id TEXT PRIMARY KEY,
  channel_id TEXT NOT NULL REFERENCES channels(id),
  start_unix INTEGER NOT NULL,
  end_unix INTEGER NOT NULL,
  title TEXT NOT NULL,
  subtitle TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  category TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS programs_window ON programs(start_unix, end_unix);
CREATE TABLE IF NOT EXISTS recordings (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  program_id TEXT NOT NULL,
  channel_id TEXT NOT NULL,
  channel_number TEXT NOT NULL,
  title TEXT NOT NULL,
  program_start_unix INTEGER NOT NULL,
  program_end_unix INTEGER NOT NULL,
  scheduled_start_unix INTEGER NOT NULL,
  scheduled_end_unix INTEGER NOT NULL,
  status TEXT NOT NULL,
  playlist_path TEXT NOT NULL DEFAULT '',
  process_id INTEGER NOT NULL DEFAULT 0,
  started_at_unix INTEGER NOT NULL DEFAULT 0,
  finished_at_unix INTEGER NOT NULL DEFAULT 0,
  heartbeat_at_unix INTEGER NOT NULL DEFAULT 0,
  error_message TEXT NOT NULL DEFAULT '',
  created_at_unix INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS recordings_allocation ON recordings(status, scheduled_start_unix, scheduled_end_unix);
CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS authentication (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  username TEXT NOT NULL,
  password_hash TEXT NOT NULL,
  session_secret BLOB NOT NULL,
  created_at_unix INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS favorite_channels (
  username TEXT NOT NULL,
  channel_id TEXT NOT NULL,
  created_at_unix INTEGER NOT NULL,
  PRIMARY KEY(username, channel_id)
);
`)
	if err != nil {
		return err
	}
	for name, definition := range map[string]string{
		"process_id":        "INTEGER NOT NULL DEFAULT 0",
		"started_at_unix":   "INTEGER NOT NULL DEFAULT 0",
		"finished_at_unix":  "INTEGER NOT NULL DEFAULT 0",
		"heartbeat_at_unix": "INTEGER NOT NULL DEFAULT 0",
		"error_message":     "TEXT NOT NULL DEFAULT ''",
	} {
		if err := s.ensureColumn(ctx, "recordings", name, definition); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensureColumn(ctx context.Context, table, name, definition string) error {
	rows, err := s.db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var position int
		var columnName, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&position, &columnName, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		if columnName == name {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN "+name+" "+definition)
	return err
}

func (s *Store) ReplaceGuide(ctx context.Context, xmlData []byte) (int, int, error) {
	channels, programs, err := guide.ParseXMLTV(bytes.NewReader(xmlData))
	if err != nil {
		return 0, 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM programs"); err != nil {
		return 0, 0, err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM channels"); err != nil {
		return 0, 0, err
	}
	for _, channel := range channels {
		if _, err = tx.ExecContext(ctx, "INSERT INTO channels(id, number, name) VALUES(?, ?, ?)", channel.ID, channel.Number, channel.Name); err != nil {
			return 0, 0, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE recordings
SET channel_number=(SELECT number FROM channels WHERE channels.id=recordings.channel_id)
WHERE status='scheduled' AND EXISTS (SELECT 1 FROM channels WHERE channels.id=recordings.channel_id)`); err != nil {
		return 0, 0, err
	}
	for _, program := range programs {
		if _, err = tx.ExecContext(ctx, `INSERT INTO programs(id, channel_id, start_unix, end_unix, title, subtitle, description, category) VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
			program.ID, program.ChannelID, program.Start.Unix(), program.End.Unix(), program.Title, program.Subtitle, program.Description, program.Category); err != nil {
			return 0, 0, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO metadata(key, value) VALUES('guide_updated_at', ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return 0, 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, 0, err
	}
	return len(channels), len(programs), nil
}

func (s *Store) Guide(ctx context.Context, from, to time.Time) (model.Guide, error) {
	result := model.Guide{From: from, To: to, Channels: []model.Channel{}, Programs: []model.Program{}}
	rows, err := s.db.QueryContext(ctx, "SELECT id, number, name FROM channels ORDER BY CAST(number AS REAL), number")
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var channel model.Channel
		if err := rows.Scan(&channel.ID, &channel.Number, &channel.Name); err != nil {
			rows.Close()
			return result, err
		}
		result.Channels = append(result.Channels, channel)
	}
	rows.Close()

	rows, err = s.db.QueryContext(ctx, `SELECT p.id, p.channel_id, c.number, c.name, p.start_unix, p.end_unix, p.title, p.subtitle, p.description, p.category
FROM programs p JOIN channels c ON c.id=p.channel_id
WHERE p.start_unix < ? AND p.end_unix > ? ORDER BY p.start_unix`, to.Unix(), from.Unix())
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var program model.Program
		var start, end int64
		if err := rows.Scan(&program.ID, &program.ChannelID, &program.Channel.Number, &program.Channel.Name, &start, &end, &program.Title, &program.Subtitle, &program.Description, &program.Category); err != nil {
			return result, err
		}
		program.Channel.ID = program.ChannelID
		program.Start = time.Unix(start, 0).UTC()
		program.End = time.Unix(end, 0).UTC()
		result.Programs = append(result.Programs, program)
	}
	return result, rows.Err()
}

// GuideDays returns only calendar days that contain at least one programme.
// The server's local timezone is used so the selector matches the broadcast
// schedule and the timestamp display shown to the household.
func (s *Store) GuideDays(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT start_unix, end_unix FROM programs")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	days := map[string]struct{}{}
	for rows.Next() {
		var startUnix, endUnix int64
		if err := rows.Scan(&startUnix, &endUnix); err != nil {
			return nil, err
		}
		start := time.Unix(startUnix, 0).In(time.Local)
		end := time.Unix(endUnix, 0).In(time.Local)
		day := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.Local)
		// An exactly-midnight end belongs to the preceding day, not the next one.
		if end.Hour() == 0 && end.Minute() == 0 && end.Second() == 0 && end.Nanosecond() == 0 && end.After(start) {
			end = end.Add(-time.Nanosecond)
		}
		lastDay := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.Local)
		for !day.After(lastDay) {
			days[day.Format("2006-01-02")] = struct{}{}
			day = day.AddDate(0, 0, 1)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]string, 0, len(days))
	for day := range days {
		result = append(result, day)
	}
	sort.Strings(result)
	return result, nil
}

// SearchGuide finds programmes across every guide day currently stored. LIKE
// wildcards from a search box are escaped so a viewer's text is always treated
// as text, not as a database pattern.
func (s *Store) SearchGuide(ctx context.Context, query string) ([]model.Program, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []model.Program{}, nil
	}
	escaped := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(query)
	pattern := "%" + escaped + "%"
	rows, err := s.db.QueryContext(ctx, `SELECT p.id, p.channel_id, c.number, c.name, p.start_unix, p.end_unix, p.title, p.subtitle, p.description, p.category
FROM programs p JOIN channels c ON c.id=p.channel_id
WHERE p.title LIKE ? ESCAPE '\' COLLATE NOCASE
   OR p.subtitle LIKE ? ESCAPE '\' COLLATE NOCASE
   OR p.description LIKE ? ESCAPE '\' COLLATE NOCASE
   OR p.category LIKE ? ESCAPE '\' COLLATE NOCASE
   OR c.name LIKE ? ESCAPE '\' COLLATE NOCASE
   OR c.number LIKE ? ESCAPE '\' COLLATE NOCASE
ORDER BY p.start_unix, c.number LIMIT 200`, pattern, pattern, pattern, pattern, pattern, pattern)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]model.Program, 0)
	for rows.Next() {
		var program model.Program
		var start, end int64
		if err := rows.Scan(&program.ID, &program.ChannelID, &program.Channel.Number, &program.Channel.Name, &start, &end, &program.Title, &program.Subtitle, &program.Description, &program.Category); err != nil {
			return nil, err
		}
		program.Channel.ID = program.ChannelID
		program.Start = time.Unix(start, 0).UTC()
		program.End = time.Unix(end, 0).UTC()
		result = append(result, program)
	}
	return result, rows.Err()
}

func (s *Store) Schedule(ctx context.Context, programID string, preMinutes, postMinutes, tunerCount int) (model.Recording, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Recording{}, err
	}
	defer tx.Rollback()
	var result model.Recording
	var programStart, programEnd int64
	err = tx.QueryRowContext(ctx, `SELECT p.id, p.channel_id, c.number, p.title, p.start_unix, p.end_unix
FROM programs p JOIN channels c ON c.id=p.channel_id WHERE p.id=?`, programID).
		Scan(&result.ProgramID, &result.ChannelID, &result.ChannelNumber, &result.Title, &programStart, &programEnd)
	if err != nil {
		return result, err
	}
	result.ProgramStart = time.Unix(programStart, 0).UTC()
	result.ProgramEnd = time.Unix(programEnd, 0).UTC()
	result.ScheduledStart = result.ProgramStart.Add(-time.Duration(preMinutes) * time.Minute)
	result.ScheduledEnd = result.ProgramEnd.Add(time.Duration(postMinutes) * time.Minute)
	result.Status = "scheduled"
	result.CreatedAt = time.Now().UTC()
	if tunerCount < 1 {
		tunerCount = 1
	}
	var overlapping int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM recordings
WHERE status IN ('scheduled','recording') AND scheduled_start_unix < ? AND scheduled_end_unix > ?`, result.ScheduledEnd.Unix(), result.ScheduledStart.Unix()).Scan(&overlapping)
	if err != nil {
		return result, err
	}
	if overlapping >= tunerCount {
		return result, ErrTunerConflict
	}
	insert, err := tx.ExecContext(ctx, `INSERT INTO recordings(program_id, channel_id, channel_number, title, program_start_unix, program_end_unix, scheduled_start_unix, scheduled_end_unix, status, created_at_unix)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, result.ProgramID, result.ChannelID, result.ChannelNumber, result.Title, programStart, programEnd, result.ScheduledStart.Unix(), result.ScheduledEnd.Unix(), result.Status, result.CreatedAt.Unix())
	if err != nil {
		return result, err
	}
	result.ID, err = insert.LastInsertId()
	if err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Store) Recordings(ctx context.Context) ([]model.Recording, error) {
	rows, err := s.db.QueryContext(ctx, recordingSelect+` ORDER BY scheduled_start_unix`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []model.Recording{}
	for rows.Next() {
		var recording model.Recording
		if err := scanRecording(rows, &recording); err != nil {
			return nil, err
		}
		result = append(result, recording)
	}
	return result, rows.Err()
}

const recordingSelect = `SELECT id, program_id, channel_id, channel_number, title,
program_start_unix, program_end_unix, scheduled_start_unix, scheduled_end_unix,
status, playlist_path, process_id, started_at_unix, finished_at_unix,
heartbeat_at_unix, error_message, created_at_unix FROM recordings`

type rowScanner interface {
	Scan(...any) error
}

func scanRecording(row rowScanner, recording *model.Recording) error {
	var programStart, programEnd, scheduledStart, scheduledEnd, created int64
	var started, finished, heartbeat int64
	if err := row.Scan(&recording.ID, &recording.ProgramID, &recording.ChannelID,
		&recording.ChannelNumber, &recording.Title, &programStart, &programEnd,
		&scheduledStart, &scheduledEnd, &recording.Status, &recording.PlaylistPath,
		&recording.ProcessID, &started, &finished, &heartbeat,
		&recording.ErrorMessage, &created); err != nil {
		return err
	}
	recording.ProgramStart = time.Unix(programStart, 0).UTC()
	recording.ProgramEnd = time.Unix(programEnd, 0).UTC()
	recording.ScheduledStart = time.Unix(scheduledStart, 0).UTC()
	recording.ScheduledEnd = time.Unix(scheduledEnd, 0).UTC()
	recording.CreatedAt = time.Unix(created, 0).UTC()
	recording.StartedAt = unixTimePointer(started)
	recording.FinishedAt = unixTimePointer(finished)
	recording.HeartbeatAt = unixTimePointer(heartbeat)
	return nil
}

func unixTimePointer(value int64) *time.Time {
	if value == 0 {
		return nil
	}
	result := time.Unix(value, 0).UTC()
	return &result
}

// ClaimDueRecording atomically moves the next runnable job into recording state.
// It also closes jobs whose complete window elapsed while the service was offline.
func (s *Store) ClaimDueRecording(ctx context.Context, now time.Time) (*model.Recording, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE recordings SET status='failed', finished_at_unix=?,
error_message='recording window elapsed before capture started'
WHERE status='scheduled' AND scheduled_end_unix<=?`, now.Unix(), now.Unix()); err != nil {
		return nil, err
	}
	row := tx.QueryRowContext(ctx, recordingSelect+`
WHERE status='scheduled' AND scheduled_start_unix<=? AND scheduled_end_unix>?
ORDER BY scheduled_start_unix, id LIMIT 1`, now.Unix(), now.Unix())
	var recording model.Recording
	if err := scanRecording(row, &recording); errors.Is(err, sql.ErrNoRows) {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE recordings SET status='recording',
started_at_unix=CASE WHEN started_at_unix=0 THEN ? ELSE started_at_unix END,
heartbeat_at_unix=?, finished_at_unix=0, error_message='', process_id=0
WHERE id=? AND status='scheduled'`, now.Unix(), now.Unix(), recording.ID)
	if err != nil {
		return nil, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return nil, fmt.Errorf("claim recording %d", recording.ID)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	recording.Status = "recording"
	recording.StartedAt = unixTimePointer(now.Unix())
	recording.HeartbeatAt = unixTimePointer(now.Unix())
	return &recording, nil
}

func (s *Store) InterruptedRecordings(ctx context.Context) ([]model.Recording, error) {
	rows, err := s.db.QueryContext(ctx, recordingSelect+` WHERE status='recording' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []model.Recording
	for rows.Next() {
		var recording model.Recording
		if err := scanRecording(rows, &recording); err != nil {
			return nil, err
		}
		result = append(result, recording)
	}
	return result, rows.Err()
}

func (s *Store) SetRecordingProcess(ctx context.Context, id int64, playlistPath string, processID int, now time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE recordings SET
playlist_path=CASE WHEN ?='' THEN playlist_path ELSE ? END,
process_id=?, heartbeat_at_unix=? WHERE id=? AND status='recording'`,
		playlistPath, playlistPath, processID, now.Unix(), id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return fmt.Errorf("recording %d is not active", id)
	}
	return nil
}

func (s *Store) TouchRecording(ctx context.Context, id int64, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE recordings SET heartbeat_at_unix=? WHERE id=? AND status='recording'`, now.Unix(), id)
	return err
}

func (s *Store) FinishRecording(ctx context.Context, id int64, status, message string, now time.Time) error {
	if status != "completed" && status != "failed" {
		return fmt.Errorf("invalid terminal recording status %q", status)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE recordings SET status=?, process_id=0,
heartbeat_at_unix=?, finished_at_unix=?, error_message=? WHERE id=? AND status='recording'`,
		status, now.Unix(), now.Unix(), message, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return fmt.Errorf("recording %d is not active", id)
	}
	return nil
}

func (s *Store) RequeueRecording(ctx context.Context, id int64, message string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE recordings SET status='scheduled', process_id=0,
heartbeat_at_unix=0, finished_at_unix=0, error_message=? WHERE id=? AND status='recording'`, message, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return fmt.Errorf("recording %d is not active", id)
	}
	return nil
}

func (s *Store) CancelRecording(ctx context.Context, id int64, now time.Time) (model.Recording, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Recording{}, err
	}
	defer tx.Rollback()
	var recording model.Recording
	if err := scanRecording(tx.QueryRowContext(ctx, recordingSelect+` WHERE id=?`, id), &recording); err != nil {
		return model.Recording{}, err
	}
	if recording.Status == "scheduled" || recording.Status == "recording" {
		if _, err := tx.ExecContext(ctx, `UPDATE recordings SET status='cancelled', process_id=0,
heartbeat_at_unix=?, finished_at_unix=?, error_message='' WHERE id=?`, now.Unix(), now.Unix(), id); err != nil {
			return model.Recording{}, err
		}
		recording.Status = "cancelled"
		recording.ProcessID = 0
		recording.FinishedAt = unixTimePointer(now.Unix())
	}
	if err := tx.Commit(); err != nil {
		return model.Recording{}, err
	}
	return recording, nil
}

func (s *Store) DeleteRecording(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM recordings WHERE id=? AND status NOT IN ('recording')", id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("recording not found or still active")
	}
	return nil
}

func (s *Store) AuthCredentials(ctx context.Context) (model.AuthCredentials, bool, error) {
	var credentials model.AuthCredentials
	var createdAt int64
	err := s.db.QueryRowContext(ctx, `SELECT username, password_hash, session_secret, created_at_unix FROM authentication WHERE id=1`).
		Scan(&credentials.Username, &credentials.PasswordHash, &credentials.SessionSecret, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.AuthCredentials{}, false, nil
	}
	if err != nil {
		return model.AuthCredentials{}, false, err
	}
	credentials.CreatedAt = time.Unix(createdAt, 0).UTC()
	return credentials, true, nil
}

// CreateAuthCredentials is deliberately a one-time operation. Reconfiguration
// happens through a future explicit account-reset flow, never by overwriting an
// account just because a container was restarted.
func (s *Store) CreateAuthCredentials(ctx context.Context, credentials model.AuthCredentials) error {
	result, err := s.db.ExecContext(ctx, `INSERT INTO authentication(id, username, password_hash, session_secret, created_at_unix)
VALUES(1, ?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`, credentials.Username, credentials.PasswordHash, credentials.SessionSecret, credentials.CreatedAt.Unix())
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrAuthenticationConfigured
	}
	return nil
}

// UpdateAuthCredentials replaces the password hash and session secret while
// retaining the original account identity. Rotating the secret invalidates all
// existing browser sessions after a password change.
func (s *Store) UpdateAuthCredentials(ctx context.Context, credentials model.AuthCredentials) error {
	result, err := s.db.ExecContext(ctx, `UPDATE authentication SET password_hash=?, session_secret=? WHERE id=1`, credentials.PasswordHash, credentials.SessionSecret)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return errors.New("account has not been configured")
	}
	return nil
}

func (s *Store) FavoriteChannels(ctx context.Context, username string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT channel_id FROM favorite_channels WHERE username=? ORDER BY created_at_unix, channel_id", username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	channels := make([]string, 0)
	for rows.Next() {
		var channelID string
		if err := rows.Scan(&channelID); err != nil {
			return nil, err
		}
		channels = append(channels, channelID)
	}
	return channels, rows.Err()
}

func (s *Store) SetFavoriteChannel(ctx context.Context, username, channelID string, favorite bool) error {
	if favorite {
		_, err := s.db.ExecContext(ctx, `INSERT INTO favorite_channels(username, channel_id, created_at_unix)
VALUES(?, ?, ?) ON CONFLICT(username, channel_id) DO NOTHING`, username, channelID, time.Now().UTC().Unix())
		return err
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM favorite_channels WHERE username=? AND channel_id=?", username, channelID)
	return err
}

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
