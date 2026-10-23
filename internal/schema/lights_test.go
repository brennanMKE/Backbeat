package schema

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

const lightsExample = "../../docs/examples/lights-example.json"

func loadExample(t *testing.T) *Lights {
	t.Helper()
	data, err := os.ReadFile(lightsExample)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields() // every documented field must exist in the Go types
	var l Lights
	if err := dec.Decode(&l); err != nil {
		t.Fatalf("decode %s: %v", lightsExample, err)
	}
	return &l
}

func TestLightsExampleIsValid(t *testing.T) {
	l := loadExample(t)
	if err := l.Validate(); err != nil {
		t.Fatal(err)
	}
	if l.Frames.FPS != LightsFPS {
		t.Errorf("example fps %d, want %d", l.Frames.FPS, LightsFPS)
	}
}

func TestLightsValidateRejects(t *testing.T) {
	cases := map[string]func(*Lights){
		"short curve":        func(l *Lights) { l.Frames.Energy = l.Frames.Energy[1:] },
		"curve out of range": func(l *Lights) { l.Frames.Onset[0] = 256 },
		"bad count":          func(l *Lights) { l.Frames.Count++ },
		"section gap":        func(l *Lights) { l.Sections[1].StartMs++ },
		"sections short":     func(l *Lights) { l.Sections[len(l.Sections)-1].EndMs-- },
		"palette size":       func(l *Lights) { l.Sections[0].Palette = l.Sections[0].Palette[:2] },
		"events unsorted":    func(l *Lights) { l.Events[1].Ms, l.Events[2].Ms = l.Events[2].Ms, l.Events[1].Ms },
		"no end event":       func(l *Lights) { l.Events = l.Events[:len(l.Events)-1] },
		"wrong version":      func(l *Lights) { l.Version = 2 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			l := loadExample(t)
			mutate(l)
			if err := l.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
