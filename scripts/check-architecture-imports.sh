#!/usr/bin/env bash

set -euo pipefail

[[ $# -eq 0 ]] || { echo "usage: $0" >&2; exit 2; }

module="$(go list -m)"
provider_actwire="$module/internal/providers/actwire"
provider_openaiwire="$module/internal/providers/openaiwire"
provider_factory="$module/internal/providerfactory"

ACTIVE_RULES=(
  "ARCH010 frontend-provider-construction"
)

readonly RULE_FRONTEND_PROVIDER="ARCH010 frontend-provider-construction"
readonly RULE_FRONTEND_PROVIDER_TEXT="frontend coordinator must use internal/providerfactory instead of concrete provider adapters."
readonly RULE_FRONTEND_PROVIDER_GUIDANCE="move provider construction behind internal/providerfactory; do not import concrete provider adapters from frontend packages."

emit_violation() {
  local rule="$1" importer="$2" target="$3" import_source="$4" trusted_rule="$5" guidance="$6"
  {
    echo "error: architecture import boundary violation"
    echo "rule: $rule"
    echo "importer: $importer"
    echo "target: $target"
    echo "import_source: $import_source"
    echo "trusted_rule: $trusted_rule"
    echo "source_fact: go list reported importer imports target."
    echo "guidance: $guidance"
    echo
  } >&2
}

kind_of() {
  case "$1" in
    "$module") echo root ;;
    "$module"/internal/core/*) echo core ;;
    "$module"/internal/session) echo frontend_runtime ;;
    "$provider_factory") echo provider_factory ;;
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
    provider_factory)
      case "$target" in
        "$module"|"$module"/internal/providers/anthropic|"$module"/internal/providers/openai|"$module"/internal/providers/openairesponses|"$module"/internal/providers/providerconfig) ;;
        *) echo "error: $importer -> $target: internal/providerfactory may import only root clnkr, concrete provider adapters, and provider request semantics" >&2; return 1 ;;
      esac
      ;;
    compaction) [[ "$target" == "$module" ]] || { echo "error: $importer -> $target: cmd/internal/compaction should keep repo-local imports to root clnkr only" >&2; return 1; } ;;
    cmd)
      case "$target" in
        "$module"|"$module"/cmd/internal/*|"$module"/internal/session|"$module"/internal/providers/providerconfig) ;;
        "$provider_factory") [[ "$importer" == "$module/cmd/internal/clnkrapp" ]] || { echo "error: $importer -> $target: only cmd/internal/clnkrapp may import internal/providerfactory" >&2; return 1; } ;;
        *) echo "error: $importer -> $target: cmd/... may import only root clnkr, cmd/internal/..., frontend runtime packages, internal/providers/providerconfig, or internal/providerfactory from clnkrapp" >&2; return 1 ;;
      esac
      ;;
    other) echo "error: $importer -> $target: unclassified repo-local importer" >&2; return 1 ;;
  esac
}

is_frontend_coordinator() {
  [[ "$1" == "$module/cmd/internal/clnkrapp" ]]
}

is_concrete_provider_adapter() {
  case "$1" in
    "$module/internal/providers/anthropic"|"$module/internal/providers/openai"|"$module/internal/providers/openairesponses") return 0 ;;
    *) return 1 ;;
  esac
}

check_frontend_provider_construction() {
  local bad=0 edge importer target import_source
  for edge in "${import_edges[@]}"; do
    IFS=$'\t' read -r importer target import_source <<<"$edge"
    if is_frontend_coordinator "$importer" && is_concrete_provider_adapter "$target"; then
      emit_violation "$RULE_FRONTEND_PROVIDER" "$importer" "$target" "$import_source" "$RULE_FRONTEND_PROVIDER_TEXT" "$RULE_FRONTEND_PROVIDER_GUIDANCE"
      bad=1
    fi
  done
  return "$bad"
}

run_active_rule() {
  case "$1" in
    "$RULE_FRONTEND_PROVIDER") check_frontend_provider_construction ;;
    *) echo "error: unknown architecture rule: $1" >&2; exit 2 ;;
  esac
}

import_edges=()
while IFS=$'\t' read -r importer target import_source; do
  [[ -n "$importer" && "$target" == "$module"* && "$target" != "$importer" ]] || continue
  import_edges+=("$importer"$'\t'"$target"$'\t'"$import_source")
done < <(go list -json ./... | jq -r '. as $pkg | ($pkg.ImportPath // empty) as $importer |
  [($pkg.Imports // [] | .[]? | [$importer, ., "imports"]),
   ($pkg.TestImports // [] | .[]? | [$importer, ., "test_imports"]),
   ($pkg.XTestImports // [] | .[]? | [$importer, ., "external_test_imports"])] |
  .[] | @tsv')

bad=0
edge_count=0
for edge in "${import_edges[@]}"; do
  IFS=$'\t' read -r importer target import_source <<<"$edge"
  edge_count=$((edge_count + 1))
  check_edge "$importer" "$target" || bad=1
done

for rule in "${ACTIVE_RULES[@]}"; do
  run_active_rule "$rule" || bad=1
done

(( bad == 0 )) || exit 1
package_count="$(go list ./... | sort -u | wc -l | tr -d '[:space:]')"
echo "target architecture import checks passed (${package_count} packages, ${edge_count} repo-local edges)"
