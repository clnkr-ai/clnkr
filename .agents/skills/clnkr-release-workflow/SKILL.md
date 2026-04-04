---
name: clnkr-release-workflow
description: |
  Cuts, watches, and verifies clnkr releases using git tags, GitHub Actions, and
  Debian artifacts. Use when: (1) choosing the next semver tag, (2) publishing a
  release from main, (3) verifying or installing the resulting package.
author: Claude Code
version: 1.0.0
date: 2026-04-01
---

# clnkr Release Workflow

## When to Use

- Preparing the next release tag for this repo
- Determining the current released version from repository state
- Monitoring the GitHub Actions release workflow after pushing a tag
- Installing or verifying the produced Debian package

## When NOT to Use

- Making unreleased local builds only
- Guessing version numbers from `main.version = "dev"`
- Treating generated `CHANGELOG.md` edits as the source of truth for release state

## Problem

This repo does not store a single checked-in release constant. The authoritative released version comes from git tags, while binaries inject the version at build time. It is easy to misread generated files or install the wrong Debian artifact for the host architecture.

## Solution

### Step 1: Determine the current release from git tags

Use the highest semver release tag as the current release source of truth.

- Do not infer the current release from `cmd/clnkr/main.go` or `cmd/clnku/main.go`; those default to `"dev"` and are overwritten at build time.
- Use git tags and the diff since the latest tag to decide the next version.

### Step 2: Choose semver from the actual diff

In this repo:

- patch = backward-compatible fixes and small behavior corrections
- minor = backward-compatible user-facing features (new workflow commands, new frontend capabilities, additive behavior)
- major = incompatible CLI/core/protocol break

A TUI-only additive feature like `/delegate` is a **minor** bump, even if `clnku` remains unchanged.

### Step 3: Tag and push in order

- create an annotated tag
- push `main`
- push the tag

That matches the current release convention.

### Step 4: Watch the release workflow, not just the tag push

The release is not ready when the tag exists. The GitHub Actions `Release` workflow still has to complete.

Use `gh run list` / `gh run view` / `gh release view` to distinguish:

- tag exists, release workflow still running
- binaries and Debian jobs done, final release job still pending
- GitHub release published with assets

### Step 5: Install the Debian package matching host architecture

Check host architecture before choosing the `.deb`.

Example failure mode:

- installing `clnkr_..._amd64.deb` on an `arm64` host fails with
  `package architecture (amd64) does not match system (arm64)`

On this host, `uname -m` returned `aarch64`, so the correct package was the `arm64` Debian artifact.

### Step 6: Verify installed binaries explicitly

After install, verify both:

```bash
clnkr --version
clnku --version
```

## Verification

1. Confirm the next version from tags + diff since latest tag.
2. Confirm the release workflow finishes successfully.
3. Confirm the GitHub release contains the expected assets.
4. Install the host-matching Debian package.
5. Verify `clnkr` and `clnku` report the expected version.

## References

- `Makefile`
- `cmd/clnkr/main.go`
- `cmd/clnku/main.go`
- `.github/workflows/release.yml`
