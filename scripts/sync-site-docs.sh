#!/usr/bin/env bash
set -euo pipefail

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
content_dir="$repo_root/site/content/docs"
clankerval_repo_url="${CLANKERVAL_DOCS_REPO_URL:-https://github.com/clnkr-ai/clankerval.git}"
clankerval_tmpdir=""

mkdir -p "$content_dir"

cleanup() {
  if [ -n "$clankerval_tmpdir" ] && [ -d "$clankerval_tmpdir" ]; then
    rm -rf "$clankerval_tmpdir"
  fi
}

render_page() {
  src="$1"
  source_repo="$2"
  weight="$3"
  base="$(basename "$src" .1.md)"
  title="$base"
  description="$base manual page"

  case "$base" in
    clnkr)
      title="clnkr"
      description="Terminal UI manual page"
      ;;
    clnku)
      title="clnku"
      description="Plain CLI manual page"
      ;;
    clankerval)
      title="clankerval"
      description="Evaluation runner manual page"
      ;;
  esac

  dest="$content_dir/$base.md"

  {
    printf -- "+++\n"
    printf -- 'title = "%s"\n' "$title"
    printf -- 'description = "%s"\n' "$description"
    printf -- 'slug = "%s"\n' "$base"
    printf -- "weight = %s\n" "$weight"
    printf -- "+++\n\n"
    printf -- "> Generated from [%s](https://github.com/clnkr-ai/%s/blob/main/doc/%s.1.md).\n\n" "$base.1.md" "$source_repo" "$base"
    tail -n +4 "$src"
  } >"$dest"
}

trap cleanup EXIT

for src in "$repo_root"/doc/*.1.md; do
  render_page "$src" "clnkr" "10"
done

clankerval_tmpdir="$(mktemp -d)"
git clone --depth 1 "$clankerval_repo_url" "$clankerval_tmpdir"

clankerval_src="$clankerval_tmpdir/doc/clankerval.1.md"
if [ ! -f "$clankerval_src" ]; then
  echo "error: missing clankerval manpage at $clankerval_src" >&2
  exit 1
fi

render_page "$clankerval_src" "clankerval" "20"
