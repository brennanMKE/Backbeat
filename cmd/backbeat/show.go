package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/brennanMKE/Backbeat/internal/config"
	"github.com/brennanMKE/Backbeat/internal/schema"
	"github.com/brennanMKE/Backbeat/internal/store"
)

var shaRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

func cmdShow(ctx context.Context, args []string) error {
	fs, _ := newFlagSet("show", "<sha256 | file path | relative path> [flags]")
	out := fs.String("out", config.Default().Out, "results directory")
	raw := fs.Bool("json", false, "print the raw result JSON")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	dir, err := outDir(fs, *out, cfg)
	if err != nil {
		return err
	}
	st, ok, err := store.OpenExisting(dir)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no results in %s", dir)
	}
	defer st.Close()

	sha, err := resolveSHA(ctx, st, pos[0])
	if err != nil {
		return err
	}
	if *raw {
		data, err := os.ReadFile(st.TrackPath(sha))
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(data)
		return err
	}
	res, err := st.ReadResult(sha)
	if err != nil {
		return err
	}
	printResult(res)
	return nil
}

// resolveSHA accepts a hash, a local file path, or a relative path in the index.
func resolveSHA(ctx context.Context, st *store.Store, arg string) (string, error) {
	if shaRE.MatchString(arg) {
		return arg, nil
	}
	if info, err := os.Stat(arg); err == nil && info.Mode().IsRegular() {
		return hashPath(arg)
	}
	recs, err := st.FilesByRelPath(ctx, arg)
	if err != nil {
		return "", err
	}
	switch len(recs) {
	case 0:
		return "", fmt.Errorf("%q is not a hash, a file, or an indexed relative path", arg)
	case 1:
		return recs[0].SHA256, nil
	}
	var b strings.Builder
	for _, r := range recs {
		fmt.Fprintf(&b, "\n  %s  %s", r.SHA256, r.Source)
	}
	return "", fmt.Errorf("%q is in several sources; pass a hash:%s", arg, b.String())
}

func hashPath(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func printResult(r *schema.Result) {
	t := r.Track
	title := t.Tags.Title
	if title == "" {
		title = t.RelPath
	}
	if t.Tags.Artist != "" {
		title = t.Tags.Artist + " - " + title
	}
	fmt.Println(title)
	row := func(k, v string) {
		if v != "" {
			fmt.Printf("  %-12s %s\n", k+":", v)
		}
	}
	row("Album", t.Tags.Album)
	row("Source", t.Source)
	row("Path", t.RelPath)
	row("SHA-256", t.SHA256)
	row("Duration", formatDuration(t.DurationSec))
	row("Codec", formatCodec(t))
	if t.Tags.MusicBrainzTrackID != "" {
		row("MusicBrainz", t.Tags.MusicBrainzTrackID)
	}
	row("Analyzed", r.AnalyzedAt.Local().Format("2006-01-02 15:04:05"))
	fmt.Println()

	dim := func(name, analyzer, summary string) {
		fmt.Printf("  %-12s %s  [%s]\n", name+":", summary, analyzer)
	}
	if x := r.Rhythm; x != nil {
		dim("rhythm", x.Analyzer, fmt.Sprintf("%.1f BPM, %d beats, %d downbeats (%s)", x.BPM, len(x.Beats), len(x.Downbeats), x.Quality))
	}
	if x := r.Structure; x != nil {
		labels := make([]string, len(x.Sections))
		for i, s := range x.Sections {
			labels[i] = s.Label
		}
		dim("structure", x.Analyzer, strings.Join(labels, " "))
	}
	if x := r.Loudness; x != nil {
		dim("loudness", x.Analyzer, fmt.Sprintf("%.1f LUFS, LRA %.1f LU, peak %.1f dBTP", x.IntegratedLUFS, x.LoudnessRange, x.TruePeakDBTP))
	}
	if x := r.Key; x != nil {
		dim("key", x.Analyzer, fmt.Sprintf("%s %s (%.2f)", x.Global.Tonic, x.Global.Mode, x.Global.Confidence))
	}
	if x := r.Pace; x != nil {
		dim("pace", x.Analyzer, fmt.Sprintf("%d sections", len(x.Sections)))
	}
	if x := r.Instruments; x != nil {
		dim("instruments", x.Analyzer, fmt.Sprintf("%d stems at %g Hz", len(x.Stems), x.Hz))
	}
	if x := r.Onsets; x != nil {
		dim("onsets", x.Analyzer, fmt.Sprintf("%d onsets, envelope at %g Hz", x.Count, x.Hz))
	}
	if len(r.Missing) > 0 {
		fmt.Printf("  %-12s %s\n", "missing:", strings.Join(r.Missing, ", "))
	}
}

func formatDuration(sec float64) string {
	if sec <= 0 {
		return ""
	}
	s := int(sec + 0.5)
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func formatCodec(t schema.Track) string {
	parts := []string{t.Codec}
	if t.SampleRate > 0 {
		parts = append(parts, fmt.Sprintf("%g kHz", float64(t.SampleRate)/1000))
	}
	switch t.Channels {
	case 1:
		parts = append(parts, "mono")
	case 2:
		parts = append(parts, "stereo")
	case 0:
	default:
		parts = append(parts, fmt.Sprintf("%d ch", t.Channels))
	}
	if t.BitRate > 0 {
		parts = append(parts, fmt.Sprintf("%d kbps", t.BitRate/1000))
	}
	return strings.Join(parts, ", ")
}
