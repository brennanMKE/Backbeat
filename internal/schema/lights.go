package schema

import (
	"fmt"
	"time"
)

// Lights format identifiers. See docs/lights-format.md.
const (
	LightsFormat  = "backbeat-lights"
	LightsVersion = 1
	LightsFPS     = 20
)

// Lights is the render-ready plan for one track, written to lights/<sha256>.json.
type Lights struct {
	Format      string          `json:"format"`
	Version     int             `json:"version"`
	Generator   string          `json:"generator"`
	GeneratedAt time.Time       `json:"generatedAt"`
	Track       LightsTrack     `json:"track"`
	Tempo       LightsTempo     `json:"tempo"`
	Frames      LightsFrames    `json:"frames"`
	Sections    []LightsSection `json:"sections"`
	Events      []LightsEvent   `json:"events"`
	Inputs      LightsInputs    `json:"inputs"`
}

// LightsTrack identifies the track for playback matching.
type LightsTrack struct {
	SHA256             string `json:"sha256"`
	DurationMs         int    `json:"durationMs"`
	Title              string `json:"title,omitempty"`
	Artist             string `json:"artist,omitempty"`
	Album              string `json:"album,omitempty"`
	MusicBrainzTrackID string `json:"musicbrainzTrackId,omitempty"`
}

// LightsTempo is the global tempo and beat phase.
type LightsTempo struct {
	BPM             float64 `json:"bpm"`
	BeatMs          int     `json:"beatMs"`
	BeatsPerBar     int     `json:"beatsPerBar"`
	FirstDownbeatMs int     `json:"firstDownbeatMs"`
	Quality         string  `json:"quality"`
}

// LightsFrames holds fixed-rate 0–255 curves.
type LightsFrames struct {
	FPS        int              `json:"fps"`
	Count      int              `json:"count"`
	Brightness []int            `json:"brightness"`
	Energy     []int            `json:"energy"`
	Onset      []int            `json:"onset"`
	Stems      map[string][]int `json:"stems,omitempty"`
}

// Color is a perceptual HSV color with its sRGB hex equivalent.
type Color struct {
	H   int     `json:"h"`
	S   float64 `json:"s"`
	V   float64 `json:"v"`
	Hex string  `json:"hex"`
}

// Transition describes how to enter a section.
type Transition struct {
	Type string `json:"type"` // "fade" or "cut"
	Ms   int    `json:"ms"`
}

// SectionKey is a section's key, when detected.
type SectionKey struct {
	Tonic string `json:"tonic"`
	Mode  string `json:"mode"`
}

// LightsSection is a span with a suggested look.
type LightsSection struct {
	StartMs    int         `json:"startMs"`
	EndMs      int         `json:"endMs"`
	Label      string      `json:"label"`
	Energy     float64     `json:"energy"`
	Key        *SectionKey `json:"key,omitempty"`
	Palette    []Color     `json:"palette"`
	Effect     string      `json:"effect"`
	Transition Transition  `json:"transition"`
}

// Event types.
const (
	EventSection  = "section"
	EventDownbeat = "downbeat"
	EventBeat     = "beat"
	EventDrop     = "drop"
	EventBuild    = "build"
	EventAccent   = "accent"
	EventSilence  = "silence"
	EventEnd      = "end"
)

// Effects.
const (
	EffectSolid   = "solid"
	EffectBreathe = "breathe"
	EffectPulse   = "pulse"
	EffectChase   = "chase"
	EffectSparkle = "sparkle"
	EffectStrobe  = "strobe"
)

// LightsEvent is one cue. Fields beyond Ms and Type depend on Type.
type LightsEvent struct {
	Ms       int      `json:"ms"`
	Type     string   `json:"type"`
	Strength *float64 `json:"strength,omitempty"`
	Bar      int      `json:"bar,omitempty"`
	Beat     int      `json:"beat,omitempty"`
	Index    *int     `json:"index,omitempty"`
	Label    string   `json:"label,omitempty"`
	EndMs    int      `json:"endMs,omitempty"`
}

// LightsInputs records which analyzers the file was built from.
type LightsInputs struct {
	Analysis       string   `json:"analysis"`
	IntegratedLUFS float64  `json:"integratedLUFS"`
	Rhythm         *string  `json:"rhythm"`
	Structure      *string  `json:"structure"`
	Key            *string  `json:"key"`
	Instruments    *string  `json:"instruments"`
	Missing        []string `json:"missing"`
}

// Validate checks the structural rules in docs/lights-format.md.
func (l *Lights) Validate() error {
	if l.Format != LightsFormat || l.Version != LightsVersion {
		return fmt.Errorf("format %q version %d, want %q %d", l.Format, l.Version, LightsFormat, LightsVersion)
	}
	d := l.Track.DurationMs
	if d <= 0 {
		return fmt.Errorf("durationMs %d", d)
	}
	f := l.Frames
	if f.FPS <= 0 {
		return fmt.Errorf("fps %d", f.FPS)
	}
	if want := (d*f.FPS + 999) / 1000; f.Count != want {
		return fmt.Errorf("frames.count %d, want %d", f.Count, want)
	}
	curves := map[string][]int{"brightness": f.Brightness, "energy": f.Energy, "onset": f.Onset}
	for name, c := range f.Stems {
		curves["stems."+name] = c
	}
	for name, c := range curves {
		if len(c) != f.Count {
			return fmt.Errorf("frames.%s has %d values, want %d", name, len(c), f.Count)
		}
		for i, v := range c {
			if v < 0 || v > 255 {
				return fmt.Errorf("frames.%s[%d] = %d, out of 0–255", name, i, v)
			}
		}
	}
	if len(l.Sections) == 0 {
		return fmt.Errorf("no sections")
	}
	for i, s := range l.Sections {
		wantStart := 0
		if i > 0 {
			wantStart = l.Sections[i-1].EndMs
		}
		if s.StartMs != wantStart || s.EndMs <= s.StartMs {
			return fmt.Errorf("section %d [%d, %d) is not contiguous", i, s.StartMs, s.EndMs)
		}
		if len(s.Palette) != 3 {
			return fmt.Errorf("section %d palette has %d colors, want 3", i, len(s.Palette))
		}
		for _, c := range s.Palette {
			if c.H < 0 || c.H > 359 || c.S < 0 || c.S > 1 || c.V < 0 || c.V > 1 {
				return fmt.Errorf("section %d color %+v out of range", i, c)
			}
		}
	}
	if last := l.Sections[len(l.Sections)-1]; last.EndMs != d {
		return fmt.Errorf("sections end at %d, want %d", last.EndMs, d)
	}
	if len(l.Events) == 0 {
		return fmt.Errorf("no events")
	}
	for i, e := range l.Events {
		if i > 0 && e.Ms < l.Events[i-1].Ms {
			return fmt.Errorf("event %d at %d ms is out of order", i, e.Ms)
		}
		if e.Ms < 0 || e.Ms > d {
			return fmt.Errorf("event %d at %d ms is outside the track", i, e.Ms)
		}
		if e.Strength != nil && (*e.Strength < 0 || *e.Strength > 1) {
			return fmt.Errorf("event %d strength %v out of 0–1", i, *e.Strength)
		}
	}
	if last := l.Events[len(l.Events)-1]; last.Type != EventEnd || last.Ms != d {
		return fmt.Errorf("last event must be end at %d ms", d)
	}
	return nil
}
