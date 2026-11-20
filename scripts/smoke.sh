#!/usr/bin/env bash
# Say whether a running HuntDeck is healthy, in one command: the pages, the
# API, the login gate, and the MCP server over stdio.
#
#   HD_PASSWORD='…' scripts/smoke.sh --url http://127.0.0.1:8080
#   scripts/smoke.sh --url https://huntdeck.example --password-env HD_PASSWORD
#   scripts/smoke.sh --url … --json     # one JSON object, for monitoring
#   scripts/smoke.sh --url … --backups /opt/huntdeck/data/backups   # and that
#                                        # a fresh backup exists (30 hours)
#
# Exit code 0 when everything answers as it should, 1 otherwise. Nothing
# secret is printed: the password is read from the environment only.
set -euo pipefail

url="http://127.0.0.1:8080"
password_env="HD_PASSWORD"
mcp=""
json=""
backups=""
while [ $# -gt 0 ]; do
  case "$1" in
    --url) url="${2:?}"; shift 2 ;;
    --password-env) password_env="${2:?}"; shift 2 ;;
    --mcp) mcp="${2:?}"; shift 2 ;;
    --json) json="1"; shift ;;
    --backups) backups="${2:?}"; shift 2 ;;
    -h|--help) sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
url="${url%/}"
password="${!password_env:-}"
jar=$(mktemp)
trap 'rm -f "$jar"' EXIT
fails=0

results=$(mktemp)
trap 'rm -f "$jar" "$results"' EXIT
say() { [ -n "$json" ] || printf '%-44s %s\n' "$1" "$2"; }
record() { printf '%s\t%s\t%s\n' "$1" "$2" "$3" >>"$results"; }
ok() { say "$1" "ok ($2)"; record "$1" "true" "$2"; }
bad() { say "$1" "FAIL ($2)"; record "$1" "false" "$2"; fails=$((fails + 1)); }
# curl writes 000 when nothing answered; `|| true` keeps that a failed
# check rather than the end of the script.
code() { curl -s -o /dev/null -m 15 -w '%{http_code}' "$@" || true; }

# 1. health, and the only open route
body=$(curl -s -m 15 "$url/healthz" || true)
case "$body" in
  *'"status":"ok"'*) ok "/healthz" "ok" ;;
  *) bad "/healthz" "${body:-no answer}" ;;
esac

# 2. everything that holds data is closed to strangers when a password is set:
# the API answers 401, a page answers a redirect to the login page.
if [ -n "$password" ]; then
  while IFS="|" read -r path want; do
    [ -n "$path" ] || continue
    got=$(code "$url$path")
    case "$got" in
      401|302|303) ok "closed: $path" "$got" ;;
      *) bad "closed: $path" "answered $got" ;;
    esac
  done <<EOF
/api/v1/status|401
/api/v1/programs?format=csv|401
/api/v1/targets?kind=domain|401
/api/v1/changes?limit=1|401
/api/v1/disclosed?format=txt|401
/changes?format=txt|303
/disclosed?format=txt|303
/programs/x/y?tab=scope&format=txt|303
EOF
else
  api=$(code "$url/api/v1/status")
  [ "$api" = 200 ] && ok "API without a session" "200 (no password set)" || bad "API without a session" "answered $api"
fi

# 3. the login page, and signing in
login_page=$(code "$url/login")
[ "$login_page" = 200 ] && ok "login page" "200" || bad "login page" "answered $login_page"

# The session travels as a header, not a cookie jar: curl 8.18 refuses to write
# a jar for an IP-literal host, so every scripted login against 127.0.0.1
# authenticated nothing and thirteen checks reported 401s.
cookie=""
if [ -n "$password" ]; then
  headers=$(curl -s -D - -o /dev/null -m 15 -X POST --data-urlencode "password=$password" --data "next=/" "$url/login" || true)
  status=$(printf '%s' "$headers" | head -1 | awk '{print $2}')
  cookie=$(printf '%s' "$headers" | tr -d '' | awk 'tolower($1) == "set-cookie:" {print $2; exit}')
  case "$status" in
    200|303) ok "sign in" "$status${cookie:+ (session)}" ;;
    *) bad "sign in" "answered $status" ;;
  esac
else
  ok "sign in" "skipped (no password)"
fi

# 4. the API behind the session
for path in "/api/v1/status" "/api/v1/programs?limit=1" "/api/v1/targets?kind=domain&limit=1" "/api/v1/changes?limit=1"; do
  got=$(curl -s -m 20 -H "Cookie: $cookie" -o /dev/null -w '%{http_code}' "$url$path" || true)
  [ "$got" = 200 ] && ok "GET $path" "200" || bad "GET $path" "answered $got"
done
# The key has to be there; an empty list is a fresh install with nothing
# configured yet, which the README's own post-install check runs into.
status=$(curl -s -m 20 -H "Cookie: $cookie" "$url/api/v1/status" | python3 -c 'import json,sys
d = json.load(sys.stdin)
if "platforms" not in d or not isinstance(d.get("platforms"), list):
    print("missing")
else:
    print(len(d["platforms"]))' 2>/dev/null || echo "")
case "$status" in
  ''|missing) bad "status payload" "no platforms key in the answer" ;;
  0) ok "status payload" "0 platforms (none configured yet)" ;;
  *) ok "status payload" "$status platforms" ;;
esac

# 5. the pages, and something only that page has: a template that renders an
# empty shell, or one page's body served for another, answers 200 like any
# other and would otherwise pass. The markers are ids, not words, so they read
# the same in either language.
while IFS="|" read -r path marker; do
  body=$(curl -s -m 20 -H "Cookie: $cookie" "$url$path" || true)
  want=$(printf 'data-page="%s"' "$marker")
  if [ -z "$body" ]; then
    bad "GET $path" "no answer"
  # No pipe: grep -q exits on the first match, and under pipefail the writer
  # then dies of SIGPIPE, which turns a match into a failure.
  elif [ "${body#*$want}" != "$body" ]; then
    ok "GET $path" "200, $marker"
  else
    bad "GET $path" "200 without $want (a login page, an empty shell, or the wrong page?)"
  fi
done <<EOF
/|dashboard
/queue|queue
/programs|programs
/targets|targets
/disclosed|disclosed
/changes|changes
/settings|settings
/api|api
EOF

# 6. the MCP server, when it is beside us
if [ -n "$mcp" ] && [ -x "$mcp" ]; then
  tools=$(printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' \
    | HUNTDECK_URL="$url" HUNTDECK_PASSWORD="$password" "$mcp" 2>/dev/null \
    | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["result"]["tools"]))' 2>/dev/null || echo "")
  case "$tools" in
    ''|0) bad "MCP tools/list" "no answer" ;;
    *) ok "MCP tools/list" "$tools tools" ;;
  esac
  # Listing the tools says nothing about calling one: a tool whose body is broken
  # passes a list and fails a user. Ask for status and read the answer.
  called=$(printf '%s\n%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"smoke","version":"1"}}}' \
    '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"huntdeck_status","arguments":{}}}' \
    | HUNTDECK_URL="$url" HUNTDECK_PASSWORD="$password" "$mcp" 2>/dev/null | tail -1 \
    | python3 -c 'import json,sys
d=json.load(sys.stdin)
t=json.loads(d["result"]["content"][0]["text"])
print(len(t.get("platforms") or []))' 2>/dev/null || echo "")
  case "$called" in
    "") bad "MCP tools/call" "no answer" ;;
    *) ok "MCP tools/call" "status: $called platforms" ;;
  esac
fi


# 7. a backup newer than 30 hours, when the caller names where they are: the
# schedule living on another service is exactly the thing that stops unnoticed.
if [ -n "$backups" ]; then
  if [ ! -d "$backups" ]; then
    bad "backups: $backups" "no such directory"
  else
    newest=$(ls -t "$backups"/huntdeck-backup-*.tar.gz 2>/dev/null | head -1 || true)
    if [ -z "$newest" ]; then
      bad "a fresh backup" "none in $backups"
    else
      age=$(( $(date +%s) - $(date -r "$newest" +%s) ))
      hours=$(( age / 3600 ))
      if [ "$age" -le 108000 ]; then
        ok "a fresh backup" "$(basename "$newest"), ${hours}h old"
      else
        bad "a fresh backup" "$(basename "$newest") is ${hours}h old; is the schedule running?"
      fi
    fi
  fi
fi
if [ -n "$json" ]; then
  python3 - "$results" "$fails" <<'PY'
import json, sys
path, fails = sys.argv[1], int(sys.argv[2])
checks = []
for line in open(path, encoding="utf-8"):
    name, ok, detail = line.rstrip("\n").split("\t", 2)
    checks.append({"name": name, "ok": ok == "true", "detail": detail})
print(json.dumps({"ok": fails == 0, "failed": fails, "checks": checks}, ensure_ascii=False))
PY
else
  echo
  if [ "$fails" -gt 0 ]; then echo "$fails check(s) failed"; else echo "all checks passed"; fi
fi
if [ "$fails" -gt 0 ]; then
  exit 1
fi
