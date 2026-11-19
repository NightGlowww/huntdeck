# AGENTS.md

Guidance for AI coding agents (and people) working with HuntDeck: installing
and running it for a user, and changing its code. Start with
[README.md](README.md); the reference is in [docs/](docs/).

## What HuntDeck is

A self-hosted, **single-container** Go application for bug bounty hunters. It
pulls programs and scope, **including private invites**, from HackerOne,
Bugcrowd, Intigriti and YesWeHack with the user's own credentials, stores
everything in embedded SQLite, tracks changes over time, collects public
program stats, and serves a web UI, a REST API and plain-text exports.

It has **no accounts, by design**: it's a personal tool reached only from the
user's own machine or private network. An optional single access password
(`HUNTDECK_PASSWORD`) puts a login page in front for a copy on a public
address; it adds no users, roles or tokens. Treat every HuntDeck database as
holding **confidential private-program data** (covered by the platforms'
terms) and every `.env` as holding live credentials.

## Installing it for a user

The README's "Deploy it with your AI agent" section is the checklist. In
short:

1. Needs Docker with Compose. Don't install system packages without asking.
2. `cp .env.example .env && chmod 600 .env`. Ask which platforms the user has
   and fill in only those variables ([docs/configuration.md](docs/configuration.md)).
   The user can also skip this and enter credentials later in Settings ›
   Accounts (saved encrypted).
3. `docker compose pull && docker compose up -d` (or `up -d --build`).
4. `curl -fsS http://127.0.0.1:8080/healthz` → `{"status":"ok"}`, then
   `curl -s http://127.0.0.1:8080/api/v1/status` for programs per platform.
5. Settings › Polling (`/settings`) shows whether each credential was
   accepted, or what the platform said.

Rules while doing it:

- **Never print, log, echo or repeat credentials**, TOTP secrets, session
  cookies or notification webhook URLs, and never commit `.env`, `data/` or
  any `*.db`. Read values from the user straight into `.env`.
- **Keep it on localhost.** The compose file publishes `127.0.0.1:8080`;
  change the port with `HUNTDECK_PUBLISH=127.0.0.1:<port>`. Never publish it
  on `0.0.0.0` or a public interface, and never put it behind a public
  reverse proxy or tunnel: anyone who reaches it can read the user's private
  programs. For other devices, use a private network (Tailscale, WireGuard)
  and add the hostname to `HUNTDECK_ALLOWED_HOSTS`.
- Don't use `network_mode: host`: the login browser needs the container's own
  network.
- Don't loosen the container's hardening (`read_only`, `cap_drop: [ALL]`,
  `no-new-privileges`, the non-root user).

## Running it

```sh
docker compose logs --tail 100 huntdeck          # what it's doing, errors
docker compose exec huntdeck /huntdeck status    # what's stored, recent polls
docker compose exec huntdeck /huntdeck queue     # targets ranked by "can I start today, what is it worth" (HuntQueue)
docker compose exec huntdeck /huntdeck doctor    # data directory, integrity, schema, key, disk, and how fresh the polls and queue are
docker compose exec huntdeck /huntdeck compact   # VACUUM: return the space deleted rows leave inside the file (stop HuntDeck first)
docker compose exec huntdeck /huntdeck poll --platform h1 --mode quick
docker compose exec huntdeck /huntdeck export wildcards
docker compose exec huntdeck /huntdeck backup --to /data/backups/manual.tar.gz   # db snapshot, key, sessions; reads it back to prove it opens
docker compose exec huntdeck /huntdeck --help    # every command
docker compose pull && docker compose up -d      # update (the database upgrades itself, after a backup)
```

The API is documented in the app (`/api`) and at `/api/v1/openapi.json`;
[docs/api.md](docs/api.md) has examples.

Data lives in the `/data` volume: `huntdeck.db`, `backups/` (taken before
every schema upgrade), `sessions/` (platform logins) and `secret.key` (the
key for credentials saved in Settings). A copy of the volume is a full
backup, and as sensitive as the credentials themselves. For a consistent
copy, stop the container first:

```sh
docker compose stop
docker run --rm --volumes-from "$(docker compose ps -aq huntdeck)" -v "$PWD":/backup \
  busybox tar czf /backup/huntdeck-backup.tgz -C /data .
docker compose start
```

Don't restart or recreate the container while a poll is running unless the
user asks: in `/api/v1/status`, a poll in `last_polls` with `"finished_at": null` is still running. Saving
credentials in Settings › Accounts restarts HuntDeck in place by itself,
once no poll is running.

### Troubleshooting

| Symptom | Cause and fix |
|---|---|
| `docker compose pull` is denied, or says there's no matching manifest for `linux/arm64` | Build it instead: `docker compose up -d --build` (the published image is amd64 only) |
| The build can't reach the network (`docker0 ... does not exist`) | Hosts without the default bridge: build with host networking, e.g. a `docker-compose.override.yml` with `build: {context: ., network: host}` under `huntdeck` |
| `421` and a page about `HUNTDECK_ALLOWED_HOSTS` | Opened by a hostname HuntDeck doesn't know: add it to `HUNTDECK_ALLOWED_HOSTS` in `.env`, then `docker compose up -d` |
| "starter seed not downloaded; starting empty" | No internet to GitHub, or the download failed. Harmless: it fills up from its own polls. `HUNTDECK_SEED_URL=off` silences it |
| Behind a proxy, a handful of wrong passwords locks everyone out | The login budget counts the proxy's address for every visitor: set `HUNTDECK_CLIENT_IP_HEADER` to the header your proxy fills in (`X-Real-IP`, `CF-Connecting-IP`) |
| A password is refused that works in the browser | A `$` or `#` in `.env` unquoted: wrap the value in single quotes (`BC_PASSWORD='pa$$word#1'`) |
| `permission denied` on a host directory (`HUNTDECK_DATA_PATH`) | It must be writable by UID 65532: `sudo chown -R 65532:65532 <dir>` |
| Bugcrowd blocked for hours | Its WAF; keep `BC_RATE` at 1 or lower |
| Intigriti's website login keeps failing | Its login page scores visitors with reCAPTCHA: use a residential `HUNTDECK_PROXY` (an `http://` one) and set `HUNTDECK_CHROME_TIMEZONE` to the proxy's country. `HUNTDECK_VNC=true` shows the login browser in Settings › Browser |
| Every platform fails at once with `proxyconnect tcp: ... connection refused` | `HUNTDECK_PROXY` is set and the proxy behind it is down; it applies to every platform request, so one dead proxy stops all polling. Check it (`curl -x $HUNTDECK_PROXY https://api.hackerone.com`), fix it, or unset the variable to go direct |
| A poll is "partial" | One platform request failed; nothing is removed on a partial poll, and the next one retries. The poll history in Settings says why |

## Changing the code

### Layout

```
cmd/huntdeck/          main + subcommand wiring only (no business logic)
cmd/huntdeck-mcp/      the MCP server over stdio (internal/mcpserver): tools over the REST API
internal/config/       env + file config, validation, secret redaction; which variables Settings can edit
internal/secretbox/    encrypts values saved in Settings (AES-GCM, key file in the data directory)
internal/overrides/    Settings > Accounts: values saved over the environment's, encrypted, write-only
internal/db/           SQLite open/pragmas, migrations runner, migrations/*.sql
internal/store/        ALL SQL lives here
internal/httpclient/   shared rate-limited, retrying, redacting HTTP client
internal/browser/      minimal Chrome driver (CDP over a pipe) for logins needing a real browser
internal/weblogin/     website logins in that browser: page-by-page sign-in, login history, TOTP
internal/sessionjar/   cookie jar that keeps a web login's session on disk across restarts
internal/platform/     Platform interface + one package per platform, plus fake/, independent/ (programs added by hand) and custom/ (custom sources)
internal/scope/        identifier parsing, target expansion, host matching
internal/totp/         RFC 6238 codes for platform logins
internal/humanize/     money and durations as pages show them
internal/logging/      the slog setup, with secrets redacted
internal/version/      the version stamped in at build time
internal/syncer/       poll orchestration + diff engine → changes
internal/scheduler/    runs quick/full polls on their intervals
internal/huntscore/    HuntScore: scores programs from stored data (no SQL, no I/O of its own)
internal/huntqueue/    HuntQueue: ranks targets by "can I start today, and what is it worth per hour" (deterministic, no LLM)
internal/notify/       notifications: Discord, Telegram, webhook; outbox with retries
internal/seedfetch/    a new install's one download of the starter seed
internal/web/          UI pages, REST/text API, host guard, templates/ and static/ (embedded)
<package>/testdata/    sanitised fixtures only, next to the package that uses them
```

### Commands

```sh
make build        # bin/huntdeck and bin/huntdeck-mcp (docs/mcp.md)
make test         # go test -race ./...
make lint         # go vet + golangci-lint
make vuln         # govulncheck
make check        # gofmt, vet+lint, race tests, govulncheck and i18n: every change must pass it
make i18n         # report interface copy the templates still write in English
make docker       # build the image
make run          # local server on 127.0.0.1:8080, data in ./data
scripts/deploy.sh --host <ssh-host> [--mode binary|full] [--dry-run]   # ship a build (docs/deploy.md)
node scripts/ui-audit.mjs --base URL --sizes 390,1280,1400 --out /tmp/ui   # screenshots for a visual check
HUNTDECK_PERF_DB=/path/to/copy.db go test ./internal/store/ -run TestPerf -v   # time the hot queries on real data
scripts/bench.sh [--check] [--cold]  # page latencies; --check enforces the budgets, --cold adds a restart's first hits (docs/performance.md)
scripts/perf-snapshot.sh -o FILE     # one record of pages + probes, to compare two releases
scripts/perf-diff.py A.md B.md       # that comparison, largest change first
```

Run `make check` before calling a change done, and `make docker` for changes
to the container. If you couldn't run something, say so. Neither lint tool has
to be installed: `make lint` says when golangci-lint is missing, and `make vuln`
runs govulncheck through `go run` when the binary isn't on the PATH (it then
needs the module proxy).

### Performance

The numbers, the budgets and the history are in
[docs/performance.md](docs/performance.md). The workflow they fit into:

- **After a change that touches SQL, indexes or templates:** `scripts/bench.sh --check`.
  It exits 1 when a page's warm time is over 50 ms or an API route's over 100 ms.
- **After a change that touches start-up, the warm-up or a memo:** run
  `scripts/bench.sh --cold --check` right after a restart (900 ms per route).
  A first hit is waiting on cold pages; what matters is that it does not grow.
- **Queries:** with `HUNTDECK_PERF_DB` set to an unpacked `huntdeck backup`
  snapshot, `go test ./internal/store/ -run TestPerf -v` times every read that has
  been slow or sits on a page's critical path. When one
  of them regresses, `EXPLAIN QUERY PLAN` on that copy: a `USE TEMP B-TREE FOR ORDER BY`
  over the page's filter has meant a missing index every time so far.
- **Every deploy writes a record** to `perf/` beside the compose file (the last ten
  are kept) and prints one line: `every route inside its budget`, or the routes
  that are over it. That line never fails a deploy. Compare two records with
  `scripts/perf-diff.py A.md B.md`.

A performance change is kept only with a before/after number from one of those
runs; a change that cannot show one is reverted (that is why the reader pool and
a board memoisation are not in the code).

Two checks guard the interface itself: `make i18n` reports interface copy a
template still writes in English (the scripts have their own small catalog and
a test that every message they ask for exists), and `TestPaletteContrast`
reads `static/css/00-theme.css` and fails when a palette's text drops below
WCAG AA (4.5:1) on the surfaces it sits on, in light or dark.

Two more guard the reference: `TestEnvVarsAreDocumented` fails when a variable
the config reads is missing from `docs/configuration.md` or `.env.example`, and
`TestEveryToolIsDocumented` fails when an MCP tool and `docs/mcp.md` disagree.

### Ground rules

1. **Never leak private data or secrets.** Don't log credentials, tokens,
   cookies or `Authorization` headers (use the redacting logger and HTTP
   client). Fixtures in `testdata/` are sanitised: fake handles, fake domains
   (`example.com`, `*.example.net`), fake IDs; never real platform
   responses. No telemetry, no CDN assets, no outbound calls except to
   configured platforms and notification channels.
2. **Never record a removal that didn't happen.** Programs and scope entries
   are only marked removed from a *complete* listing or fetch. Partial runs
   may add and update, never remove.
3. **Be polite to platforms.** All platform HTTP goes through
   `internal/httpclient`: per-platform rate limits, `429`/`Retry-After`,
   bounded retries with backoff and jitter, context cancellation. No
   unbounded retry loops, no package-level rate limiters or other global
   state.
4. **One container.** Nothing that needs a second service (no Postgres,
   Redis or external queue): SQLite is the database and the job queue.
5. **No accounts.** HuntDeck deliberately has no users, roles or per-user
   identity; don't add any. The gate is the optional single access password
   (`HUNTDECK_PASSWORD`, `internal/web/auth.go`): one password, one signed
   cookie, no identity behind it. `api_tokens` (migration 0065) adds one
   narrow exception for scripts: a token is another key to the **same** gate,
   carries no identity, is stored only as a SHA-256 digest, and is revoked from
   the API page. Adding a token must never grow into roles or scopes. Protection is where it's reachable plus the
   guards in `internal/web`: `hostGuard` (DNS rebinding),
   `http.CrossOriginProtection` (CSRF), and a strict CSP (HuntDeck's own
   scripts, styles and connections only), `no-referrer`, `frame-ancestors 'none'`.

### Go conventions

- The Go version pinned in `go.mod`; `gofmt`/`goimports` clean.
- Standard library and `golang.org/x/*` first; a new third-party module
  needs a one-line justification in the pull request. SQLite is
  `modernc.org/sqlite` (pure Go, `CGO_ENABLED=0`).
- `context.Context` first on anything that does I/O, and honoured. Wrap
  errors with context (`fmt.Errorf("hackerone: list programs page %d: %w", n, err)`);
  don't log *and* return the same error. Log with `log/slog`.
- Small files: split by topic before about 500 lines. Build anything two
  pages need once (a partial in `templates/partials/`, a shared script, a
  stylesheet in `static/css/`, joined in name order).
- No package-level mutable state and no `init()` side effects beyond
  registering migrations and templates; dependencies come in through
  constructors.
- Platform packages implement `platform.Platform` (plus optional interfaces
  such as `KeepAliver`, `StatsFetcher`, `CredentialReporter`) and **never**
  touch the database: they return plain structs, and `internal/syncer`
  persists and diffs.
- API: every route is listed once in `apiRoutes` (`internal/web/server.go`)
  and described in `internal/web/openapi.json`; responses are named types in
  `internal/web/apitypes.go`. The tests say what's missing.
- Database: SQL only in `internal/store`. Migrations are append-only: never
  edit one that has shipped. A new table or column is also listed in
  `internal/store/seedspec.go` as copied into seeds (public platform data
  only) or left out (anything of the user's); `TestSeedSpecCoversSchema`
  fails until it is. Timestamps in UTC, RFC 3339. One program's writes in
  one transaction.
- Tests are table-driven. Platform clients are tested against `httptest`
  servers replaying fixtures (pagination, 429, 5xx, auth failure, partial
  failure); the sync engine with `internal/platform/fake`, including "a
  second identical poll yields zero changes". Live tests against real
  platforms are opt-in (`HUNTDECK_LIVE_TESTS=1`) and never part of
  `make check`.

New platform: [docs/adding-a-platform.md](docs/adding-a-platform.md) lists
every place one is wired in; for programs only, a
[custom source](docs/custom-sources.md) needs no Go at all.

### Security checklist for every change

- [ ] No secrets, tokens, cookies or private program data in logs, errors
      shown in the UI, fixtures or commits.
- [ ] State-changing endpoints use POST (never GET), so cross-site protection
      applies; `hostGuard` and `CrossOriginProtection` aren't bypassed.
- [ ] User input reaches SQL only through parameters; template output goes
      through `html/template` escaping.
- [ ] New outbound destinations are documented and only contacted when the
      user configured them.
- [ ] The container still runs as non-root with a read-only root filesystem.

### Pull requests

Small and focused, with a clear description; update the README, `docs/`
and this file when you change behaviour, configuration, layout or
commands. Never include `.env`, databases, `data/`, real credentials or
real private-program data. Security problems go through
[SECURITY.md](SECURITY.md), not public issues.
