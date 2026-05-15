#!/usr/bin/env bash

set -euo pipefail

[[ $# -eq 0 ]] || { echo "usage: $0" >&2; exit 2; }

module="$(go list -m)"
provider_actwire="$module/internal/providers/actwire"
provider_openaiwire="$module/internal/providers/openaiwire"
provider_factory="$module/internal/providerfactory"
internal_session="$module/internal/session"

ACTIVE_RULES=(
  "ARCH010 frontend-provider-construction"
  "ARCH011 frontend-session-boundary"
  "ARCH012 cli-parser-boundary"
  "ARCH013 frontend-providerconfig-boundary"
)

readonly RULE_FRONTEND_PROVIDER="ARCH010 frontend-provider-construction"
readonly RULE_FRONTEND_PROVIDER_TEXT="frontend coordinator must use internal/providerfactory instead of concrete provider adapters."
readonly RULE_FRONTEND_PROVIDER_GUIDANCE="move provider construction behind internal/providerfactory; do not import concrete provider adapters from frontend packages."
readonly RULE_FRONTEND_SESSION="ARCH011 frontend-session-boundary"
readonly RULE_FRONTEND_SESSION_TEXT="frontend adapters must use cmd/internal/clnkrapp instead of importing internal/session directly."
readonly RULE_FRONTEND_SESSION_GUIDANCE="move session persistence calls behind cmd/internal/clnkrapp; do not import internal/session from cmd/... outside cmd/internal/clnkrapp."
readonly RULE_CLI_PARSER="ARCH012 cli-parser-boundary"
readonly RULE_CLI_PARSER_TEXT="CLI option parsing must stay local and stdlib-only."
readonly RULE_CLI_PARSER_GUIDANCE="keep cmd/clnkr/cli_*.go parser-only; move app-service and config-resolution calls out of the parser."
readonly RULE_FRONTEND_PROVIDERCONFIG="ARCH013 frontend-providerconfig-boundary"
readonly RULE_FRONTEND_PROVIDERCONFIG_TEXT="frontend adapters must use cmd/internal/clnkrapp instead of importing cmd/internal/providerconfig directly."
readonly RULE_FRONTEND_PROVIDERCONFIG_GUIDANCE="move provider config and startup construction behind cmd/internal/clnkrapp; do not import cmd/internal/providerconfig from cmd/... outside cmd/internal/clnkrapp."

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

emit_file_import_violation() {
  local rule="$1" importer="$2" target="$3" trusted_rule="$4" guidance="$5"
  {
    echo "error: architecture import boundary violation"
    echo "rule: $rule"
    echo "importer: $importer"
    echo "target: $target"
    echo "import_source: file_imports"
    echo "trusted_rule: $trusted_rule"
    echo "source_fact: file import scan reported importer imports target."
    echo "guidance: $guidance"
    echo
  } >&2
}

kind_of() {
  case "$1" in
    "$module") echo root ;;
    "$module"/internal/core/*) echo core ;;
    "$internal_session") echo frontend_runtime ;;
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
        "$module"|"$module"/cmd/internal/*|"$module"/internal/providers/providerconfig) ;;
        "$internal_session") [[ "$importer" == "$module/cmd/internal/clnkrapp" ]] || { echo "error: $importer -> $target: only cmd/internal/clnkrapp may import internal/session" >&2; return 1; } ;;
        "$provider_factory") [[ "$importer" == "$module/cmd/internal/clnkrapp" ]] || { echo "error: $importer -> $target: only cmd/internal/clnkrapp may import internal/providerfactory" >&2; return 1; } ;;
        *) echo "error: $importer -> $target: cmd/... may import only root clnkr, cmd/internal/..., internal/providers/providerconfig, internal/session from cmd/internal/clnkrapp, or internal/providerfactory from clnkrapp" >&2; return 1 ;;
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

is_session_boundary_target() {
  [[ "$1" == "$internal_session" ]]
}

is_providerconfig_boundary_target() {
  [[ "$1" == "$module/cmd/internal/providerconfig" ]]
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

check_frontend_session_boundary() {
  local bad=0 edge importer target import_source
  for edge in "${import_edges[@]}"; do
    IFS=$'\t' read -r importer target import_source <<<"$edge"
    if [[ "$importer" == "$module"/cmd/* && "$importer" != "$module/cmd/internal/clnkrapp" ]] && is_session_boundary_target "$target"; then
      emit_violation "$RULE_FRONTEND_SESSION" "$importer" "$target" "$import_source" "$RULE_FRONTEND_SESSION_TEXT" "$RULE_FRONTEND_SESSION_GUIDANCE"
      bad=1
    fi
  done
  return "$bad"
}

cli_parser_imports() {
  local scanner_dir scanner status
  scanner_dir="$(mktemp -d "${TMPDIR:-/tmp}/cli-parser-imports.XXXXXX")"
  scanner="$scanner_dir/scanner.go"
  cat > "$scanner" <<'GO'
package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"strconv"
)

func main() {
	path := os.Getenv("CLI_PARSER_SCAN_FILE")
	if path == "" {
		fmt.Fprintln(os.Stderr, "CLI_PARSER_SCAN_FILE is required")
		os.Exit(2)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Println(path)
	}
}
GO
  CLI_PARSER_SCAN_FILE="$1" go run "$scanner"
  status=$?
  rm -rf "$scanner_dir"
  return "$status"
}

is_standard_import() {
  local target="$1" standard
  standard="$(go list -f '{{.Standard}}' "$target" 2>/dev/null)" || return 1
  [[ "$standard" == "true" ]]
}

check_frontend_providerconfig_boundary() {
  local bad=0 edge importer target import_source
  for edge in "${import_edges[@]}"; do
    IFS=$'	' read -r importer target import_source <<<"$edge"
    if [[ "$importer" == "$module"/cmd/* && "$importer" != "$module/cmd/internal/clnkrapp" ]] && is_providerconfig_boundary_target "$target"; then
      emit_violation "$RULE_FRONTEND_PROVIDERCONFIG" "$importer" "$target" "$import_source" "$RULE_FRONTEND_PROVIDERCONFIG_TEXT" "$RULE_FRONTEND_PROVIDERCONFIG_GUIDANCE"
      bad=1
    fi
  done
  return "$bad"
}

check_cli_parser_boundary() {
  local bad=0 file target imports_output
  for file in cmd/clnkr/cli_*.go; do
    [[ -f "$file" ]] || continue
    if ! imports_output="$(cli_parser_imports "$file")"; then
      echo "error: cannot scan imports for $file" >&2
      return 1
    fi
    while IFS= read -r target; do
      [[ -n "$target" ]] || continue
      if ! is_standard_import "$target"; then
        emit_file_import_violation "$RULE_CLI_PARSER" "$file" "$target" "$RULE_CLI_PARSER_TEXT" "$RULE_CLI_PARSER_GUIDANCE"
        bad=1
      fi
    done <<<"$imports_output"
  done
  return "$bad"
}

run_active_rule() {
  case "$1" in
    "$RULE_FRONTEND_PROVIDER") check_frontend_provider_construction ;;
    "$RULE_FRONTEND_SESSION") check_frontend_session_boundary ;;
    "$RULE_CLI_PARSER") check_cli_parser_boundary ;;
    "$RULE_FRONTEND_PROVIDERCONFIG") check_frontend_providerconfig_boundary ;;
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
