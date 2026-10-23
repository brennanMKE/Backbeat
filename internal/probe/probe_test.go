package probe

import (
	"context"
	"math"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseTags(t *testing.T) {
	tags := parseTags(
		map[string]string{
			"TITLE":                "Song",
			"artist":               "Artist",
			"album_artist":         "Various",
			"track":                "3/12",
			"disc":                 "1",
			"TOTALDISCS":           "2",
			"MusicBrainz Track Id": "rec-id",
			"MUSICBRAINZ_ALBUMID":  "album-id",
		},
		map[string]string{"title": "Stream Title", "album": "Album"},
	)
	if tags.Title != "Song" || tags.Album != "Album" || tags.AlbumArtist != "Various" {
		t.Errorf("text tags: %+v", tags)
	}
	if tags.Track != 3 || tags.TrackTotal != 12 || tags.Disc != 1 || tags.DiscTotal != 2 {
		t.Errorf("numbers: %+v", tags)
	}
	if tags.MusicBrainzTrackID != "rec-id" || tags.MusicBrainzAlbumID != "album-id" {
		t.Errorf("musicbrainz: %+v", tags)
	}
}

func TestParseNoAudio(t *testing.T) {
	if _, err := parse([]byte(`{"streams":[{"codec_type":"video"}],"format":{}}`)); err == nil {
		t.Fatal("expected error")
	}
}

func TestProbeGenerated(t *testing.T) {
	if err := CheckTools(); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	cases := []struct {
		name, codec string
		args        []string
	}{
		{"tone.m4a", "aac", []string{"-c:a", "aac"}},
		{"tone.mp3", "mp3", []string{"-c:a", "libmp3lame", "-metadata", "MusicBrainz Track Id=rec-id"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := filepath.Join(dir, c.name)
			args := []string{"-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=2:sample_rate=44100",
				"-metadata", "title=Tone", "-metadata", "artist=Test", "-metadata", "album_artist=Various", "-metadata", "track=3/12"}
			args = append(append(args, c.args...), out)
			if b, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
				t.Skipf("ffmpeg cannot encode %s: %v: %s", c.name, err, b)
			}
			info, err := Probe(context.Background(), out)
			if err != nil {
				t.Fatal(err)
			}
			if info.Codec != c.codec || info.SampleRate != 44100 || info.Channels != 1 {
				t.Errorf("stream info: %+v", info)
			}
			if math.Abs(info.DurationSec-2) > 0.1 {
				t.Errorf("duration = %v", info.DurationSec)
			}
			tg := info.Tags
			if tg.Title != "Tone" || tg.Artist != "Test" || tg.AlbumArtist != "Various" || tg.Track != 3 || tg.TrackTotal != 12 {
				t.Errorf("tags: %+v", tg)
			}
			if c.codec == "mp3" && tg.MusicBrainzTrackID != "rec-id" {
				t.Errorf("musicbrainz id = %q", tg.MusicBrainzTrackID)
			}
		})
	}
}
