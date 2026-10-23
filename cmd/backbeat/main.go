// Command backbeat analyzes a music library into per-track timelines for LED lighting.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/spf13/pflag"

	"github.com/brennanMKE/Backbeat/internal/config"
)

// version is set with -ldflags "-X main.version=...".
var version = "dev"

const usage = `Usage: backbeat <command> [flags]

Commands:
  scan <source>          list matching files and their cache status
  analyze <source>       analyze every .mp3 and .m4a under source
  show <path-or-hash>    print one result
  workers                list configured workers
  version                print version

A source is a local directory, [user@]host:path, or ssh://[user@]host[:port]/path.
Run "backbeat <command> --help" for command flags.
`

// errUsage marks errors that should print usage and exit 2.
var errUsage = errors.New("usage")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:]))
}

func run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	cmds := map[string]func(context.Context, []string) error{
		"scan":    cmdScan,
		"analyze": cmdAnalyze,
		"show":    cmdShow,
		"workers": cmdWorkers,
		"version": cmdVersion,
	}
	name, rest := args[0], args[1:]
	switch name {
	case "help", "-h", "--help":
		fmt.Print(usage)
		return 0
	}
	cmd, ok := cmds[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "backbeat: unknown command %q\n\n%s", name, usage)
		return 2
	}
	err := cmd(ctx, rest)
	var ec exitCode
	switch {
	case err == nil:
		return 0
	case errors.Is(err, pflag.ErrHelp):
		return 0
	case errors.Is(err, errUsage):
		return 2
	case errors.As(err, &ec):
		return int(ec)
	default:
		fmt.Fprintf(os.Stderr, "backbeat %s: %v\n", name, err)
		return 1
	}
}

// exitCode is returned by commands that already reported their outcome.
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit %d", int(e)) }

// common holds flags shared by every command.
type common struct {
	jsonLog bool
	verbose bool
}

func newFlagSet(name, args string) (*pflag.FlagSet, *common) {
	fs := pflag.NewFlagSet(name, pflag.ContinueOnError)
	c := &common{}
	fs.BoolVar(&c.jsonLog, "json-log", false, "write logs as JSON")
	fs.BoolVarP(&c.verbose, "verbose", "v", false, "debug logging")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: backbeat %s %s\n\nFlags:\n%s", name, args, fs.FlagUsages())
	}
	return fs, c
}

// parse parses flags and checks the positional argument count.
func parse(fs *pflag.FlagSet, args []string, nargs int) ([]string, error) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return nil, err
		}
		fmt.Fprintln(os.Stderr, err)
		fs.Usage()
		return nil, errUsage
	}
	if fs.NArg() != nargs {
		fs.Usage()
		return nil, errUsage
	}
	return fs.Args(), nil
}

func (c *common) logger() *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if c.verbose {
		opts.Level = slog.LevelDebug
	}
	if c.jsonLog {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}

// outDir resolves --out over the config file value.
func outDir(fs *pflag.FlagSet, flagVal string, cfg config.Config) (string, error) {
	out := cfg.Out
	if fs.Changed("out") {
		out = flagVal
	}
	return config.ExpandHome(out)
}

func cmdVersion(_ context.Context, args []string) error {
	fs, _ := newFlagSet("version", "")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	rev := ""
	goVersion := ""
	if bi, ok := debug.ReadBuildInfo(); ok {
		goVersion = bi.GoVersion
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 12 {
				rev = s.Value[:12]
			}
		}
	}
	fmt.Printf("backbeat %s", version)
	if rev != "" {
		fmt.Printf(" (%s)", rev)
	}
	fmt.Printf(" %s\n", goVersion)
	return nil
}
