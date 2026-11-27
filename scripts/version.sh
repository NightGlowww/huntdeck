#!/usr/bin/env bash
# Derives a strict SemVer 2.0.0 version string from Git tags (vMAJOR.MINOR.PATCH).
# - On an exact tag: v1.4.0
# - Ahead of a tag: v1.4.1-dev.<N>+<7-char-sha> (sorts after v1.4.0, before v1.4.1)
# - No tags in repo: v0.0.0-dev+<7-char-sha> (or "dev" outside a git repository)
set -euo pipefail

if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo "dev"
  exit 0
fi

if exact=$(git describe --tags --exact-match --match 'v[0-9]*' 2>/dev/null); then
  echo "$exact"
  exit 0
fi

short=$(git rev-parse --short=7 HEAD 2>/dev/null || echo "0000000")

if desc=$(git describe --tags --long --match 'v[0-9]*' 2>/dev/null); then
  # Format: <tag>-<N>-g<sha>
  no_sha="${desc%-g*}"
  dist="${no_sha##*-}"
  tag="${no_sha%-*}"
  core="${tag#v}"
  core="${core%%[-+]*}"
  IFS='.' read -r major minor patch <<< "$core"
  major="${major:-0}"
  minor="${minor:-0}"
  patch="${patch:-0}"
  next_patch=$((patch + 1))
  echo "v${major}.${minor}.${next_patch}-dev.${dist}+${short}"
  exit 0
fi

echo "v0.0.0-dev+${short}"
