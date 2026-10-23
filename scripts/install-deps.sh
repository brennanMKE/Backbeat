#!/usr/bin/env bash
# Install runtime and build dependencies: Go, ffmpeg/ffprobe, ssh, uv.
# Supports macOS (Homebrew) and Debian/Ubuntu/Raspberry Pi OS (apt).
set -euo pipefail

need() { command -v "$1" >/dev/null 2>&1; }
log() { printf '==> %s\n' "$*"; }

install_macos() {
    if ! need brew; then
        echo "Homebrew is required: https://brew.sh" >&2
        exit 1
    fi
    need go || { log "Installing go"; brew install go; }
    need ffmpeg && need ffprobe || { log "Installing ffmpeg"; brew install ffmpeg; }
    need uv || { log "Installing uv"; brew install uv; }
}

install_linux() {
    if ! need apt-get; then
        echo "Only apt-based Linux is supported by this script; install go, ffmpeg, openssh-client manually." >&2
        exit 1
    fi
    local sudo=""
    [ "$(id -u)" -ne 0 ] && sudo="sudo"
    local pkgs=()
    need ffmpeg && need ffprobe || pkgs+=(ffmpeg)
    need ssh || pkgs+=(openssh-client)
    if [ ${#pkgs[@]} -gt 0 ]; then
        log "Installing ${pkgs[*]}"
        $sudo apt-get update
        $sudo apt-get install -y "${pkgs[@]}"
    fi
    # Distro Go packages lag behind; point to the official tarball instead.
    need go || echo "Go not found. Install the latest from https://go.dev/dl/" >&2
    # uv is only needed for Python workers, which are not intended for the Pi.
    need uv || log "uv not installed (only needed for workers): curl -LsSf https://astral.sh/uv/install.sh | sh"
}

case "$(uname -s)" in
    Darwin) install_macos ;;
    Linux) install_linux ;;
    *) echo "Unsupported OS: $(uname -s)" >&2; exit 1 ;;
esac

log "Versions"
need go && go version
need ffmpeg && ffmpeg -version | head -1
need ffprobe && ffprobe -version | head -1
need ssh && ssh -V 2>&1
need uv && uv --version

missing=()
for c in go ffmpeg ffprobe ssh; do need "$c" || missing+=("$c"); done
if [ ${#missing[@]} -gt 0 ]; then
    echo "Missing: ${missing[*]}" >&2
    exit 1
fi
log "All required dependencies present"
