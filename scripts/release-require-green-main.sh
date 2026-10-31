#!/usr/bin/env bash

set -euo pipefail

[[ $# -eq 0 ]] || { echo "usage: $0" >&2; exit 2; }

repo="${GITHUB_REPOSITORY:-}"
if [[ -z "$repo" ]]; then
  echo "error: GITHUB_REPOSITORY is required" >&2
  exit 2
fi

version="${GITHUB_REF_NAME:-}"
release_sha="${GITHUB_SHA:-}"
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ || -z "$release_sha" ]]; then
  echo "error: GITHUB_REF_NAME must be a plain version tag and GITHUB_SHA its commit" >&2
  exit 2
fi

changelog="$(git show "${release_sha}:CHANGELOG.md")"
heading="$(awk '/^## / { print; exit }' <<<"$changelog")"
heading_pattern="^## ${version//./\\.} \([0-9]{4}-[0-9]{2}-[0-9]{2}\)$"
if [[ ! "$heading" =~ $heading_pattern ]]; then
  echo "error: tagged CHANGELOG.md must start with ## ${version} (YYYY-MM-DD)" >&2
  exit 1
fi
notes="$(awk '/^## / { if (seen++) exit; next } seen && NF { print }' <<<"$changelog")"
if [[ -z "$notes" ]]; then
  echo "error: tagged CHANGELOG.md needs release notes for ${version}" >&2
  exit 1
fi

main_json="$(gh api "/repos/${repo}/git/ref/heads/main")"
main_sha="$(jq -r '.object.sha // empty' <<<"$main_json")"
if [[ -z "$main_sha" ]]; then
  echo "error: could not resolve heads/main" >&2
  exit 1
fi

if [[ "$release_sha" != "$main_sha" ]]; then
  echo "error: tagged commit ${release_sha} must match main snapshot ${main_sha}" >&2
  exit 1
fi

for workflow in ci.yml site.yml; do
  run_json="$(
    gh api "/repos/${repo}/actions/workflows/${workflow}/runs?branch=main&event=push&head_sha=${main_sha}&per_page=1"
  )"
  count="$(jq '.workflow_runs | length' <<<"$run_json")"
  if [[ "$count" -eq 0 ]]; then
    echo "error: no ${workflow} runs found for main" >&2
    exit 1
  fi

  status="$(jq -r '.workflow_runs[0].status' <<<"$run_json")"
  conclusion="$(jq -r '.workflow_runs[0].conclusion' <<<"$run_json")"
  head_sha="$(jq -r '.workflow_runs[0].head_sha // empty' <<<"$run_json")"
  url="$(jq -r '.workflow_runs[0].html_url' <<<"$run_json")"

  echo "main ${workflow}: ${head_sha} ${status}/${conclusion}"
  echo "$url"

  if [[ "$head_sha" != "$main_sha" ]]; then
    echo "error: ${workflow} run must match main snapshot ${main_sha}" >&2
    exit 1
  fi

  if [[ "$status" != "completed" || "$conclusion" != "success" ]]; then
    echo "error: ${workflow} run for main snapshot must be completed/success before release" >&2
    exit 1
  fi
done
