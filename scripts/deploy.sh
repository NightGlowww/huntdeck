#!/usr/bin/env bash
# Ship this checkout to a host and restart it there.
#
# Two ways a host can run HuntDeck from source:
#   full   the host builds the image itself (needs Docker Hub, proxy.golang.org,
#          dl.google.com and github.com to be reachable)
#   binary this machine cross-compiles the binary and the host only wraps it in
#          the image it already has: for hosts where one of those is blocked
#          (a mirror or a mainland network, typically)
#
# Usage:
#   scripts/deploy.sh --host prod-1 [--dir /opt/huntdeck] [--mode binary|full]
#                     [--image huntdeck:local] [--base <image>] [--dry-run] [--check]
#
# It never reads or prints secrets: .env stays on the host, and the app's access
# password is set there (HUNTDECK_PASSWORD) rather than passed through here.
set -euo pipefail

host=""
dir="/opt/huntdeck"
mode="binary"
image="huntdeck:local"
base_arg=""
dry=""
check=""
expect=""

usage() { sed -n '2,18p' "$0" | sed 's/^# \{0,1\}//'; }

while [ $# -gt 0 ]; do
  case "$1" in
    --host) host="${2:?}"; shift 2 ;;
    --dir) dir="${2:?}"; shift 2 ;;
    --mode) mode="${2:?}"; shift 2 ;;
    --image) image="${2:?}"; shift 2 ;;
    --base) base_arg="${2:?}"; shift 2 ;;
    --dry-run) dry="1"; shift ;;
    --check) check="1"; shift ;;
    --expect) expect="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done
[ -n "$host" ] || { echo "--host is required" >&2; usage >&2; exit 2; }
case "$mode" in binary|full) ;; *) echo "--mode must be binary or full" >&2; exit 2 ;; esac

# One deploy per host at a time. Two at once fight over the same image tag and the
# same container name: the loser's `docker compose up` died with "container name
# already in use", after both had spent minutes building.
# mkdir is atomic and portable (flock is not on macOS). A lock older than half an
# hour is a deploy that was killed: take it over rather than block forever.
lock="/tmp/huntdeck-deploy-$(printf %s "$host" | tr -c 'a-zA-Z0-9' '_').lock.d"
lock_held=""
tmp=""
SSH_CTL="/tmp/hd-ssh-$$.sock"
cleanup() {
  if [ -n "${SSH_CTL:-}" ] && [ -S "$SSH_CTL" ]; then
    ssh -S "$SSH_CTL" -O exit "$host" >/dev/null 2>&1 || true
    rm -f "$SSH_CTL"
  fi
  [ -n "$tmp" ] && rm -rf "$tmp" || true
  [ -n "$lock_held" ] && rmdir "$lock" 2>/dev/null || true
}
trap cleanup EXIT
if [ -z "$dry" ] && [ -z "$check" ]; then
  if ! mkdir "$lock" 2>/dev/null; then
    # A lock with no deploy process behind it is a leftover: the script that took
    # it was killed, or exited in a way that skipped its trap. Age alone is the
    # wrong test - a run that died a minute ago still blocks the next one.
    # pgrep exits 1 when nothing matches, and under `set -o pipefail` that fails
    # the whole pipeline: the script then died here with no output at all, which
    # looked like an empty deploy log.
    others=$({ pgrep -f 'scripts/deploy.sh' 2>/dev/null || true; } | grep -vx "$$" | grep -vx "$PPID" | wc -l | tr -d ' ')
    if [ "$others" = "0" ]; then
      echo "taking over a lock left by a deploy that is no longer running" >&2
      rmdir "$lock" 2>/dev/null || true
      mkdir "$lock" 2>/dev/null || { echo "another deploy to $host is running (lock $lock)" >&2; exit 3; }
    elif [ -n "$(find "$lock" -maxdepth 0 -mmin +30 2>/dev/null)" ]; then
      echo "taking over a stale deploy lock (older than 30 minutes)" >&2
      rmdir "$lock" 2>/dev/null || true
      mkdir "$lock" 2>/dev/null || { echo "another deploy to $host is running (lock $lock)" >&2; exit 3; }
    else
      echo "another deploy to $host is running (lock $lock)" >&2
      exit 3
    fi
  fi
  lock_held="1"
fi

run() {
  if [ -n "$dry" ]; then printf '  [dry-run] %s\n' "$*"; else "$@"; fi
}
SSH_OPTS="-o ConnectTimeout=15 -o ServerAliveInterval=15 -o ServerAliveCountMax=4 -o ControlMaster=auto -o ControlPath=$SSH_CTL -o ControlPersist=120"
remote() { run ssh $SSH_OPTS "$host" "$@"; }

# ship_rsync copies one file, retrying a transfer the network dropped. Twice in
# one evening the connection reset mid-transfer and the deploy died at the ship
# step: the build survives a drop now, the copy did not.
ship_rsync() {
  local attempt=1
  while [ $attempt -le 3 ]; do
    if run rsync -az -e "ssh $SSH_OPTS" "$1" "$2"; then return 0; fi
    echo "  transfer failed (attempt $attempt of 3); retrying" >&2
    attempt=$((attempt + 1))
    sleep 2
  done
  echo "  the transfer did not succeed after 3 attempts: $1" >&2
  return 1
}

remote_out() {
  if [ -n "$dry" ]; then printf ''; else ssh $SSH_OPTS "$host" "$@"; fi
}

root=$(cd -- "$(dirname "$0")/.." >/dev/null && pwd)   # >/dev/null: CDPATH printouts
cd "$root"

# --check asks the running instance which build it is, and stops there: no
# tests, no build, no restart.
if [ -n "$check" ]; then
  echo "==> running on $host"
  line=$(remote_out "cd $dir && sudo docker compose exec -T huntdeck /huntdeck version") || {
    echo "  could not read the version (is the service up?)" >&2
    exit 1
  }
  version=$(printf '%s' "$line" | awk '{print $2}')
  echo "  ${line:-unknown}"
  remote "cd $dir && sudo docker compose ps --format '  {{.Name}} {{.Status}}'"
  if [ -n "$expect" ] && [ "$version" != "$expect" ]; then
    echo "  expected $expect, the host runs ${version:-nothing}" >&2
    exit 1
  fi
  exit 0
fi

echo "==> tests"
if [ "${SKIP_TESTS:-0}" = "1" ]; then
  echo "  skipped (already verified in CI)"
else
  run go vet ./...
  run go test ./...
fi

# One name for this deploy, whichever mode builds it.
ver="${DEPLOY_VERSION:-$(bash scripts/version.sh 2>/dev/null || echo dev)}"

if [ "$mode" = "full" ]; then
  echo "==> source"
  run rsync -az --exclude '.env' --exclude '.git' --exclude 'bin' --exclude 'data' ./ "$host:$dir/"
  echo "==> image (the host builds it)"
  # A network that cannot reach proxy.golang.org can name a mirror: the
# Dockerfile takes GOPROXY as a build arg and defaults to the public one.
build_args=""
[ -n "${GOPROXY:-}" ] && build_args="--build-arg GOPROXY=$GOPROXY"
# The Dockerfile stamps the version in, and defaults to dev: without this the
# image answers dev and the check below refuses it.
build_args="$build_args --build-arg VERSION=$ver"
# noVNC comes from github.com; a host that cannot reach it skips it, and the
# login browser still works without the in-page viewer.
[ -n "${WITH_NOVNC:-}" ] && build_args="$build_args --build-arg WITH_NOVNC=$WITH_NOVNC"
remote "cd $dir && sudo docker compose build $build_args && sudo docker compose up -d --pull never"
else
  echo "==> binary (linux/amd64)"
  # One name for this deploy: it goes into the binaries and is asserted
  # against the running process afterwards, so a deploy that ships nothing
  # cannot pass.
  tmp=$(mktemp -d)
  run env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags "-s -w -X github.com/mdpsec/huntdeck/internal/version.Version=$ver -X github.com/mdpsec/huntdeck/internal/version.Pending=$(git -C "$(dirname "$0")/.." status --porcelain 2>/dev/null | wc -l | tr -d " ")" \
    -o "$tmp/huntdeck" ./cmd/huntdeck
  run env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags "-s -w -X github.com/mdpsec/huntdeck/internal/version.Version=$ver -X github.com/mdpsec/huntdeck/internal/version.Pending=$(git -C "$(dirname "$0")/.." status --porcelain 2>/dev/null | wc -l | tr -d " ")" \
    -o "$tmp/huntdeck-mcp" ./cmd/huntdeck-mcp
  echo "==> ship"
  ship_rsync "$tmp/huntdeck" "$host:$dir/huntdeck"
  ship_rsync "$tmp/huntdeck-mcp" "$host:$dir/huntdeck-mcp"
  ship_rsync Dockerfile.prod "$host:$dir/Dockerfile.prod"
  ship_rsync scripts/smoke.sh "$host:$dir/scripts/smoke.sh"
  # The scheduler (1Panel's 计划任务) runs this one from the app directory.
  ship_rsync scripts/backup.sh "$host:$dir/backup.sh"
  ship_rsync scripts/watch.sh "$host:$dir/scripts/watch.sh"
  ship_rsync scripts/verify-backup.sh "$host:$dir/verify-backup.sh"
  ship_rsync scripts/bench.sh "$host:$dir/scripts/bench.sh"
  ship_rsync scripts/perf-snapshot.sh "$host:$dir/scripts/perf-snapshot.sh"
  ship_rsync scripts/perf-diff.py "$host:$dir/scripts/perf-diff.py"
  # The base has to be here already: a host that cannot reach ghcr.io would
  # otherwise spend half a minute failing to pull it, with docker's message and
  # no way out. Say what is missing and what to do instead.
  base="${base_arg:-ghcr.io/mdpsec/huntdeck:latest}"
  if ! remote "sudo docker image inspect $base >/dev/null 2>&1"; then
    echo "  $base is not on the host; trying to pull it"
    if ! remote "timeout 60 sudo docker pull $base >/dev/null 2>&1"; then
      echo "  cannot pull $base and it is not local." >&2
      echo "  Either pull it first (docker pull $base), or layer onto an image" >&2
      echo "  the host already has: scripts/deploy.sh --host … --base <image>" >&2
      exit 1
    fi
  fi
  echo "==> image ($image, the binary layered onto $base)"
  # BASE defaults to ghcr.io/mdpsec/huntdeck:latest, which the host keeps in
  # its image cache after the first pull; wrapping the running image instead
  # would add a layer on every deploy.
  # Detached: an image build takes minutes, the host runs a scanner beside it,
  # and an SSH session that drops mid-build kills the build with it. Measured
  # twice: "Connection to ... closed by remote host" at load average 5.6. The
  # build runs under nohup; this only waits for its log.
  remote "cd $dir && sudo rm -f /tmp/huntdeck-build.log"
  # The PID comes back so a build that dies is noticed in seconds: a Docker
  # failure does not start its last line with ERROR, and waiting out the whole
  # timeout to say so is six minutes of pretending to work.
  prod_args="--build-arg BASE=$base"
  [ -n "${APT_MIRROR:-}" ] && prod_args="$prod_args --build-arg APT_MIRROR=$APT_MIRROR"
  buildpid=$(remote "cd $dir && sudo nohup docker build -f Dockerfile.prod $prod_args -t $image . >/tmp/huntdeck-build.log 2>&1 & echo \$!" | tail -1)
  case "$buildpid" in (''|*[!0-9]*) buildpid="" ;; esac
  built=0
  for _ in $(seq 1 300); do
    sleep 2
    if remote_out "grep -q 'Successfully tagged $image' /tmp/huntdeck-build.log 2>/dev/null"; then
      built=1
      break
    fi
    if [ -n "$buildpid" ] && ! remote_out "sudo kill -0 $buildpid 2>/dev/null"; then
      remote_out "grep -q 'Successfully tagged $image' /tmp/huntdeck-build.log 2>/dev/null" && built=1
      break
    fi
    # The pid is not always captured (the shell that echoed it may be the one
    # that exits). Without it the loop cannot see a dead build and waits the
    # whole ten minutes in silence, which reads as a hung deploy.
    if [ -z "$buildpid" ] && [ $((_ % 30)) -eq 0 ]; then
      if remote_out '[ -n "$(find /tmp/huntdeck-build.log -mmin +2 2>/dev/null)" ]'; then
        remote_out "grep -q 'Successfully tagged $image' /tmp/huntdeck-build.log 2>/dev/null" && built=1
        if [ "$built" != 1 ]; then
          echo "  the build stopped writing two minutes ago; it died" >&2
        fi
        break
      fi
    fi
  done
  if [ "$built" != 1 ]; then
    echo "  the image build did not finish; its log:" >&2
    remote_out "tail -20 /tmp/huntdeck-build.log" >&2 || true
    exit 1
  fi
  echo "==> restart"
  remote "cd $dir && sudo docker compose up -d --pull never"
fi

echo "==> verify"
# A restarted container needs a moment: wait for healthz rather than racing it.
if [ -n "$dry" ]; then
  echo "  [dry-run] health check skipped"
elif remote 'for i in $(seq 1 30); do curl -fs -o /dev/null http://127.0.0.1:8080/healthz && exit 0; sleep 1; done; exit 1'; then
  echo "  healthz ok"
else
  echo "  healthz did not come up; see the logs above" >&2
  exit 1
fi
# The container is recreated, not hot-swapped: for a moment the old one still
# answers, and a smoke run against it fails 200-without-the-new-markup.
# Wait for the version this deploy built before judging anything else.
if [ -z "$dry" ]; then
  running=""
  for i in $(seq 1 30); do
    running=$(remote "cd $dir && sudo docker compose exec -T huntdeck /huntdeck version 2>/dev/null" | awk '{print $2}') || running=""
    [ "$running" = "$ver" ] && break
    sleep 1
  done
  if [ "$running" = "$ver" ]; then
    echo "  running $running"
  else
    echo "  the container runs ${running:-nothing} after 30s, this deploy built $ver" >&2
    exit 1
  fi
fi

# Then the whole of it, from the host: pages, API, login. The password is
# read from the host's .env there and never travels through here.
echo "==> smoke"
if [ -n "$dry" ]; then
  echo "  [dry-run] smoke skipped"
else
  # When the host keeps scheduled backups, the smoke run also insists one is
  # fresh: a schedule that silently stopped is the failure nobody sees.
  backups_flag=""
  if remote "[ -d $dir/data/backups ]"; then
    backups_flag="--backups $dir/data/backups"
  fi
  # The version matches before the HTTP side is ready: one deploy's smoke ran
  # seconds after the restart and reported thirteen 401s, which read as a broken
  # release. Wait on /healthz, which costs no login attempt - polling the login
  # form instead burnt the login budget and locked the smoke out.
  ready=""
  for i in $(seq 1 40); do
    code=$(remote "curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/healthz" 2>/dev/null) || code=""
    if [ "$code" = "200" ]; then
      ready=1
      break
    fi
    sleep 1
  done
  if [ -z "$ready" ]; then
    echo "  /healthz never answered 200; the smoke would only see a login page" >&2
    echo "  last answer: ${code:-none}" >&2
    exit 1
  fi
  # One settle, so the first authenticated request is not the one that races the
  # cookie key being written.
  sleep 2
  smoke_run() {
    remote "cd $dir && (set -a; . ./.env 2>/dev/null; set +a; HD_PASSWORD=\"\${HUNTDECK_PASSWORD:-}\" bash scripts/smoke.sh --url http://127.0.0.1:8080 $backups_flag)"
  }
  if smoke_run; then
    :
  else
    rc=$?
    # 255 is ssh giving up (a dropped transport, not a failed check): try once
    # more before blaming the deploy.
    if [ "$rc" = 255 ]; then
      echo "  the connection dropped mid-run; running the checks once more"
      smoke_run || { echo "  smoke failed after the deploy; the container is up, see the lines above" >&2; exit 1; }
    else
      echo "  smoke failed after the deploy; the container is up, see the lines above" >&2
      exit 1
    fi
  fi
fi

# What the deploy cost, as a report rather than a gate: a slow route here is
# worth knowing about at once, but a shared host with a noisy neighbour should
# not fail a deploy.
echo "==> performance"
if [ -n "$dry" ]; then
  echo "  [dry-run] benchmark skipped"
else
  # The warm-up takes about three seconds after a restart; measuring before it
  # finishes reads the warm-up's own work as the page's cost.
  remote "sleep 6" || true
  if remote "cd $dir && HD_PASSWORD= bash scripts/bench.sh --check" >/tmp/hd-bench.$$ 2>&1; then
    tail -1 /tmp/hd-bench.$$
  else
    grep -E "OVER|slower" /tmp/hd-bench.$$ | sed "s/^/  /" || true
    echo "  (a route is over its budget; this does not fail the deploy. The numbers are
  taken while the deploy is still running: re-run scripts/bench.sh once it settles
  before treating one as a regression.)" >&2
  fi
  rm -f /tmp/hd-bench.$$
  # Keep the record too: one file per deploy, the last ten kept, so two
  # releases can be compared instead of remembered.
  record="perf/$(date +%Y%m%dT%H%M%S).md"
  # The previous record, before this one is written: comparing is the only way a
  # number becomes a signal, and nobody opens perf/ by hand a week later.
  prev=$(remote "cd $dir && ls -t perf/*.md 2>/dev/null | head -1" | tr -d '\r')
  if remote "cd $dir && mkdir -p perf && bash scripts/perf-snapshot.sh -o $record >/dev/null 2>&1 && ls -t perf/*.md | tail -n +11 | xargs -r rm -f"; then
    echo "  record: $dir/$record"
    if [ -n "$prev" ]; then
      # Coverage first: a record that measures fewer routes than the last one is
      # not a faster release, it is a page nobody measures any more - and that is
      # silent unless someone counts. Deliberately dropping a page sets
      # PERF_FEWER_ROUTES_OK=1.
      # Digits only: a stray CR or a banner line over SSH turns [ -lt ] into a
      # status-2 error that aborts the deploy after a successful release.
      prev_n=$(remote "grep -cE '^/[^ ]+ +200/' '$prev' 2>/dev/null" | tr -dc '0-9' || true)
      new_n=$(remote "grep -cE '^/[^ ]+ +200/' '$record' 2>/dev/null" | tr -dc '0-9' || true)
      if [ -n "$prev_n" ] && [ -n "$new_n" ] && [ "$new_n" -lt "$prev_n" ]; then
        echo "  this record measures $new_n routes, the previous one $prev_n" >&2
        if [ "${PERF_FEWER_ROUTES_OK:-0}" != "1" ]; then
          echo "  !! a route dropped out of the measurement; set PERF_FEWER_ROUTES_OK=1" >&2
          echo "     if that page was removed on purpose" >&2
          exit 1
        fi
      fi
      echo "  against the previous record ($(basename "$prev")):"
      remote "cd $dir && python3 scripts/perf-diff.py '$prev' '$record' 2>/dev/null | head -6" | sed 's/^/    /' || true
    fi
  else
    echo "  (could not write the performance record)" >&2
  fi
fi
echo "done. logs: ssh $host 'cd $dir && sudo docker compose logs --tail 50'"
