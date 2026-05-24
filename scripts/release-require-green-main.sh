#!/usr/bin/env bash

set -euo pipefail

[[ $# -eq 0 ]] || { echo "usage: $0" >&2; exit 2; }

repo="${GITHUB_REPOSITORY:-}"
if [[ -z "$repo" ]]; then
  echo "error: GITHUB_REPOSITORY is required" >&2
  exit 2
fi

for workflow in ci.yml site.yml; do
  run_json="$(
    gh api "/repos/${repo}/actions/workflows/${workflow}/runs?branch=main&event=push&per_page=1"
  )"
  count="$(jq '.workflow_runs | length' <<<"$run_json")"
  if [[ "$count" -eq 0 ]]; then
    echo "error: no ${workflow} runs found for main" >&2
    exit 1
  fi

  status="$(jq -r '.workflow_runs[0].status' <<<"$run_json")"
  conclusion="$(jq -r '.workflow_runs[0].conclusion' <<<"$run_json")"
  head_sha="$(jq -r '.workflow_runs[0].head_sha' <<<"$run_json")"
  url="$(jq -r '.workflow_runs[0].html_url' <<<"$run_json")"

  echo "latest main ${workflow}: ${head_sha} ${status}/${conclusion}"
  echo "$url"

  if [[ "$status" != "completed" || "$conclusion" != "success" ]]; then
    echo "error: latest ${workflow} run for main must be completed/success before release" >&2
    exit 1
  fi
done
