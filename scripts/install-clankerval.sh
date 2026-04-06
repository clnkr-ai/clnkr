#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=/dev/null
. "$ROOT/tools/clankerval.env"

REPO="clnkr-ai/clankerval"

log() {
    printf '%s\n' "$*" >&2
}

verify_installed_command() {
    local name="$1"
    local expected_path="$2"
    local resolved_path

    resolved_path="$(command -v "$name" 2>/dev/null || true)"
    if [ -z "$resolved_path" ]; then
        log "error: installed ${name} to ${expected_path}, but ${name} is not on PATH in this shell"
        return 1
    fi
    if [ "$resolved_path" != "$expected_path" ]; then
        log "error: installed ${name} to ${expected_path}, but PATH resolves ${name} to ${resolved_path}"
        return 1
    fi
    if ! "$name" --version >/dev/null 2>&1; then
        log "error: ${name} resolves to ${resolved_path}, but ${name} --version failed"
        return 1
    fi

    return 0
}

download() {
    local url="$1"
    local destination="$2"

    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$url" -o "$destination"
        return 0
    fi
    if command -v wget >/dev/null 2>&1; then
        wget -qO "$destination" "$url"
        return 0
    fi

    log "error: need curl or wget to download clankerval"
    return 1
}

detect_os() {
    case "$(uname -s)" in
        Linux) printf 'linux\n' ;;
        Darwin) printf 'darwin\n' ;;
        *)
            log "error: unsupported OS $(uname -s)"
            return 1
            ;;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64 | amd64) printf 'amd64\n' ;;
        arm64 | aarch64) printf 'arm64\n' ;;
        *)
            log "error: unsupported architecture $(uname -m)"
            return 1
            ;;
    esac
}

install_deb() {
    local arch deb_url tmpdir deb_path

    if [ "$(detect_os)" != "linux" ]; then
        return 1
    fi
    if ! command -v dpkg >/dev/null 2>&1 || ! command -v apt-get >/dev/null 2>&1 || ! command -v sudo >/dev/null 2>&1; then
        return 1
    fi
    if ! sudo -n true >/dev/null 2>&1; then
        log "skipping Debian package install because sudo is not available without a prompt"
        return 1
    fi

    arch="$(dpkg --print-architecture)"
    case "$arch" in
        amd64 | arm64) ;;
        *)
            log "skipping Debian package install for unsupported dpkg architecture: $arch"
            return 1
            ;;
    esac

    tmpdir="$(mktemp -d)"
    deb_path="$tmpdir/clankerval.deb"
    deb_url="https://github.com/${REPO}/releases/download/${CLANKERVAL_PINNED_TAG}/clankerval_${CLANKERVAL_PINNED_DEB_VERSION}_${arch}.deb"
    log "installing clankerval ${CLANKERVAL_PINNED_DEB_VERSION} from Debian package"
    if ! download "$deb_url" "$deb_path"; then
        rm -rf "$tmpdir"
        return 1
    fi
    if ! sudo apt-get update || ! sudo apt-get install -y "$deb_path"; then
        rm -rf "$tmpdir"
        return 1
    fi
    rm -rf "$tmpdir"
    return 0
}

install_raw_binary() {
    local os arch bindir binary_path binary_url

    os="$(detect_os)"
    arch="$(detect_arch)"
    bindir="${HOME}/.local/bin"
    binary_path="${bindir}/clankerval"
    binary_url="https://github.com/${REPO}/releases/download/${CLANKERVAL_PINNED_TAG}/clankerval-${os}-${arch}"

    log "installing clankerval ${CLANKERVAL_PINNED_VERSION} to ${bindir}"
    mkdir -p "$bindir"
    download "$binary_url" "$binary_path"
    chmod 755 "$binary_path"
    hash -r 2>/dev/null || true

    if ! verify_installed_command "clankerval" "$binary_path"; then
        log "error: add ${bindir} to PATH before using the installed runner"
        return 1
    fi

    return 0
}

if install_deb; then
    log "installed clankerval ${CLANKERVAL_PINNED_DEB_VERSION}"
    exit 0
fi

if install_raw_binary; then
    log "installed clankerval ${CLANKERVAL_PINNED_VERSION}"
    exit 0
fi

exit 1
