// Package probe wraps ffprobe and ffmpeg.
package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/brennanMKE/Backbeat/internal/schema"
)

// CheckTools fails if ffmpeg or ffprobe is not on PATH.
func CheckTools() error {
	var missing []string
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			missing = append(missing, tool)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s not found on PATH; install ffmpeg (scripts/install-deps.sh)", strings.Join(missing, " and "))
	}
	return nil
}

// Info is the stream and tag metadata of an audio file.
type Info struct {
	DurationSec float64
	Codec       string
	SampleRate  int
	Channels    int
	BitRate     int64
	Tags        schema.Tags
}

type ffprobeOutput struct {
	Streams []struct {
		CodecType  string            `json:"codec_type"`
		CodecName  string            `json:"codec_name"`
		SampleRate string            `json:"sample_rate"`
		Channels   int               `json:"channels"`
		Duration   string            `json:"duration"`
		BitRate    string            `json:"bit_rate"`
		Tags       map[string]string `json:"tags"`
	} `json:"streams"`
	Format struct {
		Duration string            `json:"duration"`
		BitRate  string            `json:"bit_rate"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
}

// Probe reads duration, codec and tags from the first audio stream of path.
func Probe(ctx context.Context, path string) (Info, error) {
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_format", "-show_streams", "-of", "json", "--", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return Info{}, fmt.Errorf("ffprobe: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return parse(out)
}

func parse(data []byte) (Info, error) {
	var p ffprobeOutput
	if err := json.Unmarshal(data, &p); err != nil {
		return Info{}, fmt.Errorf("ffprobe output: %w", err)
	}
	idx := -1
	for i, s := range p.Streams {
		if s.CodecType == "audio" {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Info{}, errors.New("no audio stream")
	}
	s := p.Streams[idx]
	info := Info{Codec: s.CodecName, Channels: s.Channels}
	info.SampleRate, _ = strconv.Atoi(s.SampleRate)
	info.DurationSec = parseFloat(p.Format.Duration)
	if info.DurationSec == 0 {
		info.DurationSec = parseFloat(s.Duration)
	}
	info.BitRate, _ = strconv.ParseInt(s.BitRate, 10, 64)
	if info.BitRate == 0 {
		info.BitRate, _ = strconv.ParseInt(p.Format.BitRate, 10, 64)
	}
	info.Tags = parseTags(p.Format.Tags, s.Tags)
	return info, nil
}

// parseTags merges container and stream tags, preferring the container.
// Keys are matched case-insensitively, ignoring spaces, underscores and dashes,
// so ID3 TXXX, MP4 freeform and Vorbis-style names all resolve.
func parseTags(maps ...map[string]string) schema.Tags {
	norm := map[string]string{}
	for _, m := range maps {
		for k, v := range m {
			key := normalizeKey(k)
			if _, ok := norm[key]; !ok && strings.TrimSpace(v) != "" {
				norm[key] = strings.TrimSpace(v)
			}
		}
	}
	get := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := norm[k]; ok {
				return v
			}
		}
		return ""
	}
	var t schema.Tags
	t.Title = get("title")
	t.Artist = get("artist")
	t.Album = get("album")
	t.AlbumArtist = get("albumartist")
	t.Date = get("date", "year")
	t.Genre = get("genre")
	t.Track, t.TrackTotal = parsePair(get("track", "tracknumber"))
	if n, _ := strconv.Atoi(get("tracktotal", "totaltracks")); n > 0 && t.TrackTotal == 0 {
		t.TrackTotal = n
	}
	t.Disc, t.DiscTotal = parsePair(get("disc", "discnumber"))
	if n, _ := strconv.Atoi(get("disctotal", "totaldiscs")); n > 0 && t.DiscTotal == 0 {
		t.DiscTotal = n
	}
	t.MusicBrainzTrackID = get("musicbrainztrackid")
	t.MusicBrainzReleaseTrackID = get("musicbrainzreleasetrackid")
	t.MusicBrainzAlbumID = get("musicbrainzalbumid")
	t.MusicBrainzArtistID = get("musicbrainzartistid")
	return t
}

func normalizeKey(k string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '_', '-':
			return -1
		}
		return r
	}, strings.ToLower(k))
}

// parsePair parses "3" or "3/12".
func parsePair(s string) (int, int) {
	n, total, _ := strings.Cut(s, "/")
	a, _ := strconv.Atoi(strings.TrimSpace(n))
	b, _ := strconv.Atoi(strings.TrimSpace(total))
	return a, b
}

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
