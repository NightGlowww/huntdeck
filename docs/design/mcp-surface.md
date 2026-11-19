# HuntDeck MCP: current surface, gaps, and a design for what it should offer

Status: design proposal. Nothing here is implemented; no file outside /tmp was
touched. Read from the checkout: `internal/mcpserver/{server,tools,docs_test,server_test}.go`,
`internal/web/api*.go`, `internal/web/server.go`, `internal/web/openapi.json`,
`docs/mcp.md`, plus the page handlers whose output the tools borrow
(`/changes?format=txt`, `/programs/{p}/{h}?tab=scope&format=txt`, the export routes).

---

## 1. Today's surface

`bin/huntdeck-mcp` is a stdio MCP server that speaks to a running instance over its
own REST API, signing in with `HUNTDECK_PASSWORD` like a browser and holding the
`hd_session` cookie in memory. Tools are declared in a package-level slice
(`internal/mcpserver/tools.go:24`) and dispatched by name (`:269`). The protocol
layer (`server.go`) answers `initialize`, `ping`, `tools/list`, `tools/call` and
nothing else; it advertises `capabilities: {tools:{}}` only (`server.go:124-128`)
at protocol `2025-06-18` (`:25`).

### 1.1 The 13 tools

| Tool | Wire call | Parameters it forwards | Real ceiling |
|---|---|---|---|
| `huntdeck_status` | GET /api/v1/status | none | whole body |
| `huntdeck_programs` | GET /api/v1/programs | q, platform, state, bounty, sort, limit (30, "at most 200"), offset | API clamps limit to 1,000; the 200 is a doc-only claim (`api_programs.go:207`) |
| `huntdeck_programs_csv` | GET /api/v1/programs?format=csv | q, platform, state, bounty, sort, cols | no paging at all: `programsCSV` sets limit to 1<<20 (`programscsv.go:108`) |
| `huntdeck_program` | GET /api/v1/programs/{p}/{h} | platform, handle | v1 shape only; whole program: scope + up to 5,000 disclosures (`api_reports.go:107`) |
| `huntdeck_targets` | GET /api/v1/targets?format=json | kind, platform, q, scope, bounty, added, tag, sort, limit (100) | no offset; `kind` forced to `domain` when absent (`tools.go:102-104`) |
| `huntdeck_match` | GET /api/v1/match | host | one host per call; the bulk route is not offered |
| `huntdeck_changes` | GET /api/v1/changes | since, platform, kind, program, limit (50) | no bounty, order, offset; kinds hardcoded in the description (`tools.go:132`) |
| `huntdeck_changes_text` | GET /changes?format=txt | since, platform, group, kind, bounty, program, sort, rev | 5,000-line server cap; no scope/change/watch |
| `huntdeck_disclosed` | GET /api/v1/disclosed | q, platform, severity, cve, sort, limit (50) | no weakness, paid, since, offset; no `high+` severity |
| `huntdeck_scope_text` | GET /programs/{p}/{h}?tab=scope&format=txt | platform, handle, show, type, since | no q, no order; page route, not API |
| `huntdeck_export` | GET .../scope.{burp.json,zap.context,caido.json} | platform, handle, format | no comment=1; a typo in `format` silently returns Burp (`tools.go:212-219`); response headers dropped (generated-at, X-HuntDeck-Skipped) |
| `huntdeck_stats` | GET /api/v1/stats | **none** | 20 API parameters ignored (`tools.go:224-230`) |
| `huntdeck_poll` | POST /api/v1/poll/{platform} | platform | no `mode`; one **global** 60 s cooldown (`tools.go:345-360`), so polling bc right after h1 is refused |

### 1.2 Global limits, as implemented

| Limit | Value | Where | Behaviour when hit |
|---|---|---|---|
| Call budget | 120 calls / 60 s sliding window, per MCP process | `tools.go:372-395` | tool error "too many calls... try again in Xs"; client sees `isError:true` |
| Response cap | 8 MiB, `io.LimitReader` | `server.go:212` | **silent truncation**: the call succeeds with a cut body |
| HTTP timeout | 30 s | `server.go:60` | wrapped transport error |
| Redirects | never followed | `server.go:60-62` | a 3xx that is not /login becomes an error |
| Auth | one password, cookie in memory, re-login on 401/403 | `server.go:159-183, 227-249` | `errUnauthorized` |
| Read-only | hides `Write` tools and refuses them by name | `tools.go:260-262, 277-279` | error naming HUNTDECK_MCP_READONLY |
| Poll cooldown | 60 s, global | `tools.go:345` | error naming the wait |

### 1.3 What today's server cannot do

- **No resources, no prompts, no structured output.** Everything is a `content:[{type:"text"}]`
  blob of raw REST JSON, so the model re-parses JSON from a string at full token cost.
- **No tool annotations** (`readOnlyHint`, `idempotentHint`, `destructiveHint`), so clients
  cannot auto-approve the 12 read-only tools.
- **No conditional requests.** The API sends `ETag`/`Last-Modified` on program lists,
  program pages, invites and `GET /api/v1/match` (`docs/api.md:55`); the client ignores
  them, so a polling agent re-transfers identical bodies.
- **No pagination contract.** Only three tools take `offset`; `targets`, `disclosed` and
  `programs_csv` cannot page, and no tool reports `total` next to what it returned (the
  API does return `total`; it sits inside the blob).
- **No partial/projected program reads.** A big program is one indivisible response.
- **No bulk scope check** (POST /api/v1/match, 20,000 lines / 4 MB).
- **No board, no invites, no per-platform stats, no v2 freshness.**
- **Writes are one tool, unguarded by intent:** `huntdeck_poll` is offered by default;
  nothing else writes. There is no dry-run, no per-write confirmation, and the
  read-only switch is all-or-nothing.

---

## 2. Defects worth fixing before adding anything

Ranked; all are in code I read, with the line that carries them.

1. **Silent truncation at 8 MiB** (`server.go:212`). `io.ReadAll(io.LimitReader(body, 8<<20))`
   returns a short body with no error. The dangerous caller is `huntdeck_export`: an
   agent that saves a truncated Burp/Caido JSON produces a file that will not import,
   and nothing in the response says so. `huntdeck_program` on a large program and
   `huntdeck_programs_csv` on a 10k-program instance are the likeliest to cross it.
2. **Global poll cooldown** (`tools.go:345-360`): one `lastPoll` for every platform. The
   error text ("a poll was asked for 12s ago") is also wrong for the second platform,
   which was never polled.
3. **`huntdeck_stats` has no parameters** while `/api/v1/stats` accepts 20
   (`openapi.json`). The tool answers "everything, by currency, unsorted", which is
   almost never the question ("who pays best", "which private programs pay").
4. **Forced `kind=domain`** (`tools.go:102-104`): a wildcard-only program's scope
   disappears from "what is in scope" unless the model guesses `kind=wildcard`.
   `kind=all` exists in the API (`api.go:81`) and should be the default.
5. **`huntdeck_export` silently falls back to Burp** for an unknown `format`
   (`tools.go:212-219`). Should be an invalid-argument error naming burp/zap/caido.
6. **Behaviour/doc mismatch** on `huntdeck_programs` ("at most 200" vs 1,000) and
   `huntdeck_targets` ("at most 1000" vs API 5,000): an agent that trusts the
   description under-reads and cannot tell.
7. **No `read_only` in `/api/v1/status`** (`apitypes.go:25-29`). An MCP server pointed at
   a read-only or public copy cannot know it: the write tools are offered, then 403 at
   call time with "this copy of HuntDeck is read-only" (`readonly.go:57`).
8. **Poll API answers collapse into generic HTTP errors.** 202 started, 409
   already-running, 404 not configured, 409 disabled in Settings, 503 unavailable
   (`api.go:224-237`) all become "POST ...: HTTP 409: {json}"; the agent cannot branch.

---

## 3. Gap list: REST exposes it, MCP does not

### 3.1 Endpoints with no tool at all

| REST | What it answers | Why it matters to an agent | Read-only |
|---|---|---|---|
| `GET /api/v1/board` | totals, 24 h / 7 d change counts, week counters, reopened/paused, flip-floppers, top hunts | "what should I look at today" in one call, memoised server-side (`api_board.go:66`) | yes |
| `GET /api/v1/invites`, `/api/v2/invites` | private programs / pending invitations | private scope is otherwise invisible unless the agent lists with `visibility=private` | yes |
| `POST /api/v1/invites/{p}/{h}/accept` | accept an invitation | closes the loop after `huntdeck_invites` | write |
| `POST /api/v1/match` | 20,000 hosts per call (2,000 on a read-only copy), `format=txt` | subfinder/nuclei pipelines: the single most valuable missing capability | yes (the route is whitelisted as read-only, `readonly.go:22`) |
| `GET /api/v1/stats/platforms` | the Stats page: every platform, a year of months, day series | "which platform is worth the time" | yes |
| `GET /api/v1/stats/platforms/{platform}` | one platform, its programs with sortable columns | "who pays best on H1 this quarter" | yes |
| `GET /api/v2/programs`, `/v2/programs/{p}/{h}` | every estimable number as `{value, estimated, source, updated_at}` | an agent must know whether `paid_90d` is measured or estimated before quoting it | yes |
| `GET /api/v1/openapi.json` | the spec itself | self-description / drift check | yes |
| `GET /healthz` | `ok` / `unavailable` | distinguishes "DB down" from "no data" | yes |

### 3.2 Endpoints MCP calls, with parameters it drops

| Endpoint | Parameters missing from the tool |
|---|---|
| `/api/v1/programs` | `not`, `watch`, `noted`, `top`, `visibility`, `invite`, `new`, `launched`, `resumed`, `wild`, `apps`, `api`, `creds`, `noreq`, `ignored`, `tag`, `rev`, `trends`, `since` (19 of 22) |
| `/api/v1/targets` | `private`, `include_paused`, `include_ignored`, `since`, `offset`; `sort=az\|bounty`; text output (`format=txt`, `download`, `comment`); `kind=roots\|all` and asset-type aliases |
| `/api/v1/changes` | `bounty`, `order` (newest/oldest), `offset`, limit 1,000 |
| `/api/v1/disclosed` | `weakness`, `paid`, `since`, `offset`; the `high+` severity (critical + high, `disclosed.go:15-18`); `format=txt` |
| `/api/v1/stats` | all of them: `platform`, `state`, `bounty`, `top`, `visibility`, `new`, `launched`, `resumed`, `wild`, `apps`, `api`, `creds`, `noreq`, `tag`, `ignored`, `q`, `sort`, `rev`, `limit`, `offset` |
| scope exports | `comment=1` (ZAP XML comment / targets text comment) and the response headers the client discards: `X-HuntDeck-Generated-At`, `X-HuntDeck-Skipped` (`labels.go:108-140`) |
| `/api/v1/poll/{platform}` | `mode=quick\|full` |
| page routes | `/changes`: `scope`, `change`, `watch`; `/programs/{p}/{h}` scope text: `q`, `order` |

### 3.3 The items named in the brief, and where they actually stand

- **Program detail / scope export formats** - detail is exposed (v1 only); the three
  export routes are exposed but lose `comment`, the skipped count and the generated
  timestamp. Fix by extending the tool, not by adding routes.
- **Disclosed search** - exposed, but with 4 of 8 filters and half the severity
  vocabulary. `weakness` is the one that matters: the Disclosed page's rail is built
  from it (`disclosed.go:91-100`).
- **Stats** - three routes, one parameterless tool.
- **Saved views** - **not a REST capability.** Views live in `localStorage` per page
  (`myviews.go:1-38`); the server never sees them, so MCP cannot read them.
  Recommendation: do **not** add a `views` table for this; expose the full filter
  vocabulary so a pasted view URL's keys are all understood. If users ask for
  server-side views later that is a separate change: a `views` table, a migration, a
  `seedspec` entry (user data, left out of seeds), `GET/POST /api/v1/views`, then a tool.
- **Changes feed with filters** - see 3.2; `/changes.atom` is XML and not worth an agent
  tool.
- **Notifications preview** - exists only as an HTML fragment
  (`GET /settings/notifications/preview`, `notifypreview.go:116`). Not usable over MCP.
  Prerequisite: a JSON route (4.4) returning the same `notify.Preview` the fragment
  renders, without channel secrets.
- **Poll triggering with guards** - `mode`, per-platform cooldown, a running-poll
  pre-check from `last_polls[].finished_at`, and a per-session hourly cap. Today: none.

---

## 4. Proposed additions

Design rules: one REST call per tool where possible; no new third-party module; no
accounts; nothing that writes without an explicit opt-in; every tool declares its cost
and its size cap; text stays text (scope, CSV, exports) and JSON becomes
`structuredContent` plus a short text rendering.

### 4.1 Tool struct and protocol changes (enabling work)

    type tool struct {
        Name, Description string
        Schema   map[string]any
        Write    bool   // hidden and refused in read-only mode
        Cost     int    // call-budget units; 0 means 1
        MaxBytes int64  // response cap; 0 means defaultMax
        Call     func(ctx context.Context, s *Server, args map[string]any) (Result, error)
    }

    type Result struct {
        Text       string         // always sent
        Structured any            // optional: structuredContent
        Meta       map[string]any // total, returned, next_offset, truncated
    }

- `initialize` advertises `resources` and `prompts` once implemented, and gains a short
  `instructions` string ("Start with huntdeck_board; scope is authoritative - call
  huntdeck_match before testing; 120 calls/minute").
- Every tool gets `annotations:{readOnlyHint, idempotentHint, openWorldHint:false}`;
  writes add `destructiveHint`.
- `do` becomes `doReply` returning `{Body, Status, Header}`; `get` keeps its signature
  on top of it. That unlocks ETag caching, export metadata and precise poll-answer
  mapping without touching call sites much.
- Response cap: read `max+1` bytes and **fail** with a remedy, never truncate:

      func readCapped(r io.Reader, max int64) ([]byte, error) {
          b, err := io.ReadAll(io.LimitReader(r, max+1))
          if err != nil { return nil, err }
          if int64(len(b)) > max {
              return nil, fmt.Errorf("the answer is over %d KiB: narrow the filters, lower limit, or page with offset", max>>10)
          }
          return b, nil
      }

- ETag cache: a `map[string]etagEntry` bounded to 64 entries, keyed by path+query;
  `if_none_match:true` on list/detail tools sends `If-None-Match` and answers
  `{"unchanged":true,"etag":"..."}` on 304. The cheapest "did anything move" loop an
  agent can have, and it costs no body bytes.

### 4.2 New tools

Legend: **RO** = readable in read-only mode; **cost** in call-budget units; **cap** =
response cap.

#### huntdeck_board
- Purpose: the hunt board summary - the first call of any session.
- Call: `GET /api/v1/board`.
- Parameters: none.
- Returns: `{totals{programs,private,open,paying,in_scope_entries,wildcards,domains},
  changes_24h, changes_7d, week{scope_added,programs_added_scope,wildcards_added,
  domains_added,new_programs,new_invites,bounty_changes}, reopened[], paused[],
  flip_flopping[], top_hunts[{program,offers_bounty,hunt_score,in_scope_entries,
  wildcards,top_bounty,average_bounty,currency}]}`.
- RO yes - cost 1 - cap 512 KiB - memoised by the instance.

#### huntdeck_invites
- Purpose: private programs and pending invitations.
- Call: `GET /api/v1/invites` (or `/api/v2/invites` when `freshness:true`).
- Parameters: `platform`, `invite` (pending|joined), `state`, `bounty`, `sort` (invited
  default), `limit` (30), `offset`, `freshness`.
- Returns: the API's `{total, programs[]}` with `invite{pending,invited_at,joined_at}`.
- RO yes - cost 1 - cap 512 KiB.

#### huntdeck_match_many  (highest value)
- Purpose: check a host/IP/URL list against all live scope in one call.
- Call: `POST /api/v1/match` with the list in the body, `format=json`.
- Parameters: `hosts` (array, <=5,000) **or** `hosts_text` (newline/comma separated,
  <=1 MB); `mode` = `in` (default: only in-scope hosts, grouped by program), `all`
  (per-input verdict), `text` (in-scope hosts, one per line); `bounty` (only
  bounty-eligible); `include_out` (with `mode=all`, keep the `out` verdicts too).
- Returns: `{checked, in, out, none, groups:[{program, bounty, hosts[]}],
  results:[{input,verdict,matches[{via,via_kind,program,in_scope}]}]}` (`results`
  omitted in `in` mode; `text` returns plain lines).
- RO yes (the instance whitelists this POST as read-only) - cost 3 - cap 2 MiB -
  <=5,000 lines and 1 MB per call, split internally into 2,000-line batches, and the
  sixth bulk call in five minutes is refused. Instance limits to respect: 20,000
  lines/4 MB, 2,000/2 MB read-only (`api_match.go:15-20`), `ErrTooManyLookups` -> 413.

#### huntdeck_stats_platforms
- Purpose: the Stats page - per platform, or one platform's programs.
- Call: `GET /api/v1/stats/platforms` or `/stats/platforms/{platform}`.
- Parameters: `platform` (omit for the overview), `sort` (received|valid|bounties|paid|
  resolve), `rev`, `q`, `bounty`, `series` (default false), `limit` (20), `offset`.
- Returns: `{month, all|platform{...,months[],reports_received_90d_by_day[],
  paid_90d_by_day[]}, columns[], sort, total, programs[]}`.
- RO yes - cost 2 (the overview walks every program) - cap 1 MiB. `series:false` drops
  the day lines, which are most of the bytes.

#### huntdeck_scope
- Purpose: a program's scope entries as JSON, filtered and paged - the companion to
  `huntdeck_scope_text`.
- Call: `GET /api/v1/programs/{p}/{h}`, projected to `scope` locally.
- Parameters: `platform`, `handle`, `show` (in|out|removed|all), `type`, `q` (substring
  on identifier), `bounty`, `order` (added|identifier), `limit` (200), `offset`.
- Returns: `{platform,handle,total,returned,offset,next_offset,entries[{in_scope,
  asset_type,platform_asset_type,identifier,bounty_eligible,test_credentials,
  max_severity,instruction,first_seen_at,added_at,removed_at,targets[{kind,value}]}]}`.
- RO yes - cost 1 - cap 1 MiB. Filtering happens in the MCP server so the instance's
  program query is paid once; keep a 5-minute, 8-entry per-program cache so
  `limit/offset` paging does not refetch.

#### huntdeck_accept_invite  (write, opt-in)
- Purpose: accept one invitation, then suggest the follow-up poll.
- Call: `POST /api/v1/invites/{p}/{h}/accept`.
- Parameters: `platform`, `handle`, `confirm` (must equal `handle`), `dry_run`.
- Returns: `{accepted:true, program:{platform,handle}, next:"huntdeck_poll platform=...
  mode=full"}`.
- RO **no** - requires `HUNTDECK_MCP_WRITE_TOOLS` to include `invites` - cost 3.
- Hidden entirely when the instance reports `read_only:true` (4.4).

#### huntdeck_label  (write, opt-in)
- Purpose: tag, ignore/unignore, or note a program - the only mutations the UI offers
  besides invites.
- Calls (form-encoded page routes): `POST /programs/{p}/{h}/tags` with `tags`,
  `POST /programs/{p}/{h}/ignore` with `ignored=1|0`, `POST /programs/{p}/{h}/note` with
  `note` (`labels.go:41,63`; `program.go:344`).
- Parameters: `platform`, `handle`, `field` (tags|ignored|note), `value`, `expect` (the
  value the caller believes is there now; a mismatch is refused), `dry_run`.
- Returns: `{applied, field, before, after, rescored:true}`. These routes answer 303;
  the client needs a mode that treats 303 as success and reads the new value back.
- RO **no** - requires `HUNTDECK_MCP_WRITE_TOOLS` to include `labels` - cost 2.
- No bulk variant: `POST /programs/ignore` is deliberately not exposed (a loop there is
  a mass mutation behind one tool name).

#### huntdeck_notify_preview  (needs a REST prerequisite)
- Purpose: "would my rules have sent anything this week?" - a read-only sandbox for
  notification rules.
- Prerequisite route: `GET /api/v1/notifications/preview?kind=&instant=&digest_at=...`
  returning the same `notify.Preview` the HTML fragment renders
  (`notifypreview.go:96-111`), with message bodies but **no** channel address or secret.
- Parameters: `kind` (discord|telegram|webhook|bark|serverchan), `instant`, `digest_at`,
  `since` (7d default), `limit` (10).
- Returns: `{kind, since, sent, rate_limited, shown:[{title,body,program,url}]}`.
- RO yes - cost 2 - cap 256 KiB. `POST /settings/notifications/test` must never be
  exposed: it sends real messages.

### 4.3 Modified tools

| Tool | Change |
|---|---|
| `huntdeck_program` | add `parts` (default core,numbers,hunt,brief,requirements,facts,testing; scope, disclosed, accepted, resolved, announcements, trends on request), `freshness` (calls /api/v2/...), `scope_limit`/`scope_offset`, `disclosed_limit`/`disclosed_offset` (local projection) |
| `huntdeck_programs` | full filter set from 3.2, `rev`, `since`, `freshness` (v2), honest limit doc (1-1,000) |
| `huntdeck_targets` | `kind=all` default, `private`, `include_paused`, `include_ignored`, `since`, `offset`, `sort` (best\|az\|bounty\|newest), `format` (json\|txt) |
| `huntdeck_changes` | `bounty`, `order` (newest\|oldest), `offset`, limit 1,000; kinds generated from `store.ChangeKinds()` through a shared vocabulary so the description cannot drift |
| `huntdeck_changes_text` | `scope`, `change`, `watch` |
| `huntdeck_disclosed` | `weakness`, `paid`, `since`, `offset`; severities critical\|high+\|high\|medium\|low\|none; `format=txt` |
| `huntdeck_scope_text` | `q`, `order`; report the line count |
| `huntdeck_export` | `comment`, `meta` (returns `{generated_at,skipped,content_type,filename,body}`), invalid format is an error, cap 16 MiB (these are deliverables) |
| `huntdeck_stats` | the 20 filters, `sort` from `store.StatsSorts`, `limit` (100), `offset` |
| `huntdeck_poll` | `mode` (quick\|full), per-platform cooldown, running-check from `last_polls`, <=6 polls/hour per session, 409/404/503 mapped to their own messages, hidden when the instance is read-only |

### 4.4 REST-side prerequisites (small, and worth their own change)

1. `read_only` (and maybe `mcp:{write_tools:...}`) in `/api/v1/status` - lets any client
   avoid offering writes that will 403.
2. `GET /api/v1/notifications/preview` (JSON, no secrets) - see 4.2.
3. Optionally `GET /api/v1/tags` (the tag set `store.Tags` already returns for the
   pages): without it an agent cannot know which `tag=` values exist, and a typo
   silently behaves as "no filter" (`labels.go:80-88`). Cheap, high value.

---

## 5. Resources

Resources are the right shape for artifacts an agent wants whole, repeatedly, without
spending call budget on JSON assembly. They are read-only by construction.

| URI | MIME | Content | Cap |
|---|---|---|---|
| `huntdeck://board` | application/json | board snapshot | 256 KiB |
| `huntdeck://stats/overview` | application/json | `/stats/platforms` without day series | 256 KiB |
| `huntdeck://changes/{24h\|7d\|30d}` | application/json | changes in the window | 512 KiB |
| `huntdeck://program/{platform}/{handle}/brief` | text/markdown | assembled brief: name, platform, bounty range + source, numbers with estimate flags, rules (rate limit, no automation, UA, header, credentials), requirements, policy, in/out-of-scope notes, severity notes, exclusions, top disclosed weaknesses | 256 KiB |
| `huntdeck://program/{platform}/{handle}/scope` | text/plain | in-scope identifiers, one per line (the export the UI copies) | 2 MiB |
| `huntdeck://program/{platform}/{handle}/scope-out` | text/plain | out-of-scope list, for filtering | 2 MiB |
| `huntdeck://program/{platform}/{handle}/export/{burp\|zap\|caido}` | application/json or application/xml | the import file itself | 16 MiB |

`resources/templates/list` publishes the four parameterized ones; `resources/list`
publishes the three static ones. Reads share a separate 60/min budget (resources are
cheap but not free) and are capped as above. Nothing subscribable: the instance is
polled, not evented.

Why a brief resource rather than a tool: the JSON already carries every piece, but
assembling it is exactly the work an agent does badly - `brief.policy`,
`brief.in_scope_notes`, `brief.scope_exclusions`, `testing_rules`, `requirements` and
`facts.known_issues` are six separate keys, and the rules for testing are prose in
three of them. One markdown document with the same facts, plus the out-of-scope list at
the bottom, is what a hunter actually reads before testing.

## 6. Prompts

Prompts are user-invoked templates. They may fetch (within the call budget) and must
stay bounded; they render into one user message plus a short system line.

| Prompt | Arguments | What it produces |
|---|---|---|
| `hunt_brief` | platform, handle | "Before you test": the brief resource inline, then explicit constraints (rate limit, no-automation, required header/UA, test accounts, out-of-scope list) and the ask: propose five test surfaces that are in scope and unusual, with the rule each stays inside |
| `scope_triage` | platform, handle, since (7d) | the window's scope changes inline, grouped added/removed/edited; ask which new assets are worth testing first and why, and which removals invalidate prior work |
| `host_triage` | hosts (newline) | runs `huntdeck_match_many`, then asks which program to report under for each host, which hosts are covered by more than one program, which are out of scope |
| `daily_digest` | since (24h) | board week numbers + changes + new invites, as a short morning brief with three priorities |
| `duplicate_check` | platform, handle, query | disclosed reports of that program matching the query, framed as "read these before testing; say what is already covered" |

Bounded: each prompt refuses when its rendered text would exceed 64 KiB and says what
to narrow (`since`, `query`, fewer hosts).

## 7. Safety limits (what the design promises)

| Limit | Value | Rationale |
|---|---|---|
| Tool calls | 120/min/session, configurable `HUNTDECK_MCP_CALL_BUDGET` | unchanged; documented with the cost table |
| Cost weights | board/invites/scope/text 1, stats/program 2, match_many/accept 3, poll 5 | a bulk call must not be as cheap as a status call |
| Response cap | 1 MiB default, 2 MiB lists, 16 MiB exports, 512 KiB saved answers - **error, never truncate** | today's 8 MiB silent cut is the worst failure mode |
| Worst-case egress | 120 x 1 MiB/min = 120 MiB/min instead of 960 | the cap is what makes the budget mean something |
| Bulk match | 5,000 hosts / 1 MB per call, 20,000 hosts and 6 calls per 5 min | four times under the instance's 20,000 |
| Program reads | scope <=500 entries/page, disclosed <=100/page | keeps one program readable in pieces |
| ETag cache | <=64 entries, in memory | repeat reads answer `unchanged` |
| Resources | 60 reads/min plus the caps above | separate from the tool budget so a big brief cannot starve tools |
| Write tools | **off unless `HUNTDECK_MCP_WRITE_TOOLS` names them** (poll is the default, then invites, labels, all) | today's poll default is kept; nothing new becomes writable by accident |
| Write guards | `dry_run` on every write; `confirm` echo for accept; `expect` for labels; no bulk mutation | a mis-inferred argument must not change data |
| Poll | 60 s per platform, <=6/hour, running-check, `mode` restricted to quick\|full | the platforms are shared with everyone else |
| Never exposed | settings, accounts/credentials, notification test, restart, seed export, VNC, arbitrary-path proxy | no secrets, no remote sends, no host operations |

## 8. Documentation requirements

`docs/mcp.md` (87 lines today) gains:

1. **Tools table**, one row per tool with the parameters in backticks:
   `| Tool | What it answers | Parameters |`. Every parameter named in the schema must
   appear in its row (new test).
2. **Limits table**: default cap, cost, and what the error says when hit.
3. **Read-only and write opt-in**: the three modes (`HUNTDECK_MCP_READONLY=1`, the
   default, `HUNTDECK_MCP_WRITE_TOOLS=`), and that a read-only *instance* is detected
   from `/api/v1/status` and hides writes by itself.
4. **Resources** table and **Prompts** table, with one worked example each.
5. **Recipes**: bulk scope check from subfinder; brief before testing; "what changed
   this week"; export to Burp then import.
6. **A note that saved views are browser-local**, so nobody asks the server for them.
7. README line 151 (the MCP bullet) gains board / scope check / brief.

## 9. Test requirements

`internal/mcpserver/docs_test.go` (34 lines today):

- Keep the two-way name check (`TestEveryToolIsDocumented`).
- Add `TestEveryToolParameterIsDocumented`: every property key in a tool's `inputSchema`
  must appear in its docs row, and no documented parameter is missing from the schema.
- Add `TestEveryResourceAndPromptIsDocumented`: the same two-way rule for `huntdeck://`
  URIs and prompt names.
- Add `TestToolSchemaIsSelfConsistent`: every `required` name exists in `properties`;
  every property has a `type` and a non-empty `description`; `additionalProperties:false`
  everywhere.

`internal/mcpserver/server_test.go` (294 lines today, `httptest` fake instance):

- Extend the fake with the new routes: `/api/v1/board`, `/api/v1/invites`,
  `POST /api/v1/match`, `/api/v1/stats/platforms[/{p}]`, `/api/v2/programs[/{p}/{h}]`,
  `/api/v1/programs/{p}/{h}/scope.zap.context`, `/programs/{p}/{h}` (page text),
  `POST /programs/{p}/{h}/{tags|ignore|note}` answering 303.
- `TestToolForwardsEveryParameter`: table of tool -> args -> expected query keys and
  values recorded by the fake. The drift guard for 3.2 and for the `kind=domain` class
  of bug.
- `TestResponseCapIsAnError`: the fake returns cap+1 bytes; the tool must error with the
  remedy text and must not return a partial body (replaces today's silent cut).
- `TestExportFormatIsValidated`: `format=burpsuite` errors and names burp/zap/caido.
- `TestPollPerPlatformCooldown`: h1 then bc both start; a second h1 within 60 s is
  refused with the wait; six polls in an hour hit the hourly cap.
- `TestPollModeForwarded` and `TestPollAnswersAreMapped`: 409 running, 404 unknown, 409
  disabled, 503 unavailable each produce their own message.
- `TestWriteToolsRequireOptIn`: with the default write set, `huntdeck_label` and
  `huntdeck_accept_invite` are absent from `tools/list` and refused by name, while
  `huntdeck_poll` is present.
- `TestDryRunMakesNoRequest`: the fake fails the test if any POST arrives.
- `TestMatchManyBatchesAndAggregates`: 5,001 hosts refused; 4,500 -> three POSTs of
  <=2,000, one merged answer, counts consistent.
- `TestETagAnswersUnchanged`: the fake sends `ETag`; a second identical call sends
  `If-None-Match` and answers `{"unchanged":true}` on 304.
- `TestStructuredContentMatchesText`: for every JSON tool, `structuredContent` parses
  and its top-level keys match the text summary.
- `TestProgramPartsProjection`: `parts=["brief"]` returns only `brief` (plus the ref);
  `parts=["scope"]` honours `scope_limit/offset` and reports `next_offset`.
- `TestResourceReadAndBudgets`: brief/scope/export resources resolve; the 61st read in a
  minute is refused; an over-cap brief is an error.
- `TestPromptsRenderBounded`: each prompt needs its arguments, refuses over 64 KiB, and
  inlines data only from the fake.

New drift test in `internal/web` (package `web`, importing `internal/mcpserver` for its
exported `Paths()`): `TestMCPPathsExistInAPIRoutes` asserts every path template the MCP
server builds is in `apiRoutes()`. That is the only mechanical defence against a route
being renamed and an MCP tool 404ing in production. Page routes
(`/changes?format=txt`, `/programs/{p}/{h}?tab=scope`) go in a second allowlist in the
same test, next to the mux registration they mirror.

`internal/web` tests for the REST prerequisites: `status` includes `read_only`; the
notifications preview JSON carries no channel address or secret; (if added)
`/api/v1/tags` lists what the pages filter by.

## 10. Rollout

1. **Phase 0 - correctness**: cap-as-error, per-platform poll cooldown, export format
   validation, honest limits in descriptions, `read_only` in status. No new tools. Every
   item is a bug fix with a test, so `docs/mcp.md` only gains the limits section.
2. **Phase 1 - the missing read surface**: `huntdeck_board`, `huntdeck_match_many`,
   `huntdeck_scope`, parameter completion on the existing six, `huntdeck_program`
   `parts`/`freshness`.
3. **Phase 2 - resources and prompts**: the brief, scope, export and board resources;
   hunt_brief, scope_triage, host_triage, duplicate_check.
4. **Phase 3 - guarded writes**: `HUNTDECK_MCP_WRITE_TOOLS`, dry-run, accept, label, then
   the notifications preview once its JSON route exists.

Open questions for the maintainer, each with the default I would ship:

- Default write set: keep `poll` (today's behaviour) or move to `none`? Keep `poll` so no
  existing client breaks, and require the new names for anything else.
- Is a 120 MiB/min worst case acceptable, or should the default cap drop to 512 KiB with
  exports exempt? Ship 1 MiB plus 16 MiB exports and document it.
- Should `huntdeck_scope` filter locally (one program fetch, cached) or should the
  instance grow a scope API with paging? Local projection now; an API route only if a
  second consumer appears.
