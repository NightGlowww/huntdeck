# HuntDeck — engineering / design gap audit

Scope: /Users/night/Workspaces/security_workspace/_tools/huntdeck (working tree at `1aa6d33` + uncommitted work).
Method: read of config/db/store/web/syncer/scheduler/browser/notifier/backup-restore/seed paths, docs, Dockerfile, Makefile; `go vet ./...`, `go test ./...`, targeted probes. No project file was modified.

Verdict: the code is unusually careful (cancellation, redaction, partial-poll removal rules, seed allow-list, CSP/CSRF/host guards are all real and tested). The gaps below are concentrated in *operational* surfaces — the release gate, the login gate, and the ability to see what a running instance is doing.

---

## P0

### 1. There is no CI, and the only release gate is red right now
**Gap.** `make check` is the whole quality gate, it is run by hand, and it currently fails. Nothing else (race detector, gosec/golangci, govulncheck, docker build) is enforced anywhere.

**Evidence.**
- `ls -la .github` → "No such file or directory"; `Makefile:28`: "Every check a change must pass, run locally (there's no hosted CI)."
- `go test ./...` → `--- FAIL: TestTemplatesAreTranslated ... templates/partials/accounts.html: Clear what is saved here, so this field falls back to your environment` (`internal/web/i18ncover_test.go:44-47`, template at `internal/web/templates/partials/accounts.html:31`).
- `make i18n` prints "english left: 0" for the same tree — the two i18n guards disagree, so the cheaper one gives false comfort.
- `Makefile:21`: golangci-lint is skipped with a message and exit 0 when missing; `Makefile:24-26`: govulncheck likewise (both binaries absent on this machine).
- A `v1.0.0` tag exists in a 3-commit history, i.e. the tag is not backed by a green suite.

**Why it matters.** A public repo that ships a self-hosted tool holding private-program data has exactly one automated defence here, and it does not exist; today's tree would ship a failing test suite, and unattended gosec/govulncheck means the "no secrets in logs / no SQL string building" rules are review-only.

**Fix.** Add `.github/workflows/ci.yml` (push, PR, tag) running `make fmtcheck`, `go vet ./...`, `go test -race ./...`, `golangci-lint run`, `govulncheck ./...`, and `docker build .`, with lint/vuln as hard steps; make it a required check. Fix the failing pair by adding the missing `zh` entry (or reusing the existing shorter title) for the accounts.html label so `make check` is green before the workflow lands.

### 2. The password gate can be bypassed per-IP, locked out globally, and grown without bound
**Gap.** Three defects in the same control: the per-IP budget keys on a raw address with no IPv6 aggregation; the failure map never evicts idle entries; and `POST /login` parses an unbounded body *before* the budget is consulted.

**Evidence.**
- `internal/web/auth.go:244-254` — `clientIP` returns `Header.Get(ClientIPHeader)` or the socket host verbatim; compare `internal/web/ratelimit.go:97-102`, which deliberately aggregates IPv6 to /64.
- `internal/web/auth.go:49-52` (`fails` map), `:120-132` (`tooMany` deletes only the *same* IP's stale entry), `:155-166` (`failed` appends). No eviction path anywhere; `succeeded` only clears the one IP.
- `internal/web/auth.go:39-40`: `sprayBudget = 20` wrong passwords/minute pause logins for *everyone*.
- `internal/web/login.go:35-41`: `r.ParseForm()` runs before `tooMany/spraying`; no `http.MaxBytesReader` on this route, while every other POST route sets one (`settings.go:173`, `channelform.go:181`, `ignoremany.go:15`, `api_match.go:29`).

**Why it matters.** `HUNTDECK_PASSWORD` exists precisely so a copy can sit on a public address (AGENTS.md). Over IPv6 that copy hands out 8 guesses per /128 from a single /64; because the global spray budget is only 20/min, one host can keep it saturated and lock the owner out indefinitely; each distinct address (or spoofed `HUNTDECK_CLIENT_IP_HEADER` value) leaves a permanent `fails` entry, so the gate itself is a memory-exhaustion vector. Parsing a 10 MB form before the budget check makes the only public endpoint a cheap CPU/alloc amplifier.

**Fix.** Normalise in `clientIP` exactly as `visitorLimiter.clientKey` does (`netip.ParseAddr` → `Unmap` → /64 for v6) and cap `fails` with the same idle-eviction scheme (`ratelimit.go:138-153`); put the budget check first and wrap the body in `http.MaxBytesReader(w, r.Body, 4<<10)` before `ParseForm`. Add tests for a rotating /64 and for map bounds.

---

## P1

### 3. No request log, no metrics, and a health check that only pings SQLite
**Gap.** Nothing observes a request end-to-end, and `/healthz` cannot tell "working" from "wedged".

**Evidence.**
- `internal/web/server.go:286-291`: the chain is `securityHeaders(compress(hostGuard(csrf(auth(readOnly(mux))))))` — no logging, metrics or recover middleware. `grep -rn 'metrics|pprof|expvar'` finds only unrelated locals; `grep 'recover()'` finds nothing.
- `internal/web/server.go:391-404`: health = `DB.Ping` with a 2 s timeout.
- Consequence: a poll stuck with a `running` row (`store/runs.go:67-90`), a dead notifier loop (`notify/notifier.go:50`), a wedged Chrome, or a full volume all keep returning `{"status":"ok"}`.

**Why it matters.** This is a single-container appliance with no shell and no APM; the container log plus `/healthz` are the entire diagnostic surface, and docs/performance.md's budgets are measured by a script, not by the running process. "Which route is slow" and "is polling still alive" are currently unanswerable without reading code.

**Fix.** Add one middleware that logs method, path, status, duration, response bytes and (redacted) client IP — Info for 5xx and >500 ms, Debug otherwise — and expose `/metrics` (expvar or hand-written text) with requests by route/status, p50/p95 latency, DB ping latency, age of the last successful poll per platform, and notify-outbox depth/age. Split health into `/healthz` (cheap, unauthenticated, for Docker) and `/readyz` (stuck polls, outbox age, disk free).

### 4. The login browser runs unsandboxed, with the key and database on the same writable volume
**Gap.** Chrome is started with `--no-sandbox` in the shipped container and keeps its profile (live platform cookies) inside `/data`, next to `huntdeck.db` and `secret.key`.

**Evidence.**
- `internal/browser/browser.go:186-189`: `if os.Geteuid() == 0 || os.Getenv("container") != "" || fileExists("/.dockerenv") { args = append(args, "--no-sandbox") }` — `/.dockerenv` always exists in the image (`Dockerfile:48-74`), so the flag is always on.
- `cmd/huntdeck/platforms.go:197-199`: profile = `<HUNTDECK_DATA_DIR>/chrome/browser`; that profile holds the H1/Intigriti/Bugcrowd session cookies (`internal/sessionjar/sessionjar.go:1-5`).
- `docker-compose.yml:20-27` hardens the app (read_only, cap_drop ALL, no-new-privileges, non-root) — none of which constrains a renderer running as the same uid with `/data` writable.

**Why it matters.** Login pages are third-party content (including reCAPTCHA and their analytics scripts); a renderer bug becomes direct read of the encrypted-credential key, the whole database and every platform session. `--no-sandbox` is the single largest attack-surface amplifier in the stack, and it is on by default.

**Fix.** Run Chrome as a second uid (e.g. 65533) with `--user-data-dir` on a tmpfs or a volume that uid owns, so the browser cannot read `secret.key`/`huntdeck.db`; only add `--no-sandbox` when `os.Geteuid() == 0`, and state the blast radius in `docs/web-ui.md` where VNC/browser mode is described.

### 5. `restore` leaves the old WAL in place and does not check for a running server
**Gap.** Restore replaces `huntdeck.db` but never removes `huntdeck.db-wal`/`-shm` at the destination, and has no lock or handshake with a live `serve`.

**Evidence.**
- `cmd/huntdeck/restore.go:96-114`: only `secret.key`, `sessions` and `huntdeck.db` are touched; the WAL companions are not mentioned anywhere in the file.
- `cmd/huntdeck/restore.go:36-51`: the only guard is "stop HuntDeck first" in the help text; there is no PID file, lock or port probe.
- `cmd/huntdeck/restore_test.go` round-trips a cleanly closed database, so the crash case is untested.

**Why it matters.** If the previous process died without a clean close (Docker's 10 s stop grace vs. a long poll, OOM, power loss), a WAL belonging to the *replaced* file remains; SQLite will attempt recovery of it against the restored database, which mixes pages from two databases (silent corruption or "database disk image is malformed"). Restoring into a running server instead splits writes between an unlinked inode and the new file, so the server keeps serving pre-restore data and writes a WAL nobody will open again.

**Fix.** Before the rename: refuse when `<dst>-wal`/`<dst>-shm` exist unless `--force`, then delete them (after `PRAGMA quick_check` on the staged DB); take an exclusive `flock` on `<data>/huntdeck.lock` that `serve` holds for its lifetime and that `restore` requires, so "stop HuntDeck first" is enforced rather than documented.

### 6. Migrations are append-only by convention only, and the upgrade path is untested with data
**Gap.** Nothing records or verifies a migration's contents, and no test upgrades a populated database across the embedded chain.

**Evidence.**
- `internal/db/migrate.go:84-91`: the only consistency check is `MAX(version)` vs. `len(migs)`; `:113-135` records `version/name/applied_at` only — no hash of the SQL. Editing a shipped file changes nothing for an already-migrated DB and silently changes what a fresh one gets.
- `internal/db/db_test.go:90-179` exercises synthetic `0001/0002` migrations only; the sole populated-upgrade test is one migration short (`internal/store/seed_test.go` around :224-260). `internal/db/migrations/0032_web_runs.sql` rebuilds `poll_runs` with a `CREATE TEMP TABLE`/`DROP TABLE`/`UPDATE ... FROM` dance that never runs against real rows in any test.

**Why it matters.** AGENTS.md's "never edit a shipped migration" is the load-bearing rule for every user's upgrade; an accidental edit or a hand-patched install produces schema drift whose first symptom is a query error weeks later, after the pre-migration backup has rotated away.

**Fix.** Store `sha256(sql)` per version in `schema_migrations` and refuse to start (or warn naming file/version) when a shipped migration's hash differs; add a test that builds a v(N-3) database with representative rows, applies the current chain, and asserts row counts + `PRAGMA foreign_key_check` + the 0032 link-restoration invariant.

### 7. The API has no machine-readable errors, silently clamps bad input, and its spec is stale about auth
**Gap.** The documented API contract (docs/api.md, the in-app API page, the MCP server) cannot express failure in a parseable way, and the spec contradicts a supported mode.

**Evidence.**
- `internal/web/api.go:18-34`: the only error shape is `{"error": "<sentence>"}`; every handler string-builds its message (`api.go:224-237`, `api_programs.go`, `api_match.go`).
- Wrong method on an API path falls through to Go's mux: plain-text `405 Method Not Allowed` (routes are registered method-scoped, `server.go:302-327`; no `/api/` 405 handler).
- `internal/web/api.go:258-264`: `clampInt` turns `limit=abc` into the default and `limit=99999` into the maximum — typos look like success.
- `internal/web/openapi.json:5`: `"version": "1"` is a constant, not the build version; `:6`: "There is no login and no token", while `HUNTDECK_PASSWORD` makes `/api/*` answer 401 (`internal/web/auth.go:197-200`); no v1 operation is marked `deprecated` although v2 exists (`api_v2.go:1-15`).

**Why it matters.** The stated consumers are scripts and an MCP agent; silent clamping returns wrong data instead of an error, and string-matching `"platform not configured"` is the only way to distinguish a 404 from a 503.

**Fix.** Add `code` (and `param` where relevant) to `apiErrorJSON` and use it in every handler, add a JSON 404/405 fallback under `/api/`, return 400 for unparseable/out-of-range `limit`/`offset`, stamp `info.version` from `internal/version` with a note on the password/401 mode, and mark v1 `deprecated: true`.

### 8. Nothing bounds database growth; deleted rows never return space
**Gap.** History tables grow forever and the file is never compacted or measured.

**Evidence.**
- `grep -rn 'DELETE FROM' internal/store` shows pruning only for `notify_outbox`, `notify_pending`, `program_thanks`, `program_tags`, `program_notes`; there is no retention for `changes`, `poll_runs`, `program_values`, `disclosed_reports`, `resolved_reports`, `accepted_reports`.
- No `VACUUM`/`PRAGMA optimize`/incremental-vacuum on the live DB — `VACUUM` appears only for backups (`internal/db/migrate.go:144-155`, `cmd/huntdeck/backup.go:128`) and the seed file (`internal/store/seed.go:71`).
- `cmd/huntdeck/doctor.go:142-153` reports free *disk*; nothing reports DB size, page count or freelist.

**Why it matters.** Four platforms polled hourly plus a change row per scope/policy/status delta for years, in a volume that only grows and never gives pages back, on a host where "disk full" is (per doctor's own comment) "the failure that looks like everything else".

**Fix.** Add a retention job with explicit, configurable windows (e.g. `changes` 24 months, `poll_runs` 12 months, default off for existing installs) and a `huntdeck compact` command running `VACUUM` (plus `PRAGMA optimize` after large writes); surface `page_count`, `freelist_count` and file size in `doctor` and `/api/v1/status`.

---

## P2

### 9. Open redirect through the login return path (`next=/\evil.example`)
**Gap.** `safeNext` rejects `//host` and absolute URLs but accepts a backslash, which browsers treat as `/` for http(s).

**Evidence.** `internal/web/auth.go:258-266`. Probe (added and removed a temporary `zz_probe_test.go`): `safeNext("/\evil.example/x")` → `"/\evil.example/x"`; `safeNext("/%5Cevil.example")` → `"/%5Cevil.example"`. `TestSafeNext` (`internal/web/auth_test.go:84-97`) has no backslash/encoded case. Reachable from `GET /login?next=…` (`login.go:17-27`) and the POST handler (`login.go:39`), then used in `http.Redirect` (`login.go:58`).

**Why it matters.** A trusted HuntDeck URL becomes a post-login hop to an attacker site — phish-bait on the one page the user is already primed to trust, and it defeats the stated intent of the guard.

**Fix.** Reject any `next` containing `\`, NUL or `%5c` (and `%00`), or normalise backslashes to `/` before parsing and require `u.Host == "" && u.Scheme == ""`; add those payloads to `TestSafeNext`.

### 10. `huntdeck doctor` can report "no problems found" on a corrupt database
**Gap.** The command advertised as the first diagnostic step checks presence, writability, schema version, key mode and disk space — but never the database's own integrity.

**Evidence.** `cmd/huntdeck/doctor.go:164-201`: `MAX(version)`, `PRAGMA journal_mode`, and a `sqlite_master` count; no `PRAGMA quick_check`/`integrity_check`, no `foreign_key_check`, no page/freelist figures, no warning when `-wal`/`-shm` are present (the state restore must not ignore, see item 5). `doctor.go:17-18` promises "the check to run first when something is off".

**Why it matters.** SQLite corruption is silent until a query fails; the one tool a user is told to run will tell them everything is fine, and the next step is usually a needless restore.

**Fix.** Run `PRAGMA quick_check` (bounded by a context timeout) and `foreign_key_check` on request, print `page_count`/`freelist_count`/`journal_mode`/WAL presence and file size, and exit non-zero on any finding.

### 11. Seed import has no size ceiling and its authenticity rests on a same-host digest
**Gap.** `unpack` copies the seed with no limit, and the download is verified against a `.sha256` fetched from the same origin.

**Evidence.** `internal/store/seed.go:216-235`: `io.Copy(w, r)` with an explicit `//nolint:gosec` comment declining a bound; `ImportSeed` (`:93-120`) runs on every start until a seed loads (`cmd/huntdeck/seed.go:74-110`). `internal/seedfetch/seedfetch.go:37-63`: digest read from `url+".sha256"` on the same host, with a race-tolerant second read.

**Why it matters.** A gzip bomb or an oversized `seed.db.gz` dropped into `/data` (or served by a compromised release) fills the volume before the app can start, and the checksum only protects against truncation — not against a replaced seed, whose rows become data the user sees.

**Fix.** Cap `ImportSeed`'s unpack at `seedfetch.MaxSeedBytes` (1 GiB) with a clear message, and record the loaded digest in `settings` (and log it) so a later seed can be compared; publish the expected digest with the release and allow pinning it via `HUNTDECK_SEED_SHA256`.

### 12. No panic containment: a handler bug drops the connection instead of returning 500
**Gap.** There is no recover middleware; `net/http`'s per-connection recovery closes the connection and writes the stack to `srv.ErrorLog` with no request context.

**Evidence.** `grep -rn 'recover()' internal cmd` → nothing; `internal/web/server.go:286-291` is the complete middleware chain; `NewServer` sets `ErrorLog` (`server.go:339`) but nothing else.

**Why it matters.** Any nil dereference or template panic (a new template func, a missing map key) is invisible to the user as an error page and hard to attribute in the log; the API variant violates the documented JSON error contract.

**Fix.** Wrap the mux in a `recover` middleware that logs method/path/stack and renders the themed 500 page (or `writeJSONError` under `/api/`), with a test that a panicking handler yields 500.

### 13. Cross-process writers rely on a 5 s busy timeout, and the lease error is matched by string
**Gap.** The documented CLI usage runs a second process against the same database, but writes have no busy retry and the poll-lease violation is detected by substring.

**Evidence.** `internal/store/store.go:106-125` (`Write` begins/commits, no retry or error classification); `internal/db/db.go:29-34` (only `busy_timeout(5000)`); `internal/store/runs.go:77-85`: `if strings.Contains(err.Error(), "UNIQUE constraint failed") { return ErrRunInProgress }` — any other UNIQUE failure on that INSERT becomes "a poll is already running". AGENTS.md tells users to run `docker compose exec huntdeck /huntdeck poll`/`seed export` beside the server. No store or syncer test uses goroutines (`grep 'go func' internal/store/*_test.go internal/syncer/*_test.go` → none), so the busy path and the two-writer case are untested.

**Why it matters.** A CLI command can fail with "database is locked" mid-way while a poll writes (leaving a partially-applied command and a confusing message), and a future UNIQUE index on `poll_runs` would silently misreport lease conflicts.

**Fix.** Classify `SQLITE_BUSY`/`SQLITE_LOCKED` from the driver error and retry writes with bounded backoff; match the lease with `errors.As` on the driver's constraint error (or check `RunningRun` inside the same transaction) instead of a substring; add a concurrent-writer test.

---

## Notes (not ranked)
- Positives worth keeping: `config.Overlay` correctly hides `NAME_FILE` when a value is saved in Settings (`internal/config/editable.go:108-133`); `syncer`'s partial-poll and mass-removal guards are tested (`syncer_test.go:74-221`); `secret.key`/DB/backup modes are 0600 via `privateFiles()` umask 077 and verified (`TestOpenMakesTheDatabasePrivate`); `httpclient` redacts URLs and scrubs `url.Error`; `notify` outbox is at-least-once with per-part progress.
- `docker-compose.yml` has no `pids_limit`/`mem_limit` for a container that runs Chrome; worth adding if the image is ever run on a shared host.
- The published image is `linux/amd64` only (`Makefile:56-66`) and the Chrome .deb is fetched unpinned from `dl.google.com` at build time (`Dockerfile:50-58`) while noVNC is hash-pinned — asymmetric supply-chain posture, and no automated release path (manual `make release-image`).
