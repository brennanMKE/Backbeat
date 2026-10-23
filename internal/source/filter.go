package source

import (
	"fmt"
	"path"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Filter selects entries by glob. Patterns support "**". A pattern without a
// slash also matches against the file's base name.
type Filter struct {
	Include []string
	Exclude []string
}

// Validate reports the first malformed pattern.
func (f Filter) Validate() error {
	for _, p := range append(append([]string{}, f.Include...), f.Exclude...) {
		if !doublestar.ValidatePattern(p) {
			return fmt.Errorf("invalid glob %q", p)
		}
	}
	return nil
}

// Empty reports whether the filter passes everything.
func (f Filter) Empty() bool { return len(f.Include) == 0 && len(f.Exclude) == 0 }

// Match reports whether relPath passes the filter.
func (f Filter) Match(relPath string) bool {
	if len(f.Include) > 0 && !matchAny(f.Include, relPath) {
		return false
	}
	return !matchAny(f.Exclude, relPath)
}

// Apply returns the entries that pass the filter.
func (f Filter) Apply(entries []FileEntry) []FileEntry {
	if f.Empty() {
		return entries
	}
	var out []FileEntry
	for _, e := range entries {
		if f.Match(e.RelPath) {
			out = append(out, e)
		}
	}
	return out
}

func matchAny(patterns []string, relPath string) bool {
	base := path.Base(relPath)
	for _, p := range patterns {
		if ok, _ := doublestar.Match(p, relPath); ok {
			return true
		}
		if ok, _ := doublestar.Match(p, base); ok && !strings.Contains(p, "/") {
			return true
		}
	}
	return false
}
