// Package store keeps the SQLite index and writes per-track result files.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/brennanMKE/Backbeat/internal/schema"

	_ "modernc.org/sqlite" // pure Go driver, keeps CGO_ENABLED=0
)

const (
	indexDB   = "index.sqlite"
	indexJSON = "index.json"
	tracksDir = "tracks"
)

const ddl = `
CREATE TABLE IF NOT EXISTS files (
	source   TEXT NOT NULL,
	rel_path TEXT NOT NULL,
	size     INTEGER NOT NULL,
	mtime_ns INTEGER NOT NULL,
	sha256   TEXT NOT NULL,
	PRIMARY KEY (source, rel_path)
);
CREATE INDEX IF NOT EXISTS files_sha256 ON files (sha256);
CREATE INDEX IF NOT EXISTS files_rel_path ON files (rel_path);
CREATE TABLE IF NOT EXISTS results (
	sha256         TEXT PRIMARY KEY,
	schema_version INTEGER NOT NULL,
	analyzers      TEXT NOT NULL,
	missing        TEXT NOT NULL,
	analyzed_at    TEXT NOT NULL,
	duration_sec   REAL NOT NULL,
	title          TEXT NOT NULL,
	artist         TEXT NOT NULL,
	album          TEXT NOT NULL
);
PRAGMA user_version = 1;
`

// Store is an output directory: index plus result files.
type Store struct {
	dir string
	db  *sql.DB
}

// Open opens or creates the store in dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, tracksDir), 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.Join(dir, indexDB) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(ddl); err != nil {
		db.Close()
		return nil, fmt.Errorf("init index: %w", err)
	}
	return &Store{dir: dir, db: db}, nil
}

// OpenExisting opens the store only if its index already exists.
func OpenExisting(dir string) (*Store, bool, error) {
	if _, err := os.Stat(filepath.Join(dir, indexDB)); errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	s, err := Open(dir)
	return s, err == nil, err
}

// Close closes the index.
func (s *Store) Close() error { return s.db.Close() }

// Dir returns the output directory.
func (s *Store) Dir() string { return s.dir }

// FileRecord maps a file location to the content hash seen there.
type FileRecord struct {
	Source  string
	RelPath string
	Size    int64
	ModTime time.Time
	SHA256  string
}

// File looks up a location.
func (s *Store) File(ctx context.Context, source, relPath string) (FileRecord, bool, error) {
	r := FileRecord{Source: source, RelPath: relPath}
	var mtime int64
	err := s.db.QueryRowContext(ctx,
		`SELECT size, mtime_ns, sha256 FROM files WHERE source = ? AND rel_path = ?`, source, relPath,
	).Scan(&r.Size, &mtime, &r.SHA256)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	if err != nil {
		return r, false, err
	}
	r.ModTime = time.Unix(0, mtime)
	return r, true, nil
}

// FilesByRelPath returns every location with relPath, across sources.
func (s *Store) FilesByRelPath(ctx context.Context, relPath string) ([]FileRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT source, rel_path, size, mtime_ns, sha256 FROM files WHERE rel_path = ? ORDER BY source`, relPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FileRecord
	for rows.Next() {
		var r FileRecord
		var mtime int64
		if err := rows.Scan(&r.Source, &r.RelPath, &r.Size, &mtime, &r.SHA256); err != nil {
			return nil, err
		}
		r.ModTime = time.Unix(0, mtime)
		out = append(out, r)
	}
	return out, rows.Err()
}

// PutFile records a location.
func (s *Store) PutFile(ctx context.Context, r FileRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO files (source, rel_path, size, mtime_ns, sha256) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (source, rel_path) DO UPDATE SET size = excluded.size, mtime_ns = excluded.mtime_ns, sha256 = excluded.sha256`,
		r.Source, r.RelPath, r.Size, r.ModTime.UnixNano(), r.SHA256)
	return err
}

// PruneFiles deletes locations under source that are not in keep.
func (s *Store) PruneFiles(ctx context.Context, source string, keep map[string]bool) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT rel_path FROM files WHERE source = ?`, source)
	if err != nil {
		return 0, err
	}
	var stale []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return 0, err
		}
		if !keep[p] {
			stale = append(stale, p)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, p := range stale {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM files WHERE source = ? AND rel_path = ?`, source, p); err != nil {
			return 0, err
		}
	}
	return len(stale), nil
}

// ResultRecord is the index summary of a stored result.
type ResultRecord struct {
	SHA256        string
	SchemaVersion int
	Analyzers     map[string]string // type -> analyzer ID
	Missing       []string
	AnalyzedAt    time.Time
}

// Result looks up the stored result for a content hash.
func (s *Store) Result(ctx context.Context, sha string) (ResultRecord, bool, error) {
	r := ResultRecord{SHA256: sha}
	var analyzers, missing, analyzedAt string
	err := s.db.QueryRowContext(ctx,
		`SELECT schema_version, analyzers, missing, analyzed_at FROM results WHERE sha256 = ?`, sha,
	).Scan(&r.SchemaVersion, &analyzers, &missing, &analyzedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	if err != nil {
		return r, false, err
	}
	if err := json.Unmarshal([]byte(analyzers), &r.Analyzers); err != nil {
		return r, false, err
	}
	if err := json.Unmarshal([]byte(missing), &r.Missing); err != nil {
		return r, false, err
	}
	r.AnalyzedAt, _ = time.Parse(time.RFC3339, analyzedAt)
	// A result row whose file was deleted is not usable.
	if _, err := os.Stat(s.TrackPath(sha)); err != nil {
		return r, false, nil
	}
	return r, true, nil
}

// PutResult writes tracks/<sha256>.json atomically, then indexes it.
func (s *Store) PutResult(ctx context.Context, res *schema.Result) error {
	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	if err := WriteFileAtomic(s.TrackPath(res.Track.SHA256), append(data, '\n'), 0o644); err != nil {
		return err
	}
	analyzers, _ := json.Marshal(res.Analyzers())
	missing, _ := json.Marshal(res.Missing)
	t := res.Track
	_, err = s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO results (sha256, schema_version, analyzers, missing, analyzed_at, duration_sec, title, artist, album)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.SHA256, res.SchemaVersion, string(analyzers), string(missing), res.AnalyzedAt.UTC().Format(time.RFC3339),
		t.DurationSec, t.Tags.Title, t.Tags.Artist, t.Tags.Album)
	return err
}

// ReadResult loads a stored result file.
func (s *Store) ReadResult(sha string) (*schema.Result, error) {
	data, err := os.ReadFile(s.TrackPath(sha))
	if err != nil {
		return nil, err
	}
	var res schema.Result
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("%s: %w", s.TrackPath(sha), err)
	}
	return &res, nil
}

// TrackPath returns the result file path for a hash.
func (s *Store) TrackPath(sha string) string {
	return filepath.Join(s.dir, tracksDir, sha+".json")
}

// Index is the exported index.json.
type Index struct {
	SchemaVersion int          `json:"schemaVersion"`
	GeneratedAt   time.Time    `json:"generatedAt"`
	Tracks        []IndexTrack `json:"tracks"`
}

// IndexTrack summarizes one result and every location it was seen at.
type IndexTrack struct {
	SHA256      string     `json:"sha256"`
	File        string     `json:"file"`
	DurationSec float64    `json:"durationSec"`
	Title       string     `json:"title,omitempty"`
	Artist      string     `json:"artist,omitempty"`
	Album       string     `json:"album,omitempty"`
	AnalyzedAt  string     `json:"analyzedAt"`
	Missing     []string   `json:"missing"`
	Locations   []Location `json:"locations"`
}

// Location is a source and relative path where a track was found.
type Location struct {
	Source  string `json:"source"`
	RelPath string `json:"relPath"`
}

// ExportIndex writes index.json atomically from the SQLite index.
func (s *Store) ExportIndex(ctx context.Context) error {
	idx := Index{SchemaVersion: schema.Version, GeneratedAt: time.Now().UTC().Truncate(time.Second), Tracks: []IndexTrack{}}
	rows, err := s.db.QueryContext(ctx, `
		SELECT sha256, duration_sec, title, artist, album, analyzed_at, missing FROM results
		ORDER BY artist, album, title, sha256`)
	if err != nil {
		return err
	}
	pos := map[string]int{}
	for rows.Next() {
		var t IndexTrack
		var missing string
		if err := rows.Scan(&t.SHA256, &t.DurationSec, &t.Title, &t.Artist, &t.Album, &t.AnalyzedAt, &missing); err != nil {
			rows.Close()
			return err
		}
		_ = json.Unmarshal([]byte(missing), &t.Missing)
		if t.Missing == nil {
			t.Missing = []string{}
		}
		t.File = tracksDir + "/" + t.SHA256 + ".json"
		t.Locations = []Location{}
		pos[t.SHA256] = len(idx.Tracks)
		idx.Tracks = append(idx.Tracks, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	rows, err = s.db.QueryContext(ctx, `SELECT sha256, source, rel_path FROM files ORDER BY source, rel_path`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sha string
		var loc Location
		if err := rows.Scan(&sha, &loc.Source, &loc.RelPath); err != nil {
			return err
		}
		if i, ok := pos[sha]; ok {
			idx.Tracks[i].Locations = append(idx.Tracks[i].Locations, loc)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(filepath.Join(s.dir, indexJSON), append(data, '\n'), 0o644)
}
