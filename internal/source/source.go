// Package source lists and opens audio files from local or SSH-reachable libraries.
package source

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/brennanMKE/Backbeat/internal/config"
)

// FileEntry is one matching file, identified by its path relative to the source root.
type FileEntry struct {
	RelPath string // forward slashes
	Size    int64
	ModTime time.Time
}

// LocalFile is a readable local path for an entry. It may be a temp copy; call Close when done.
type LocalFile struct {
	Path    string
	cleanup func() error
}

// Close releases any temp copy behind the file.
func (f LocalFile) Close() error {
	if f.cleanup == nil {
		return nil
	}
	return f.cleanup()
}

// Source is a library of audio files.
type Source interface {
	// ID is the canonical source string recorded in results and the index.
	ID() string
	// List returns every .mp3 and .m4a file, recursively, sorted by RelPath.
	List(ctx context.Context) ([]FileEntry, error)
	// Open returns a local path to read the entry from.
	Open(ctx context.Context, e FileEntry) (LocalFile, error)
}

// Spec is a parsed source argument.
type Spec struct {
	Remote bool
	User   string
	Host   string
	Port   int // 0 means the ssh default or ssh config
	Path   string
}

// Parse parses a local path, an scp-style "[user@]host:path", or an "ssh://" URL.
func Parse(s string) (Spec, error) {
	if s == "" {
		return Spec{}, errors.New("empty source")
	}
	if strings.HasPrefix(s, "ssh://") {
		return parseURL(s)
	}
	if spec, ok, err := parseSCP(s); ok || err != nil {
		return spec, err
	}
	p, err := config.ExpandHome(s)
	if err != nil {
		return Spec{}, err
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return Spec{}, err
	}
	return Spec{Path: abs}, nil
}

func parseURL(s string) (Spec, error) {
	u, err := url.Parse(s)
	if err != nil {
		return Spec{}, fmt.Errorf("source %q: %w", s, err)
	}
	spec := Spec{Remote: true, Host: u.Hostname(), Path: u.Path}
	if spec.Host == "" {
		return Spec{}, fmt.Errorf("source %q: missing host", s)
	}
	if u.User != nil {
		spec.User = u.User.Username()
	}
	if p := u.Port(); p != "" {
		if spec.Port, err = strconv.Atoi(p); err != nil || spec.Port < 1 || spec.Port > 65535 {
			return Spec{}, fmt.Errorf("source %q: invalid port %q", s, p)
		}
	}
	// ssh://host/~/music means a path under the remote home directory.
	if strings.HasPrefix(spec.Path, "/~") {
		spec.Path = spec.Path[1:]
	}
	if spec.Path == "" {
		spec.Path = "."
	}
	return spec, nil
}

// parseSCP follows scp's rule: a colon before any slash makes the argument remote.
func parseSCP(s string) (Spec, bool, error) {
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, ".") || strings.HasPrefix(s, "~") {
		return Spec{}, false, nil
	}
	var userHost, p string
	if at := strings.Index(s, "@["); at >= 0 || strings.HasPrefix(s, "[") {
		// Bracketed IPv6 host: [user@][::1]:path
		end := strings.Index(s, "]:")
		if end < 0 {
			return Spec{}, false, nil
		}
		userHost, p = s[:end+1], s[end+2:]
	} else {
		colon := strings.IndexByte(s, ':')
		if colon < 0 || strings.Contains(s[:colon], "/") {
			return Spec{}, false, nil
		}
		userHost, p = s[:colon], s[colon+1:]
	}
	spec := Spec{Remote: true, Host: userHost, Path: p}
	if at := strings.LastIndexByte(userHost, '@'); at >= 0 {
		spec.User, spec.Host = userHost[:at], userHost[at+1:]
	}
	spec.Host = strings.TrimSuffix(strings.TrimPrefix(spec.Host, "["), "]")
	if spec.Host == "" {
		return Spec{}, true, fmt.Errorf("source %q: missing host", s)
	}
	if spec.Path == "" {
		spec.Path = "."
	}
	return spec, true, nil
}

// String returns the canonical form: an absolute local path, scp-style when
// no port is set, or an ssh:// URL otherwise.
func (s Spec) String() string {
	if !s.Remote {
		return s.Path
	}
	host := s.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	userHost := host
	if s.User != "" {
		userHost = s.User + "@" + host
	}
	if s.Port == 0 {
		return userHost + ":" + s.Path
	}
	p := s.Path
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "ssh://" + userHost + ":" + strconv.Itoa(s.Port) + p
}

// New opens the source described by spec.
func New(spec Spec, log *slog.Logger) (Source, error) {
	if spec.Remote {
		return nil, fmt.Errorf("remote source %s: SSH sources are not implemented yet", spec)
	}
	return newLocal(spec.Path, log)
}

type fileKind int

const (
	kindOther fileKind = iota
	kindAudio
	kindDRM
)

// classify decides what to do with a file by name.
func classify(name string) fileKind {
	if strings.HasPrefix(name, ".") { // hidden files and ._* AppleDouble
		return kindOther
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".mp3", ".m4a":
		return kindAudio
	case ".m4p":
		return kindDRM
	}
	return kindOther
}
