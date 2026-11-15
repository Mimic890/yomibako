// Package store keeps track of what has been downloaded, what is subscribed, and what is running.
//
// The database is a single SQLite file. It is the only state the service has: deleting it loses
// the history but never the books, which live in the library as ordinary EPUB files.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Volume statuses.
const (
	StatusBuilt    = "built"
	StatusImported = "imported"
	StatusFailed   = "failed"
	StatusMissing  = "missing"
)

// Title is a tracked book series.
type Title struct {
	ID            int64
	Source        string
	Slug          string
	Title         string
	Authors       []string
	CoverURL      string
	SourceURL     string
	TotalChapters int
	TotalVolumes  int
	CreatedAt     time.Time
	UpdatedAt     time.Time
	Volumes       []Volume
	Subscription  *Subscription
}

// Volume is one downloaded volume of a title.
type Volume struct {
	ID         int64
	TitleID    int64
	Label      string
	Number     int
	Chapters   int
	Status     string
	FilePath   string
	SizeBytes  int64
	BuiltAt    time.Time
	ImportedAt *time.Time
}

// Subscription is a title the service checks for new volumes.
type Subscription struct {
	TitleID         int64
	Enabled         bool
	IntervalHours   int
	LastCheckedAt   *time.Time
	LastVolumeLabel string
	CreatedAt       time.Time
}

// Store is an open database.
type Store struct {
	db *sql.DB
}

// Open opens (and creates) the database at path.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("store: empty database path")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	// SQLite writes are serialised anyway; one connection avoids "database is locked" surprises.
	db.SetMaxOpenConns(1)

	store := &Store{db: db}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

const schema = `
CREATE TABLE IF NOT EXISTS titles (
	id             INTEGER PRIMARY KEY,
	source         TEXT NOT NULL,
	slug           TEXT NOT NULL,
	title          TEXT NOT NULL,
	authors        TEXT NOT NULL DEFAULT '[]',
	cover_url      TEXT NOT NULL DEFAULT '',
	source_url     TEXT NOT NULL DEFAULT '',
	total_chapters INTEGER NOT NULL DEFAULT 0,
	total_volumes  INTEGER NOT NULL DEFAULT 0,
	created_at     TEXT NOT NULL,
	updated_at     TEXT NOT NULL,
	UNIQUE (source, slug)
);

CREATE TABLE IF NOT EXISTS volumes (
	id          INTEGER PRIMARY KEY,
	title_id    INTEGER NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
	label       TEXT NOT NULL,
	number      INTEGER NOT NULL DEFAULT 0,
	chapters    INTEGER NOT NULL DEFAULT 0,
	status      TEXT NOT NULL,
	file_path   TEXT NOT NULL DEFAULT '',
	size_bytes  INTEGER NOT NULL DEFAULT 0,
	built_at    TEXT NOT NULL,
	imported_at TEXT,
	UNIQUE (title_id, label)
);

CREATE TABLE IF NOT EXISTS subscriptions (
	title_id          INTEGER PRIMARY KEY REFERENCES titles(id) ON DELETE CASCADE,
	enabled           INTEGER NOT NULL DEFAULT 1,
	interval_hours    INTEGER NOT NULL DEFAULT 24,
	last_checked_at   TEXT,
	last_volume_label TEXT NOT NULL DEFAULT '',
	created_at        TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS jobs (
	id           INTEGER PRIMARY KEY,
	kind         TEXT NOT NULL,
	title_id     INTEGER REFERENCES titles(id) ON DELETE SET NULL,
	volume_label TEXT NOT NULL DEFAULT '',
	status       TEXT NOT NULL,
	progress     TEXT NOT NULL DEFAULT '',
	error        TEXT NOT NULL DEFAULT '',
	created_at   TEXT NOT NULL,
	updated_at   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS volumes_title_idx ON volumes (title_id);
CREATE INDEX IF NOT EXISTS jobs_status_idx ON jobs (status);
`

func (s *Store) migrate() error {
	_, err := s.db.Exec(schema)
	return err
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func parseTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func parseTimePtr(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	parsed := parseTime(value.String)
	if parsed.IsZero() {
		return nil
	}
	return &parsed
}

// UpsertTitle inserts a title or refreshes its metadata, and returns its id.
func (s *Store) UpsertTitle(ctx context.Context, title Title) (int64, error) {
	authors, err := json.Marshal(title.Authors)
	if err != nil {
		return 0, err
	}
	nowValue := now()

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO titles (source, slug, title, authors, cover_url, source_url, total_chapters, total_volumes, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (source, slug) DO UPDATE SET
			title = excluded.title,
			authors = excluded.authors,
			cover_url = excluded.cover_url,
			source_url = excluded.source_url,
			total_chapters = excluded.total_chapters,
			total_volumes = excluded.total_volumes,
			updated_at = excluded.updated_at`,
		title.Source, title.Slug, title.Title, string(authors), title.CoverURL, title.SourceURL,
		title.TotalChapters, title.TotalVolumes, nowValue, nowValue)
	if err != nil {
		return 0, err
	}

	var id int64
	err = s.db.QueryRowContext(ctx, `SELECT id FROM titles WHERE source = ? AND slug = ?`, title.Source, title.Slug).Scan(&id)
	return id, err
}

// RecordVolume stores the result of a build. Rebuilding the same volume updates the row instead of
// creating a second one.
func (s *Store) RecordVolume(ctx context.Context, volume Volume) error {
	builtAt := volume.BuiltAt
	if builtAt.IsZero() {
		builtAt = time.Now()
	}
	var importedAt any
	if volume.ImportedAt != nil {
		importedAt = volume.ImportedAt.UTC().Format(time.RFC3339)
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO volumes (title_id, label, number, chapters, status, file_path, size_bytes, built_at, imported_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (title_id, label) DO UPDATE SET
			number = excluded.number,
			chapters = excluded.chapters,
			status = excluded.status,
			file_path = excluded.file_path,
			size_bytes = excluded.size_bytes,
			built_at = excluded.built_at,
			imported_at = COALESCE(excluded.imported_at, volumes.imported_at)`,
		volume.TitleID, volume.Label, volume.Number, volume.Chapters, volume.Status,
		volume.FilePath, volume.SizeBytes, builtAt.UTC().Format(time.RFC3339), importedAt)
	return err
}

// SetVolumeStatus changes the status of one volume.
func (s *Store) SetVolumeStatus(ctx context.Context, titleID int64, label, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE volumes SET status = ? WHERE title_id = ? AND label = ?`, status, titleID, label)
	return err
}

// MarkVolumeImported flips a volume to imported once BookOrbit has taken the file.
func (s *Store) MarkVolumeImported(ctx context.Context, titleID int64, label string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE volumes SET status = ?, imported_at = ? WHERE title_id = ? AND label = ?`,
		StatusImported, now(), titleID, label)
	return err
}

// VolumeLabels returns the labels of every volume already recorded for a title.
func (s *Store) VolumeLabels(ctx context.Context, titleID int64) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT label, status FROM volumes WHERE title_id = ?`, titleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var label, status string
		if err := rows.Scan(&label, &status); err != nil {
			return nil, err
		}
		out[label] = status
	}
	return out, rows.Err()
}

// Titles returns every tracked title with its volumes and subscription.
func (s *Store) Titles(ctx context.Context) ([]Title, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, source, slug, title, authors, cover_url, source_url, total_chapters, total_volumes, created_at, updated_at
		FROM titles ORDER BY title COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var titles []Title
	for rows.Next() {
		var (
			title   Title
			authors string
			created string
			updated string
		)
		if err := rows.Scan(&title.ID, &title.Source, &title.Slug, &title.Title, &authors,
			&title.CoverURL, &title.SourceURL, &title.TotalChapters, &title.TotalVolumes, &created, &updated); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(authors), &title.Authors)
		title.CreatedAt = parseTime(created)
		title.UpdatedAt = parseTime(updated)
		titles = append(titles, title)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range titles {
		volumes, err := s.Volumes(ctx, titles[i].ID)
		if err != nil {
			return nil, err
		}
		titles[i].Volumes = volumes
		subscription, err := s.subscription(ctx, titles[i].ID)
		if err != nil {
			return nil, err
		}
		titles[i].Subscription = subscription
	}
	return titles, nil
}

// Volumes returns every recorded volume of a title.
func (s *Store) Volumes(ctx context.Context, titleID int64) ([]Volume, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, title_id, label, number, chapters, status, file_path, size_bytes, built_at, imported_at
		FROM volumes WHERE title_id = ? ORDER BY number, label`, titleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var volumes []Volume
	for rows.Next() {
		var (
			volume     Volume
			builtAt    string
			importedAt sql.NullString
		)
		if err := rows.Scan(&volume.ID, &volume.TitleID, &volume.Label, &volume.Number, &volume.Chapters,
			&volume.Status, &volume.FilePath, &volume.SizeBytes, &builtAt, &importedAt); err != nil {
			return nil, err
		}
		volume.BuiltAt = parseTime(builtAt)
		volume.ImportedAt = parseTimePtr(importedAt)
		volumes = append(volumes, volume)
	}
	return volumes, rows.Err()
}

func (s *Store) subscription(ctx context.Context, titleID int64) (*Subscription, error) {
	var (
		sub       Subscription
		enabled   int
		checked   sql.NullString
		createdAt string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT title_id, enabled, interval_hours, last_checked_at, last_volume_label, created_at
		FROM subscriptions WHERE title_id = ?`, titleID).
		Scan(&sub.TitleID, &enabled, &sub.IntervalHours, &checked, &sub.LastVolumeLabel, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sub.Enabled = enabled == 1
	sub.LastCheckedAt = parseTimePtr(checked)
	sub.CreatedAt = parseTime(createdAt)
	return &sub, nil
}

// Subscribe adds or updates a subscription.
func (s *Store) Subscribe(ctx context.Context, titleID int64, intervalHours int) error {
	if intervalHours <= 0 {
		intervalHours = 24
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO subscriptions (title_id, enabled, interval_hours, created_at)
		VALUES (?, 1, ?, ?)
		ON CONFLICT (title_id) DO UPDATE SET enabled = 1, interval_hours = excluded.interval_hours`,
		titleID, intervalHours, now())
	return err
}

// Unsubscribe removes a subscription.
func (s *Store) Unsubscribe(ctx context.Context, titleID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM subscriptions WHERE title_id = ?`, titleID)
	return err
}

// Subscriptions returns every subscription with its title.
func (s *Store) Subscriptions(ctx context.Context) ([]Title, error) {
	titles, err := s.Titles(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Title, 0, len(titles))
	for _, title := range titles {
		if title.Subscription != nil && title.Subscription.Enabled {
			out = append(out, title)
		}
	}
	return out, nil
}

// MarkChecked records when a subscription was last checked and which volume was newest then.
func (s *Store) MarkChecked(ctx context.Context, titleID int64, lastVolumeLabel string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE subscriptions SET last_checked_at = ?, last_volume_label = ? WHERE title_id = ?`,
		now(), lastVolumeLabel, titleID)
	return err
}

// FindTitle looks a title up by source and slug.
func (s *Store) FindTitle(ctx context.Context, source, slug string) (*Title, error) {
	var (
		title   Title
		authors string
		created string
		updated string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, source, slug, title, authors, cover_url, source_url, total_chapters, total_volumes, created_at, updated_at
		FROM titles WHERE source = ? AND slug = ?`, source, slug).
		Scan(&title.ID, &title.Source, &title.Slug, &title.Title, &authors, &title.CoverURL,
			&title.SourceURL, &title.TotalChapters, &title.TotalVolumes, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(authors), &title.Authors)
	title.CreatedAt = parseTime(created)
	title.UpdatedAt = parseTime(updated)
	return &title, nil
}

// Stats is a short summary for the CLI.
type Stats struct {
	Titles        int
	Volumes       int
	Imported      int
	Subscriptions int
}

// Stats returns counters for the whole database.
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var stats Stats
	err := s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM titles),
			(SELECT COUNT(*) FROM volumes),
			(SELECT COUNT(*) FROM volumes WHERE status = 'imported'),
			(SELECT COUNT(*) FROM subscriptions WHERE enabled = 1)`).
		Scan(&stats.Titles, &stats.Volumes, &stats.Imported, &stats.Subscriptions)
	return stats, err
}

// ErrNotFound is returned when a title is missing.
var ErrNotFound = fmt.Errorf("title not found")
