#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: $0" >&2
}

if [[ "$#" -ne 0 ]]; then
  usage
  exit 1
fi

readonly repo_root="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
readonly limit="${CORE_SLOC_LIMIT:-1300}"

module_path="$(cd "$repo_root" && go list -m)"
readonly module_path
go_list_format='{{if and .Module (eq .Module.Path "'"$module_path"'")}}{{range .GoFiles}}{{$.Dir}}/{{.}}{{"\n"}}{{end}}{{end}}'

mapfile -t files < <(
  cd "$repo_root"
  go list -deps -f "$go_list_format" "$module_path" | sed '/^$/d' | sort -u
)

if [[ "${#files[@]}" -eq 0 ]]; then
  echo "error: no core runtime files found from root package: $module_path" >&2
  exit 1
fi

sloc="$(cloc --quiet --csv "${files[@]}" | awk -F, 'END { print $5 }')"
echo "core runtime graph: $sloc / $limit SLOC"
if [[ "$sloc" -gt "$limit" ]]; then
  echo "error: core runtime graph exceeds $limit SLOC limit" >&2
  exit 1
fi
