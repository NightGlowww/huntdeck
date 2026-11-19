#!/usr/bin/env bash
# Writes one performance record: the pages' latencies, the probes on a
# database copy, and what produced them. Compare two records to see what a
# change did.
#
#   scripts/perf-snapshot.sh -o /tmp/perf.md
#   scripts/perf-snapshot.sh --cold --db /tmp/copy.db -o /tmp/perf.md
#
# --cold adds the first hits (run it right after a deploy or restart).
# --db    a `huntdeck backup` snapshot, for the per-query numbers.
set -uo pipefail
dir="$(dirname "$0")"
out=""
cold=""
db=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="${2:?}"; shift 2 ;;
    --cold) cold="--cold"; shift ;;
    --db) db="${2:?}"; shift 2 ;;
    *) echo "usage: $0 [-o FILE] [--cold] [--db COPY.db]" >&2; exit 2 ;;
  esac
done
if [ -z "$out" ]; then
  out="/tmp/huntdeck-perf-$(date +%Y%m%dT%H%M%S).md"
fi
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT

{
  echo "# Performance snapshot"
  echo
  echo "- taken: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "- host: $(hostname)"
  echo "- binary: $(ls -l /opt/huntdeck/huntdeck 2>/dev/null | awk '{print $6, $7, $8}' || echo "not on this host")"
  if [ -n "$db" ]; then
    echo "- database copy: $db"
  fi
  echo
  echo "## Pages"
  echo
  echo '```'
  if [ -n "$cold" ]; then
    bash "$dir/bench.sh" --cold --check 2>&1 | sed -n "/first hit/,/^$/p"
    echo
  fi
  bash "$dir/bench.sh" --check 2>&1
  echo '```'
  echo
  if [ -n "$db" ]; then
    echo "## Queries (a copy of the database)"
    echo
    echo '```'
    (cd "$dir/.." >/dev/null && HUNTDECK_PERF_DB="$db" go test ./internal/store/ -run TestPerf -count=1 -v 2>&1 \
      | grep -E "ms|skip" | sed "s/^ *//")
    echo '```'
    echo
  fi
  echo "## How it was made"
  echo
  echo "\`scripts/perf-snapshot.sh\` runs \`scripts/bench.sh\` and, with \`--db\`,"
  echo "the probe suite in \`internal/store\`. Numbers move with the page cache:"
  echo "a warm run is what a visitor feels, the first hit is what a restart costs."
} > "$out" 2>&1

echo "wrote $out"
