#!/usr/bin/env bash
# Prove the shell gates fail when they should. A check nobody has seen fail is a
# guess, so each gate is pointed at a deliberately wrong world and must say so.
# Nothing outside /tmp is touched: no host, no database, no password.
#
#   scripts/gates.sh
#
# Exit 0 when every gate failed as expected.
set -uo pipefail
# CDPATH makes cd print the directory it chose, and the command substitution
# would capture that as part of the path. Silence it.
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." >/dev/null 2>&1 && pwd)
fails=0
say() { printf '%-24s %s\n' "$1" "$2"; }
pass() { say "$1" "fails, as it should"; }
blind() { say "$1" "DID NOT FAIL: the gate is blind"; fails=$((fails + 1)); }

# 1. bench against nothing. A refused connection takes no time, which is
#    exactly how it used to pass.
if base=http://127.0.0.1:1 "$root/scripts/bench.sh" --check >/tmp/gates-bench.log 2>&1; then
  blind "bench --check"
elif grep -q 'did not answer' /tmp/gates-bench.log; then
  pass "bench --check"
else
  blind "bench --check (failed for another reason)"
fi

# 2. smoke against a server that answers 200 to everything, with no HuntDeck
#    markup. A status-only check would pass this; the content check must not.
dir=$(mktemp -d)
port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
printf '<html><body><h1>not huntdeck</h1></body></html>' > "$dir/index.html"
( cd "$dir" && exec python3 -m http.server "$port" --bind 127.0.0.1 >/dev/null 2>&1 ) &
srv=$!
for _ in $(seq 1 25); do curl -fs -o /dev/null "http://127.0.0.1:$port/" && break; sleep 0.2; done
if "$root/scripts/smoke.sh" --url "http://127.0.0.1:$port" >/tmp/gates-smoke.log 2>&1; then
  blind "smoke (content)"
elif grep -q 'without data-page' /tmp/gates-smoke.log; then
  pass "smoke (content)"
else
  blind "smoke (failed for another reason)"
fi
kill "$srv" 2>/dev/null
wait "$srv" 2>/dev/null
rm -rf "$dir"

echo
if [ "$fails" -gt 0 ]; then
  echo "$fails gate(s) did not fail when they should" >&2
  exit 1
fi
echo "every shell gate fails when it should"
