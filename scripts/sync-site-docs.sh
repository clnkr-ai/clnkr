#!/usr/bin/env bash
set -euo pipefail

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
content_dir="$repo_root/site/content/docs"

mkdir -p "$content_dir"

for src in "$repo_root"/doc/*.1.md; do
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
  esac

  dest="$content_dir/$base.md"

  {
    printf -- "+++\n"
    printf -- 'title = "%s"\n' "$title"
    printf -- 'description = "%s"\n' "$description"
    printf -- 'slug = "%s"\n' "$base"
    printf -- "weight = 10\n"
    printf -- "+++\n\n"
    printf -- "> Generated from [%s](https://github.com/clnkr-ai/clnkr/blob/main/doc/%s.1.md).\n\n" "$base.1.md" "$base"
    tail -n +4 "$src"
  } >"$dest"
done
