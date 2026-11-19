#!/usr/bin/env bash
# A backup nobody has read is a hope, not a backup. This restores the newest
# archive's manifest and database into a scratch directory (--dry-run: checks
# the archive and its schema, writes nothing) so a corrupt or unreadable backup
# shows up now, not on the day it is needed.
#
# 1Panel 计划任务 (Shell 脚本) runs it weekly; the exit code is the report.
set -euo pipefail

container="${HUNTDECK_CONTAINER:-huntdeck-huntdeck-1}"

newest=$(docker exec "$container" sh -c 'ls -t /data/backups/huntdeck-backup-*.tar.gz 2>/dev/null | head -1' || true)
if [ -z "$newest" ]; then
  echo "no backup archive in /data/backups" >&2
  exit 1
fi

echo "verifying $newest"
docker exec "$container" /huntdeck restore --from "$newest" --data /data/verify-check --dry-run
