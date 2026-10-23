// Package pipeline runs the per-track steps: cache check, fetch, hash, probe,
// analyze and write.
package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/brennanMKE/Backbeat/internal/probe"
	"github.com/brennanMKE/Backbeat/internal/schema"
	"github.com/brennanMKE/Backbeat/internal/source"
	"github.com/brennanMKE/Backbeat/internal/store"
)

// Status is the cache state of an entry, computed without reading its bytes.
type Status string

const (
	StatusNew     Status = "new"     // never seen at this location
	StatusChanged Status = "changed" // size or mtime differ from the index
	StatusStale   Status = "stale"   // result missing or analyzer versions outdated
	StatusCached  Status = "cached"  // up to date
)

// Pipeline analyzes entries from one source into one store.
type Pipeline struct {
	Src   source.Source
	Store *store.Store
	Log   *slog.Logger
	// Types are the requested dimensions.
	Types []string
	// Analyzers maps each type that can currently be produced to its analyzer ID.
	Analyzers map[string]string
	Jobs      int
	Force     bool
}

// Check reports the cache status of e.
func (p *Pipeline) Check(ctx context.Context, e source.FileEntry) (Status, error) {
	rec, ok, err := p.Store.File(ctx, p.Src.ID(), e.RelPath)
	if err != nil || !ok {
		return StatusNew, err
	}
	if rec.Size != e.Size || !rec.ModTime.Equal(e.ModTime) {
		return StatusChanged, nil
	}
	res, ok, err := p.Store.Result(ctx, rec.SHA256)
	if err != nil {
		return "", err
	}
	if !ok || !p.current(res) {
		return StatusStale, nil
	}
	return StatusCached, nil
}

// current reports whether a stored result satisfies the requested types with
// the current analyzer versions. Types nobody can produce do not force a rerun.
func (p *Pipeline) current(r store.ResultRecord) bool {
	if r.SchemaVersion != schema.Version {
		return false
	}
	for _, t := range p.Types {
		want, ok := p.Analyzers[t]
		if ok && r.Analyzers[t] != want {
			return false
		}
	}
	return true
}

// Outcome is what happened to one entry.
type Outcome string

const (
	Analyzed  Outcome = "analyzed"
	Cached    Outcome = "cached"
	Relinked  Outcome = "relinked" // content already analyzed under another path or mtime
	Failed    Outcome = "failed"
	Cancelled Outcome = "cancelled"
)

// Failure records why an entry failed.
type Failure struct {
	RelPath string
	Err     error
}

// Summary totals a run.
type Summary struct {
	Total    int
	Counts   map[Outcome]int
	Failures []Failure
	// Missing counts, per type, analyzed tracks that lack that dimension.
	Missing   map[string]int
	Cancelled bool
	Elapsed   time.Duration
}

// Run processes entries with p.Jobs concurrent workers. On cancellation it
// stops dispatching, lets in-flight results finish writing, and returns.
func (p *Pipeline) Run(ctx context.Context, entries []source.FileEntry) Summary {
	start := time.Now()
	sum := Summary{Total: len(entries), Counts: map[Outcome]int{}, Missing: map[string]int{}}
	var mu sync.Mutex
	work := make(chan source.FileEntry)
	var wg sync.WaitGroup
	for range max(1, p.Jobs) {
		wg.Go(func() {
			for e := range work {
				out, res, err := p.process(ctx, e)
				mu.Lock()
				sum.Counts[out]++
				switch {
				case out == Failed:
					sum.Failures = append(sum.Failures, Failure{RelPath: e.RelPath, Err: err})
					p.Log.Warn("failed", "path", e.RelPath, "err", err)
				case out == Analyzed:
					for _, t := range res.Missing {
						sum.Missing[t]++
					}
					p.Log.Info("analyzed", "path", e.RelPath, "sha256", res.Track.SHA256[:12])
				default:
					p.Log.Debug(string(out), "path", e.RelPath)
				}
				mu.Unlock()
			}
		})
	}
dispatch:
	for _, e := range entries {
		select {
		case work <- e:
		case <-ctx.Done():
			break dispatch
		}
	}
	close(work)
	wg.Wait()
	sum.Cancelled = ctx.Err() != nil
	sum.Elapsed = time.Since(start)
	return sum
}

func (p *Pipeline) process(ctx context.Context, e source.FileEntry) (Outcome, *schema.Result, error) {
	if ctx.Err() != nil {
		return Cancelled, nil, nil
	}
	fail := func(err error) (Outcome, *schema.Result, error) {
		if ctx.Err() != nil {
			return Cancelled, nil, nil
		}
		return Failed, nil, err
	}
	if !p.Force {
		st, err := p.Check(ctx, e)
		if err != nil {
			return fail(fmt.Errorf("cache check: %w", err))
		}
		if st == StatusCached {
			return Cached, nil, nil
		}
	}

	lf, err := p.Src.Open(ctx, e)
	if err != nil {
		return fail(fmt.Errorf("open: %w", err))
	}
	defer lf.Close()

	sha, err := hashFile(ctx, lf.Path)
	if err != nil {
		return fail(fmt.Errorf("hash: %w", err))
	}
	loc := store.FileRecord{Source: p.Src.ID(), RelPath: e.RelPath, Size: e.Size, ModTime: e.ModTime, SHA256: sha}
	// Writes below must not be cut off by cancellation.
	wctx := context.WithoutCancel(ctx)

	if !p.Force {
		if res, ok, err := p.Store.Result(ctx, sha); err != nil {
			return fail(fmt.Errorf("cache check: %w", err))
		} else if ok && p.current(res) {
			if err := p.Store.PutFile(wctx, loc); err != nil {
				return fail(fmt.Errorf("index: %w", err))
			}
			return Relinked, nil, nil
		}
	}

	info, err := probe.Probe(ctx, lf.Path)
	if err != nil {
		return fail(err)
	}
	res := &schema.Result{
		SchemaVersion: schema.Version,
		Track: schema.Track{
			SHA256:      sha,
			Source:      p.Src.ID(),
			RelPath:     e.RelPath,
			DurationSec: info.DurationSec,
			Codec:       info.Codec,
			SampleRate:  info.SampleRate,
			Channels:    info.Channels,
			BitRate:     info.BitRate,
			Tags:        info.Tags,
		},
		AnalyzedAt: time.Now().UTC().Truncate(time.Second),
	}

	// Analyzers are wired in here in later milestones.

	produced := res.Analyzers()
	res.Missing = []string{}
	for _, t := range p.Types {
		if _, ok := produced[t]; !ok {
			res.Missing = append(res.Missing, t)
		}
	}
	if ctx.Err() != nil {
		return Cancelled, nil, nil
	}
	if err := p.Store.PutResult(wctx, res); err != nil {
		return Failed, nil, fmt.Errorf("write result: %w", err)
	}
	if err := p.Store.PutFile(wctx, loc); err != nil {
		return Failed, nil, fmt.Errorf("index: %w", err)
	}
	return Analyzed, res, nil
}

// SortedTypes returns the keys of m in canonical type order.
func SortedTypes(m map[string]int) []string {
	var out []string
	for _, t := range schema.AllTypes {
		if m[t] > 0 {
			out = append(out, t)
		}
	}
	return out
}

func hashFile(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, ctxReader{ctx, f}); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ctxReader stops a long copy when the context is cancelled.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(b []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(b)
}

// ValidateTypes checks a requested type list and returns it in canonical order.
func ValidateTypes(types []string) ([]string, error) {
	if len(types) == 0 {
		return slices.Clone(schema.AllTypes), nil
	}
	var out []string
	for _, t := range schema.AllTypes {
		if slices.Contains(types, t) {
			out = append(out, t)
		}
	}
	for _, t := range types {
		if !schema.IsType(t) {
			return nil, fmt.Errorf("unknown type %q; valid: %s", t, strings.Join(schema.AllTypes, ","))
		}
	}
	return out, nil
}
