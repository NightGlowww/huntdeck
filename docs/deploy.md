# Deploying HuntDeck

Three shapes, from the least to the most work. All end with the same thing:
one container, a data volume, and a reverse proxy that terminates TLS.

## 1. The published image

The shortest path, and the one the README describes:

```sh
docker compose pull && docker compose up -d
```

`image: ghcr.io/mdpsec/huntdeck:latest` comes with Chrome (for website logins)
and noVNC (for watching them). Nothing needs a compiler.

## 2. Built on the host

For a fork, a patch, or a host that would rather build:

```sh
docker compose up -d --build
```

The build needs Docker Hub (base images), `proxy.golang.org` (modules),
`dl.google.com` (Chrome) and `github.com` (noVNC). Where one of those is slow
or blocked, two build arguments help:

```sh
docker compose build \
  --build-arg GOPROXY=https://goproxy.cn,direct \
  --build-arg WITH_NOVNC=0
```

`GOPROXY` picks the module proxy; `WITH_NOVNC=0` skips the browser viewer
(`HUNTDECK_VNC`), which nothing else depends on.

## 3. A binary built elsewhere, wrapped in the image already there

When the host cannot build at all (a mainland network where GitHub's release
CDN times out, say), compile on a machine that can and ship the binary:

```sh
scripts/deploy.sh --host prod-1 --mode binary
```

That script runs `go vet` and the tests, cross-compiles `linux/amd64` with
`CGO_ENABLED=0`, copies the binary and `Dockerfile.prod` to the host, builds
`huntdeck:local` **around the image the host already has** (so Chrome and the
fonts stay), restarts the service and waits for `/healthz`. Point the service
at that image once, with a `docker-compose.override.yml`:

```yaml
services:
  huntdeck:
    image: huntdeck:local
```

`--mode full` instead rsyncs the whole checkout and lets the host build
(`docker compose build`). `--dry-run` prints every command without running it.

## Before calling a build a release

The order matters: each step can fail the one after it, and the cheap ones
are first.

| Step | What it proves | Time |
|---|---|---|
| `make check` | gofmt, vet+lint, the race tests, govulncheck, i18n. The long one is `internal/web` under `-race` (about 4.5 minutes here) | ~6 min |
| `scripts/bench.sh --check` | every route inside 50 ms (pages) and 100 ms (API) on the target host | ~15 s |
| `make docker` | the image still builds: multi-stage, the browser, the noVNC assets, `/data` owned by 65532. Deploys that ship a binary never touch this, so it is the one that rots quietly | ~2 min, 1.2 GB |
| `scripts/deploy.sh --host HOST --mode binary` | the deploy itself: build, ship, restart, and the perf record it writes to `perf/` | ~40 s |
| `make smoke URL=…` | 21 checks against the running instance, after the restart it just did | ~10 s |
| `go run golang.org/x/tools/cmd/deadcode@latest -test ./…` | nothing is unreachable once tests count. Without `-test` it flags interface methods (`Secret.String`, `Secret.LogValue`) and test-support helpers, so read the list before deleting anything | ~40 s |
| `huntdeck doctor` on the host | schema, integrity, foreign keys, free space inside the file, **poll and queue freshness**, key, backups | ~2 s |

Two of those were added after they caught something: `bench --check` found a
route at 780 ms after an index was missing, and `make docker` had never run in
this tree before it was checked by hand.

A deploy is not a release until the smoke run after it passes: the restart is
the part that fails, and it fails after the script has already said done.

## When something is off, ask doctor first

```sh
docker compose exec huntdeck /huntdeck doctor
```

It looks at the data directory rather than the app: writable? schema version
against this binary (`up to date`, `migrates on next start`, or `newer than this
binary`), `secret.key` present and private, how many platform sessions and
pre-migration backups are there, and how much space is left. Every `FAIL` line
exits 1, so it can gate a deploy.

## Access control: pick one gate

HuntDeck has **no accounts** by design. There are two ways to keep a copy on a
public address private, and one of them is enough:

- **`HUNTDECK_PASSWORD`** (recommended): the app's own login page, one
  password, one signed cookie (30 days, 8 attempts per 10 minutes). Localized,
  themed, and it holds the session so the browser does not ask twice.
- **The proxy's HTTP Basic Auth** (1Panel's *网站 → 密码访问*, nginx
  `auth_basic`): one shared user for the whole vhost. It works, but it is the
  browser's native dialog: no styling, no language, and no way to sign out.

Running both means two prompts for one page. Choose one. If the image predates
the login page (`/login` answers 404), the proxy's Basic Auth is the only gate
that exists — set `HUNTDECK_PASSWORD` when you deploy a build that has it, then
turn the proxy gate off.

Keep the port published to `127.0.0.1` either way; the proxy is what faces the
internet.

## After a deploy

```sh
curl -fsS http://127.0.0.1:8080/healthz          # {"status":"ok"}
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/login   # 200 when a password is set
docker compose logs --tail 50                    # "database ready", "huntdeck started"
```

The database upgrades itself on start, writing a copy to `backups/` first.

**Rollback**: remove `docker-compose.override.yml` (or point `image:` back) and
`docker compose up -d --pull never`. The previous image is still on the host
until `docker image prune` reclaims it.

**Updating**: rebuild the same way — `scripts/deploy.sh` or
`docker compose up -d --build` — and the container is replaced; the data volume
is untouched.

## Checking what a host is running

`scripts/deploy.sh --host <ssh-host> --check` asks the instance which build it
is and prints the container's status, without building, restarting or touching
anything:

```
$ scripts/deploy.sh --host prod-1 --check
==> running on prod-1
huntdeck deploy-202611181914 (1aa6d33db261-dirty)
huntdeck-huntdeck-1 Up About a minute (healthy)
```

The version string is what `-ldflags -X .../version.Version` was set to at build
time: `deploy-<timestamp>` from this script, `dev` from a plain `make build`.

With `--expect <version>` it also fails (exit 1) when the host runs something
else, so a scheduled run can tell drift from a deploy that worked:

```
$ scripts/deploy.sh --host prod-1 --check --expect deploy-202611181920
==> running on prod-1
huntdeck deploy-202611181920 (1aa6d33db261-dirty)
huntdeck-huntdeck-1 Up 2 seconds (health: starting)
$ echo $?
0
```

## What keeps script kiddies out

Three layers, each cheap and each visible in the logs:

1. **The edge**: `limit_req` on `POST/GET /login` (12 requests a minute per
   address, burst 5, `429` after that) from
   `/opt/1panel/apps/openresty/openresty/conf/http.d/huntdeck-ratelimit.conf`,
   used by a `location = /login` block in the site's proxy config. Scans of
   `/login` cost the scanner, not the owner: the budget is per address.
2. **The app**: `HUNTDECK_PASSWORD`'s login counts wrong passwords per
   client address (8 per 10 minutes), and separately across all addresses
(20 per minute) so a distributed spray — or a proxy hiding everyone behind
one address — slows to a crawl without locking the owner out for long.
   `HUNTDECK_CLIENT_IP_HEADER=X-Real-IP` makes the per-address count see the
   real client behind 1Panel's proxy.
3. **The app's own guards**: the API and every export answer `401` (API) or a
   `303` to the login page (the page exports) without a session
   (`/api/v1/*`, `/api/v1/openapi.json`), `/healthz` is the only unauthenticated
   route, `hostGuard` refuses unknown Host headers (`421`), and every response
   carries CSP (no inline script without a nonce), `X-Frame-Options: DENY`,
   `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer` and HSTS.

### What to do about the panel

1Panel's own entrance (`ops.<domain>`) is a louder target than HuntDeck: keep
it on a non-default path, with a long password and 2FA, and restrict it to
your addresses in 1Panel → 面板设置 → 安全入口 / IP 白名单. Nothing in
HuntDeck can protect it for you.

### Checking it still holds

```sh
# 401 without a session, and only /healthz answers openly
curl -s -o /dev/null -w '%{http_code}\n' https://<host>/api/v1/status   # 401
curl -s -o /dev/null -w '%{http_code}\n' https://<host>/healthz         # 200

# guesses start failing at the edge after a handful
for i in $(seq 1 8); do curl -s -o /dev/null -w '%{http_code} ' -X POST -d 'password=x' https://<host>/login; done
```

### One command afterwards

`scripts/smoke.sh` checks a running instance the way a user meets it — health,
the login gate, **that every export is closed without a session** (the API
answers `401`, the page exports redirect), the API behind a session, the
pages, and the MCP server — and exits non-zero on the first thing that is
wrong:

```sh
HD_PASSWORD='…' scripts/smoke.sh --url https://huntdeck.example --mcp bin/huntdeck-mcp
# or: HD_PASSWORD='…' make smoke URL=https://huntdeck.example
```

`--backups DIR` adds one more check: a `huntdeck-backup-*.tar.gz` in `DIR`
newer than 30 hours. `scripts/deploy.sh` passes it when the host has that
directory, so a scheduler that quietly stopped — the failure nobody notices —
turns the next deploy red.

It prints one line per check (`ok` / `FAIL`), never the password. With
`--json` it prints a single object instead — `{"ok":…,"failed":…,"checks":[…]}` —
for a monitor to read, and still exits non-zero when anything failed. An
instance that doesn't answer at all is reported as failed checks, not as a
crash.

`scripts/deploy.sh` runs it for you at the end, on the host, with the instance's
own password read there (it never travels through the deploy): a deploy that
leaves a page or an API route broken exits non-zero instead of reporting
success. If ssh itself drops mid-run (exit 255, a flaky link rather than a
failing check), it runs the checks once more before giving up.

### Watching it from outside

The app's own channels can't tell you the app is down. `scripts/watch.sh` runs
the smoke checks from anywhere and alerts when the state changes — down once,
and again when it recovers (no repeats while it stays up or down):

```sh
# one run, cron-friendly: exit 0 healthy, 1 not
HD_PASSWORD='…' scripts/watch.sh --url https://huntdeck.example \
  --alert-webhook https://hooks.example/…

# keep watching in the foreground (every 5 minutes)
HD_PASSWORD='…' scripts/watch.sh --url https://huntdeck.example --every 300 \
  --alert-bark DEVICE_KEY --alert-serverchan SCT…
```

Targets: a webhook (`{text, content}` JSON, so Slack and Discord both read it),
Bark (with `HD_ALERT_BARK_SERVER` for a self-hosted one), or ServerChan. State
lives in `$TMPDIR/huntdeck-watch.state`, so a cron run every few minutes knows
whether it already told you.

`--backups DIR` (or `HD_ALERT_BACKUPS`) folds the backup check in: a stale or
missing archive alerts, and it is worded "needs attention" rather than "is
down" — the app is up, something else isn't.

`--alert-after N` waits for N bad runs in a row before saying anything, which
keeps a restart or a one-minute network blip from paging you; the recovery
message only arrives if a page was sent. `--alert-every N` then repeats the
warning every N bad runs while it stays down, so a long outage isn't forgotten
after its first message. `--state FILE` (or `HD_WATCH_STATE`) keeps the memory
elsewhere: one file per watched instance when a host watches several.

Cron example on the host itself:

```
*/5 * * * * cd /opt/huntdeck && set -a && . ./.env && set +a && HD_PASSWORD="$HUNTDECK_PASSWORD" \
  HD_ALERT_BARK=… scripts/watch.sh --url http://127.0.0.1:8080 >/var/log/huntdeck-watch.log 2>&1
```

## Backing it up

`huntdeck backup` writes one file holding what a restore needs: a **consistent**
copy of the database (`VACUUM INTO`, safe while the app runs), the key that
decrypts credentials saved in Settings, and the platform sessions
(`--no-sessions` leaves those out; they are live logins, so treat the file as
secret either way):

```sh
docker compose exec huntdeck /huntdeck backup --to /data/backups/manual.tar.gz
docker compose exec huntdeck /huntdeck backup          # names it itself
```

The archive holds `manifest.json` (version, time, whether sessions are in,
and what the snapshot holds: programs, scope entries, changes and polls),
`huntdeck.db`, `secret.key` when there is one, and `sessions/`. It needs no
stopped container, unlike the volume tarball above. `--keep N` deletes older
`huntdeck-backup-*.tar.gz` beside the new file, keeping the newest N.

Restoring is the other way: **stop the container first**, then

```sh
docker compose stop
docker compose run --rm huntdeck /huntdeck restore --from /data/backups/manual.tar.gz --force
docker compose start
```

Every restore also checks the database against those counts and refuses an
archive whose manifest and contents disagree — damage that would otherwise
surface only when the backup is needed.

`--force` is needed when a database is already there (a fresh directory needs
none). Two things are refused rather than half-done: a database whose schema
is **newer** than the binary's migrations (use a build at least as new as the
backup), and an archive holding no `huntdeck.db`. Files are unpacked into a
staging directory first and moved into place with the database last, so a
failed restore leaves the old one untouched. `--no-sessions` restores without
the platform logins, which means signing in to each platform again.

In the container the root filesystem is read-only: give `--data` a path under
`/data` (the volume), and the directory is created when missing.

`--dry-run` unpacks to a staging directory, reports the archive's schema version
and whether it carries the sessions, and writes nothing — the way to check a
backup before stopping the app.

### Automatic backups (1Panel's scheduler)

1Panel owns the schedule, the retention and the off-host upload; HuntDeck
supplies the one thing a file copy can't: a consistent snapshot. The script
`scripts/backup.sh` (on the host: `/opt/huntdeck/backup.sh`) asks the app for
one with `huntdeck backup`, then copies the archive to
`/opt/huntdeck/data/backups/`, a directory 1Panel can back up.

In 1Panel: **计划任务 → 创建任务 → Shell 脚本**, daily, running
`/opt/huntdeck/backup.sh`. Then, for an off-host copy, add a
**备份账号** (COS/OSS/S3) and a **文件备份** of `/opt/huntdeck/data/backups`.

A second task, weekly, runs `scripts/verify-backup.sh` (on the host:
`/opt/huntdeck/verify-backup.sh`): it takes the newest archive and runs
`restore --dry-run` against it, so a corrupt backup is found now rather than on
the day it is needed. Its exit code is the report the panel keeps.

Two things worth knowing, both learned the hard way:

- 1Panel's cron spec is **5 fields** (`15 3 * * *`). A 6-field spec is accepted
  by the database but never runs; the panel's own API rejects it outright
  with `expected exactly 5 fields`.
- Copying `/var/lib/docker/volumes/…` directly is a hot copy of a live SQLite
  database: its WAL can be mid-write. That is why the snapshot comes from
  inside the container.

## Performance

What the pages cost and how it was measured: [performance.md](performance.md).
`scripts/bench.sh` re-measures them on a running instance, and a deploy ends
with one `==> performance` line: `every route inside its budget`, or the routes
that are over it. That report never fails a deploy — jitter on a shared host
should not turn a working release red — so a red route there is a signal to run
`bench.sh --check` yourself. The same step writes one record per deploy to
`perf/` beside the compose file (the last ten are kept), so two releases can be
compared file against file instead of from memory.

## Compression

The vhost compresses text at the edge
(`/opt/1panel/www/sites/huntdeck/proxy/gzip.conf`): HTML, CSS, JS, JSON and
SVG over 1 KB. It matters most where the pages are largest — the Programs
list is 178 KB raw and about 28 KB on the wire.

```sh
curl -s -b jar -H 'Accept-Encoding: gzip' -o /dev/null -w '%{size_download}\n' \
  https://huntdeck.example/programs     # ~28k with gzip, 178k without
```

The file sits beside the site's `root.conf` (1Panel's proxy include), so it
applies to this site only; delete it and reload openresty to turn it off.

## Reaching the host when SSH is gone (learned the hard way)

A command of mine ran `rm -f /dev/null` on the production host. The device node
went away, and with it every process that redirects to it: sshd refused new
sessions (`kex_exchange_identification: Connection reset`), and the TAT agent
could not start a task (`START_FAILED`). The container kept serving — it was
already running — so the site stayed at 200 while the host was unreachable.

What worked, in order:

1. **`tccli lighthouse DescribeInstances`** — the box is a Lighthouse instance,
   not a CVM; `cvm DescribeInstances` returns nothing. Instance id, public IP
   and name came from there.
2. **`tccli tat RunCommand`** — the API is reachable (a first attempt returned a
   WAF HTML page; a retry worked), but the task came back `START_FAILED`
   because the agent needs `/dev/null` too.
3. **`tccli lighthouse RebootInstances --InstanceIds '["lhins-…"]'`** — this is
   what fixed it: `/dev` is devtmpfs, so the node is recreated at boot. The site
   was down for under a minute, came back 200, and `doctor` reported schema,
   integrity, foreign keys and freshness all ok.

The rule that follows: never put `rm` in the same line as a `/dev/...` path, and
keep the instance id written down somewhere that does not need the host.

