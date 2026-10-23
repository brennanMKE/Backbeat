package main

import (
	"context"
	"fmt"

	"github.com/brennanMKE/Backbeat/internal/config"
)

func cmdWorkers(_ context.Context, args []string) error {
	fs, _ := newFlagSet("workers", "[flags]")
	workers := fs.StringSlice("workers", nil, "worker base URLs (default from config)")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	urls := cfg.WorkerURLs()
	if fs.Changed("workers") {
		urls = *workers
	}
	if len(urls) == 0 {
		path, _ := config.Path()
		fmt.Printf("No workers configured. Add [[worker]] entries to %s or pass --workers.\n", path)
		return nil
	}
	for _, u := range urls {
		fmt.Println(u)
	}
	fmt.Println("\nHealth and capability checks are not implemented yet.")
	return nil
}
