#!/usr/bin/env bash
set -euo pipefail
export LANG=C.UTF-8
export LC_ALL=C.UTF-8

if ! command -v gum >/dev/null 2>&1; then
  echo "render-readme-banner: gum is required" >&2
  exit 1
fi

bg="#050301"
ink="#ff9e2c"
width=61
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
mascot_path="$script_dir/assets/mascot.txt"
inside=59
frame=$'\e[38;2;255;158;44;48;2;5;3;1m'
reset=$'\e[0m'

strip_ansi() {
  sed -E $'s/\x1B\\[[0-9;?]*[ -\\/]*[@-~]//g'
}

visible_width() {
  local plain
  plain="$(printf '%s' "$1" | strip_ansi)"
  printf '%s' "${#plain}"
}

emit_frame_line() {
  printf '%s%s%s\n' "$frame" "$1" "$reset"
}

emit_mascot_line() {
  local line="$1"
  local width left right

  line="${line//$'\e[?25l'/}"
  line="${line//$'\e[?25h'/}"
  width="$(visible_width "$line")"
  if (( width > inside )); then
    printf 'render-readme-banner: %s is %d columns, max %d\n' "$mascot_path" "$width" "$inside" >&2
    exit 1
  fi

  left=$(((inside - width) / 2))
  right=$((inside - width - left))
  printf '%s│%*s%s%s%*s│%s\n' "$frame" "$left" "" "$line" "$frame" "$right" "" "$reset"
}

top="$(
  gum style \
    --foreground "$bg" \
    --background "$ink" \
    --bold \
    --padding "0 1" \
    --width "$width" \
    ""
)"

main="$(
  if [[ ! -f "$mascot_path" ]]; then
    printf 'render-readme-banner: %s not found\n' "$mascot_path" >&2
    exit 1
  fi

  emit_frame_line "┌───────────────────────────────────────────────────────────┐"
  emit_frame_line "│ NAME                                             clnkr(1) │"
  emit_frame_line "├───────────────────────────────────────────────────────────┤"
  emit_frame_line "│                                                           │"
  while IFS= read -r line || [[ -n "$line" ]]; do
    emit_mascot_line "$line"
  done <"$mascot_path"
  emit_frame_line "│                                                           │"
  emit_frame_line "└───────────────────────────────────────────────────────────┘"
)"

bottom="$(
  gum style \
    --foreground "$bg" \
    --background "$ink" \
    --padding "0 1" \
    --width "$width" \
    ""
)"

gum join --vertical "$top" "$main" "$bottom"
