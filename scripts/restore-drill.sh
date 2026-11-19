#!/usr/bin/env bash
# Restore the live database into a scratch directory and check the copy, so the
# recovery path is known to work before it is needed. verify-backup.sh only reads
# the archive; this writes a real database and runs doctor on it.
#
#   scripts/restore-drill.sh [container] [scratch]
#
# Leaves nothing behind: the scratch directory and the drill archive are removed.
set -euo pipefail
container="${HUNTDECK_CONTAINER:-huntdeck-huntdeck-1}"
scratch="${2:-/data/restore-drill}"
archive="/data/backups/drill-$(date +%Y%m%d%H%M%S).tar.gz"
dkr() {
  if docker ps >/dev/null 2>&1; then docker "$@"; else sudo -n docker "$@"; fi
}
cleanup() {
  dkr exec "$container" rm -rf "$scratch" >/dev/null 2>&1 || true
  dkr exec "$container" rm -f "$archive" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "==> backup"
dkr exec "$container" /huntdeck backup --to "$archive" | tail -2

echo "==> restore into $scratch"
dkr exec "$container" /huntdeck restore --from "$archive" --data "$scratch" | tail -2

echo "==> doctor on the restored copy"
out=$(dkr exec -e HUNTDECK_DATA_DIR="$scratch" "$container" /huntdeck doctor 2>&1)
echo "$out" | grep -E 'schema v|integrity|foreign key|secret.key' || true
case "$out" in
  *"no problems found"*) : ;;
  *) echo "the restored copy does not pass doctor" >&2; exit 1 ;;
esac

echo "==> counts, live against restored"
live=$(dkr exec "$container" /huntdeck status 2>/dev/null | grep -E 'programs \(|scope entries' | md5sum)
copy=$(dkr exec -e HUNTDECK_DATA_DIR="$scratch" "$container" /huntdeck status 2>/dev/null | grep -E 'programs \(|scope entries' | md5sum)
if [ "$live" = "$copy" ]; then
  echo "  the copy holds what the live database holds"
else
  echo "  the counts differ: live $live, copy $copy" >&2
  exit 1
fi
echo
echo "a backup restores into a working database"
