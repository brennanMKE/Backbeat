package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brennanMKE/Backbeat/internal/schema"
)

func TestFilesAndResults(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, ok, err := s.File(ctx, "src", "a.mp3"); err != nil || ok {
		t.Fatalf("File on empty store: ok=%v err=%v", ok, err)
	}
	mtime := time.Unix(1700000000, 123456789)
	rec := FileRecord{Source: "src", RelPath: "a.mp3", Size: 10, ModTime: mtime, SHA256: "abc"}
	if err := s.PutFile(ctx, rec); err != nil {
		t.Fatal(err)
	}
	rec.Size = 11
	if err := s.PutFile(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.File(ctx, "src", "a.mp3")
	if err != nil || !ok || got.Size != 11 || !got.ModTime.Equal(mtime) || got.SHA256 != "abc" {
		t.Fatalf("File = %+v, %v, %v", got, ok, err)
	}

	res := &schema.Result{
		SchemaVersion: schema.Version,
		Track:         schema.Track{SHA256: "abc", Source: "src", RelPath: "a.mp3", DurationSec: 1.5, Tags: schema.Tags{Title: "A"}},
		AnalyzedAt:    time.Now(),
		Loudness:      &schema.Loudness{Analyzer: "backbeat-ebur128@1"},
		Missing:       []string{"rhythm"},
	}
	if err := s.PutResult(ctx, res); err != nil {
		t.Fatal(err)
	}
	rr, ok, err := s.Result(ctx, "abc")
	if err != nil || !ok {
		t.Fatalf("Result: %v %v", ok, err)
	}
	if rr.Analyzers["loudness"] != "backbeat-ebur128@1" || len(rr.Missing) != 1 {
		t.Fatalf("Result = %+v", rr)
	}
	back, err := s.ReadResult("abc")
	if err != nil || back.Track.Tags.Title != "A" {
		t.Fatalf("ReadResult = %+v, %v", back, err)
	}

	if err := s.ExportIndex(ctx); err != nil {
		t.Fatal(err)
	}
	var idx Index
	data, _ := os.ReadFile(filepath.Join(dir, "index.json"))
	if err := json.Unmarshal(data, &idx); err != nil {
		t.Fatal(err)
	}
	if len(idx.Tracks) != 1 || len(idx.Tracks[0].Locations) != 1 || idx.Tracks[0].File != "tracks/abc.json" {
		t.Fatalf("index = %+v", idx)
	}

	// A result whose file was removed is treated as absent.
	os.Remove(s.TrackPath("abc"))
	if _, ok, _ := s.Result(ctx, "abc"); ok {
		t.Fatal("expected missing result after file removal")
	}

	n, err := s.PruneFiles(ctx, "src", map[string]bool{})
	if err != nil || n != 1 {
		t.Fatalf("PruneFiles = %d, %v", n, err)
	}
}

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.json")
	if err := WriteFileAtomic(p, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(p, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	entries, _ := os.ReadDir(dir)
	if string(b) != "two" || len(entries) != 1 {
		t.Fatalf("content %q, %d entries", b, len(entries))
	}
}
