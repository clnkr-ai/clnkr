#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
manifest="${1:-$repo_root/scripts/core-runtime-packages.txt}"
limit="${CORE_SLOC_LIMIT:-1300}"

if [[ "$manifest" != /* ]]; then
	manifest="$repo_root/$manifest"
fi

if [[ ! -f "$manifest" ]]; then
	echo "error: core runtime manifest not found: $manifest" >&2
	exit 1
fi

mapfile -t roots < <(sed -e 's/#.*//' "$manifest" | awk 'NF')
if [[ "${#roots[@]}" -eq 0 ]]; then
	echo "error: core runtime manifest is empty: $manifest" >&2
	exit 1
fi

module_path="$(cd "$repo_root" && go list -m)"
go_list_format='{{if and .Module (eq .Module.Path "'"$module_path"'")}}{{range .GoFiles}}{{$.Dir}}/{{.}}{{"\n"}}{{end}}{{end}}'

mapfile -t files < <(
	cd "$repo_root"
	go list -deps -f "$go_list_format" "${roots[@]}" | sed '/^$/d' | sort -u
)

if [[ "${#files[@]}" -eq 0 ]]; then
	echo "error: no core runtime files found from manifest: $manifest" >&2
	exit 1
fi

sloc="$(cloc --quiet --csv "${files[@]}" | awk -F, 'END { print $5 }')"
echo "core runtime graph: $sloc / $limit SLOC"
if [[ "$sloc" -gt "$limit" ]]; then
	echo "error: core runtime graph exceeds $limit SLOC limit" >&2
	exit 1
fi
