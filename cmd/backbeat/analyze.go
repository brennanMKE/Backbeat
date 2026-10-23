package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/brennanMKE/Backbeat/internal/config"
	"github.com/brennanMKE/Backbeat/internal/pipeline"
	"github.com/brennanMKE/Backbeat/internal/probe"
	"github.com/brennanMKE/Backbeat/internal/source"
	"github.com/brennanMKE/Backbeat/internal/store"
)

func cmdAnalyze(ctx context.Context, args []string) error {
	fs, c := newFlagSet("analyze", "<source> [flags]")
	out := fs.String("out", config.Default().Out, "results directory")
	types := fs.StringSlice("types", nil, "comma list of types (default all): rhythm,structure,loudness,key,pace,instruments,onsets")
	jobs := fs.Int("jobs", config.Default().Jobs, "concurrent tracks")
	workers := fs.StringSlice("workers", nil, "worker base URLs (default from config)")
	nativeOnly := fs.Bool("native-only", false, "skip workers; only Go analyzers")
	force := fs.Bool("force", false, "ignore cache")
	include := fs.StringArray("include", nil, "only paths matching glob (repeatable)")
	exclude := fs.StringArray("exclude", nil, "skip paths matching glob (repeatable)")
	dryRun := fs.Bool("dry-run", false, "show what would run")
	keepRemote := fs.Bool("keep-remote-copies", false, "keep downloaded remote files in the cache dir")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return err
	}
	log := c.logger()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	dir, err := outDir(fs, *out, cfg)
	if err != nil {
		return err
	}
	if fs.Changed("jobs") {
		cfg.Jobs = max(1, *jobs)
	}
	workerURLs := cfg.WorkerURLs()
	if fs.Changed("workers") {
		workerURLs = *workers
	}
	if *nativeOnly {
		workerURLs = nil
	}
	// Workers (milestone 4) and remote copies (milestone 2) are accepted now so
	// scripts and config stay stable.
	log.Debug("options", "workers", workerURLs, "keepRemoteCopies", *keepRemote)

	reqTypes, err := pipeline.ValidateTypes(*types)
	if err != nil {
		return err
	}
	filter := source.Filter{Include: *include, Exclude: *exclude}
	if err := filter.Validate(); err != nil {
		return err
	}
	if err := probe.CheckTools(); err != nil {
		return err
	}

	src, entries, err := openAndList(ctx, pos[0], filter, log)
	if err != nil {
		return err
	}
	st, err := store.Open(dir)
	if err != nil {
		return err
	}
	defer st.Close()

	p := &pipeline.Pipeline{
		Src:       src,
		Store:     st,
		Log:       log,
		Types:     reqTypes,
		Analyzers: map[string]string{},
		Jobs:      cfg.Jobs,
		Force:     *force,
	}
	if *dryRun {
		return dryRunReport(ctx, p, entries)
	}

	log.Info("analyzing", "source", src.ID(), "files", len(entries), "out", dir, "jobs", cfg.Jobs, "types", strings.Join(reqTypes, ","))
	sum := p.Run(ctx, entries)

	// Background context: the index must be written even after Ctrl-C.
	bg := context.WithoutCancel(ctx)
	if !sum.Cancelled && filter.Empty() && sum.Counts[pipeline.Failed] == 0 {
		keep := make(map[string]bool, len(entries))
		for _, e := range entries {
			keep[e.RelPath] = true
		}
		if n, err := st.PruneFiles(bg, src.ID(), keep); err != nil {
			log.Warn("prune index", "err", err)
		} else if n > 0 {
			log.Info("removed index entries for deleted files", "count", n)
		}
	}
	if err := st.ExportIndex(bg); err != nil {
		return fmt.Errorf("export index: %w", err)
	}
	printSummary(sum)
	switch {
	case sum.Cancelled:
		return exitCode(130)
	case len(sum.Failures) > 0:
		return exitCode(1)
	}
	return nil
}

// openAndList opens the source and returns its filtered entries.
func openAndList(ctx context.Context, arg string, filter source.Filter, log *slog.Logger) (source.Source, []source.FileEntry, error) {
	spec, err := source.Parse(arg)
	if err != nil {
		return nil, nil, err
	}
	src, err := source.New(spec, log)
	if err != nil {
		return nil, nil, err
	}
	entries, err := src.List(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list %s: %w", src.ID(), err)
	}
	return src, filter.Apply(entries), nil
}

func dryRunReport(ctx context.Context, p *pipeline.Pipeline, entries []source.FileEntry) error {
	n := 0
	for _, e := range entries {
		st := pipeline.StatusNew
		if !p.Force {
			var err error
			if st, err = p.Check(ctx, e); err != nil {
				return err
			}
		}
		if st != pipeline.StatusCached {
			fmt.Printf("%-8s %s\n", st, e.RelPath)
			n++
		}
	}
	fmt.Printf("\nWould analyze %d of %d files (types: %s)\n", n, len(entries), strings.Join(p.Types, ","))
	return nil
}

func printSummary(s pipeline.Summary) {
	c := s.Counts
	fmt.Printf("\nAnalyzed %d, cached %d, relinked %d, failed %d of %d files in %s\n",
		c[pipeline.Analyzed], c[pipeline.Cached], c[pipeline.Relinked], c[pipeline.Failed], s.Total,
		s.Elapsed.Round(time.Millisecond))
	if s.Cancelled {
		fmt.Printf("Cancelled; %d files not processed\n", s.Total-c[pipeline.Analyzed]-c[pipeline.Cached]-c[pipeline.Relinked]-c[pipeline.Failed])
	}
	if len(s.Failures) > 0 {
		fmt.Println("Failed:")
		for _, f := range s.Failures {
			fmt.Printf("  %s: %v\n", f.RelPath, f.Err)
		}
	}
	if missing := pipeline.SortedTypes(s.Missing); len(missing) > 0 {
		parts := make([]string, len(missing))
		for i, t := range missing {
			parts[i] = fmt.Sprintf("%s %d", t, s.Missing[t])
		}
		fmt.Printf("Missing dimensions (tracks): %s\n", strings.Join(parts, ", "))
	}
}
