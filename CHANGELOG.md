# Changelog

All notable changes to HuntDeck. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[Semantic Versioning](https://semver.org/): the API under `/api/v1` and
`/api/v2`, the configuration variables and the database (upgraded in place on
start) stay compatible within a major version.

## [Unreleased]

- Opening `/programs/<platform>`, with or without a trailing slash, redirects
  to the Programs list filtered to that platform.
- HuntDeck Public has a shorter banner with a single self-hosting link.
  Feedback and the optional X account link are in the sidebar.

## v1.3.0 (2026-11-19)

### Added

- **Policy extraction with a model, when one is configured**: `huntdeck ai-extract`
  (or the button in Settings › Polling) asks an OpenAI-compatible endpoint for the
  rules a program's policy states, caches the answer against the policy's hash so
  the same text is never sent twice, and shows it beside the rules quoted from the
  policy — on the program page and as `ai_rules` in the API and the MCP tool. No
  page render calls a model; with no endpoint set, nothing changes.
- **Optional capabilities in Settings**, so an off-by-default feature is visible
  where a person looks: on, off, which model, how many readings are cached.
- **A text-size setting** (small, standard, large, extra large) that scales type
  rather than the layout.
- `scripts/gates.sh` and `scripts/restore-drill.sh`: the first proves the shell
  gates fail when they should, the second restores a backup into a scratch
  directory, runs `doctor` on it and compares counts with the live database.

### Fixed

- **Smoke passed on any 200.** The page checks now look for each page's own
  `data-page` marker, so a login page served for every route, or an empty shell,
  fails. A fresh install with no platforms is no longer reported as broken.
- **bench passed on an unanswered route.** A refused connection takes no time;
  `--check` counts routes that did not answer and exits 1.
- **A deploy that shipped nothing passed.** The version this run built is waited
  for and asserted against the running container.
- **English inside the Chinese interface**: the Stats cells ("32d to decision",
  "since Oct 28", an English month in the collecting line), the Settings sign-in
  column ("accepted at the last poll") and the program policy note. Text is now
  produced in templates from translation keys, which is a written rule in
  `AGENTS.md`.
- The API page claimed there are no tokens, and the token endpoints were
  registered outside `apiRoutes`, invisible to the page, the spec and every guard.
- The copy and download actions on the Changes page sat on different baselines.
- The board's queue box showed five rows of one program.

## v1.2.0 (2026-11-19)

The 1.1.1-1.1.3 tags were patch releases the same day; their fixes are below.

### Added

- **API tokens** for scripts (`/api` → API tokens): a bearer token is another
  key to the same password gate, stored only as a SHA-256 digest, shown once
  and revocable. It carries no identity, so it never grows into accounts or
  scopes.
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

- A table wider than its column could not be scrolled below 1601 px: the wrap
  had `overflow-x: clip`, which also removed the overflow from the page, so the
  poll history was cut off with no way to reach its right side.
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
## v1.3.0 (2026-11-25)

- Email › HuntMail › Overview lists the **top senders** of the last 7 days
  (HuntMail 1.6 or later): how much each site sent, how much of
  it was newsletters and its size, with **Block newsletters** on its row,
  or **Unblock** on one a block already covers. Beside the blocks where
  there's room.
- HuntDeck sets up and updates to HuntMail 1.6.0: its top senders, and
  fewer false codes and links (numbers in postal addresses aren't codes;
  verify and reset links need account wording).

## v1.2.0 (2026-11-24)

- Everything about HuntMail is on one page, **Email › HuntMail**, in two
  tabs: **Overview** (its numbers and your blocks) and **Setup** (its
  address, key and domains, and setting it up on Cloudflare or updating
  it). Settings no longer has an Email tab; its old addresses lead to the
  new one.
- Email: mail a platform passes on from its own domain (HackerOne's
  `@wearehackerone.com` forwarder, and Bugcrowd's, Intigriti's and
  YesWeHack's aliases) is guessed to be for the program its sender's name
  starts ("Acme" for that platform's Acme Corp), or, when the relay names
  the real sender in the subject as Bugcrowd's does, the program whose
  scope has that sender's domain.
- HuntDeck sets up and updates to HuntMail 1.5.0, whose search also
  matches the address mail arrived at, so mail forwarded in (which keeps
  the To it was first sent to) is found by the address it came to.

## v1.1.0 (2026-11-24)

- **Reports**: your own reports on HackerOne, Bugcrowd and Intigriti, read
  with the logins HuntDeck already has (titles, states, severities, dates
  and bounties; never write-ups or comments), plus reports added by hand.
  States are the same on every platform; tiles and filters show what needs
  your reply and what has gone quiet; a report's page has its timeline, and
  you can correct, snooze or mute one. Save view, My views, Customize
  columns and CSV work there as on Programs.
- **Analytics** (under Reports): acceptance and duplicate rates, reports and
  earnings by month, by severity and by platform, how triage treated the
  severities you submitted, your times against the ones programs publish,
  and your top programs, over any range. Customize arranges its boxes as
  on the Hunt board.
- A program's Overview shows your reports there, with your times against
  the program's.
- Notifications for your reports: a bounty, a new state, a program waiting
  on you (on for new channels), a severity change and a new report found.
- API: `GET /api/v1/reports` and `GET /api/v1/reports/analytics`. Command
  line: `huntdeck reports`.
- Settings › Polling: a switch per platform for reading your reports.
  Settings › Reports: after how many days without a word from the program
  a report has gone quiet (14 by default).
- **Email**: with [HuntMail](https://github.com/mdpsec/huntmail), a
  catch-all inbox on your own Cloudflare account, an Email page lists your
  mail with each program, the one-time code and links ready to copy, and
  each program gets an Email tab that makes fresh addresses for it and shows
  their mail. Mail is shown as text; HuntMail's own view opens in a new tab.
  Mail sent to other addresses names the program its sender's domain is in
  the scope of, as a guess, and without a code, the verify, reset, sign-in
  or invitation link it's about. On a wide screen a message opens beside the
  list (j and k move through it, o opens one). Page through it 30, 50 or 100
  at a time, or keep to one of HuntMail's domains; the **HuntMail** page
  shows its numbers, emails per day and mail by domain. HuntDeck sets
  HuntMail up on your Cloudflare account itself (Settings › Email: a domain
  on Cloudflare and an API token, used once and never kept), and updates it
  the same way; or enter an existing one's address and key there
  (`HUNTMAIL_URL`, `HUNTMAIL_API_KEY`, and `HUNTMAIL_DOMAINS` for the
  domains new addresses go on). With a HuntMail that tells newsletters
  apart, the Email page and the programs' Email tabs leave newsletters out:
  **Mail**, **Newsletters** and **All** switch between the rest, just them
  and everything. Blocks (on the HuntMail page, and a Block menu on every
  message) have HuntMail turn away mail from a domain (only its newsletters,
  or all of it), to an address, or to a program's addresses, as it arrives.
  HuntDeck now sets up HuntMail 1.4.0, which has both, and sorts a message's
  links (content first, the sender's own social, legal and unsubscribe links
  folded away). Mail to an address you made by hand names its program when
  the address starts with the program's name, or the sender's domain is
  named as the program is.
- The sidebar folds to its icons (the arrow at its bottom), and stays
  folded in that browser.
- Boxes side by side end level (Settings › Accounts and Display, the
  notification channel form), and form fields line up by their labels,
  with any help under the field. The channel form's event choices fill
  their rows on wide screens.

## v1.0.1 (2026-11-21)

- Phones: the Menu button's page list shows the pages' names again, not
  only their icons.
- `/programs/<platform>` (such as `/programs/hackerone/`) opens the
  Programs list narrowed to that platform, instead of a "not found" page.
- The Add program form's seed checkbox reads "Include in seeds": it has
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
