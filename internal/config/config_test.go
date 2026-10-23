package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Out != Default().Out || cfg.Jobs < 1 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeConfig(t, dir, `
out = "~/results"
jobs = 3

[[worker]]
url = "http://macmini.local:8765"
`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Out != "~/results" || cfg.Jobs != 3 {
		t.Fatalf("got %+v", cfg)
	}
	if urls := cfg.WorkerURLs(); len(urls) != 1 || urls[0] != "http://macmini.local:8765" {
		t.Fatalf("workers = %v", urls)
	}
}

func TestLoadRejectsUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeConfig(t, dir, `outt = "typo"`)
	if _, err := Load(); err == nil {
		t.Fatal("expected error for unknown key")
	}
}

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	for in, want := range map[string]string{
		"~":       home,
		"~/Music": filepath.Join(home, "Music"),
		"/abs":    "/abs",
		"~user/x": "~user/x",
		"./~/rel": "./~/rel",
	} {
		got, err := ExpandHome(in)
		if err != nil || got != want {
			t.Errorf("ExpandHome(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	p := filepath.Join(dir, appName, "config.toml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
