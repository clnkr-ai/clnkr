#!/usr/bin/env bash
set -euo pipefail
export LANG=C.UTF-8
export LC_ALL=C.UTF-8
script_name="run-clnkr"

usage() {
  cat <<'USAGE'
Usage:
  scripts/run-clnkr.sh [--clnkr-bin PATH] [--event-log PATH] [-- CLNKR_ARGS...]

Human-only clnkr wrapper.

Runs existing clnkr unchanged in the foreground with --event-log, then renders
the captured JSONL event timeline with gum and jq after clnkr exits.
USAGE
}

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf '%s: %s is required\n' "$script_name" "$1" >&2
    exit 127
  fi
}

clnkr_bin=""
event_log=""
run_cwd="${CLNKR_RUN_CWD:-}"
clnkr_args=()

while (($#)); do
  case "$1" in
  -h|--help)
    usage
    exit 0
    ;;
  --clnkr-bin)
    (($# >= 2)) || { printf '%s: --clnkr-bin requires a path\n' "$script_name" >&2; exit 2; }
    clnkr_bin=$2
    shift 2
    ;;
  --event-log)
    (($# >= 2)) || { printf '%s: --event-log requires a path\n' "$script_name" >&2; exit 2; }
    event_log=$2
    shift 2
    ;;
  --)
    shift
    clnkr_args=("$@")
    break
    ;;
  *)
    clnkr_args+=("$1")
    shift
    ;;
  esac
done

require gum
require jq

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_dir="$(cd "$script_dir/.." && pwd)"
bg="#050301"
ink="#ff9e2c"
width=61
inside=59
frame=$'\e[38;2;255;158;44;48;2;5;3;1m'
reset=$'\e[0m'

if [[ -z "$clnkr_bin" ]]; then
  if [[ -f "$repo_dir/clnkr" && -x "$repo_dir/clnkr" ]]; then
    clnkr_bin=$repo_dir/clnkr
  elif [[ -f ./clnkr && -x ./clnkr ]]; then
    clnkr_bin=./clnkr
  elif [[ -f ./bin/clnkr && -x ./bin/clnkr ]]; then
    clnkr_bin=./bin/clnkr
  else
    clnkr_bin=$(command -v clnkr || true)
  fi
fi

if [[ -z "$clnkr_bin" || ! -f "$clnkr_bin" || ! -x "$clnkr_bin" ]]; then
  printf '%s: cannot find executable clnkr; pass --clnkr-bin PATH\n' "$script_name" >&2
  exit 127
fi

if [[ -n "$run_cwd" && ! -d "$run_cwd" ]]; then
  printf '%s: CLNKR_RUN_CWD is not a directory: %s\n' "$script_name" "$run_cwd" >&2
  exit 2
fi

for arg in "${clnkr_args[@]}"; do
  case "$arg" in
  --event-log|--event-log=*)
    printf '%s: this wrapper owns --event-log; pass --event-log to the wrapper instead\n' "$script_name" >&2
    exit 2
    ;;
  esac
done

if [[ -z "$event_log" ]]; then
  event_log=$(mktemp "${TMPDIR:-/tmp}/clnkr.XXXXXX.jsonl")
else
  mkdir -p "$(dirname "$event_log")"
  : >"$event_log"
fi
event_log="$(cd "$(dirname "$event_log")" && pwd)/$(basename "$event_log")"

strip_ansi() {
  sed -E $'s/\x1B\\[[0-9;?]*[ -\\/]*[@-~]//g'
}

visible_width() {
  local plain
  plain="$(printf '%s' "$1" | strip_ansi)"
  printf '%s' "${#plain}"
}

fit_text() {
  local text="$1"
  local max="$2"
  if (( ${#text} <= max )); then
    printf '%s' "$text"
  elif (( max > 1 )); then
    printf '%s~' "${text:0:$((max - 1))}"
  fi
}

emit_frame_line() {
  printf '%s%s%s\n' "$frame" "$1" "$reset"
}

emit_field_line() {
  local label="$1"
  local value="$2"
  local label_width=11
  local value_width=$((inside - label_width - 4))
  local fitted line line_width pad

  fitted="$(fit_text "$value" "$value_width")"
  printf -v line "│ %-*s  %-*s │" "$label_width" "$label" "$value_width" "$fitted"
  line_width="$(visible_width "$line")"
  pad=$((width - line_width))
  if (( pad > 0 )); then
    line="${line:0:${#line}-1}$(printf '%*s' "$pad" "")│"
  fi
  emit_frame_line "$line"
}

emit_bar() {
  gum style \
    --foreground "$bg" \
    --background "$ink" \
    --bold \
    --padding "0 1" \
    --width "$width" \
    "$1"
}

title() { gum style --foreground "$ink" --bold "$1"; }
muted() { gum style --foreground 245 "$1"; }
danger() { gum style --foreground 196 --bold "$1"; }

show_splash() {
  if [[ ! -t 1 ]]; then
    return
  fi

  "$script_dir/render-readme-banner.sh"
  emit_bar "press any key"

  if [[ -t 0 ]]; then
    IFS= read -r -s -n 1 _ || true
    clear
  fi
}

render_run_header() {
  emit_bar "clnkr"
  emit_frame_line "┌───────────────────────────────────────────────────────────┐"
  emit_field_line "BINARY" "$clnkr_bin"
  emit_field_line "EVENT LOG" "$event_log"
  [[ -n "$run_cwd" ]] && emit_field_line "CWD" "$run_cwd"
  emit_frame_line "└───────────────────────────────────────────────────────────┘"
}

render_debug() {
  local msg version provider api model protocol
  msg=$(jq -r '.payload.message // ""' <<<"$1")
  if jq -e . >/dev/null 2>&1 <<<"$msg"; then
    version=$(jq -r '.clnkr_version // "unknown"' <<<"$msg")
    provider=$(jq -r '.provider // "unknown"' <<<"$msg")
    api=$(jq -r '.provider_api // "unknown"' <<<"$msg")
    model=$(jq -r '.model // "unknown"' <<<"$msg")
    protocol=$(jq -r '.act_protocol // "unknown"' <<<"$msg")
    title "RUN"
    printf '  %-10s %s\n' "clnkr" "$version"
    printf '  %-10s %s / %s\n' "provider" "$provider" "$api"
    printf '  %-10s %s\n' "model" "$model"
    printf '  %-10s %s\n' "protocol" "$protocol"
  else
    muted "  debug $msg"
  fi
}

render_response() {
  local line=$1 type reasoning usage
  type=$(jq -r '.payload.turn.type // "response"' <<<"$line")
  reasoning=$(jq -r '.payload.turn.reasoning // empty' <<<"$line")
  usage=$(jq -r '.payload.usage | "tokens in \(.input_tokens), out \(.output_tokens)"' <<<"$line")
  case "$type" in
  act)
    title "ACT"
    [[ -n "$reasoning" ]] && muted "  $reasoning"
    jq -r '.payload.turn.bash.commands[]? | "  $ " + .command + (if .workdir then "  [" + .workdir + "]" else "" end)' <<<"$line"
    muted "  $usage"
    ;;
  clarify)
    title "CLARIFY"
    jq -r '.payload.turn.question // ""' <<<"$line" | sed 's/^/  /'
    ;;
  done)
    title "DONE"
    jq -r '.payload.turn.summary // ""' <<<"$line" | sed 's/^/  /'
    ;;
  *)
    title "RESPONSE"
    jq -c '.payload.turn' <<<"$line" | sed 's/^/  /'
    ;;
  esac
}

render_done() {
  local line=$1 exit_code stdout stderr err
  exit_code=$(jq -r '.payload.exit_code // 0' <<<"$line")
  stdout=$(jq -r '.payload.stdout // ""' <<<"$line")
  stderr=$(jq -r '.payload.stderr // ""' <<<"$line")
  err=$(jq -r '.payload.err // empty' <<<"$line")
  title "COMMAND exit $exit_code"
  muted "  stdout ${#stdout} bytes, stderr ${#stderr} bytes"
  [[ -n "$err" ]] && danger "  $err"
  if [[ "${CLNKR_FULL_OUTPUT:-0}" == "1" ]]; then
    [[ -n "$stdout" ]] && printf '%s\n' "$stdout"
    [[ -n "$stderr" ]] && gum style --foreground 208 "$stderr"
  fi
}

render_line() {
  local line=$1 type
  if ! type=$(jq -r '.type // "unknown"' <<<"$line" 2>/dev/null); then
    muted "$line"
    return
  fi
  case "$type" in
  debug) render_debug "$line" ;;
  response) render_response "$line" ;;
  command_start)
    title "COMMAND"
    jq -r '.payload.command' <<<"$line" | sed 's/^/  $ /'
    jq -r '.payload.dir // empty' <<<"$line" | sed '/^$/d;s/^/  dir: /'
    ;;
  command_done) render_done "$line" ;;
  protocol_failure)
    danger "PROTOCOL FAILURE"
    jq -r '.payload.reason // "unknown"' <<<"$line" | sed 's/^/  reason: /'
    ;;
  *) muted "$line" ;;
  esac
  printf '\n'
}

render_event_log() {
  emit_bar "event timeline"
  emit_frame_line "┌───────────────────────────────────────────────────────────┐"
  emit_field_line "SOURCE" "$event_log"
  emit_field_line "OUTPUT" "CLNKR_FULL_OUTPUT=1 expands command payloads"
  emit_frame_line "└───────────────────────────────────────────────────────────┘"

  if [[ ! -s "$event_log" ]]; then
    muted "  no events recorded"
    return
  fi

  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ -n "$line" ]] && render_line "$line"
  done <"$event_log"
}

show_splash
render_run_header

status=0
if [[ -n "$run_cwd" ]]; then
  cd "$run_cwd"
fi
"$clnkr_bin" "${clnkr_args[@]}" --event-log "$event_log" || status=$?

printf '\n'
render_event_log
printf '\n'
emit_bar "clnkr exited with status $status"
muted "  event log kept at $event_log"
exit "$status"
