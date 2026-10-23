// Package config loads user configuration from config.toml.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
)

const appName = "backbeat"

// Worker is a configured analysis worker.
type Worker struct {
	URL string `toml:"url"`
}

// Config is the merged configuration. Flags override file values.
type Config struct {
	Out     string   `toml:"out"`
	Jobs    int      `toml:"jobs"`
	Workers []Worker `toml:"worker"`
}

// Default returns the built-in defaults.
func Default() Config {
	return Config{Out: "./" + appName + "-out", Jobs: max(1, runtime.NumCPU()/2)}
}

// Path returns the config file location, honoring XDG_CONFIG_HOME.
func Path() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, appName, "config.toml"), nil
}

// Load reads the config file over the defaults. A missing file is not an error.
func Load() (Config, error) {
	cfg := Default()
	path, err := Path()
	if err != nil {
		return cfg, err
	}
	md, err := toml.DecodeFile(path, &cfg)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return cfg, fmt.Errorf("config %s: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return cfg, fmt.Errorf("config %s: unknown keys: %s", path, strings.Join(keys, ", "))
	}
	if cfg.Jobs < 1 {
		cfg.Jobs = 1
	}
	return cfg, nil
}

// WorkerURLs returns the configured worker base URLs.
func (c Config) WorkerURLs() []string {
	urls := make([]string, 0, len(c.Workers))
	for _, w := range c.Workers {
		urls = append(urls, w.URL)
	}
	return urls
}

// ExpandHome replaces a leading "~" or "~/" with the user's home directory.
func ExpandHome(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, p[1:]), nil
}

// CacheDir returns the per-user cache directory for temp copies and SSH sockets.
func CacheDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, appName), nil
}
