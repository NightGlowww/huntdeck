# Changelog

All notable changes to HuntDeck. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[Semantic Versioning](https://semver.org/): the API under `/api/v1` and
`/api/v2`, the configuration variables and the database (upgraded in place on
start) stay compatible within a major version.

## [Unreleased]

### Added

- **HuntQueue**: targets ranked by whether they can start today and what the
  work is worth per hour, rebuilt every half hour. A `/queue` page with
  combinable filter chips, `huntdeck queue` on the command line,
  `/api/v1/queue` and an MCP tool; click a score (or pass `explain`) for the
  factor breakdown, and what the queue keeps out is listed with its reason
  rather than hidden. A snapshot of the day's scores is written once a day,
  which is what `huntdeck backtest` counts its cuts from.
- **An MCP server** over stdio (`huntdeck-mcp`): sixteen tools covering
  programs, targets, scope checks, changes, disclosed reports and the queue,
  with projections, paging and documented failure semantics.
- **An optional single access password** (`HUNTDECK_PASSWORD`) in front of
  the whole app, with a per-IP login budget and `HUNTDECK_CLIENT_IP_HEADER`
  for copies behind a proxy.
- **A Chinese interface** beside the English one, switchable in the sidebar
  and remembered; a test keeps every template string translated.
- **Maintenance commands**: `doctor` (integrity, schema, backups, and how
  fresh the polls, the queue and the score history are), `backup` (which
  reads its own archive back before trusting it), `restore` with
  `--dry-run`, `compact`, `backtest` and `ai-check`.
- `/metrics` with Prometheus counters for this process, behind the login.
- API program answers take `parts=` and page the scope, so a tool can read a
  program's facts, brief and rules in 64 KB instead of the full 155 KB.
- Guides and the reviews behind them: a daily-use order for the pages, a
  deploy runbook with a release checklist, a performance reference, and four
  independent audits with every finding resolved or accepted in writing —
  plus tests that keep the docs honest (every route, page, command, tool and
  source path the docs name is checked).

### Changed

- `/api/v1/queue` honours the page's filters (`platform`, `kind`,
  `added`, `paying`, `one`); a window that does not parse is a 400, not a
  silently ignored filter.
- The queue's quality input subtracts freshness's weighted contribution
  rather than its raw part, so a program whose strength is fresh scope is no
  longer over-penalised (migration 0064 stores the contribution per row).
- Payouts in other currencies are converted to US dollars with a fixed table
  before they are compared; the page says so, and says what happens to a
  currency the table does not know.
- A build from a tree with uncommitted work says how many files were pending,
  so two dirty builds from one commit are distinguishable.
- Deploys run the checks first and write a performance record beside the
  compose file.

### Fixed

- The queue's second page was unreachable: its pager sat inside a branch that
  is false on every normal day.
- `/queue?one=1&added=7d` returned 500 after "one per program" moved into
  Go without its join.
- "One per program" returned every tied row (574 rows over 114 programs);
  the tie-break is now deterministic.
- The board's footer claimed "40 targets shown" over 12 rows; it now says
  how many match the filter.
- Sign out, and the language switch, did not exist on a phone: the sidebar's
  footer was hidden below 700 px and the open menu brought back only the
  navigation.
- Relative times froze on an open page (the top bar's "Updated 5m ago", the
  queue's build time, every WHEN cell); they tick now, and a test fails if a
  relative time is printed without its timestamp.
- The light palette dropped below WCAG AA on platform tags and status chips,
  and input borders below 3:1; both measured and corrected.
- At 400% zoom the board and Changes scrolled sideways; the headers wrap and
  the pages fit their width.
- Screen-reader fixes: filter state uses `aria-current` instead of
  `aria-pressed` on links, 119 decorative icons are marked hidden, and the
  accessible names the page gave controls are translated.
- Policy extraction lost a program's real deadline when a response-time
  sentence came first, and produced invalid UTF-8 on non-ASCII policy text.
- The AI client accepted `null` as a successful answer and miscounted
  braces inside strings.
- `robots.txt`, the sitemap and `security.txt` answered with a login
  redirect; a crawler now reads `Disallow: /` instead of the login page.
- `/notifications` was a 404 (the page lives under `/settings/`), and the
  deliveries panel hid its own filters when no message had been sent yet.
- A program's "show all" scope rendered every entry; it stops at 2,000 and
  points at the export, and the client-side filter says how many rows remain.
- The notifications channel filter could not be applied without JavaScript.
- The `?` shortcut list the search box promised now opens, and `g` then a
  letter navigates, as the keyboard documentation had claimed all along.

### Removed

- A dead `missingScoreDay` helper and its test: the daily snapshot is driven
  by the server's ticker, which the helper's existence obscured.
- A stray 30 MB cross-compiled binary from the tree; `.gitignore` covers
  build outputs now.

  always covered every seed.

## v1.0.0 (2026-11-13)

The first public release.

### Platforms

- HackerOne (official Hacker API, plus its public program pages for stats;
  optional website login for private programs' stats and invitations),
  Bugcrowd, Intigriti and YesWeHack, each with your own credentials,
  **private invites included**. Logins with TOTP run in a built-in browser
  where a platform needs one; sessions are kept across restarts.
- Programs added by hand (`independent`), and custom sources: a script of
  yours writes any other platform's programs and scope as JSON.
- Polite polling: per-platform rate limits, `Retry-After`, bounded retries,
  quick polls every 15 minutes and full ones every 6 hours (editable in
  Settings), and a removal is only ever recorded from a complete listing.

### Tracking

- Scope and its history: what was added, removed or edited and when (dated
  from the platforms' own scope history where they have one), rules and
  bounty table changes with word-level diffs, status changes, announcements.
- Public program stats on every platform: bounties paid, reports received
  and valid in 90 days, average bounty, response times, hunters and
  leaderboards, Hacktivity and CrowdStream reports, with daily history.
  Estimates are marked as such; the Methodology page explains each number.
- HuntScore: which programs are best to hunt now, with its reasons.

### Web UI

- Hunt board (fresh attack surface, the week's scope changes, top picks,
  invites), Programs and Invites with a filter rail, views and column
  picker, program pages (overview, scope, policy, stats, changes, notes),
  Targets, Changes, Disclosed, Stats, the scope check, and Settings.
- Kept in the browser: a watchlist, notes on programs, named saved views,
  "new since your last visit" marks. Keyboard shortcuts (`?` lists them).
- Light and dark themes in three palettes; phone-friendly cards.

### Exports and API

- Plain-text target lists for recon tools, Burp, ZAP and Caido scope files,
  CSV, Atom feeds of changes, a bulk scope check for tool output.
- A REST API described by OpenAPI 3.1 (`/api/v1/openapi.json`) and an API
  page that sends requests for you. `/api/v2` adds, for each number, whether
  it's an estimate, where it comes from and when it last changed; program
  lists and pages, invites and `GET /api/v1/match` answer conditional
  requests (ETag, 304); `since=` lists only what changed.

### Notifications

- Discord, Telegram and webhooks, with rules per channel (event kinds,
  platforms, bounty and scope filters, tags), instant or as a daily digest,
  through an outbox that retries.

### Running it

- One container (Docker Compose or `docker run`), SQLite inside, a
  read-only root filesystem, non-root. The published image is linux/amd64;
  on arm64 (Apple Silicon, Raspberry Pi, ARM servers) build it with
  `docker compose up -d --build`.
- A new install loads the public starter seed on its first start, once, so
  it begins with months of history for the public programs
  (`HUNTDECK_SEED_URL=off` to start empty).