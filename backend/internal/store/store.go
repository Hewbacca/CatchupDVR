package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Hewbacca/CatchupDVR/backend/internal/guide"
	"github.com/Hewbacca/CatchupDVR/backend/internal/model"
)

var ErrTunerConflict = errors.New("all tuners are reserved for this time")

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
  created_at_unix INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS recordings_allocation ON recordings(status, scheduled_start_unix, scheduled_end_unix);
CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL);
`)
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
	rows, err := s.db.QueryContext(ctx, `SELECT id, program_id, channel_id, channel_number, title, program_start_unix, program_end_unix, scheduled_start_unix, scheduled_end_unix, status, playlist_path, created_at_unix FROM recordings ORDER BY scheduled_start_unix`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []model.Recording{}
	for rows.Next() {
		var recording model.Recording
		var programStart, programEnd, scheduledStart, scheduledEnd, created int64
		if err := rows.Scan(&recording.ID, &recording.ProgramID, &recording.ChannelID, &recording.ChannelNumber, &recording.Title, &programStart, &programEnd, &scheduledStart, &scheduledEnd, &recording.Status, &recording.PlaylistPath, &created); err != nil {
			return nil, err
		}
		recording.ProgramStart = time.Unix(programStart, 0).UTC()
		recording.ProgramEnd = time.Unix(programEnd, 0).UTC()
		recording.ScheduledStart = time.Unix(scheduledStart, 0).UTC()
		recording.ScheduledEnd = time.Unix(scheduledEnd, 0).UTC()
		recording.CreatedAt = time.Unix(created, 0).UTC()
		result = append(result, recording)
	}
	return result, rows.Err()
}

func (s *Store) DeleteRecording(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM recordings WHERE id=? AND status NOT IN ('recording')", id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("recording not found or active")
	}
	return nil
}

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
