# Backbeat

Backbeat analyzes a music library and writes per-track timelines (beats, sections, key, loudness, pace, instrument activity, onsets) for driving LED lighting. It is a single static Go binary for macOS and Linux, including Raspberry Pi, with optional Python worker backends for the heavy ML analysis.

> **Status:** early development. See [music-analysis-handoff.md](music-analysis-handoff.md) for the full design and milestones.

## Requirements

- Go (latest stable) to build
- `ffmpeg` and `ffprobe` on `PATH`
- OpenSSH client for remote sources
- Optional: [uv](https://docs.astral.sh/uv/) for Python workers

Install them with:

```sh
scripts/install-deps.sh
```

The script uses Homebrew on macOS and apt on Debian, Ubuntu and Raspberry Pi OS.

## Build

```sh
make build   # ./backbeat
make test
make dist    # dist/backbeat-{darwin-arm64,linux-arm64,linux-amd64}
```

Builds use `CGO_ENABLED=0`, so every binary is static.

## Usage

```sh
backbeat scan <source>              # list matching files and their cache status
backbeat analyze <source> [flags]   # analyze every .mp3 and .m4a under source
backbeat show <path-or-hash>        # print one result
backbeat workers                    # list configured workers
backbeat version
```

A source is a local directory or a directory reachable over SSH:

```
/Volumes/Music
~/Music/Library
brennan@macmini.local:/srv/music
ssh://brennan@macmini.local:22/srv/music
macmini:/srv/music                  # host alias from ~/.ssh/config
```

Common `analyze` flags:

| Flag | Default | Notes |
|---|---|---|
| `--out DIR` | `./backbeat-out` | Results directory |
| `--types LIST` | all | `rhythm,structure,loudness,key,pace,instruments,onsets` |
| `--jobs N` | half the CPUs | Concurrent tracks |
| `--workers URL,...` | from config | Worker base URLs |
| `--native-only` | false | Skip workers |
| `--force` | false | Ignore the cache |
| `--include GLOB` / `--exclude GLOB` | none | Path filters, repeatable |
| `--dry-run` | false | Show what would run |

## Configuration

`~/.config/backbeat/config.toml` (respects `XDG_CONFIG_HOME`). Flags override it.

```toml
out = "~/backbeat-out"
jobs = 4

[[worker]]
url = "http://macmini.local:8765"
```

## Output

```
backbeat-out/
  index.sqlite         # cache and lookup index
  index.json           # exported index for easy consumption
  tracks/<sha256>.json # one versioned result per track
  onsets/<sha256>.f16  # onset envelope, little-endian float16
```

All times are seconds from track start. Time series use a fixed `hz` and a values array.

## License

See [LICENSE](LICENSE).

### Third-party models

The optional workers depend on projects with their own licenses. They run as separate processes behind an HTTP boundary, so the Go binary does not link against them.

- **madmom:** the code is BSD, but the pretrained models are CC BY-NC-SA 4.0 (non-commercial).
- **allin1:** MIT, but it depends on madmom.
- **Essentia** (possible future worker): AGPL-3.0.
