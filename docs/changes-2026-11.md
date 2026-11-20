# What changed in November 2026

A record of the work behind the current tree: what was wrong, what changed, and
how it was checked. Written for the next person (or the next agent) rather than
for a changelog parser.

## Interface

| Change | Why | How it was checked |
|---|---|---|
| The Clear action beside a saved credential is a small ghost button, not a bare checkbox; the value's source is a quiet note rather than a badge | a native checkbox with a two-word label reads as a form control, not an action | screenshots at 1440 px, before and after |
| A form on its way disables its button and shows a spinner; long actions say what they are doing (`Accepting…`, `Sending…`, `Polling…`) | an invite can take 40 s and the button used to claim it was ready after 8 s | `TestInvites` asserts the attributes; screenshot |
| Every plain-link navigation gets a 2 px progress bar after 150 ms | filters, sorts and pagers are links, and a cold page takes half a second | screenshot with the bar forced on |
| Relative times tick every 30 s; the 7-day fresh highlight expires on its own | a page left open said "5m ago" an hour later | element probe + code |
| List headings stick below 1601 px; a table wider than its column keeps its scroll | a 1440 px laptop lost the headings on a 100-row list | `getBoundingClientRect().top === 56` at 1440 px |
| Pagination anchors to `#list`, restoring scroll and focus | paging used to drop the reader above the filters | `id="list" tabindex="-1"` on the four lists |
| HuntQueue: a board strip, a `/queue` page with filters and pagination, card layout on phones | the ranked answer to "what now" existed only in the CLI | screenshots at 1440 and 390 px; the page counts rows |
| An empty filter result says so and offers one click back | an empty list used to show bare headings — and, on `/queue`, claimed the queue had never been built | `/queue?platform=nope` shows the message |
| The top bar shows a `Polling` pill while a poll runs | a background poll changes every number on the page | the element stays hidden when idle; screenshot when forced on |

## Engineering

| Change | Why |
|---|---|
| CI: `make fmtcheck`, vet, race tests, i18n lint, golangci-lint (v2), govulncheck, `docker build` | the only gate was "run it locally", and it was red |
| `golangci-lint` to zero findings, with justified `//nolint` where the rule does not apply | 23 findings, none of them looked at before |
| Login budget counts IPv6 by /64, is bounded at 4096 addresses, and reads the body with a 4 KiB cap before checking the budget | one host could mint addresses, an address map never shrank, and an unauthenticated request could make the route parse a large form |
| `restore` takes an exclusive lock on the data directory and removes a stale WAL | a running server kept writing into the replaced inode; a stale WAL would be replayed into the new database |
| `compact`: VACUUM + `PRAGMA optimize`, refusing while a server holds the directory | deleted rows never return their space, and the doctor could only warn about it |
| Migration digests: a shipped migration edited afterwards is refused by name | "append-only" was a convention, not a check |
| `doctor` runs `quick_check` and `foreign_key_check`, reports free space inside the file and a stale WAL | it used to say "no problems found" over damage |
| A panic becomes a logged 500 (JSON under `/api/`) instead of a dropped connection | net/http's own recovery leaves no trace |
| `/metrics`: per-route counts and latency, plus poll freshness, queue age, notify backlog, DB ping | there was no way to see "is it working" from outside |
| Seed unpacking is capped at 8 GiB; an edited migration, a panic and a decompression bomb all have tests | a small gzip could fill the disk before the database opened |
| The login browser's unsandboxed state is logged at start, its profile is 0700, and `docs/configuration.md` states the blast radius | measured: Chrome aborts (exit 134) here with its sandbox on; the flag was invisible |

## HuntQueue

Deterministic ranking of targets by "can I start today, and what is it worth
per hour": hard gates, then a weighted geometric mean (quality 0.30, money 0.25,
speed 0.15, duplicates 0.15, freshness 0.10, readiness 0.05, times a personal
factor). Median days to accept, not the mean. Materialised into `hunt_queue`
every 30 minutes; read by `/queue`, the board strip, `GET /api/v1/queue` and the
`huntdeck_queue` MCP tool; measured by `huntdeck backtest`.

**Unverified**: the acceptance line in `docs/design/ai-layer.md` §3.4 (top-10
capture at least 10% above HuntScore's, on 6 of 8 point-in-time cuts) has not
been met yet — the instance had two days of HuntScore history when the harness
was written. `hunt_scores` now gains a row a day on its own, so the cuts
accumulate; `/queue` shows the count until there are eight.

## AI

`docs/design/ai-layer.md` is the design: what an LLM is worth here (policy text
to structured constraints; choosing among stored rows; fuzzy clustering; natural
language to controlled parameters) and what is theatre (summarising changes,
polishing score reasons, suggesting attacks, chatting with the database). The
offline path — HuntQueue — was built first on purpose: it needs no key, no
network, and it can be wrong in a way that can be measured.

**Built since**: policy extraction. `huntdeck ai-extract` (or the button in
Settings › Polling) asks an OpenAI-compatible model for the rules a policy
states, caches them against the policy's hash so the same text is never sent
twice, and the program page, the API (`ai_rules`) and the MCP tool show them
beside the rules quoted from the policy, marked as a paraphrase. No page render
calls a model, and with no endpoint configured every page is exactly as it was.
Still proposal: the brief that returns row ids only, and the one-call query
translation.

## A day of hardening (2026-11-20)

One session, spent mostly on the things that fail quietly: a proxy nobody
checked, a poll that could not be told from a hung one, a gate that reported a
skip as a pass. Each row says how it was checked, because "it compiles" is not a
check.

| Change | Why | How it was checked |
|---|---|---|
| `huntdeck account` prints the standing on each platform | the numbers that decide what you may do next lived on four different pages | run against production: Bugcrowd 4 of 6 left, Intigriti reputation 4 rank 9652; HackerOne says plainly that the hacker API has no account resource |
| `huntdeck bugcrowd-caps` reads the weekly allowance from the submission page | Bugcrowd caps at six a week and lifts it above a 50% acceptance rate; the API only says no once the cap is hit | parser tested against the shape measured from Bugcrowd's own page (escaped into an attribute); the command run live |
| The polling row shows that allowance, cached for ten minutes | the first version fetched it during the render: `/settings?tab=polling` went from 9 ms to 1.6 s | timed before and after; `bench.sh` now measures both settings tabs |
| A poll a restart cut short is retried in five minutes, not a whole interval | with frequent deploys a full poll never finished: three sat at "interrupted" with the same timestamp | two scheduler cases, one proved by reverting the fix |
| Each scheduled poll has a deadline (45/20/30 min) and fails as its own reason | a hung poll held its platform until the row looked abandoned two hours later | a test that stalls a fake platform past the deadline; reverted to see it go red |
| `poll_runs.last_activity` is touched every five seconds | a running poll's counters are written when it ends, so a working poll and a hung one looked identical | round-trip test; the doctor prints "running 2m, last activity 1s ago" |
| `/api/v1/status` carries that live progress | the same blindness, for anything watching from outside | compared two samples twelve seconds apart |
| The doctor reports the proxy, the web logins, the running polls, notifications, scanner headroom and backup age | every platform failing at once looked like six broken integrations | each line has a failure mode that was seen for real: a dead proxy, an empty session, a full queue |
| `huntdeck doctor --short` prints only the failures and warnings | twenty lines is not something a cron job should mail | a test asserts no ok lines; reverting the flag makes it fail |
| `/api/v1/changes` is newest first, with `order=oldest` for a replay | a limit over a window means the latest N, and the page already defaulted to newest | a test comparing both orders: with the old default they came back identical |
| `/api/v1/targets` takes `sort=best|new|bounty` and refuses anything else | `sort=new` was silently ignored and answered in value order, which looks sorted | a test that fails on the old default and on an accepted unknown value |
| MCP keys can be deleted, ticked in bulk, and swept when never used | the list could only revoke, so a morning of reconnecting left 141 identical rows | handler test; the sweep spares keys younger than an hour and any that authenticated |
| The MCP key list shows the newest twenty and counts the rest | a hundred identical rows is not a list anybody reads | `?all=1` still shows every key; a test counts both |
| A finished poll folds the write-ahead log back in | SQLite reuses the WAL file instead of shrinking it: 45 MiB stayed beside a 186 MiB database | measured before (45,690,832) and after (16,777,216, the configured cap) |
| Pre-migration backups are kept to the last three, and pruned at every start | seventeen of them were 2.3 GiB on a host that also runs a scanner | the prune runs on the path a start with nothing to apply takes, which the first version missed; disk went 2.3 GiB → 578 MiB |
| The Chrome login profile drops component caches at start-up | 218 MiB of downloaded models and TTS engines a sign-in never uses | measured: 218 MiB → 63 MiB, `Default` (the cookies) untouched |
| The starter seed's marker is emptied, not kept whole | it held the 34 MiB archive after importing it | measured: 34,631,624 → 0 |
| The deploy builds detached and retries a dropped transfer | three deploys died mid-build or mid-rsync at load average 5.6 | the build survives a dropped SSH; the retry loop proved with a stubbed failure; a failed build reports its log and exits 1 |
| `make lint` runs the linter or fails | without the binary it printed "the full lint was skipped" and exited 0, so a day of changes went out with a gate that never opened | an unused variable makes it exit 1; a clean tree reports 0 issues |

Two of these were my own mistakes, kept in the record: the allowance fetched
during a page render (1.6 s, caught by timing it), and a `/v1/me` call that does
not exist in the hacker API, which reported "authentication failed" about
credentials that were fine.

## The same day, later: two outages and the guards they left behind

The morning's work was hardening. The evening's was the bill for it: two of my
own changes took production down, and the deploy script failed four different
ways. Written down because the pattern matters more than the individual fixes -
each time I checked that the logic was right and not that the thing could be
killed.

| Change | Why | How it was checked |
|---|---|---|
| The write-ahead-log checkpoint is bounded at five seconds | `context.WithoutCancel` gave it no deadline: after a poll it held the database and every later read queued behind it. The board answered in 28 s with a 500 while `healthz` still said ok | timed before (28 s, 500) and after (40 ms, 200); `huntdeck doctor` hung after the schema line and stopped hanging |
| Every read-only CLI command gives up after ninety seconds | During that outage two `huntdeck status` runs I had started were holding the very locks the server needed; killing them was the fix. A diagnostic that waits forever joins the outage instead of describing it | status, changes, score, account, bugcrowd-caps, scan-clear and doctor are bounded; a test asserts the deadline and the message |
| The writes that end a run are bounded at thirty seconds | `CountChanges` and `FinishRun` are detached from the poll context so they survive a cancel - detached also meant unbounded | the same shape as the checkpoint, with its own constant |
| A run a killed process left behind is closed at startup | An OOM or a restart kills the process before the run can record itself, so the row stays `running` and holds its platform: the doctor showed an Intigriti quick poll "running 1h" with no activity for 56 minutes, and nothing polled Intigriti in that hour | `runs=1` in the log after the restart; the test starts a run, sweeps, and asserts it is closed as interrupted and that a second sweep touches nothing |
| **A test walks the same path the serve command walks** | The sweep above ran three lines before the store existed, dereferenced nil, and crash-looped the container for six minutes with every unit test green | `TestOpenAppWiresEveryPart` builds the app in a temp directory; putting the bug back makes it panic, which is what production did |
| A queue target that is a URL is found by its host | The queue ranks a URL entry by its host, so `community.personio.com` is `https://community.personio.com/...` in the scope table; three of ten chosen targets could not be sent | `TestOnlyTargetsFindsAUrlEntryByItsHost`, and the tasks appeared in the scanner afterwards |
| One program per scan task, named after the program | A task was a count-slice of the candidate list: it could carry two programs under `huntdeck-20261120-161552-1`, a name that names nothing and a task nobody can attribute | grouping and naming tests; the fallback to a handle when a source has no name |
| A host in two programs is scanned once | The scan page listed `info.shop-apotheke.com` twice because it sits in two programs' scope, and the batch would have scanned it twice | `TestTheSameTargetIsHandedOverOnce` |
| `scan-handoff --targets` sends named targets | Choosing five programs to scan meant taking whatever the list happened to put first | used to send ten chosen targets; nine reached the scanner, the tenth does not exist in the scope data |
| The scan page lists recent handoffs by task id | A task in the scanner is named however the scanner was told to name it; nothing on this side said which targets it carried, so a UUID-named task could not be traced back | `TestRecentHandoffsGroupByTask`, proved by flipping the ORDER BY |
| A failed candidate read says so | The page assigned it to underscore and rendered "nothing waiting" over the error, which reads as a quiet day | dropping `scan_handoffs` shows the message; removing the line fails the test |
| The deploy refuses a second run on the same host | Two deploys fought over the same image tag: the loser died with "container name already in use" after both had built | `mkdir` rather than `flock` - flock does not exist on macOS and a missing command read as a held lock, which would have blocked every deploy |
| A build that stopped writing is called dead | The pid echoed back from the detached build is not always captured; without it the loop waited the full ten minutes in silence, which reads as a hung deploy | a log two minutes stale is dead; both directions tested |
| The wait after a failed login doubles | `RetryAfter` was a flat thirty minutes while the Settings page promised "1h, then 2h, 4h" - so a login that cannot work opened Chrome every half hour on a host short of memory | `TestLoginWaitDoublesAndIsCapped`: 30m, 30m, 1h, 2h, 4h, 4h |
| The idle login browser is stopped, and its cost recorded | One headful Chrome and its Xvfb had been up 25 minutes with `HUNTDECK_VNC` off, holding 273 MB on a host whose scanner was being OOM-killed | killing it freed 269 MB and left HuntDeck healthy; the number and the unwritten fix are in `docs/daily-use.md` |

**Four ways the deploy script failed, all mine, all found by running it:** a
staleness check whose nested quotes the remote shell parsed into a syntax error
(`bash -n` accepted it); a lock that refused a deploy when nothing was running;
a lock check that `set -o pipefail` turned into a silent exit with an empty log;
and the original concurrency it was written to prevent. `bash -n` is not a test.

**A fifth attempt was abandoned rather than shipped:** a self-test for the deploy
script whose own path handling was wrong. A test that breaks is worse than no
test, so it was deleted.

The night's numbers: two outages, both mine, six and twenty minutes. Three
guards that did not exist before (the openApp test, the bounded CLI, the build
death detection). Ten chosen scan targets: nine handed over, three finished,
three killed by the kernel for memory.

### Known gap: a task created but not started is never retried

When the scanner refuses to *start* a task (its per-node queue is full, or the
node's container memory is close to the limit), the task exists in its list as
"待开始" and HuntDeck has already recorded the handoff. Nothing tries again:
thirteen tasks sat parked that way. They can be started by hand - `POST
/api/task/start` with their ids took nine of them into the running state - but
that is the scanner's API, not this app's, and the credentials for it are the
web login, not the handoff key.

The fix, not written yet: record the ids whose start failed and retry them on the
next handoff, with a bounded attempt count. It needs a column (or a small table)
and a migration, which is why it was not done at four in the morning after two
outages.
