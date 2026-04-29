#!/usr/bin/env bash
set -euo pipefail
export LANG=C.UTF-8
export LC_ALL=C.UTF-8

if [[ $# -gt 1 ]]; then
  echo "usage: $0 [output.png]" >&2
  exit 2
fi

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_dir="$(cd "$script_dir/.." && pwd)"
output="${1:-$repo_dir/site/static/readme-terminal.png}"
renderer="$script_dir/render-readme-banner.sh"
font="${CLNKR_README_IMAGE_FONT:-$repo_dir/site/static/fonts/TerminessNerdFontMono-Regular.ttf}"

if ! command -v gum >/dev/null 2>&1; then
  echo "render-readme-banner-png: gum is required" >&2
  exit 1
fi

if command -v magick >/dev/null 2>&1; then
  imagemagick=(magick)
elif command -v convert >/dev/null 2>&1; then
  imagemagick=(convert)
else
  echo "render-readme-banner-png: ImageMagick is required" >&2
  exit 1
fi

if [[ ! -f "$font" ]]; then
  echo "render-readme-banner-png: font not found: $font" >&2
  exit 1
fi

esc=$'\e'
default_fg="#ff9e2c"
default_bg="#050301"
fg="$default_fg"
bg="$default_bg"
inverse=0
scale="${CLNKR_README_IMAGE_SCALE:-2}"
font_size_base="${CLNKR_README_IMAGE_FONT_SIZE:-18}"
cell_width_base="${CLNKR_README_IMAGE_CELL_WIDTH:-11}"
cell_height_base="${CLNKR_README_IMAGE_CELL_HEIGHT:-21}"
pad_x_base="${CLNKR_README_IMAGE_PAD_X:-24}"
pad_y_base="${CLNKR_README_IMAGE_PAD_Y:-24}"
font_size=$((font_size_base * scale))
cell_width=$((cell_width_base * scale))
cell_height=$((cell_height_base * scale))
pad_x=$((pad_x_base * scale))
pad_y=$((pad_y_base * scale))

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT
ansi="$tmp_dir/render.ansi"
mvg="$tmp_dir/render.mvg"
background_mvg="$tmp_dir/background.mvg"
text_mvg="$tmp_dir/text.mvg"

"$renderer" >"$ansi"

mvg_escape() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//\'/\\\'}"
  printf '%s' "$value"
}

hex_color() {
  printf '#%02x%02x%02x' "$1" "$2" "$3"
}

effective_fg() {
  if (( inverse )); then
    printf '%s' "$bg"
  else
    printf '%s' "$fg"
  fi
}

effective_bg() {
  if (( inverse )); then
    printf '%s' "$fg"
  else
    printf '%s' "$bg"
  fi
}

mvg_quote() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//\'/\\\'}"
  printf "'%s'" "$value"
}

near_black() {
  local color="$1"
  local hex r g b

  hex="${color#\#}"
  if [[ "$hex" =~ ^[0-9a-fA-F]{6}$ ]]; then
    r=$((16#${hex:0:2}))
    g=$((16#${hex:2:2}))
    b=$((16#${hex:4:2}))
    (( r <= 6 && g <= 6 && b <= 6 ))
    return
  fi

  return 1
}

normalize_dark_bg() {
  local color="$1"

  if near_black "$color"; then
    printf '%s' "$default_bg"
    return
  fi

  printf '%s' "$color"
}

reset_sgr() {
  fg="$default_fg"
  bg="$default_bg"
  inverse=0
}

apply_sgr() {
  local params="$1"
  local -a codes
  local i code mode r g b

  [[ -n "$params" ]] || params=0
  IFS=';' read -r -a codes <<<"$params"

  i=0
  while (( i < ${#codes[@]} )); do
    code="${codes[$i]}"
    [[ -n "$code" ]] || code=0
    case "$code" in
      0)
        reset_sgr
        ;;
      7)
        inverse=1
        ;;
      27)
        inverse=0
        ;;
      38|48)
        mode="${codes[$((i + 1))]:-}"
        if [[ "$mode" == "2" ]]; then
          r="${codes[$((i + 2))]:-0}"
          g="${codes[$((i + 3))]:-0}"
          b="${codes[$((i + 4))]:-0}"
          if [[ "$code" == "38" ]]; then
            fg="$(hex_color "$r" "$g" "$b")"
          else
            bg="$(hex_color "$r" "$g" "$b")"
          fi
          i=$((i + 4))
        fi
        ;;
    esac
    i=$((i + 1))
  done
}

draw_cell() {
  local row="$1"
  local col="$2"
  local ch="$3"
  local x y x2 y2 text_y cell_bg cell_fg escaped

  x=$((pad_x + col * cell_width))
  y=$((pad_y + row * cell_height))
  x2=$((x + cell_width))
  y2=$((y + cell_height))
  text_y=$((y + font_size))
  cell_bg="$(effective_bg)"
  cell_fg="$(effective_fg)"
  cell_bg="$(normalize_dark_bg "$cell_bg")"
  if [[ "$ch" != " " ]] && near_black "$cell_fg" && [[ "$cell_bg" == "$default_bg" ]]; then
    ch=" "
  fi
  printf 'fill %s\nrectangle %d,%d %d,%d\n' \
    "$(mvg_quote "$cell_bg")" "$x" "$y" "$x2" "$y2" >>"$background_mvg"
  if [[ "$ch" != " " ]]; then
    escaped="$(mvg_escape "$ch")"
    printf 'fill %s\ntext %d,%d %s\n' \
      "$(mvg_quote "$cell_fg")" "$x" "$text_y" "$(mvg_quote "$escaped")" >>"$text_mvg"
  fi
}

parse_line() {
  local line="$1"
  local row="$2"
  local col=0
  local seq params final ch

  reset_sgr
  while [[ -n "$line" ]]; do
    if [[ "$line" =~ ^${esc}\[([0-9\;\?]*)([[:alpha:]]) ]]; then
      seq="${BASH_REMATCH[0]}"
      params="${BASH_REMATCH[1]}"
      final="${BASH_REMATCH[2]}"
      line="${line:${#seq}}"
      if [[ "$final" == "m" ]]; then
        apply_sgr "$params"
      fi
      continue
    fi

    ch="${line:0:1}"
    line="${line:1}"
    draw_cell "$row" "$col" "$ch"
    col=$((col + 1))
  done
}

line_count="$(wc -l <"$ansi" | tr -d ' ')"
width=$((pad_x * 2 + 61 * cell_width))
height=$((pad_y * 2 + line_count * cell_height))

{
  printf 'viewbox 0 0 %d %d\n' "$width" "$height"
  printf 'fill %s\nrectangle 0,0 %d,%d\n' "$(mvg_quote "$default_bg")" "$width" "$height"
  printf 'font %s\nfont-size %d\n' "$(mvg_quote "$font")" "$font_size"
} >"$mvg"
: >"$background_mvg"
: >"$text_mvg"

row=0
while IFS= read -r line || [[ -n "$line" ]]; do
  parse_line "$line" "$row"
  row=$((row + 1))
done <"$ansi"

cat "$background_mvg" "$text_mvg" >>"$mvg"

mkdir -p "$(dirname "$output")"
"${imagemagick[@]}" "mvg:$mvg" "$output"
printf 'rendered %s\n' "$output"
