#!/usr/bin/env bash
# Times every page and the API against a running instance, from the host (no
# TLS, no proxy): three runs each, so the warming cost shows and the later
# numbers are what a visitor feels.
#
#   scripts/bench.sh                       # against http://127.0.0.1:8080
#   base=http://127.0.0.1:8080 scripts/bench.sh
#   scripts/bench.sh --check               # exit 1 if a page exceeds 50 ms,
#                                          #   an API route 100 ms (warm run)
#   scripts/bench.sh --cold                # the first hit of each route, in
#                                          #   order: what a restart costs;
#                                          #   with --check, 900 ms each
#
# The password comes from HD_PASSWORD, or from /opt/huntdeck/.env on a host
# where the instance runs. It is used, never printed.
set -uo pipefail
base="${base:-http://127.0.0.1:8080}"
check=0
cold=0
max_page="${max_page:-50}"
max_api="${max_api:-100}"
max_first="${max_first:-900}"
while [ $# -gt 0 ]; do
  case "$1" in
    --check) check=1; shift ;;
    --cold) cold=1; shift ;;
    --max-page) max_page="$2"; shift 2 ;;
    --max-api) max_api="$2"; shift 2 ;;
    --max-first) max_first="$2"; shift 2 ;;
    *) echo "usage: $0 [--check] [--cold] [--max-page MS] [--max-api MS] [--max-first MS]" >&2; exit 2 ;;
  esac
done
pw="${HD_PASSWORD:-}"
if [ -z "$pw" ] && [ -r /opt/huntdeck/.env ]; then
  pw=$(sudo grep "^HUNTDECK_PASSWORD=" /opt/huntdeck/.env | cut -d= -f2-)
fi
jar=$(mktemp)
trap 'rm -f "$jar"' EXIT
if [ -n "$pw" ]; then
  curl -s -c "$jar" -b "$jar" -o /dev/null -X POST --data-urlencode "password=$pw" -d "next=%2F" "$base/login"
fi
paths=(
  / /programs /invites /targets /changes /disclosed /queue /stats /settings /api /methodology
  # The settings tabs render their own data: the polling tab once carried a
  # request to bugcrowd.com inside the render and went from 9 ms to 1.6 s
  # without any other route noticing.
  "/settings?tab=polling" "/settings?tab=accounts"
  /programs/hackerone/1win_com
  "/programs/hackerone/1win_com?tab=scope"
  "/programs/hackerone/1win_com?tab=policy"
  "/programs/hackerone/1win_com?tab=changes"
  "/programs/hackerone/1win_com?tab=stats"
  "/targets?sort=best" "/targets?kind=domains" "/changes?group=scope"
  /api/v1/status "/api/v1/programs?limit=30" "/api/v1/targets?kind=domain&limit=30"
  "/api/v1/changes?limit=30" "/api/v1/disclosed?limit=30" "/api/v1/queue?limit=20" "/queue?one=1"
)
over=0
bad=0
ms_of() { awk -v t="$1" 'BEGIN { printf "%d", t * 1000 }'; }
if [ "$cold" = 1 ]; then
  echo "== first hit of each route (a restart's cost)"
  for p in "${paths[@]}"; do
    printf "%-44s " "$p"
    out=$(curl -s -o /dev/null -b "$jar" -w "%{http_code} %{time_total}" "$base$p")
    printf "first=%s/%s" "${out% *}" "${out#* }"
    if [ "$check" = 1 ]; then
      code="${out% *}"
      # 000 is curl failing to connect: a budget it beats by not answering.
      case "$code" in 2*|3*) : ;; *) printf "  FAIL (%s)" "$code"; bad=$((bad + 1)) ;; esac
      ms=$(ms_of "${out#* }")
      if [ "$ms" -gt "$max_first" ]; then
        printf "  OVER (%s ms > %s ms)" "$ms" "$max_first"
        over=$((over + 1))
      fi
    fi
    echo
  done
  echo
fi
for p in "${paths[@]}"; do
  printf "%-44s " "$p"
  warm=""
  for i in 1 2 3; do
    out=$(curl -s -o /dev/null -b "$jar" -w "%{http_code} %{time_total}" "$base$p")
    printf "%s/%s " "${out% *}" "${out#* }"
    warm="${out#* }"
  done
  if [ "$check" = 1 ]; then
    code="${out% *}"
    case "$code" in 2*|3*) : ;; *) printf "  FAIL (%s)" "$code"; bad=$((bad + 1)) ;; esac
    ms=$(ms_of "$warm")
    limit="$max_page"
    case "$p" in /api/*) limit="$max_api" ;; esac
    if [ "$ms" -gt "$limit" ]; then
      printf "  OVER (%s ms > %s ms)" "$ms" "$limit"
      over=$((over + 1))
    fi
  fi
  echo
done
if [ "$check" = 1 ]; then
  if [ "$bad" -gt 0 ]; then
    echo "$bad route(s) did not answer: is the instance up, and is $base right?" >&2
    exit 1
  fi
  if [ "$over" -gt 0 ]; then
    echo "$over route(s) slower than their budget" >&2
    exit 1
  fi
  echo "every route answered, inside its budget"
fi
