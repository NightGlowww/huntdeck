#!/usr/bin/env bash
# Watch a HuntDeck from outside it: run the smoke checks, and tell someone when
# it goes down (and again when it comes back), through a webhook, Bark or
# ServerChan. It runs where HuntDeck cannot: the app's own notification
# channels can't report that the app is gone.
#
#   HD_PASSWORD='…' scripts/watch.sh --url https://huntdeck.example
#   HD_PASSWORD='…' scripts/watch.sh --url … --every 300      # keep watching
#   HD_PASSWORD='…' scripts/watch.sh --url … --alert-webhook https://hooks.example/…
#
# Alert targets come from flags or the environment:
#   --alert-webhook URL   or HD_ALERT_WEBHOOK   JSON POST, {text, content}
#   --alert-bark KEY      or HD_ALERT_BARK      Bark device key
#                         and HD_ALERT_BARK_SERVER (default https://api.day.app)
#   --alert-serverchan KEY or HD_ALERT_SERVERCHAN  ServerChan SendKey
#   --alert-after N       only alert after N bad runs in a row (default 1)
#   --alert-every N       while it stays down, say so again every N runs (0: only once)
#   --backups DIR         also require a backup newer than 30 hours (or HD_ALERT_BACKUPS)
#   --state FILE          where the up/down memory lives (or HD_WATCH_STATE);
#                         one file per watched instance
#
# One run exits 0 when everything passed and 1 when it didn't (cron-friendly);
# --every keeps going, alerting only when the state changes.
set -euo pipefail

url=""
password_env="HD_PASSWORD"
every=""
webhook="${HD_ALERT_WEBHOOK:-}"
bark="${HD_ALERT_BARK:-}"
bark_server="${HD_ALERT_BARK_SERVER:-https://api.day.app}"
schan="${HD_ALERT_SERVERCHAN:-}"
alert_after="1"
alert_every="0"
backups="${HD_ALERT_BACKUPS:-}"
# One state file per watched instance: two watchers on one host must not
# share it, so --state (or HD_WATCH_STATE) can point elsewhere.
state="${HD_WATCH_STATE:-${TMPDIR:-/tmp}/huntdeck-watch.state}"

while [ $# -gt 0 ]; do
  case "$1" in
    --url) url="${2:?}"; shift 2 ;;
    --password-env) password_env="${2:?}"; shift 2 ;;
    --every) every="${2:?}"; shift 2 ;;
    --alert-webhook) webhook="${2:?}"; shift 2 ;;
    --alert-bark) bark="${2:?}"; shift 2 ;;
    --alert-serverchan) schan="${2:?}"; shift 2 ;;
    --alert-after) alert_after="${2:?}"; shift 2 ;;
    --alert-every) alert_every="${2:?}"; shift 2 ;;
    --state) state="${2:?}"; shift 2 ;;
    --backups) backups="${2:?}"; shift 2 ;;
    -h|--help) sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
[ -n "$url" ] || { echo "--url is required" >&2; exit 2; }
here=$(cd -- "$(dirname "$0")" >/dev/null && pwd)

say() { printf '%s %s\n' "$(date '+%Y-%m-%dT%H:%M:%S%z')" "$*"; }

# notify sends one line to every configured target.
notify() {
  local text="$1" sent=0
  if [ -n "$webhook" ]; then
    curl -fsS -m 15 -X POST -H 'Content-Type: application/json' \
      --data "$(python3 -c 'import json,sys; print(json.dumps({"text": sys.argv[1], "content": sys.argv[1]}))' "$text")" \
      "$webhook" >/dev/null && sent=1 || say "webhook alert failed"
  fi
  if [ -n "$bark" ]; then
    curl -fsS -m 15 -X POST -H 'Content-Type: application/json' \
      --data "$(python3 -c 'import json,sys; print(json.dumps({"device_key": sys.argv[1], "title": "HuntDeck alert", "body": sys.argv[2], "group": "HuntDeck"}))' "$bark" "$text")" \
      "${bark_server%/}/push" >/dev/null && sent=1 || say "bark alert failed"
  fi
  if [ -n "$schan" ]; then
    curl -fsS -m 15 -X POST --data-urlencode "title=HuntDeck alert" --data-urlencode "desp=$text" \
      "https://sctapi.ftqq.com/${schan}.send" >/dev/null && sent=1 || say "serverchan alert failed"
  fi
  [ "$sent" = 1 ] || say "no alert target configured"
}

# check runs the smoke checks and reports whether they all passed.
check() {
  local out
  out=$("$here/smoke.sh" --url "$url" --password-env "$password_env" ${backups:+--backups "$backups"} --json 2>/dev/null || true)
  python3 - "$out" <<'PY'
import json, sys
try:
    d = json.loads(sys.argv[1])
except Exception:
    print("down|down|smoke gave no answer")
    raise SystemExit(0)
bad = [c["name"] + " (" + c["detail"] + ")" for c in d["checks"] if not c["ok"]]
# Touching but unhealthy is not the same as gone: a stale backup or a broken
# page needs a look, not a page-the-owner-at-3am.
reachable = all(c["ok"] for c in d["checks"] if c["name"] == "/healthz")
kind = "down" if not reachable else "unhealthy"
status = "up" if d["ok"] else kind
print(status + "|" + kind + "|" + "; ".join(bad[:4]))
PY
}

# state holds "status:failures:alerted": up:0:0, or down:3:1 once a bad run
# lasted long enough to tell someone.
read_state() {
  local raw rest
  raw=$(cat "$state" 2>/dev/null || echo "up:0:0")
  case "$raw" in
    *:*:*) prev_status="${raw%%:*}"; rest="${raw#*:}"; prev_fails="${rest%%:*}"; prev_alerted="${rest##*:}" ;;
    *) prev_status="up"; prev_fails=0; prev_alerted=0 ;;
  esac
}

run_once() {
  local result status detail fails alerted
  result=$(check)
  status="${result%%|*}"
  rest="${result#*|}"
  kind="${rest%%|*}"
  detail="${rest#*|}"
  read_state
  if [ "$status" != up ]; then
    if [ "$prev_status" != up ]; then fails=$((prev_fails + 1)); else fails=1; fi
    alerted=$prev_alerted
    if [ "$kind" = down ]; then
      say "DOWN ($fails in a row): ${detail:-no detail}"
      headline="$url is down"
    else
      say "UNHEALTHY ($fails in a row): ${detail:-no detail}"
      headline="$url needs attention"
    fi
    # A blip should not page anyone: --alert-after says how many bad runs
    # in a row it takes (1 by default: the first).
    if [ "$fails" -ge "$alert_after" ] && [ "$alerted" != 1 ]; then
      notify "$headline: ${detail:-no detail}"
      alerted=1
    elif [ "$alerted" = 1 ] && [ "$alert_every" -gt 0 ] && [ "$fails" -gt "$alert_after" ] &&
         [ $(((fails - alert_after) % alert_every)) -eq 0 ]; then
      # Still down: a reminder every alert_every runs, so a long outage is not
      # forgotten after its first message.
      notify "$headline (still): ${detail:-no detail}"
    fi
    echo "down:$fails:$alerted" >"$state"
    return 1
  fi
  say "up"
  if [ "$prev_status" = down ] && [ "$prev_alerted" = 1 ]; then
    notify "$url is back up"
  fi
  echo "up:0:0" >"$state"
  return 0
}

if [ -z "$every" ]; then
  run_once
  exit $?
fi
say "watching $url every ${every}s"
while :; do
  run_once || true
  sleep "$every"
done
