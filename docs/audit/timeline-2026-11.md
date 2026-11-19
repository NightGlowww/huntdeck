# November 2026: what changed, and what it cost

A month in one page: the reviews, the fixes they produced, and the three things
that were wrong in this project's own claims. The detail lives in the four audit
files beside this one.

## The reviews

| When | What | Findings | Fixed |
|---|---|---|---|
| 18 Nov | [queue-review](queue-review-2026-11.md): queue, scoring, policy extraction, the AI client | 13 | 13 |
| 19 Nov | [web-ui-review](web-ui-review-2026-11.md): templates, scripts, CSS | 12 | 12 |
| 19 Nov | [ui-ux](ui-ux-2026-11.md): loading, scrolling, focus, the shape of each page | 15 | 15 |
| 19 Nov | [a11y](a11y-2026-11.md): keyboard, names, contrast, 400% zoom | 11 | 8, 2 accepted, 1 open |

Each review was run by a separate agent with its own context, given a write
scope of one file and told to verify against the running instance.

## The five findings that were worth the whole exercise

1. **The queue page could not reach page two.** Its pager sat inside
   `{{if .Gated}}`, a branch that is false on every normal day, so 2,000 targets
   were reachable 50 at a time. Nothing in daily use could notice: page one
   looked complete.
2. **`/queue?one=1&added=7d` returned 500.** A refactor had moved "one per
   program" from SQL into Go and left its `target_scores` join behind.
3. **"One per program" returned every tied row**: 574 rows over 114 programs,
   because scores are rounded to a tenth and equality matched them all.
4. **Sign out did not exist on a phone.** The sidebar's footer — the only
   logout form and the only language switch — was hidden below 700 px and the
   open menu brought back only the nav.
5. **The board said "40 targets shown" over 12 rows.** It printed the number
   that matched the filter, not the number on screen.

## Three claims of ours that were wrong, and how they were caught

- **"The notifications page renders nothing without channels."** It rendered
  nothing because the check requested `/notifications`, which is a 404; the page
  is `/settings/notifications`. The short path redirects now.
- **"The MCP server reports every failure as a wrong password."** It reported a
  wrong password because the probe passed `--url`/`--password` flags that the
  binary does not read: it takes environment variables only. A closed port says
  `connection refused`; a bad password says `401`.
- **"The backtest harness works."** It had only ever run with two days of
  history, where its answer is 0.0% for everyone. A synthetic two-cut dataset
  now proves it computes 500 of 600 paid = 83.3%.

## What is still open, stated plainly

- The queue's acceptance line needs eight daily snapshots; there are two.
- `NeedsCreds` has no data source: no program carries a credential pool, so
  the readiness factor still evaluates near 1.0.
- At 400% zoom a change row and a top-pick card still clip the tail of a long
  sentence; the page itself no longer scrolls sideways.
- 378 `title=` attributes carry information only a mouse can reach; making that
  accessible needs a tooltip surface, which was judged not worth building yet.
- The login browser runs without its sandbox (Chrome aborts here with it on).

## What the numbers look like now

| | |
|---|---|
| Tests | 30 packages, `-race` clean, 0 vulnerabilities in our code |
| Queue page | 8–15 ms warm, 11 ms cold |
| Program projection | 64 KB of 155 KB for the MCP default |
| Smoke | 21 checks against the running instance |
| Lines of audit | about 1,700, in four files |

### Where the daily HuntScore row comes from (and a dead helper that pretended otherwise)

`cmd/huntdeck/serve.go` carried `missingScoreDay`, with a unit test asserting
that its comparison is UTC rather than local. It was **never called**: the row is
written by the score recompute itself, which upserts
`hunt_scores (program_id, day, ...) ON CONFLICT DO UPDATE` with
`day = <UTC date>` on every run. Both the function and its test are gone; the
test was worse than nothing, because it made the daily trigger look wired.

The practical consequence for the backtest: a new day appears at the **first
recompute after 00:00 UTC**, not at midnight. With polls every 15–30 minutes
that is within the half hour.


### The day a snapshot lands: UTC, not Beijing

`hunt_scores.day` is a UTC date. At 23:04Z on 18 November it is already
07:04 on the 19th in Shanghai, and the newest row is still the 18th: the next
one appears after 00:00 **UTC** (08:00 Beijing) at the first score recompute,
which follows a poll.

That eight-hour window is a trap worth naming: "the snapshot did not appear
overnight" is the expected state between local midnight and UTC midnight, not
a fault. Check `date -u` before concluding anything about the data.


### The frozen-time class of bug

Three separate pages shipped a relative time the server printed once: the top
bar's "Updated 5m ago", the queue's "Built 14m ago", and every WHEN cell on
Changes and Programs. Each looked correct in a screenshot and went stale on a
page left open.

The rule now: a relative time is a `<span data-ago data-at="<RFC3339>">`, and
`TestRelativeTimesCarryTheirTimestamp` fails when a `.when` span prints a
duration without one. The ticker also keeps the 7-day `fresh` class honest,
so a row that ages out of the window stops looking new without a reload.


## Milestone: 100 rounds in, what the five goals look like

A status page written from measurements, not impressions.

| Goal | Evidence | Still open |
|---|---|---|
| 1. UI/UX polish | four reviews (queue 13, web 12, ui/ux 15, a11y 11) — 48 findings, 46 fixed; every page photographed at 1440, 390 and in Chinese | card text still clips at 400% zoom; 378 `title=` take a mouse to reach |
| 2. Engineering | 30 packages green under `-race`, 0 lint issues, 0 govulncheck hits in our code, 21 smoke checks, every route inside its 50/100 ms budget; doctor, restore (dry-run verified), compact, backup that reads itself back | — |
| 3. MCP | 16 tools, all called against production; projections (`parts`, `scope_limit`), `explain`, and the failure semantics documented | — |
| 4. AI / decision model | HuntQueue end to end: factors stored per row, four read-outs (page, CLI, REST, MCP), a backtest harness proven on synthetic data; the AI client is built and exercised by `ai-check` | F1 (LLM policy extraction) needs a key; the acceptance line needs 8 daily snapshots and there are 2 |
| 5. Agent teams | three review agents and one accessibility auditor, each with a shared task, a one-file write scope, and findings that were re-checked before being believed | — |

Numbers at round 100: 487 Go files (208 of them tests), 63 migrations, 4,107 lines of
documentation in 20 files, 1,398 programs and about 48,000 in-scope entries polled
every 15 to 30 minutes, a queue of 2,000 targets over 114 programs rebuilt every
half hour, and 175 MiB of SQLite that passes integrity, foreign-key and free-space
checks.

The part worth repeating: of the 48 findings, the four that mattered most were
invisible in daily use (a page that could not paginate, a filter that returned
500, a tie-break that returned every row, a logout button that did not exist on a
phone). Reviews found them because they read the code as an outsider would.


#### Correction (round 111): there is a daily timer, and it is not the poll

An earlier note here said the daily row rides on the score recompute that
follows a poll. Half right: `cmd/huntdeck/serve.go` runs a 30-minute ticker that
checks `LatestScoreDay != <UTC today>` and calls `huntscore.Refresh` by itself,
then rebuilds the queue. `missingScoreDay` was the standalone version of that
check and nothing called it — the live one is inline in the ticker, which is why
deleting the function was right and why the daily row does not depend on a poll.

In practice: the new day appears within **30 minutes of UTC midnight**, whether
or not a poll finished.


### The third day landed, and the log line I added was wrong

At 00:05Z the ticker found `LatestScoreDay = 2026-11-18` against UTC today
`2026-11-19`, refreshed the scores and wrote the row. Verified in four places:

| Where | Before | After |
|---|---|---|
| `huntdeck status` | 2 day(s), newest 2026-11-18 | **3 day(s), newest 2026-11-19** |
| `huntdeck doctor` | score history: 2 day(s) | **3 day(s), newest 2026-11-19** |
| `huntdeck backtest` | 2 cuts | **3 cuts** |
| `/queue` footer | 2 of 8 snapshots | **3 of 8 snapshots** |

The first real use of the log line added the round before exposed a bug in it:
it printed `day=2026-11-18`, the day that was *already* there, because the
variable it logged was the one used for the comparison. It now prints the day it
wrote (`now.UTC().Format(time.DateOnly)`). A log line that names the wrong day is
worse than no log line: it would have made the next person doubt the write that
had in fact happened.


### robots.txt answered with a login redirect

A crawler asking a password-protected instance for `/robots.txt` got `303 ->
/login` and read the login page instead of the `Disallow: /` that tells it to
stay away. The file was right; the gate in front of it was not. `/robots.txt`,
`/sitemap.xml` and `/.well-known/security.txt` are now served without a session:
the sitemap and security.txt answer 404 on a private instance, which is honest,
and robots.txt answers 200 with `Disallow: /`.

### Both scores were checked by hand, against live data

| Model | What was added up | Result |
|---|---|---|
| HuntScore | 7.8×0.35 + 67.6×0.30 + 91.6×0.25 + 52.2×0.10 | **51.13**, and the program page showed 51.1 |
| HuntQueue | 0.778 × 0.972 × 0.9 × 100 (quality, freshness, personal effects) | **68.06**, and the page showed 68.0 that rebuild |

The second number was 68.1 an hour earlier and 68.0 after the next rebuild,
because freshness fades in real time: the API and the page agree at any moment,
two screenshots an hour apart need not. Both invariants are now tests
(`TestTheScoreIsTheWeightedSumOfItsParts`, `TestExplainEffectsMultiplyToTheScore`).


### Open: quality subtracts freshness's raw part, not its contribution

`QueueCandidates` computes quality as `MAX(hs.score - hs.fresh, 0) / 100`, i.e.
it takes the fresh **part score** off the weighted total. The part's actual
contribution is `fresh × weight` (0.35 on Balanced), so a program with fresh 10.5
loses 10.5 points from a total where only 3.7 came from freshness.

Measured on live rows (top 10, 2026-11-19):

| program | quality now | score | fresh | queue score now | with `score - fresh×0.35` |
|---|---|---|---|---|---|
| bitpanda-og-bb | 0.433 | 51.1 | 7.8 | 68.0 | 70.3 |
| t-mobile | 0.326 | 36.6 | 4.0 | 58.2 | 59.6 |
| moneytreekkog | 0.257 | 36.2 | **10.5** | 51.6 | **55.4** |
| deviniti | 0.274 | 36.8 | 9.4 | 51.1 | 54.3 |

The correction moves `moneytreekkog` from 9th to 4th: it systematically
down-ranks exactly the programs whose strength is fresh scope.

**Not fixed in this round.** The weight lives in the HuntScore preset (a
setting), and `internal/store` cannot import `internal/huntscore` — that package
already imports the store, so the import is a cycle. A correct fix is one of:

1. store the weighted contribution per part in `hunt_scores` (a migration and a
   writer change), or
2. pass the fresh weight into `RefreshHuntQueue` from its callers (a signature
   change across serve, cli and the tests).

Both are design decisions with a schema or API change behind them, so the
finding is recorded here rather than half-applied.

