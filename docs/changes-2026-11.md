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
