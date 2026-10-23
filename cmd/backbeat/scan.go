package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/brennanMKE/Backbeat/internal/config"
	"github.com/brennanMKE/Backbeat/internal/pipeline"
	"github.com/brennanMKE/Backbeat/internal/schema"
	"github.com/brennanMKE/Backbeat/internal/source"
	"github.com/brennanMKE/Backbeat/internal/store"
)

func cmdScan(ctx context.Context, args []string) error {
	fs, c := newFlagSet("scan", "<source> [flags]")
	out := fs.String("out", config.Default().Out, "results directory to check the cache against")
	include := fs.StringArray("include", nil, "only paths matching glob (repeatable)")
	exclude := fs.StringArray("exclude", nil, "skip paths matching glob (repeatable)")
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
	filter := source.Filter{Include: *include, Exclude: *exclude}
	if err := filter.Validate(); err != nil {
		return err
	}
	src, entries, err := openAndList(ctx, pos[0], filter, log)
	if err != nil {
		return err
	}

	// Scanning never creates the output directory.
	st, ok, err := store.OpenExisting(dir)
	if err != nil {
		return err
	}
	var p *pipeline.Pipeline
	if ok {
		defer st.Close()
		p = &pipeline.Pipeline{Src: src, Store: st, Types: schema.AllTypes, Analyzers: map[string]string{}}
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "STATUS\tSIZE\tPATH")
	counts := map[pipeline.Status]int{}
	var total int64
	for _, e := range entries {
		status := pipeline.StatusNew
		if p != nil {
			if status, err = p.Check(ctx, e); err != nil {
				return err
			}
		}
		counts[status]++
		total += e.Size
		fmt.Fprintf(tw, "%s\t%s\t%s\n", status, humanBytes(e.Size), e.RelPath)
	}
	tw.Flush()
	fmt.Printf("\n%d files, %s: %d new, %d changed, %d stale, %d cached\n", len(entries), humanBytes(total),
		counts[pipeline.StatusNew], counts[pipeline.StatusChanged], counts[pipeline.StatusStale], counts[pipeline.StatusCached])
	return nil
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
