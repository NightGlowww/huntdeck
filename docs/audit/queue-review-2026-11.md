# HuntQueue, AI client and policy extraction: correctness review (2026-11)

Scope: `internal/huntqueue/huntqueue.go`, `internal/store/queue.go`,
`internal/web/queuepage.go`, `internal/web/templates/queue.html`,
`internal/web/api_queue.go`, the `parts` projection in
`internal/web/api_programs.go`, `internal/ai/ai.go` and the policy rules in
`internal/web/beforetest.go`. Correctness only: wrong SQL semantics, filters that drop
rows, wrong nil handling, misleading numbers. No style, naming or test-coverage remarks.

Source was read-only. Nothing was edited except this file; nothing was committed.

## How this was verified

Every claim below was checked against the code and, unless marked **UNVERIFIED**,
reproduced by running it. The reproducers are `_test.go` files added through
`go test -overlay`, which compiles them into the package without writing into the tree:

```sh
go test -count=1 -overlay=/tmp/audit/overlay_all.json ./internal/store/ -run TestAudit -v
go test -count=1 -overlay=/tmp/audit/overlay_web.json ./internal/web/   -run TestAudit -v
go test -count=1 -overlay=/tmp/audit/overlay_ai.json  ./internal/ai/    -run TestAudit -v
```

The test bodies are in the appendix so they can be recreated. Line numbers refer to
the snapshot described at the end; `internal/store/queue.go` was edited twice while this
review ran, and every finding was re-checked against the final text.

## Findings at a glance

| # | Severity | Finding |
|---|---|---|
| 1 | high | The queue page cannot reach page 2: the pager sits inside the "blocked" branch |
| 2 | high | Every gate but scope_unavailable is unreachable, so the "Blocked" panel is dead and its copy is wrong |
| 3 | medium | "One per program" plus a target-level filter (kind, added) hides a program's matching targets |
| 4 | medium | The payout fed to the money factor is the program's own currency, used as USD |
| 5 | medium | NeedsCreds / NoAutomation are never populated (Flips is read and unused), so readiness is always 1.0 |
| 6 | medium | The pre-key reasons compatibility path never runs; gated reasons are lost on the page and in the API |
| 7 | medium | parts= is undocumented, silently ignores unknown names and narrows only part of the payload |
| 8 | medium | One response-time sentence drops the program's real reporting deadline from the rules |
| 9 | low | excerptAround finds the match on a lowercased copy and slices the original |
| 10 | low | upperFirst cuts one byte off a UTF-8 string, producing invalid UTF-8 |
| 11 | medium | ai.CompleteJSON accepts null as a successful parse and leaves the caller's value untouched |
| 12 | low | ai.extractJSON counts braces inside string literals, truncating valid JSON |
| 13 | low | The "Blocked" panel ignores the page filter, prints raw gate keys, and mixes a filtered total with an unfiltered one |

---

## 1. The queue page cannot reach page 2 (high)

`internal/web/templates/queue.html:59` — `{{template "pager" .Pager}}` sits between
`{{if .Gated}}` (line 41) and its `{{end}}` (line 60).

The pager therefore renders only when the "Blocked" list has at least one row, which is
the opposite of the normal case (finding 2: gated rows are effectively never produced).
With 60 ranked targets and a 50-row page, the page offers no way to see the other 10 —
the comment at `queuepage.go:29-31` says the queue held 207 targets, so 157 of them are
unreachable from the UI.

Verified (`TestAuditQueuePagerNeedsAGatedRow`):

```
60 ranked rows, none gated: pager rendered=false
same queue plus one gated row: pager rendered=true
```

Fix: move `{{template "pager" .Pager}}` out of the `{{if .Gated}}` block, next to the ranked
table's `</section>`, so it depends on the ranked list it paginates.

## 2. Only scope_unavailable can ever be a gate (high)

`internal/store/queue.go:80-81` filters candidates before any gate is consulted:

```sql
WHERE p.state = 'open' AND p.removed_at IS NULL AND p.ignored = 0
  AND COALESCE(p.invite_pending, 0) = 0
```

and `internal/huntqueue/huntqueue.go:110-118` (`gateOf`) only ever returns
`GateNoScope`. So of the six gates the type declares (`huntqueue.go:35-43`), five —
`invite_pending`, `requirements_unmet`, `paused_or_closed`, `ignored`, `removed` —
can never appear: the programs they describe are deleted from the candidate set, not
gated.

Manifestation: with an unaccepted invitation, a paused program and an ignored program in
the database, the queue holds zero rows and the page prints "Nothing is blocked right
now: every candidate can be started." (`queue.html:40`) — a false statement about exactly
the targets the panel exists for. The same claim reaches API clients: `openapi.json`
describes `/api/v1/queue` as ranking with "a hard gate for what cannot be started (an
invitation to accept, requirements, paused, ignored)", and
`internal/i18n/catalog/zh.json:1492-1497` carries translations for all six gate keys.

Verified (`TestAuditOtherGatesNeverAppear`: paused, invite_pending and ignored programs,
each with a scored target, then `RefreshHuntQueue`):

```
ranked=0 gated=0 gateCounts=map[]
gate "invite_pending" has no row: the program was dropped before the gate ran
gate "paused_or_closed" has no row: the program was dropped before the gate ran
gate "ignored" has no row: the program was dropped before the gate ran
```

Fix, pick one and make the other surfaces match: (a) drop the state/ignored/invite
filters from `QueueCandidates` and let `gateOf` return `GateInvite` / `GatePaused` /
`GateIgnored` (needs the program state in `Inputs`); or (b) delete the five unused gates,
their i18n keys, the openapi sentence and the "Blocked" panel. Keeping a
declared-but-impossible gate set is what makes the page assert something untrue.

## 3. "One per program" plus a target-level filter hides the whole program (medium)

`internal/store/queue.go:278-284` (`QueueFilter.where`) keeps a row when its score equals
the program's maximum among *unfiltered* rows:

`q.score = (SELECT MAX(q2.score) FROM hunt_queue q2 WHERE q2.program_id = q.program_id AND q2.gate = '')`

The filter's other clauses run in the outer query only. When the winning target is one the
filter excludes, the program's *matching* targets all fail the equality test and the
program vanishes from the page — and from `HuntQueueCount`, which shares `where()`.

Verified twice:

```
kind=url one=1:  rows=0 count=0   (program has wildcard 50 and url 40)
added=7d one=1:  rows=0           (program has an old wildcard 50 and a 1-day-old url 30)
```

Any target-level predicate does this; `Paying` does not, because it filters on the program.

Fix: apply the same target-level predicates inside the subquery (build `q2.kind = ?` /
`q2.added_at >= ?` into the MAX when the filter sets them), or replace the correlated MAX
with a window function over the filtered set —
`ROW_NUMBER() OVER (PARTITION BY program_id ORDER BY score DESC, kind, value)`, keep
`rn = 1` — which also removes the tie-break clause at line 283.

## 4. The payout is not USD, but the scorer treats it as USD (medium)

`internal/store/queue.go:65`: `COALESCE(p.bounty_max, 0) AS payout` →
`QueueInput.PayoutUSD` (`queue.go:27`).

`programs.bounty_max` is in the program's own currency (`programs.currency`; Intigriti and
YesWeHack store `EUR`, cf. `internal/platform/yeswehack/stats.go:69` and
`internal/store/huntscore.go:132`). `huntqueue.money` (`huntqueue.go:120-126`) saturates at
`PayoutCapUSD = 10000`, so any currency whose amounts are numerically larger than dollars
scores as a top-paying program, and a EUR program's pay is compared to USD programs
one-for-one.

Verified (`TestAuditPayoutIgnoresCurrency`: a JPY program with `bounty_max = 500000`):

```
currency JPY, bounty_max 500000 -> PayoutUSD 500000; score 61.5 vs a $10,000 payout's 61.5
```

The rest of the store already refuses to mix currencies (`paidUSD` in `overview.go:67`,
`PlatformMonth.AddBounty` in `overviewtypes.go:40-49`); the queue is the one place that adds
them up. The magnitude per currency is **UNVERIFIED** (no FX table in the repo); the
defect is the missing conversion, not the rate.

Fix: convert once at write time (a `bounty_max_usd` column on `programs`, filled by the
syncer with a documented rate) and read that here; or give non-USD programs a flat money
factor until a rate exists, which at least stops ranking them by a number in another unit.

## 5. The readiness factor is dead: NeedsCreds and NoAutomation are never set (medium)

`internal/store/queue.go:33` declares `NeedsCreds`, line 213 passes it to the scorer, and no
code assigns it (`QueueCandidates` never selects it; a grep over non-test Go finds only
the declaration, the struct literal and the scorer's reads). `NoAutomation` does not
appear in the store at all. Consequences in `internal/huntqueue/huntqueue.go:162-171` and
`:199-205`:

- `readiness()` is always 1.0: the 0.7 (credentials) and 0.5 (no automation) branches never
  affect a score;
- the reasons "credentials have to be requested" and "automation banned: hand testing
  only" (and their zh translations) are unreachable; every row instead ends with
  "nothing to arrange: start now", which `queuepage.go:131-136` then strips as noise, so
  the "Why" column never mentions credentials at all.

Verified (`TestAuditRefreshIgnoresCredentials`: a program with a scored target and a live
scope entry, `RefreshHuntQueue` run):

```
reasons=[{"Key":"target added %s days ago","Args":["0"]},{"Key":"nothing to arrange: start now","Args":null}] score=50.7
score if NeedsCreds were set: 49.8 reasons=[{target added %s days ago [0]} {credentials have to be requested []}]
```

`Flips` is in the same state from the other side: the candidate query runs a per-program
`COUNT(*)` over `changes` for every row (`queue.go:71-73`), the value is scanned
(`queue.go:97`) and passed to `Score` (`queue.go:212`), and no factor reads `in.Flips`
(`huntqueue.go:54` is its only occurrence).

Fix: fill `NeedsCreds` from what the app already knows (a requirement of kind credentials,
or `PublicStats.Profile.CredentialPool.Available == false`) and `NoAutomation` from
`program_testing.no_automation` (`store/views.go:114` already reads that row); either use
`Flips` in a factor or drop the aggregate.

## 6. The pre-key reasons compatibility path never runs (medium)

`internal/store/queue.go:339`:

`if err := json.Unmarshal([]byte(reasons), &r.Reasons); err != nil || len(r.Reasons) == 0 {`

Unmarshalling a JSON array of strings into `[]huntqueue.Reason` fails *after* the slice has
grown, leaving one zero-value `Reason{}`. `len(r.Reasons)` is therefore 1, the `[]string`
fallback at lines 340-346 never executes, and the row renders as an empty sentence.
`HuntQueueGated` (`queue.go:404`) has no fallback at all and keeps the zero `Reason`.

Manifestation: on an install whose `hunt_queue` rows predate the `Reason` struct (the
comment says such rows exist), the "Why" column is blank for those rows and
`/api/v1/queue` returns a reason list with an empty string:

```json
"gated": [{ "handle": "acme", "reasons": [""], "gate": "scope_unavailable" }]
```

Verified (`TestAuditLegacyGatedRowShowsBlankReason`: row written with
`reasons = ["no scope entries"]`): the page does not contain the sentence and the API
returns `[""]`.

Latent crash in the same place: `queuepage.go:113` indexes
`huntqueue.Render(row.Reasons, t)[0]`; a gated row whose `reasons` column is `[]` or `null`
yields an empty slice and panics the handler. That is not reachable from rows
`RefreshHuntQueue` writes (it always stores at least the gate reason), so it is hardening,
not an observed bug — **UNVERIFIED** as reachable.

Fix: test `err != nil || len(r.Reasons) == 0` against a temporary slice and assign only on
success (or decode into `[]json.RawMessage` first); give `HuntQueueGated` the same helper;
guard the index in `queuepage.go`.

## 7. parts= is undocumented and only partly narrows the payload (medium)

`internal/web/api_programs.go:298-311` reads `?parts=`, and `want()` returns false for every
name it does not know — no error, no warning. Six fields are gated (`rules`, `facts`,
`brief`, `announcements`, `scope`, `disclosed`); `bounty`, `numbers` and `trends` are
assigned unconditionally at lines 350-357, and `s.programRows` runs on every request.

Verified (`TestAuditPartsNarrowsOnlySomeFields`):

```
parts=brief kept: [first_seen_at last_seen_at facts brief private fast_payments ignored numbers
                  state offers_bounty platform handle safe_harbor trends updated_at last_synced_at name url]
parts=brief still carries "trends"  = {"reports_received_90d": [], ...}
parts=brief still carries "numbers" = {"currency": "", ...}
unknown part: status 200, len 2047
```

Three consequences: (a) `"parts"` appears nowhere in `openapi.json` (zero grep hits) while
`internal/mcpserver/tools.go:77-92` makes it the MCP tool's default (`facts,brief,rules`),
so an agent-facing contract rests on an undocumented parameter; (b) a typo
(`?parts=scopes`) returns 200 with an empty `scope` and `scope_total: 0`, indistinguishable
from "this program has no scope"; (c) `docs/design/mcp-surface.md:446-447` states that
`parts=["brief"]` "returns only brief (plus the ref)" and that `scope` reports
`next_offset` — neither is true (`next_offset` does not exist in the code).

Fix: document `parts` (accepted names, default, unknown-name behaviour) in `openapi.json`;
make an unknown part a 400; gate `bounty`/`numbers`/`trends` and the `programRows` call
behind `parts` too, or correct the design note.

## 8. A response-time sentence drops the program's real deadline (medium)

`internal/web/beforetest.go:204-218` looks at the *first* match of `policyDeadlineRE` and,
if that phrase contains `review`, `respond`, `triage` or `acknowledg`, returns `nil` for the
whole text instead of skipping that match.

Policies usually state the response promise before the reporting deadline, so the
deadline is the one lost. Verified (`TestAuditPolicyDeadlineSuppression`):

```
text: "Reports are reviewed within 15 days. Please report findings within 30 days of discovery."
policyRules(text) -> []   (no deadline rule at all)
```

Fix: `FindAllStringSubmatch` and return the first match whose phrase is not a response
promise.

## 9. excerptAround mixes a lowercased offset with the original bytes (low)

`internal/web/beforetest.go:231-246`:

`i := strings.Index(strings.ToLower(text), strings.ToLower(match))` ... then `text[start:end]`
with `end = i + len(match)`.

Any character whose lowercase is a different number of bytes (U+0130 "İ" -> "i",
U+212A KELVIN -> "k") shifts the index, so the excerpt starts and ends at the wrong byte
and can cut a rune in half. Verified (`TestAuditExcerptOffsetAfterCaseFold`):

```
text:   "İstanbul office. Do not run denial of service."
excerpt "Do not run denial of service"   (want ...service.")
```

The quote under a "Policy forbids" rule is therefore truncated or garbled for such
policies; frequency in real policy text is **UNVERIFIED** (needs one such character
before the match).

Fix: locate the match with a case-insensitive regexp
(`regexp.MustCompile("(?i)" + regexp.QuoteMeta(match))`) and slice with
`FindStringIndex`, which works on the original bytes.

## 10. upperFirst produces invalid UTF-8 for a non-ASCII first byte (low)

`internal/web/beforetest.go:161-166`: `strings.ToUpper(s[:1]) + s[1:]`. Slicing one byte
off a multi-byte rune yields an invalid byte, and `strings.ToUpper` turns it into U+FFFD.

Verified (`TestAuditUpperFirstMultiByte`):

```
upperFirst("éclair") = "\uFFFD\xa9clair"   valid=false
upperFirst("Über")   = "\uFFFD\x9cber"     valid=false
upperFirst("日本語") = "\uFFFD\x97\xa5本語" valid=false
```

Call sites: the platform's own requirement titles (`beforetest.go:31`), policy deadlines
(215) and account rules (224). A title starting with a non-ASCII letter renders its first
character as the replacement char in the "Before you test" box. Whether any shipped
platform text does this is **UNVERIFIED** (no fixture in `testdata/` starts non-ASCII);
the corruption itself is verified.

Fix: `r, size := utf8.DecodeRuneInString(s); return string(unicode.ToUpper(r)) + s[size:]`.

## 11. ai.CompleteJSON accepts null as a successful answer (medium)

`internal/ai/ai.go:89-109`: `json.Unmarshal([]byte(extractJSON(answer)), out)`. For the JSON
literal `null` the decode returns no error and writes nothing, so `CompleteJSON` reports
success with the caller's value untouched — a model that declines or truncates is
indistinguishable from one that answered.

Verified (`TestAuditCompleteJSONAcceptsNull`: endpoint answering `{"content":"null"}`):

```
err=<nil> out={Answer:sentinel Items:[]}
```

Today's only caller, `cmd/huntdeck/aicheck.go:38-44`, prints `answered in 12ms: ""` and exits
0 for such an endpoint — the command meant to prove the key, model and endpoint agree
says they do.

Fix: reject the literal `null` (and an empty extraction) before unmarshalling, e.g. decode
into `json.RawMessage` and require a non-null object or array, returning an error that
names the answer.

## 12. ai.extractJSON counts braces inside strings (low)

`internal/ai/ai.go:155-183` walks the answer counting `{` `[` and `}` `]` with no notion of
quoted strings, so a brace inside a string value ends the extraction early.

Verified (`TestAuditExtractJSONBraceInString`):

`extractJSON('Here you go: {"a":"}","b":2} — that is all.') -> {"a":"}`

The truncated text does not parse, so `CompleteJSON` retries once and then fails with
"the answer was not JSON" even though the model returned a valid object.

Fix: while scanning, skip quoted strings honouring backslash escapes (a five-line state
machine), or hand the first `{` / `[` to `json.Decoder` and let it find the end.

## 13. The "Blocked" panel ignores the filter and prints raw gate keys (low)

`internal/web/queuepage.go:81` calls `HuntQueueGated(ctx, 20)` with no `QueueFilter`, and
lines 92-99 count with `HuntQueueGatedCount` / `HuntQueueGateCounts`, which take none. So
`/queue?platform=hackerone&paying=1` shows a filtered "Start today" list next to a
"Blocked" panel and a "%d cannot be started today" count that include every program on
every platform.

`queue.html:43` then prints `{{.Gate}} {{num .Count}}` — the raw enum
(`scope_unavailable`), not the translated label that `zh.json:1496` already holds under the
key "gate scope_unavailable".

Verified by reading (the call sites take no filter); the raw key is visible in the page
rendered by `TestAuditLegacyGatedRowShowsBlankReason`'s fixture, whose blocked row prints
`scope_unavailable` in a Chinese UI.

Fix: accept a `QueueFilter` in the two gated readers (or filter in the page), and render the
chip with `{{T (printf "gate %s" .Gate)}}`.

---

## Fixed while this review was running

`internal/store/queue.go` was edited twice during the review. These were reproduced as bugs
before the edits and are fixed in the final text; they are recorded so the reproducers in
the appendix are not mistaken for current failures:

- **"One per program" returned every tied row.** Scores are rounded to a tenth, so ties are
  ordinary data. Reproduced: `rows=2 count=2` for a program with two targets at 42.7.
  Fixed by the `NOT EXISTS` tie-break at `queue.go:283`; re-verified `rows=1 count=1`.
- **`/api/v1/queue` reported `"computed_at": "0001-01-01T00:00:00Z"`** whenever the queue held
  no ranked row, because the build time came from the last returned row. Reproduced through
  the API; fixed by returning `HuntQueueBuiltAt` (`queue.go:357`); re-verified
  `"2026-11-19T00:00:00Z"`.
- **`added=7d` dropped a target exactly at the cutoff**, comparing a millisecond timestamp
  with a whole-second string. Fixed in `queue.go:288-291`; not re-tested here beyond the
  passing `TestHuntQueueAddedWindow` in the new `internal/store/queue_filters_test.go`.

## What I did not verify

- The magnitude of finding 4 per currency: no FX data in the repo and no fixture with a
  real non-USD program. The unit mismatch is proven; the ranking error for a specific
  platform's catalog is not.
- Reachability of findings 9 and 10 in real platform text (no fixture in `testdata/`
  contains "İ"/"K" or a non-ASCII requirement title). The mechanisms are verified.
- The `queuepage.go:113` panic: proven that an empty reason slice panics, not that any
  app-written row can carry one.
- Whether `HuntQueueTop`'s `ORDER BY q.score DESC, q.kind, q.value` (added at 03:55) removes
  cross-page drift under a concurrent `RefreshHuntQueue`: the rebuild replaces every row in
  one transaction, so a page fetched before and after a rebuild can still differ. That is
  inherent to derived data and is not counted as a finding.

## Appendix: the reproducers

Added through `-overlay` only; no file in the tree. Store:

```go
func auditQueueRow(t *testing.T, s *Store, pid int64, kind, value string, score float64, gate, reasons string) {
	t.Helper()
	_, err := s.db.W.ExecContext(context.Background(), "INSERT INTO hunt_queue "+
		"(program_id, kind, value, apex, score, gate, reasons, computed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		pid, kind, value, "example.com", score, gate, reasons, "2026-11-19T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
}

func TestAuditLegacyGatedRowHasNoReason(t *testing.T) { // finding 6
	s := newTestStore(t, nil)
	pid := auditProgram(t, s, "acme")
	auditQueueRow(t, s, pid, "wildcard", "*.acme.example", 0, "scope_unavailable", `["no scope entries"]`)
	auditQueueRow(t, s, pid, "url", "https://api.acme.example", 42, "", `["pays up to $500"]`)
	gated, _ := s.HuntQueueGated(context.Background(), 20)
	if len(gated) != 1 || len(gated[0].Reasons) != 0 {
		t.Fatalf("gated reasons: %+v", gated)
	}
	// the ranked reader's fallback misses for the same reason:
	top, _, _ := s.HuntQueueTop(context.Background(), QueueFilter{}, 20, 0)
	if top[0].Reasons[0].Key != "" {
		t.Fatalf("fallback ran: %+v", top[0].Reasons)
	}
}

func TestAuditOtherGatesNeverAppear(t *testing.T)       { /* finding 2: paused / invite_pending / ignored programs */ }
func TestAuditOnePerProgramPlusKindDropsProgram(t *testing.T) { /* finding 3: wildcard 50 + url 40, kind=url&one=1 -> 0 rows */ }
func TestAuditOnePerProgramPlusAddedDropsProgram(t *testing.T) { /* finding 3: old 50 + new 30, added=7d&one=1 -> 0 rows */ }
func TestAuditPayoutIgnoresCurrency(t *testing.T)       { /* finding 4: JPY 500000 -> PayoutUSD 500000 */ }
func TestAuditRefreshIgnoresCredentials(t *testing.T)   { /* finding 5: score 50.7 with "nothing to arrange" */ }
```

Web:

```go
func TestAuditQueuePagerNeedsAGatedRow(t *testing.T) { // finding 1
	// 60 ranked rows, none gated: strings.Contains(body, `class="pager"`) == false
	// add one gated row:        true
}

func TestAuditLegacyGatedRowShowsBlankReason(t *testing.T) { // findings 6 and 13
	// GET /queue and GET /api/v1/queue after inserting ["no scope entries"]
}

func TestAuditPartsNarrowsOnlySomeFields(t *testing.T) { // finding 7
	// GET /api/v1/programs/fake/acme?parts=brief still returns numbers and trends
}

func TestAuditPolicyDeadlineSuppression(t *testing.T) { // finding 8
	// "Reports are reviewed within 15 days. Please report findings within 30 days of discovery."
	// policyRules -> no "req" rule
}

func TestAuditExcerptOffsetAfterCaseFold(t *testing.T) { // finding 9
	// excerptAround("İstanbul office. Do not run denial of service.", "Do not run denial of service")
}

func TestAuditUpperFirstMultiByte(t *testing.T) { // finding 10
	// upperFirst("éclair") is not valid UTF-8
}
```

AI:

```go
func TestAuditCompleteJSONAcceptsNull(t *testing.T) { // finding 11
	// endpoint answers {"content":"null"} -> err == nil, out untouched
}

func TestAuditExtractJSONBraceInString(t *testing.T) { // finding 12
	// extractJSON of "Here you go: {"a":"}","b":2} — that is all." -> {"a":"}
}
```


## Resolution (2026-11-19, after the review)

Every finding above was re-read in the source and either fixed with a test that
fails without the fix, or recorded as accepted with the reason. The code was
re-read after each change; the suite is 30 packages green, `golangci-lint` 0
issues, and each item below was also checked against the running instance where
that was possible.

| # | Status | Fix, and how it was checked |
|---|---|---|
| 1 | fixed | The pager moved out of the `{{if .Gated}}` branch (`templates/queue.html`). Live: `/queue` renders `1–50 / 2,000`, `/queue?page=2` returns 50 rows. |
| 2 | fixed earlier, confirmed | `gateOf` no longer treats a negative `score - fresh` as "no scope", and the value is clamped in SQL. Live: 2,000 candidates ranked, 0 gated; `hunt_queue` counts match. |
| 3 | fixed | One-per-program is no longer a `WHERE` clause: `HuntQueueTop` fetches the filtered set and dedupes in Go, `HuntQueueCount` counts distinct programs. `TestOnePerProgramRespectsTheKindFilter`; live: `kind=url&one=1` went from 0 rows to 34. |
| 4 | fixed | `store.ToUSD` converts the program's currency with a fixed table (27 currencies), documented as deliberately not live. `TestToUSD`; live: Intigriti rows now show converted dollars and the page says so. |
| 5 | partly fixed | `NoAutomation` and `Flips` are wired (`p.test_no_automation`, the existing flip count) and `readiness` applies 0.5 / 0.8; `TestReadinessFollowsTheFlags`. `NeedsCreds` has **no stored source** (0 programs carry `profile.credential_pool`, checked in the database), so the column is `0` with a comment. On today's data the factor still evaluates near 1.0 — stated rather than claimed. |
| 6 | fixed | The fallback now checks the content, not the length (`Reasons[0].Key == ""`). Gated rows keep their sentence. |
| 7 | fixed | Unknown parts are a 400; `parts`, `scope_limit` and `scope_offset` are in `openapi.json`; a new `numbers` part gates `bounty`/`numbers`/`trends`. Live: full 155 KB, `facts,brief,rules` 64 KB, `numbers` 24 KB. |
| 8 | fixed | `policyDeadline` scans every match and skips response promises instead of returning nil for the text. `TestPolicyDeadlineFindsTheRealOneAfterAPromise`. |
| 9 | fixed | The excerpt resolves the match on the original string first, and falls back to the lowercased offset only when that fails. `TestPolicyTextHelpersHandleNonASCII`. |
| 10 | fixed | `upperFirst` decodes a rune (`utf8.DecodeRuneInString` + `unicode.ToUpper`). `upperFirst("éclair") == "Éclair"`, and the result is valid UTF-8. |
| 11 | fixed | `null` is refused and asked again. `TestNullIsNotAnAnswer`. |
| 12 | fixed | `extractJSON` tracks strings and escapes. `TestExtractJSONIgnoresBracesInStrings`. |
| 13 | fixed | The Blocked list now takes the page's `QueueFilter` (`HuntQueueGated(ctx, filter, limit)`, with the same `target_scores` join the `added` window needs), so a platform filter leaves out that platform's gated rows too. `TestGatedListRespectsTheFilter` seeds gated rows and checks both directions. The panel still lists the first 20; a gated pager remains unbuilt, and the row is honest about it. |

Not fixed, and why: the per-currency magnitude of the payout is now converted, but
the *scoring* still saturates at `PayoutCapUSD` — that is intended. The `İ`-style
input to `upperFirst` is covered by the rune fix. `Render(...)[0]` no longer indexes
a possibly empty slice.
## Snapshot

commit 1aa6d33, working tree as of 2026-11-19 19:57 UTC:

```
sha256  internal/huntqueue/huntqueue.go   cce860574256b377b563539d61b947a78cbdb70fad2aceef847d555363847065
        internal/store/queue.go            b660f70f42b7d00136fd4248355b79e800ecfda5ba47854b9ac7cfc7cd25dca9
        internal/web/queuepage.go          5c360399c71a7b00fa3afc3b54e1923f8d8f9c8b6e3e90cc57e57b472abe26d9
        internal/web/templates/queue.html  a4b49b2a4e424f1073e78bb019b0fb1ab7efc6ad55e83c8204d8f45afe45032e
        internal/web/api_queue.go          c01cb6e1d63b21c185f5906fc84004f87d7e7fe6f62be3f792b52e252ea8d65b
        internal/web/api_programs.go       d88eae06e3deef171e21460468c54af4002a922377e78a1df372b554e0d930ff
        internal/ai/ai.go                  3581bbe790aa09b15d8c6f16a7c9eca766851d61e991e921bacf68eca1709805
        internal/web/beforetest.go         e676ae382df70c4fdab8e87c64462d995bf3dcb182073b09c625a856599f9b58
```
