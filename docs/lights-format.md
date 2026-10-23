# Lights Format (`backbeat-lights` v1)

The lights file is the file an LED renderer consumes. It turns the analysis of one track into three things a renderer needs:

1. **Curves**: continuous 0–255 signals at a fixed frame rate (brightness, energy, onsets, per-instrument activity).
2. **Events**: a time-sorted cue list (beats, downbeats, section changes, drops, builds, accents, silences).
3. **Sections**: spans with a suggested palette, effect and transition.

It says *what the music is doing* and suggests a look. It does not address pixels, strips or controllers; mapping to hardware is the renderer's job.

## Location

```
backbeat-out/
  tracks/<sha256>.json   # analysis result (source of truth, seconds as floats)
  lights/<sha256>.json   # this format (derived, render-ready)
```

The lights file is derived from the analysis result by a versioned generator (`backbeat-lights@N`). Changing the mapping (colors, thresholds) bumps the generator and regenerates lights files without re-analyzing audio. `index.json` lists the lights file for each track.

## Conventions

| Rule | Detail |
|---|---|
| Time | Integer **milliseconds** from track start. Field names end in `Ms` (or are `ms` in events). |
| Levels | Curves are integers **0–255**. Event and section strengths are floats **0–1**. |
| Perceptual values | Brightness and color values are perceptual. **Renderers apply LED gamma correction** (e.g. gamma 2.2–2.8) and their own power limits. |
| Color | HSV: `h` degrees 0–359, `s` and `v` 0–1. `hex` is the same color as sRGB for convenience. |
| Ordering | `events` sorted by `ms`, ties in the order listed in [Event types](#event-types). `sections` contiguous and cover the whole track. |
| Compatibility | Renderers **must ignore unknown fields, event types and effect names**. Adding fields, event types or effects does not bump `version`. Removing or changing the meaning of a field does. |

## Top level

```json
{
  "format": "backbeat-lights",
  "version": 1,
  "generator": "backbeat-lights@1",
  "generatedAt": "2026-10-23T20:00:00Z",
  "track": { ... },
  "tempo": { ... },
  "frames": { ... },
  "sections": [ ... ],
  "events": [ ... ],
  "inputs": { ... }
}
```

### `track`

Enough to match the track that is playing (by MusicBrainz ID, else artist, title and duration).

| Field | Type | Notes |
|---|---|---|
| `sha256` | string | Content hash; also the file name |
| `durationMs` | int | |
| `title`, `artist`, `album` | string | Omitted when untagged |
| `musicbrainzTrackId` | string | Recording ID, when tagged |

### `tempo`

| Field | Type | Notes |
|---|---|---|
| `bpm` | float | Global tempo |
| `beatMs` | int | `round(60000 / bpm)`, for renderers that animate between beats |
| `beatsPerBar` | int | Usually 4 |
| `firstDownbeatMs` | int | Phase anchor |
| `quality` | string | `model` (ML worker) or `basic` (native fallback). Renderers may tone down beat-locked effects for `basic`. |

The beat grid is not assumed steady: use `beat` and `downbeat` events for exact times.

### `frames`

Fixed-rate curves. Frame `i` covers `[i * 1000 / fps, (i + 1) * 1000 / fps)` ms. Renderers running faster than `fps` should interpolate linearly.

| Field | Type | Notes |
|---|---|---|
| `fps` | int | 20 in v1 (50 ms frames) |
| `count` | int | `ceil(durationMs * fps / 1000)`; every curve has exactly `count` values |
| `brightness` | int[] | **Suggested master brightness.** Energy shaped with a floor (never fully dark during music) and a section multiplier. 0 only in silences. |
| `energy` | int[] | Raw overall intensity: loudness, onset density and (when available) stem activity, smoothed over about 1 s, normalized per track. Use it to drive your own brightness, speed or saturation. |
| `onset` | int[] | Percussive activity, max-pooled per frame so short hits are not lost. Good for sparkle density or reactive flicker. |
| `stems` | object | Optional. `drums`, `bass`, `vocals`, `other`, each an int[] of `count` values. Present only when a worker produced instrument stems. |

Normalization is **per track** (255 is the loudest moment of this track), so quiet songs still use the full range. Use `inputs.integratedLUFS` to scale across tracks if you want quiet songs to stay dimmer.

### `sections`

Contiguous spans covering the track. From the structure analysis when available, otherwise 8-bar windows labeled `section`.

| Field | Type | Notes |
|---|---|---|
| `startMs`, `endMs` | int | |
| `label` | string | `intro`, `verse`, `chorus`, `bridge`, `inst`, `solo`, `break`, `outro`, or `section` |
| `energy` | float | Mean energy of the span, 0–1 |
| `key` | object | Optional `{"tonic": "A", "mode": "minor"}` |
| `palette` | color[] | 3 colors: `[base, secondary, accent]`. See [Color mapping](#color-mapping). |
| `effect` | string | Suggested effect, see [Effects](#effects) |
| `transition` | object | How to enter this section: `{"type": "fade" or "cut", "ms": int}` |

A color is `{"h": 222, "s": 0.55, "v": 0.6, "hex": "#45679a"}`.

### `events`

| Field | Type | Notes |
|---|---|---|
| `ms` | int | Event time |
| `type` | string | See below |
| `strength` | float | 0–1, optional; how hard to hit it |
| other | | Per type, below |

#### Event types

| Type | Extra fields | Meaning | Typical use |
|---|---|---|---|
| `section` | `index`, `label` | A section starts (same times as `sections[index].startMs`) | Switch palette and effect |
| `downbeat` | `bar` (1-based) | First beat of a bar | Big flash, chase step, color step |
| `beat` | `bar`, `beat` (1-based within bar) | Every other beat | Pulse |
| `drop` | | A sharp energy rise into a high-energy section | Full white or accent burst, strobe-eligible |
| `build` | `endMs` | Rising energy leading into a peak or drop | Ramp speed, saturation or brightness until `endMs` |
| `accent` | | A strong percussive hit off the beat grid | Sparkle burst, short flash |
| `silence` | `endMs` | Near-silence (gaps, fade-outs, hidden tracks) | Fade to black or idle |

Downbeats are listed as `downbeat` only, not also as `beat`. The last event is always `{"ms": durationMs, "type": "end"}`.

## Effects

A small, renderer-neutral vocabulary. The generator picks one per section from its energy and label; renderers map each name to their own pattern and may ignore the suggestion.

| Effect | When suggested (energy) | Intent |
|---|---|---|
| `solid` | silence or ambient | Steady color, no motion |
| `breathe` | < 0.30 | Slow brightness swell, about one cycle per bar |
| `pulse` | 0.30 – 0.60 | Flash on beats with decay, stronger on downbeats |
| `chase` | 0.60 – 0.85 | Motion that steps on beats |
| `sparkle` | label `break` or `solo`, or high `onset` with low `energy` | Random twinkles at a density driven by the `onset` curve |
| `strobe` | > 0.85, or at `drop` events | Accent flashes on downbeats |

**Photosensitivity:** renderers must cap full-field flashes at **3 per second** whatever the events say (WCAG 2.3.1). The generator never emits `strobe` for more than one bar without a break, but the cap belongs in the renderer.

## Color mapping

The generator maps key to hue so that musically close keys get close colors and modulations shift smoothly:

- **Hue from the circle of fifths:** C = 0°, G = 30°, D = 60°, … F = 330°. A minor key takes the hue of its relative major (A minor = C), so a verse in A minor and a chorus in C major share a color family.
- **Mode sets the mood:** major uses higher saturation and value, minor lower saturation and a slightly cooler shift (−15°).
- **Label and energy set intensity:** `v` scales with section energy (`intro`/`outro`/`break` dimmer, `chorus` brightest). `bridge` shifts hue +30° for contrast.
- **Palette:** `base` = key hue; `secondary` = analogous (+30°); `accent` = complementary (+180°), for downbeats, accents and drops.
- **No key detected:** a neutral warm palette (base 30°, low saturation).

The palette is a suggestion. A renderer with a fixed theme can ignore it and still use the curves and events.

## Inputs and fallbacks

`inputs` records what the file was built from, so renderers can adapt and a later run can upgrade it.

```json
"inputs": {
  "analysis": "tracks/<sha256>.json",
  "integratedLUFS": -9.8,
  "rhythm": "allin1-harmonix-all@1.1.0",
  "structure": "allin1-harmonix-all@1.1.0",
  "key": "backbeat-ks@1",
  "instruments": null,
  "missing": ["instruments"]
}
```

| Missing input | Effect on the lights file |
|---|---|
| `structure` | Sections are 8-bar windows labeled `section`; no `drop` events at section boundaries (energy-based drops still appear) |
| `instruments` | No `frames.stems`; `energy` uses loudness and onsets only |
| `key` | Neutral palette, no `key` on sections |
| `rhythm` (model) | Native beats with `tempo.quality = "basic"` |
| `loudness` | Not allowed: loudness is always computed natively, so a lights file always has `brightness` and `energy` |

## Size

At 20 fps a 4-minute track has 4,800 frames. With three curves plus four stems that is about 120 KB of JSON, and about 30 KB gzipped. Fine for a Pi; an ESP32 should receive the section and event lists plus a windowed slice of the curves, streamed by the Pi.

## Example

[examples/lights-example.json](examples/lights-example.json) is a complete, valid 8-second file. `internal/schema` validates it in its tests, so the example and the Go types cannot drift apart.
