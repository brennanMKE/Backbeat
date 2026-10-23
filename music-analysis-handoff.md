# Cadence: Music Analysis Tool (Claude Code Handoff)

Working name: `cadence` (rename freely). A cross-platform Go CLI that analyzes a music library and produces per-track timelines for LED lighting, modeled on the dimensions of Apple's Music Understanding framework (key, rhythm, structure, pace, instrument activity, loudness), but runnable on macOS and Linux, including Raspberry Pi.

## Goals

1. Point the tool at a **local path** or a **remote path reachable over SSH**.
2. Find and process **every `.mp3` and `.m4a`** file, recursively.
3. Produce one JSON analysis file per track plus an index, using a stable, versioned schema.
4. Go owns the API, schema, orchestration, and caching. Heavy ML analysis runs in optional **worker backends** (Python) behind a small HTTP contract, so callers never care which implementation produced a result.
5. Single static Go binary on macOS arm64, Linux arm64 (Pi 4/5, 64-bit OS), and Linux amd64.

## Non-goals for v1

- LED rendering or controller protocol (consumes this output later).
- Live mic-based sync on the Pi (designed for in this doc, built in a later phase).
- Real-time or streaming input. v1 is batch over files.
- Any GUI.

## Runtime requirements

- Go (latest stable), built with `CGO_ENABLED=0`.
- `ffmpeg` and `ffprobe` on `PATH` (decoding, tags, duration, LUFS validation).
- System `ssh` client for remote sources.
- Optional: one or more worker processes (see Workers).

Fail fast with a clear message if `ffmpeg`/`ffprobe` are missing.

## CLI

```
cadence scan <source>                      # list matching files, sizes, cached/stale status
cadence analyze <source> [flags]           # analyze all matching files
cadence show <path-or-hash>                # pretty-print one result
cadence workers                            # list configured workers, capabilities, health
cadence version
```

### Source syntax

- Local: `/Volumes/Music`, `~/Music/Library`, `./music`
- Remote (scp-style): `brennan@macmini.local:/srv/music`
- Remote (URL): `ssh://brennan@macmini.local:22/srv/music`
- Host aliases from `~/.ssh/config` must work: `macmini:/srv/music`

### `analyze` flags

| Flag | Default | Notes |
|---|---|---|
| `--out DIR` | `./cadence-out` | Results directory |
| `--types LIST` | all available | Comma list: `rhythm,structure,loudness,key,pace,instruments,onsets` |
| `--jobs N` | `NumCPU/2`, min 1 | Concurrent tracks for native analyzers |
| `--workers URL,...` | from config | Worker base URLs |
| `--native-only` | false | Skip workers; only Go analyzers |
| `--force` | false | Ignore cache |
| `--include GLOB` / `--exclude GLOB` | none | Path filters |
| `--dry-run` | false | Show what would run |
| `--keep-remote-copies` | false | Keep downloaded files in the cache dir |

Extensions are matched case-insensitively (`.MP3`, `.M4A`). Skip `.m4p` (DRM) and log it. Skip hidden files and `._*` AppleDouble files.

### Config

`~/.config/cadence/config.toml` (respect `XDG_CONFIG_HOME`), overridable by flags:

```toml
out = "~/cadence-out"
jobs = 4

[[worker]]
url = "http://macmini.local:8765"
```

## Sources

Define an interface so local and remote share the pipeline:

```go
type Source interface {
    List(ctx context.Context) ([]FileEntry, error)          // recursive, filtered to mp3/m4a
    Open(ctx context.Context, e FileEntry) (LocalFile, error) // local path to read; may be a temp copy
}

type FileEntry struct {
    RelPath string    // relative to source root, forward slashes
    Size    int64
    ModTime time.Time
}
```

### Local source

`filepath.WalkDir`. Follow symlinks to files, not directories (avoid loops).

### Remote source (SSH)

Shell out to the system `ssh` binary rather than a Go SSH library. Reason: it honors `~/.ssh/config`, ssh-agent, ProxyJump, Tailscale SSH, and known_hosts with no extra code.

- **Connection reuse:** use OpenSSH multiplexing so each file does not pay a handshake: `-o ControlMaster=auto -o ControlPath=<cachedir>/ssh-%C -o ControlPersist=60`. Close the master at exit (`ssh -O exit`).
- **Listing:** one command, parsed in Go. Must work with both GNU find (Linux) and BSD find (macOS remote):
  - Prefer: `find <root> -type f \( -iname '*.mp3' -o -iname '*.m4a' \) -print0`, then a second batched `stat` call for size and mtime, detecting GNU (`stat -c '%s %Y'`) vs BSD (`stat -f '%z %m'`) via `uname`.
  - Quote paths safely (single-quote escaping); filenames will contain spaces, quotes, and unicode.
- **Transfer:** `ssh host cat -- '<path>'` streamed to a temp file in `<cachedir>/remote/`. Delete after analysis unless `--keep-remote-copies`.
- **Prefetch:** download up to `jobs + 1` files ahead of analysis so the network and CPU overlap. Bound temp disk usage.
- Treat transient SSH failures with a retry (3 attempts, backoff); a failing file must not abort the run.

## Pipeline per track

1. **Cache check (cheap):** look up `(sourceID, relPath, size, mtime)` in the index. If present and its analyzers' versions match the current ones, skip without downloading.
2. **Fetch** (remote only).
3. **Hash:** SHA-256 of file bytes. If the hash already has a current result (file moved or renamed), link it and skip analysis.
4. **Probe:** `ffprobe -show_format -show_streams -of json` for duration, codec, sample rate, tags (title, artist, album, album artist, track number, MusicBrainz IDs if present).
5. **Decode once:** `ffmpeg -i file -ac 1 -ar 22050 -f f32le -` into memory for native analyzers. (22.05 kHz mono is enough for onsets, tempo, chroma; loudness needs the original rate and channels, so the loudness analyzer requests its own decode at native rate, stereo.)
6. **Run analyzers** for requested types, native and worker in parallel.
7. **Write** `<out>/tracks/<sha256>.json` atomically (temp file + rename), update index.

Each analyzer records its name and version in the result. Bumping an analyzer version invalidates only that dimension.

## Analyzers

### Native Go (always available, including on Pi)

| Type | Method | Notes |
|---|---|---|
| `loudness` | ITU-R BS.1770 / EBU R128: K-weighting, 400 ms blocks, absolute and relative gating; integrated LUFS, loudness range (LRA), true peak (4x oversampled), short-term (3 s) series at 10 Hz | Validate against `ffmpeg -af ebur128=peak=true`, target within 0.1 LU |
| `onsets` | Spectral flux on log-magnitude STFT (2048 window, 512 hop at 22.05 kHz), half-wave rectified, normalized | Stored for Pi sync; core building block |
| `rhythm` (fallback) | Tempo from onset envelope autocorrelation plus comb filter; beat positions via dynamic programming (Ellis 2007 style) | Used only when no worker provides rhythm. Mark `"quality": "basic"` |
| `key` | Chroma (CQT or STFT-folded), Krumhansl-Schmuckler profiles, global key and mode with confidence, optional per-section key when structure exists | |
| `pace` | Derived: onset density and short-term energy per section (or per 8-bar window without structure), normalized 0 to 1 | Approximates Apple's "perceived speed"; document as derived |

Implement STFT/FFT with a pure Go library (e.g. `gonum.org/v1/gonum/dsp/fourier`). Verify the choice; no cgo.

### Worker-backed (optional)

| Type | Backend | Notes |
|---|---|---|
| `rhythm` | allin1 | BPM, beats, downbeats |
| `structure` | allin1 | Segment boundaries and labels (intro, verse, chorus, bridge, inst, solo, break, outro) |
| `instruments` | Demucs stems produced inside allin1 | allin1 already demixes audio with Demucs. Keep the stems (`--demix-dir`, verify flag behavior) and compute per-stem RMS at 10 Hz, normalized 0 to 1, for drums, bass, vocals, other. Avoid running Demucs twice |

Precedence: when both native and worker results exist for a type, the worker result wins and the native one is dropped (one source of truth per dimension).

## Workers

### Contract (HTTP + JSON)

Keep it simple so a worker can be a small FastAPI app.

- `GET /info` returns:
  ```json
  {"name": "allin1-worker", "version": "1.0.0",
   "provides": ["rhythm", "structure", "instruments"],
   "analyzers": {"rhythm": "allin1-harmonix-all@1.1.0"},
   "device": "mps", "maxConcurrent": 1}
  ```
- `POST /analyze` multipart: `file` (original audio bytes), `types` (comma list). Returns partial result objects in the same schema as the Go output for those types.
- `GET /health` for liveness.

The Go side uploads the original file (workers do their own decoding). Respect `maxConcurrent` with a per-worker semaphore. Timeouts scale with track duration (e.g. 10x duration, min 60 s). If a worker is down, log once, fall back to native where possible, and record missing dimensions so a later run fills them in.

### Worker implementation (`workers/allin1/`)

- Python managed with `uv`. Pin versions.
- allin1 installs madmom from git and needs NATTEN; installation is fragile. Verify the current install steps before scripting them.
- On Apple Silicon, evaluate the `all-in-one-mps` port (reported ~5x faster than upstream on MPS) and use it if output matches upstream on a few test tracks.
- Load models once at startup, not per request.
- Provide a `launchd` plist (macOS) and a `systemd` unit (Linux), plus a Dockerfile for amd64 Linux.
- **Not intended for the Pi.** Demucs on a Pi CPU is far too slow for a library. A Pi runs `cadence --native-only` or points `--workers` at the Mac mini.

### Licensing note

For personal use these are fine, but record them in the README:
- madmom code is BSD; its pretrained models are CC BY-NC-SA 4.0 (non-commercial).
- allin1 is MIT, but depends on madmom.
- Essentia (possible future mood worker) is AGPL-3.0.

Keep all of these behind the worker boundary so the Go binary has no license entanglement.

## Output

```
cadence-out/
  index.json                 # or index.sqlite (see below)
  tracks/<sha256>.json
  onsets/<sha256>.f16        # binary onset envelope, little-endian float16
```

Use SQLite for the index if it simplifies cache lookups (pure Go driver such as `modernc.org/sqlite` to keep `CGO_ENABLED=0`). Also export `index.json` for easy consumption.

### Track schema (v1)

All times are **seconds as float64** from track start. Time series use a fixed `hz` with a values array.

```json
{
  "schemaVersion": 1,
  "track": {
    "sha256": "…",
    "source": "brennan@macmini.local:/srv/music",
    "relPath": "Artist/Album/01 Song.m4a",
    "durationSec": 241.37,
    "codec": "aac",
    "tags": {"title": "…", "artist": "…", "album": "…", "albumArtist": "…", "track": 1,
             "musicbrainzTrackId": "…"}
  },
  "analyzedAt": "2026-10-23T20:00:00Z",
  "rhythm": {
    "analyzer": "allin1-harmonix-all@1.1.0", "quality": "model",
    "bpm": 120.0,
    "beats": [0.52, 1.02],
    "downbeats": [0.52, 2.52],
    "beatPositions": [1, 2, 3, 4]
  },
  "structure": {
    "analyzer": "…",
    "sections": [{"start": 0.0, "end": 12.4, "label": "intro"}]
  },
  "loudness": {
    "analyzer": "cadence-ebur128@1",
    "integratedLUFS": -9.8, "loudnessRange": 6.2, "truePeakDBTP": -0.4,
    "shortTerm": {"hz": 10, "values": [-14.1, -13.8]}
  },
  "key": {
    "analyzer": "cadence-ks@1",
    "global": {"tonic": "A", "mode": "minor", "confidence": 0.71},
    "sections": [{"start": 0.0, "end": 12.4, "tonic": "A", "mode": "minor"}]
  },
  "pace": {
    "analyzer": "cadence-pace@1",
    "sections": [{"start": 0.0, "end": 12.4, "value": 0.35}]
  },
  "instruments": {
    "analyzer": "allin1-demucs-rms@1",
    "hz": 10,
    "stems": {"drums": [0.0, 0.1], "bass": [], "vocals": [], "other": []}
  },
  "onsets": {
    "analyzer": "cadence-flux@1",
    "hz": 43.07, "file": "onsets/<sha256>.f16", "count": 10397
  },
  "missing": ["structure"]
}
```

`tags` exist so a later runtime can match the track Music Assistant is playing (by MusicBrainz ID, else artist/title/duration). `relPath` supports path-based matching when the library path is known.

## Progress and logging

- Progress line: files done/total, current file, ETA, worker queue depth. Plain logs when not a TTY.
- `--json-log` for machine-readable logs.
- End-of-run summary: analyzed, skipped (cached), failed (with reasons), missing dimensions.
- Ctrl-C: finish writing in-flight results atomically, stop cleanly, close SSH master.

## Testing

- Unit tests for source parsing (scp-style, URL, ssh config alias) and remote path quoting (spaces, quotes, unicode, leading dashes).
- Remote source integration test against `localhost` over SSH (skippable if no sshd).
- Golden tests with a few Creative Commons tracks checked into `testdata/` (or downloaded by a script):
  - Loudness vs `ffmpeg ebur128`, within 0.1 LU integrated.
  - Native tempo vs allin1 BPM, within 2% or an octave multiple (report octave errors separately).
  - Synthetic click track at known BPM for beat placement, within 20 ms.
- Worker contract test with a fake worker (Go `httptest`) to exercise fallback, timeouts, and partial results.

## Milestones

1. **Skeleton:** CLI, config, local source, ffprobe, index, JSON output, `scan`.
2. **Remote source:** SSH listing, multiplexed transfer, prefetch, retry.
3. **Native analyzers:** loudness, onsets, key, basic rhythm, pace.
4. **Worker contract + allin1 worker:** rhythm, structure, instruments from stems.
5. **Hardening:** cancellation, progress, golden tests, Pi build and benchmark (time per track, native-only on Pi 4 and Pi 5).

## Later phase: Pi live sync (design only, do not build in v1)

Purpose: correct playback position for LED cues, since Home Assistant / Music Assistant position updates are coarse.

- `cadence sync --mic <device> --track <sha256> --approx-pos <sec>` (eventually driven by HA events).
- Capture mic audio, compute the **same** onset envelope as the `onsets` analyzer (same STFT params, resampled to 22.05 kHz).
- Cross-correlate a 4 to 6 s live window against the stored envelope within ±3 s of the approximate position. Peak gives exact offset, including speaker and network latency.
- Re-lock every few seconds; accept only when correlation confidence exceeds a threshold, otherwise fall back to the HA-reported position plus elapsed time.
- Emit beat clock messages ("beat N at local time T") to LED controllers.

This is why `onsets` is stored per track and why its parameters must be versioned and identical between batch and live code. Put the onset code in one shared Go package.

## Repo layout

```
cmd/cadence/            main
internal/source/        local, ssh
internal/probe/         ffprobe, ffmpeg decode
internal/analyze/       loudness, onsets, key, rhythm, pace (pure Go)
internal/workers/       HTTP client, registry, fallback
internal/store/         index, results, atomic writes
internal/schema/        types + JSON schema export
workers/allin1/         Python worker (uv, FastAPI), launchd/systemd/Dockerfile
testdata/
```

## Open questions for Brennan

- Where the library lives (which host and path) and roughly how many tracks, to size the first run.
- Whether results should also be copied back next to the remote library, or kept only on the machine running `cadence`.
- Final name for the tool.
