package source

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type local struct {
	root string
	id   string
	log  *slog.Logger
}

func newLocal(root string, log *slog.Logger) (*local, error) {
	// Resolve so a symlinked root (e.g. ~/Music -> /Volumes/Music) is walked.
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("source %s: %w", root, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("source %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("source %s: not a directory", root)
	}
	return &local{root: resolved, id: root, log: log}, nil
}

func (l *local) ID() string { return l.id }

func (l *local) List(ctx context.Context) ([]FileEntry, error) {
	var out []FileEntry
	err := filepath.WalkDir(l.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == l.root {
				return err
			}
			l.log.Warn("skipping unreadable path", "path", p, "err", err)
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == l.root {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		kind := classify(name)
		if kind == kindOther {
			return nil
		}
		rel, err := filepath.Rel(l.root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if kind == kindDRM {
			l.log.Info("skipping DRM-protected file", "path", rel)
			return nil
		}
		// Follow symlinks to files, never to directories (WalkDir does not descend them).
		info, err := os.Stat(p)
		if err != nil {
			l.log.Warn("skipping unreadable file", "path", rel, "err", err)
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		out = append(out, FileEntry{RelPath: rel, Size: info.Size(), ModTime: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b FileEntry) int { return strings.Compare(a.RelPath, b.RelPath) })
	return out, nil
}

func (l *local) Open(_ context.Context, e FileEntry) (LocalFile, error) {
	return LocalFile{Path: filepath.Join(l.root, filepath.FromSlash(e.RelPath))}, nil
}
