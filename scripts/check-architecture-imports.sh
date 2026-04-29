#!/usr/bin/env bash

set -euo pipefail

[[ $# -eq 0 ]] || { echo "usage: $0" >&2; exit 2; }

module="$(go list -m)"
provider_actwire="$module/internal/providers/actwire"
provider_openaiwire="$module/internal/providers/openaiwire"

kind_of() {
  case "$1" in
    "$module") echo root ;;
    "$module"/internal/core/*) echo core ;;
    "$module"/internal/providers/*) echo provider ;;
    "$module"/cmd/internal/compaction) echo compaction ;;
    "$module"/cmd/*) echo cmd ;;
    *) echo other ;;
  esac
}

check_edge() {
  local importer="$1" target="$2" kind
  kind="$(kind_of "$importer")"
  case "$target" in "$module"/cmd/internal/*) case "$importer" in "$module"/cmd/*) ;; *) echo "error: $importer -> $target: only cmd/... may import cmd/internal/..." >&2; return 1 ;; esac ;; esac
  case "$target" in "$provider_openaiwire") case "$importer" in "$module"/internal/providers/openai|"$module"/internal/providers/openairesponses) ;; *) echo "error: $importer -> $target: only OpenAI provider packages may import internal/providers/openaiwire" >&2; return 1 ;; esac ;; esac
  case "$kind" in
    root) case "$target" in "$module"/internal/core/*) ;; *) echo "error: $importer -> $target: root may import only internal/core/..." >&2; return 1 ;; esac ;;
    core) case "$target" in "$module"/internal/core/*) ;; *) echo "error: $importer -> $target: internal/core/... may import only internal/core/..." >&2; return 1 ;; esac ;;
    provider)
      case "$target" in
        "$module"|"$module"/internal/core/*|"$provider_actwire") ;;
        "$provider_openaiwire") ;;
        *) echo "error: $importer -> $target: internal/providers/... may import only root clnkr, internal/core/..., actwire, or allowed OpenAI wire helpers..." >&2; return 1 ;;
      esac
      ;;
    compaction) [[ "$target" == "$module" ]] || { echo "error: $importer -> $target: cmd/internal/compaction should keep repo-local imports to root clnkr only" >&2; return 1; } ;;
    cmd) case "$target" in "$module"|"$module"/cmd/internal/*|"$module"/internal/providers/*) ;; *) echo "error: $importer -> $target: cmd/... may import only root clnkr, cmd/internal/..., or internal/providers/..." >&2; return 1 ;; esac ;;
    other) echo "error: $importer -> $target: unclassified repo-local importer" >&2; return 1 ;;
  esac
}

bad=0
edge_count=0
while IFS=$'\t' read -r importer target; do
  [[ -n "$importer" && "$target" == "$module"* && "$target" != "$importer" ]] || continue
  edge_count=$((edge_count + 1))
  check_edge "$importer" "$target" || bad=1
done < <(go list -json ./... | jq -r '. as $pkg | ($pkg.ImportPath // empty) as $importer | [($pkg.Imports // []), ($pkg.TestImports // []), ($pkg.XTestImports // [])] | add | unique | .[] | [$importer, .] | @tsv')

(( bad == 0 )) || exit 1
package_count="$(go list ./... | sort -u | wc -l | tr -d '[:space:]')"
echo "architecture import checks passed (${package_count} packages, ${edge_count} repo-local edges)"
