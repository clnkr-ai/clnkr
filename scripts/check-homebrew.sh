#!/usr/bin/env bash
set -euo pipefail

latest_version() {
  if [ -n "${CLNKR_HOMEBREW_EXPECTED_VERSION:-}" ]; then
    printf '%s\n' "${CLNKR_HOMEBREW_EXPECTED_VERSION}"
    return 0
  fi

  curl -fsSL "https://api.github.com/repos/clnkr-ai/clnkr/releases/latest" \
    | jq -r '.tag_name'
}

install_clnkr() {
  brew tap clnkr-ai/tap
  brew reinstall clnkr-ai/tap/clnkr
}

verify_clnkr() {
  local prefix
  local version
  prefix="$(brew --prefix clnkr)"
  version="$(latest_version)"
  version="${version#v}"
  if [ -z "${version}" ]; then
    echo "error: failed to determine latest clnkr release version" >&2
    exit 1
  fi

  "${prefix}/bin/clnkr" --version | grep -F "${version}"
  "${prefix}/bin/clnku" --version | grep -F "${version}"
}

usage() {
  echo "usage: $0 {install|verify}" >&2
}

main() {
  if [ "$#" -ne 1 ]; then
    usage
    exit 1
  fi

  case "$1" in
    install)
      install_clnkr
      ;;
    verify)
      verify_clnkr
      ;;
    *)
      usage
      exit 1
      ;;
  esac
}

main "$@"
