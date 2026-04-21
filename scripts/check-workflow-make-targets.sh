#!/usr/bin/env bash

set -euo pipefail

[[ $# -eq 0 ]] || { echo "usage: $0" >&2; exit 2; }

mapfile -t targets < <(
  make -qp |
    sed -n 's/^\([A-Za-z0-9][A-Za-z0-9_-]*\):\([^=]\|$\).*/\1/p' |
    sort -u
)

bad=0
while IFS= read -r hit; do
  target="${hit##*make }"
  target="${target%%[^A-Za-z0-9_-]*}"
  [[ -n "$target" ]] || continue

  if [[ ! " ${targets[*]} " =~ [[:space:]]${target}[[:space:]] ]]; then
    if (( bad == 0 )); then
      echo "workflow make target check failed:" >&2
    fi
    echo "  unknown make target '${target}' in ${hit%%:*}" >&2
    bad=1
  fi
done < <(grep -RHEo 'make[[:space:]]+[A-Za-z0-9][A-Za-z0-9_-]*' .github/workflows/*.y*ml || true)

(( bad == 0 )) || exit 1
echo "workflow make target check passed"
