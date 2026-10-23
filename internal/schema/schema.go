// Package schema defines the versioned JSON format for track analysis results.
package schema

import (
	"slices"
	"time"
)

// Version is the current result schema version. Bump it on breaking changes.
const Version = 1

// Analysis dimensions.
const (
	TypeRhythm      = "rhythm"
	TypeStructure   = "structure"
	TypeLoudness    = "loudness"
	TypeKey         = "key"
	TypePace        = "pace"
	TypeInstruments = "instruments"
	TypeOnsets      = "onsets"
)

// AllTypes lists every dimension in canonical order.
var AllTypes = []string{TypeRhythm, TypeStructure, TypeLoudness, TypeKey, TypePace, TypeInstruments, TypeOnsets}

// IsType reports whether s names a known dimension.
func IsType(s string) bool { return slices.Contains(AllTypes, s) }

// Result is the full analysis of one track, written to tracks/<sha256>.json.
type Result struct {
	SchemaVersion int          `json:"schemaVersion"`
	Track         Track        `json:"track"`
	AnalyzedAt    time.Time    `json:"analyzedAt"`
	Rhythm        *Rhythm      `json:"rhythm,omitempty"`
	Structure     *Structure   `json:"structure,omitempty"`
	Loudness      *Loudness    `json:"loudness,omitempty"`
	Key           *Key         `json:"key,omitempty"`
	Pace          *Pace        `json:"pace,omitempty"`
	Instruments   *Instruments `json:"instruments,omitempty"`
	Onsets        *Onsets      `json:"onsets,omitempty"`
	// Missing lists requested dimensions that no analyzer produced.
	Missing []string `json:"missing"`
}

// Analyzers returns the analyzer ID of each dimension present in r, keyed by type.
func (r *Result) Analyzers() map[string]string {
	m := map[string]string{}
	add := func(t, id string) { m[t] = id }
	if r.Rhythm != nil {
		add(TypeRhythm, r.Rhythm.Analyzer)
	}
	if r.Structure != nil {
		add(TypeStructure, r.Structure.Analyzer)
	}
	if r.Loudness != nil {
		add(TypeLoudness, r.Loudness.Analyzer)
	}
	if r.Key != nil {
		add(TypeKey, r.Key.Analyzer)
	}
	if r.Pace != nil {
		add(TypePace, r.Pace.Analyzer)
	}
	if r.Instruments != nil {
		add(TypeInstruments, r.Instruments.Analyzer)
	}
	if r.Onsets != nil {
		add(TypeOnsets, r.Onsets.Analyzer)
	}
	return m
}

// Track identifies the analyzed file and carries its metadata.
type Track struct {
	SHA256      string  `json:"sha256"`
	Source      string  `json:"source"`
	RelPath     string  `json:"relPath"`
	DurationSec float64 `json:"durationSec"`
	Codec       string  `json:"codec"`
	SampleRate  int     `json:"sampleRate,omitempty"`
	Channels    int     `json:"channels,omitempty"`
	BitRate     int64   `json:"bitRate,omitempty"`
	Tags        Tags    `json:"tags"`
}

// Tags holds the metadata used to match a track at playback time.
type Tags struct {
	Title       string `json:"title,omitempty"`
	Artist      string `json:"artist,omitempty"`
	Album       string `json:"album,omitempty"`
	AlbumArtist string `json:"albumArtist,omitempty"`
	Track       int    `json:"track,omitempty"`
	TrackTotal  int    `json:"trackTotal,omitempty"`
	Disc        int    `json:"disc,omitempty"`
	DiscTotal   int    `json:"discTotal,omitempty"`
	Date        string `json:"date,omitempty"`
	Genre       string `json:"genre,omitempty"`
	// MusicBrainzTrackID is the recording ID (Picard's "MusicBrainz Track Id").
	MusicBrainzTrackID        string `json:"musicbrainzTrackId,omitempty"`
	MusicBrainzReleaseTrackID string `json:"musicbrainzReleaseTrackId,omitempty"`
	MusicBrainzAlbumID        string `json:"musicbrainzAlbumId,omitempty"`
	MusicBrainzArtistID       string `json:"musicbrainzArtistId,omitempty"`
}

// Series is a time series sampled at a fixed rate starting at time 0.
type Series struct {
	Hz     float64   `json:"hz"`
	Values []float64 `json:"values"`
}

// Rhythm holds tempo and beat positions.
type Rhythm struct {
	Analyzer string `json:"analyzer"`
	// Quality is "model" for ML workers or "basic" for the native fallback.
	Quality       string    `json:"quality"`
	BPM           float64   `json:"bpm"`
	Beats         []float64 `json:"beats"`
	Downbeats     []float64 `json:"downbeats,omitempty"`
	BeatPositions []int     `json:"beatPositions,omitempty"`
}

// Section is a labeled time span.
type Section struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Label string  `json:"label"`
}

// Structure holds the song's segmentation.
type Structure struct {
	Analyzer string    `json:"analyzer"`
	Sections []Section `json:"sections"`
}

// Loudness holds EBU R128 measurements.
type Loudness struct {
	Analyzer       string  `json:"analyzer"`
	IntegratedLUFS float64 `json:"integratedLUFS"`
	LoudnessRange  float64 `json:"loudnessRange"`
	TruePeakDBTP   float64 `json:"truePeakDBTP"`
	ShortTerm      Series  `json:"shortTerm"`
}

// KeyEstimate is a tonic and mode with optional confidence.
type KeyEstimate struct {
	Tonic      string  `json:"tonic"`
	Mode       string  `json:"mode"`
	Confidence float64 `json:"confidence,omitempty"`
}

// KeySection is a key estimate over a time span.
type KeySection struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	KeyEstimate
}

// Key holds the global key and optional per-section keys.
type Key struct {
	Analyzer string       `json:"analyzer"`
	Global   KeyEstimate  `json:"global"`
	Sections []KeySection `json:"sections,omitempty"`
}

// PaceSection is a normalized 0 to 1 pace value over a time span.
type PaceSection struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Value float64 `json:"value"`
}

// Pace holds perceived speed, derived from onset density and energy.
type Pace struct {
	Analyzer string        `json:"analyzer"`
	Sections []PaceSection `json:"sections"`
}

// Instruments holds per-stem activity, normalized 0 to 1, at a fixed rate.
type Instruments struct {
	Analyzer string               `json:"analyzer"`
	Hz       float64              `json:"hz"`
	Stems    map[string][]float64 `json:"stems"`
}

// Onsets points at the binary onset envelope stored beside the result.
type Onsets struct {
	Analyzer string  `json:"analyzer"`
	Hz       float64 `json:"hz"`
	File     string  `json:"file"`
	Count    int     `json:"count"`
}
