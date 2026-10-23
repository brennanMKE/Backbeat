package source

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestParseRemote(t *testing.T) {
	tests := []struct {
		in        string
		want      Spec
		canonical string
	}{
		{"brennan@macmini.local:/srv/music", Spec{Remote: true, User: "brennan", Host: "macmini.local", Path: "/srv/music"}, "brennan@macmini.local:/srv/music"},
		{"macmini:/srv/music", Spec{Remote: true, Host: "macmini", Path: "/srv/music"}, "macmini:/srv/music"},
		{"macmini:music", Spec{Remote: true, Host: "macmini", Path: "music"}, "macmini:music"},
		{"macmini:", Spec{Remote: true, Host: "macmini", Path: "."}, "macmini:."},
		{"a@b@host:/x", Spec{Remote: true, User: "a@b", Host: "host", Path: "/x"}, "a@b@host:/x"},
		{"macmini:/My Music/Ünïcode", Spec{Remote: true, Host: "macmini", Path: "/My Music/Ünïcode"}, "macmini:/My Music/Ünïcode"},
		{"brennan@[fe80::1]:/srv", Spec{Remote: true, User: "brennan", Host: "fe80::1", Path: "/srv"}, "brennan@[fe80::1]:/srv"},
		{"ssh://brennan@macmini.local:22/srv/music", Spec{Remote: true, User: "brennan", Host: "macmini.local", Port: 22, Path: "/srv/music"}, "ssh://brennan@macmini.local:22/srv/music"},
		{"ssh://macmini/srv/music", Spec{Remote: true, Host: "macmini", Path: "/srv/music"}, "macmini:/srv/music"},
		{"ssh://macmini/~/music", Spec{Remote: true, Host: "macmini", Path: "~/music"}, "macmini:~/music"},
		{"ssh://macmini/My%20Music", Spec{Remote: true, Host: "macmini", Path: "/My Music"}, "macmini:/My Music"},
		{"ssh://macmini", Spec{Remote: true, Host: "macmini", Path: "."}, "macmini:."},
		{"ssh://[::1]:2222/srv", Spec{Remote: true, Host: "::1", Port: 2222, Path: "/srv"}, "ssh://[::1]:2222/srv"},
	}
	for _, tt := range tests {
		got, err := Parse(tt.in)
		if err != nil {
			t.Errorf("Parse(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Parse(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
		if s := got.String(); s != tt.canonical {
			t.Errorf("Parse(%q).String() = %q, want %q", tt.in, s, tt.canonical)
		}
	}
}

func TestParseLocal(t *testing.T) {
	home, _ := os.UserHomeDir()
	wd, _ := os.Getwd()
	tests := map[string]string{
		"/Volumes/Music":  "/Volumes/Music",
		"~/Music/Library": filepath.Join(home, "Music/Library"),
		"./music":         filepath.Join(wd, "music"),
		"music":           filepath.Join(wd, "music"),
		"./a:b":           filepath.Join(wd, "a:b"),
		"dir/with:colon":  filepath.Join(wd, "dir/with:colon"),
		"/abs/with:colon": "/abs/with:colon",
	}
	for in, want := range tests {
		got, err := Parse(in)
		if err != nil {
			t.Errorf("Parse(%q): %v", in, err)
			continue
		}
		if got.Remote || got.Path != want {
			t.Errorf("Parse(%q) = %+v, want local %q", in, got, want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, in := range []string{"", ":/srv", "user@:/srv", "ssh:///srv", "ssh://host:99999/x"} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q): expected error", in)
		}
	}
}

func TestLocalList(t *testing.T) {
	root := t.TempDir()
	files := []string{
		"Artist/Album/01 Song.mp3",
		"Artist/Album/02 Song.M4A",
		"Artist/Album/03 'Quoted' Song.Mp3",
		"Artist/Album/cover.jpg",
		"Artist/Album/04 Protected.m4p",
		"Artist/Album/._01 Song.mp3",
		"Artist/.hidden.mp3",
		".Trash/deleted.mp3",
		"Ünïcode/Café.m4a",
	}
	for _, f := range files {
		write(t, filepath.Join(root, f), "x")
	}
	// Symlink to a file is followed; symlink to a directory (here, a loop) is not.
	if err := os.Symlink(filepath.Join(root, "Artist/Album/01 Song.mp3"), filepath.Join(root, "link.mp3")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(root, "Artist/loop")); err != nil {
		t.Fatal(err)
	}

	src, err := New(Spec{Path: root}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := src.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.RelPath)
	}
	want := []string{
		"Artist/Album/01 Song.mp3",
		"Artist/Album/02 Song.M4A",
		"Artist/Album/03 'Quoted' Song.Mp3",
		"link.mp3",
		"Ünïcode/Café.m4a",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("List =\n%q\nwant\n%q", got, want)
	}

	lf, err := src.Open(context.Background(), entries[0])
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	if b, err := os.ReadFile(lf.Path); err != nil || string(b) != "x" {
		t.Fatalf("Open read %q, %v", b, err)
	}
}

func TestLocalSymlinkedRoot(t *testing.T) {
	real := t.TempDir()
	write(t, filepath.Join(real, "a.mp3"), "x")
	link := filepath.Join(t.TempDir(), "Music")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	src, err := New(Spec{Path: link}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if src.ID() != link {
		t.Errorf("ID = %q, want %q", src.ID(), link)
	}
	entries, err := src.List(context.Background())
	if err != nil || len(entries) != 1 {
		t.Fatalf("List = %v, %v", entries, err)
	}
}

func TestFilter(t *testing.T) {
	f := Filter{Include: []string{"Artist/**"}, Exclude: []string{"*Live*", "**/Demos/**"}}
	if err := f.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"Artist/Album/01.mp3":      true,
		"Artist/Album/02 Live.mp3": false,
		"Artist/Demos/x/03.mp3":    false,
		"Other/Album/01.mp3":       false,
	}
	for p, want := range cases {
		if got := f.Match(p); got != want {
			t.Errorf("Match(%q) = %v, want %v", p, got, want)
		}
	}
	if err := (Filter{Include: []string{"[bad"}}).Validate(); err == nil {
		t.Error("expected invalid glob error")
	}
}

func write(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
