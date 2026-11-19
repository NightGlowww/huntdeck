#!/usr/bin/env bash
# A backup for a scheduler to call — 1Panel's 计划任务 (Shell 脚本), cron, systemd.
#
# Why a script at all: 1Panel's own backups cover what it installed (app-store
# apps, the files of the sites it made). HuntDeck runs outside that list and
# keeps its database in a Docker volume, so copying the volume's directory is a
# hot copy of a live SQLite database — its WAL can be mid-write and the copy can
# be unusable. This asks the app for a consistent snapshot (VACUUM INTO) and puts
# the archive where a file backup can pick it up:
#
#   /opt/huntdeck/backup.sh
#
# 1Panel then owns the schedule, the retention and the upload to a backup
# account (COS/OSS/S3), which is where an off-host copy comes from.
set -euo pipefail

container="${HUNTDECK_CONTAINER:-huntdeck-huntdeck-1}"
dest="${HUNTDECK_BACKUP_DIR:-/opt/huntdeck/data/backups}"
keep="${HUNTDECK_BACKUP_KEEP:-7}"

mkdir -p "$dest"
stamp=$(date +%Y%m%d)
out="/data/backups/huntdeck-backup-$stamp.tar.gz"

docker exec "$container" /huntdeck backup --to "$out" --keep "$keep"
# Copied out for 1Panel to ship off-host; the archive stays in the volume too,
# which is where `huntdeck doctor` looks for a fresh one.
docker cp "$container:$out" "$dest/huntdeck-backup-$stamp.tar.gz"

echo "backup written: $dest/huntdeck-backup-$stamp.tar.gz ($(du -h "$dest/huntdeck-backup-$stamp.tar.gz" | cut -f1))"
