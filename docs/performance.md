# Performance

What the pages cost, how it was measured, and what made them cheaper. Keep it
current when a change moves a number: it is the comparison a later change
needs.

## Measuring

`scripts/bench.sh` times every page and the API against a running instance,
from the host so TLS and the proxy are out of the picture, three runs each.
The first run of a route after a restart pays for cold caches and for the
warm-up; the later ones are what a visitor feels.

```sh
ssh <host> 'bash <repo>/scripts/bench.sh'
```

For one query: `HUNTDECK_PERF_DB=<copy.db> go test ./internal/store/ -run TestPerf -v`
times the hot ones against a copy of a real database (a `huntdeck backup`
archive unpacks to one).

`--cold` first hits every route once, in order, and prints those times: it is
the run to make right after a deploy or a restart, and it is how the table
below was taken. With `--check` it also applies a first-hit budget (900 ms:
`--max-first`), so a restart that suddenly costs seconds is caught.

`--check` turns the run into a budget: it exits 1 when a page's warm time is
over 50 ms or an API route's over 100 ms (`--max-page`, `--max-api` to change
them). Those sit above today's numbers (pages ≤30 ms, API ≤14 ms) with room for
jitter, and below anything that would be a real regression. Run it after a change that touches queries or templates; the numbers
below are what it should report.

Two records taken a release apart say what a change cost: `scripts/perf-snapshot.sh`
writes one file with the pages' numbers, the probes on a database copy and what
produced them.

```sh
scripts/perf-snapshot.sh -o /tmp/perf.md                 # pages
scripts/perf-snapshot.sh --cold --db copy.db -o /tmp/p.md  # and first hits, and queries
```

Two records are only useful if comparing them is quick: `scripts/perf-diff.py`
reads two snapshots and prints the difference per route, largest first.

```sh
scripts/perf-diff.py perf/20261118T233839.md perf/20261118T233907.md
```

```
| route | before | after | change |
|---|---|---|---|
| `/` | 40 ms | 47 ms | +6 ms |
| `/targets` | 12 ms | 11 ms | -1 ms |
| `/changes` | 19 ms | 18 ms | same |
```

A route only in one file says `added` or `gone`, so a page that stopped being
measured is visible too.

## Baseline (2026-11-18)

1,337 programs · 75,795 scope entries · 53,251 targets · 10,772 changes;
inside the container, warm, seconds after a restart. Re-verified against a fresh
`huntdeck backup` snapshot at schema 60: the probe numbers below are within a
factor of the copy, and move with the page cache (a warm run is the number to
compare).

| Route | ms |
|---|---|
| `/` | 30 |
| `/programs` | 26 |
| `/invites` | 7 |
| `/targets`, `?sort=best`, `?kind=domains` | 9–10 |
| `/changes`, `?group=scope` | 13–16 |
| `/disclosed` | 14–16 |
| `/stats` | 5 |
| `/settings` | 7–9 |
| `/programs/{p}/{h}` and its tabs | 14–18 |
| `/api/v1/programs?limit=30` | 12–14 |
| `/api/v1/status`, `/targets`, `/changes`, `/disclosed` | 1–10 |

## Deep pages

An `OFFSET` still has to walk what it skips, so the tail of every list was
measured as well as its top.

| Route | first page | deep page |
|---|---|---|
| `/disclosed` | 16 ms | 38 ms at page 40 |
| `/changes` | 16 ms | 8 ms at page 40 |
| `/stats` | 5 ms | 7.5 ms at page 13 |
| `/targets` | 10 ms | 10 ms at page 20, but 53–220 ms the first time a page is opened |

Two fixes came out of this: `0060_disclosed_date` (without an index on the
list's order every page sorted all 11,000 reports: `Disclosures` at offset
1000 went from 31.8 ms to 2.6 ms) and, earlier, the entries-first plan that
took the Targets query from 207 ms to 1 ms.

What is left is the nature of an offset: `TargetRows` at offset 0/1000/2000/4400
costs 38/33/62 ms warm, and the page memoises each page after its first visit.
Making that cheap needs a cursor instead of an offset, which changes the URL
contract rather than an index — out of scope for maintenance.

## Cold start

A restart empties every cache. The warm-up (`web.Warm`) runs at start, in the
order a visitor meets the pages — board, Programs, Stats — and warms the
Targets kinds beside it rather than before them: it finishes in about three
seconds (5.6 s when the Targets work ran first).

What the very first view after a restart costs, measured 3 s in:

| Route | first | then |
|---|---|---|
| `/programs` | 35 ms | 32 ms |
| `/changes`, `/disclosed`, `/stats` | 20–85 ms | 8–22 ms |
| `/programs/{p}/{h}` | 20 ms | 18 ms |
| `/targets` | up to 550 ms while its counts warm | 10 ms |
| `/` | up to 225 ms while its week of changes warms | 30 ms |


### Measured 2026-11-19, after the queue and index work

| Route | first hit after a restart | warm |
|---|---|---|
| `/queue` | 11 ms | 8–10 ms |
| `/queue?one=1` | 12 ms | 8 ms |
| `/api/v1/queue?limit=20` | 1.6 ms | 1.3 ms |
| `/targets` | 529 ms | 8.5 ms |
| `/targets?kind=domains` | **991 ms** | 8.6 ms |
| `/targets?sort=best` | 15 ms | 15 ms |

`/targets?kind=domains` is the one route over the 900 ms cold budget. It is
not a regression from the queue work: each filter has its own cached rows and
counts (`store.Memo`), so the first visitor to any particular filter after a
restart pays for that filter's query — the unfiltered `/targets` pays 529 ms
for the same reason, and the warm-up only primes the unfiltered one. Two ways
out, neither taken yet: warm the common kinds at start (three more second-
scale queries at boot), or make the kind facet counts share one computation
(`targetFacets` already clears `Kind` before counting, but the web memo keys on
it, so each kind recomputes). The number is recorded here so a future change
can show it moved.

**Updated the same day**: the second way out was taken. The facet cache key
included the kind although the counts never depend on it (`targetFacets` clears
`Kind` before counting, and `assetConds` never reads it), so every kind filter
recomputed the same aggregates. With `Kind` out of the key:

| Route | first hit before | first hit after |
|---|---|---|
| `/targets?kind=domains` | 991 ms | **229 ms** |
| `/targets` | 529 ms | 745 ms (it now carries the shared work) |

The counts are unchanged: both pages render `All kinds 46,873 · Wildcards
4,535` from the one computation.
The two that can still wait are waiting on the warm-up's own query (they share
it through `store.Memo`'s in-flight join), not duplicating it: cold pages of
the database have to be read once.

## Concurrency

Twenty requests at once, one session, four cores (40 requests per route, 20 at
a time):

| Route | p50 | p95 | max |
|---|---|---|---|
| `/` | 287 ms | 367 ms | 417 ms |
| `/programs` | 189 ms | 273 ms | 298 ms |
| `/disclosed` | 109 ms | 201 ms | 235 ms |
| `/targets` | 45 ms | 106 ms | 128 ms |

Every request answered 200; nothing queued behind a lock. The cost is cores,
not connections: widening the reader pool from 4 to 8 changed nothing
(measured), so it stayed at `max(4, NumCPU)`.

HuntDeck is sized for one person — a browser with a few tabs, a monitor, a
poll — where everything is 5–30 ms. A twenty-request burst is a browser
prefetching every link at once, not the intended load.

## When a page gets slow

1. **Measure, don't guess.** `scripts/bench.sh --check` first: it says which
   route is over budget. `--cold` says whether it is the restart or the page.
2. **Look inside the route.** Every list and the board log their parts at debug
   level (`HUNTDECK_LOG_LEVEL=debug`, then read `docker compose logs`):
   `list`, `facets`, `rows`, `rest`, `render` for the lists; `head`, `targets`,
   `changes`, `render` for the board; `inputs`, `build`, `rest`, `render` for
   Stats. The slow part names the next step.
3. **A query.** `HUNTDECK_PERF_DB=<copy.db> go test ./internal/store/ -run TestPerf -v`
   times the hot ones against a copy of a real database. Then
   `EXPLAIN QUERY PLAN` on that copy: a `USE TEMP B-TREE FOR ORDER BY` over a
   filter a page applies means an index is missing, and that has been the
   cause every time so far (see the fixes above).
4. **A shape that repeats.** If the cost is per-row work in Go (a history
   query per program, several scans of one table), read it once and fan out.
5. **Write the number down** here when it changes: the next person needs the
   comparison more than the story.

### What the probes cover

`go test ./internal/store/ -run TestPerf -v` (with `HUNTDECK_PERF_DB`) times
every read that has been slow or is on a page's critical path:

| Probe | Read | Warm |
|---|---|---|
| `TestPerfProgramPage` | a program page's queries | ≤4 ms each |
| `TestPerfStatsPage` | the Stats list and the all-programs pass | 2–17 ms |
| `TestPerfBoard`, `TestPerfBoardTargets` | summaries, picks, checked, best targets | 2–21 ms |
| `TestPerfFanOut` | snapshots (one pass), the board's week, its counts, target facets | ≤3.5 ms |
| `TestPerfTargetRows*`, `TestPerfTargetsOffset` | the Targets list, shallow and deep | 1–62 ms |
| `TestPerfProgramList*` | the Programs list, by name and by score | 4–7 ms |
| `TestPerfDisclosed*` | the Disclosed list, shallow and deep | 2.6–14 ms |

They skip without the environment variable, so `make test` stays hermetic.

## Scaling

The same queries on copies of the database grown to twice and four times the
data (5,348 programs, 303k scope entries, 213k targets), measured warm:

| Query | 1× | 2× | 4× |
|---|---|---|---|
| `Programs(name, 30)` | 4.0 ms | 6.5 ms | — |
| `Programs(huntscore, 30)` | 4.2 ms | 5.8 ms | 8.2 ms |
| `Disclosures(100)` | 1.3 ms | 1.4 ms | — |
| `TargetRows(domain, 100)` | 1.0 ms | 1.0 ms | 1.5 ms |
| `ProgramStatsList(100)` | 17 ms | — | 31 ms |

Two things to know from it. The index-backed reads stay flat: a Targets page
costs the same at four times the data. The lists that count every program
(`Programs`, the Stats list) grow with that count, as their `COUNT(*)` and
per-row subqueries must; at four times today's size they are still under
10 and 35 ms. Cold reads grow with the file, which is why the first visit
after a restart is the number to watch (`--cold`).
One optimization was deliberately not made. `Programs`'s `COUNT(*)` costs
2.9–4.4 ms at four times today's data (out of the list's 8.2 ms). It could go
through `cachedCount`, but nothing bumps `count_version` for
`program_public_stats`: a filter on a stats column (top bounty, reports
received) would keep the count from before the stats were fetched. Adding
those triggers is the other way, and it is worse — a stats poll writes tens of
thousands of rows, and a trigger that updates one row per write would turn a
polite poll into a write storm. A few milliseconds on a page is the cheaper
side of that trade, so the count stays uncached.

## What made them cheaper

Each of these was measured before and after; the numbers are the evidence.

- **A program page read 2,000 changes of every program** to fold flip-flops
  for one program; scoping that window to the program took the page from
  224 ms to 18 ms.
- **The Targets page's newest order sorted every matching target** (39k
  domains). An expression index on the entries' date plus an entries-first
  join let it stop after the hundredth row: the query went from 207 ms to
  1 ms, and each kind's first click from about 600 ms to 10 ms.
- **The Stats page laid itself out on every request** (grouping every program
  and day): memoised with its inputs, 55 ms → 5 ms.
- **The Programs list sorted all 1,300 rows** for its default order; the
  HuntScore and name expression indexes took the query from 15 ms to 4 ms.
- **Seven scans of the same table for one page of rows** became one: the
  daily figures are read in a single pass.
- **The warm-up ran only from its second tick**, so the first visitor after
  a restart paid for everything: it now runs at start and fills the board,
  the Stats pages and every Targets kind (rows and counts).
- **The edge compresses**: 178 KB of programs HTML goes out as 28 KB.

## Known remainders

- `/programs` spends about 12 ms in the template (30 sparkline rows), which
  is the price of drawing them unless the page is pre-rendered.
- `BestTargets` (the board's fresh surface) costs about 20 ms warm on 2,650
  scored targets; nothing measured so far waits on it.
