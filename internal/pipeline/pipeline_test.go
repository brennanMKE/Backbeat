package pipeline

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/brennanMKE/Backbeat/internal/probe"
	"github.com/brennanMKE/Backbeat/internal/schema"
	"github.com/brennanMKE/Backbeat/internal/source"
	"github.com/brennanMKE/Backbeat/internal/store"
)

func TestRunCachesAndRelinks(t *testing.T) {
	if err := probe.CheckTools(); err != nil {
		t.Skip(err)
	}
	ctx := context.Background()
	lib := t.TempDir()
	tone(t, filepath.Join(lib, "A/one.m4a"), 440)
	tone(t, filepath.Join(lib, "B/two.mp3"), 660)
	if err := os.WriteFile(filepath.Join(lib, "broken.mp3"), []byte("not audio"), 0o644); err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	run := func() Summary {
		src, err := source.New(source.Spec{Path: lib}, log)
		if err != nil {
			t.Fatal(err)
		}
		entries, err := src.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		p := &Pipeline{Src: src, Store: st, Log: log, Types: schema.AllTypes, Analyzers: map[string]string{}, Jobs: 2}
		return p.Run(ctx, entries)
	}

	s := run()
	if s.Counts[Analyzed] != 2 || s.Counts[Failed] != 1 {
		t.Fatalf("first run: %+v", s)
	}
	if s.Missing[schema.TypeRhythm] != 2 {
		t.Errorf("missing = %v", s.Missing)
	}

	s = run()
	if s.Counts[Cached] != 2 || s.Counts[Failed] != 1 {
		t.Fatalf("second run: %+v", s)
	}

	// Moving a file keeps its content hash, so it is relinked, not reanalyzed.
	if err := os.Rename(filepath.Join(lib, "B/two.mp3"), filepath.Join(lib, "B/renamed.mp3")); err != nil {
		t.Fatal(err)
	}
	// Touching a file changes mtime but not content.
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(lib, "A/one.m4a"), future, future); err != nil {
		t.Fatal(err)
	}
	s = run()
	if s.Counts[Relinked] != 2 || s.Counts[Analyzed] != 0 {
		t.Fatalf("third run: %+v", s)
	}
}

func TestCurrent(t *testing.T) {
	p := &Pipeline{Types: []string{"loudness", "rhythm"}, Analyzers: map[string]string{"loudness": "l@2"}}
	cases := []struct {
		rec  store.ResultRecord
		want bool
	}{
		{store.ResultRecord{SchemaVersion: schema.Version, Analyzers: map[string]string{"loudness": "l@2"}}, true},
		{store.ResultRecord{SchemaVersion: schema.Version, Analyzers: map[string]string{"loudness": "l@1"}}, false},
		{store.ResultRecord{SchemaVersion: schema.Version, Analyzers: map[string]string{}}, false},
		{store.ResultRecord{SchemaVersion: 0, Analyzers: map[string]string{"loudness": "l@2"}}, false},
	}
	for i, c := range cases {
		if got := p.current(c.rec); got != c.want {
			t.Errorf("case %d: current = %v, want %v", i, got, c.want)
		}
	}
}

func TestValidateTypes(t *testing.T) {
	got, err := ValidateTypes([]string{"key", "rhythm"})
	if err != nil || len(got) != 2 || got[0] != "rhythm" || got[1] != "key" {
		t.Fatalf("ValidateTypes = %v, %v", got, err)
	}
	if _, err := ValidateTypes([]string{"tempo"}); err == nil {
		t.Fatal("expected error")
	}
}

func tone(t *testing.T, path string, freq int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	src := "sine=frequency=" + strconv.Itoa(freq) + ":duration=1"
	if b, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", src, path).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v: %s", err, b)
	}
}
