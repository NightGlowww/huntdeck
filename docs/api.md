# API and command line

The API page in the sidebar documents every endpoint and can send GETs for
you; `/api/v1/openapi.json` is the same as an OpenAPI 3.1 description.

With no access password set there's no login and no token: scripts and curl
work as they are. With `HUNTDECK_PASSWORD` set, a browser sends the session
cookie it gets at the login page, and a script sends either that cookie or an
API token (`Authorization: Bearer …`, minted on the API page).


Targets come back as plain text by default, streamed in full, so they pipe
straight into tools; everything else is JSON. Lists are paged: programs with
`limit` (default 100, max 1000) and `offset`, JSON targets with `limit`
(default 1000, max 5000) and `offset`, changes with `limit`; each returns `total`.
Plain-text targets take `limit` and `offset` too.


```sh
curl -s "http://127.0.0.1:8080/api/v1/targets?kind=wildcards&bounty=1"     # one per line
curl -s "http://127.0.0.1:8080/api/v1/targets?kind=domains&format=json&limit=1000&offset=0"
curl -s "http://127.0.0.1:8080/api/v1/programs?visibility=private"
curl -s "http://127.0.0.1:8080/api/v1/programs?sort=huntscore&limit=20"   # best to hunt, with hunt_score parts and reasons
curl -s "http://127.0.0.1:8080/api/v1/invites?invite=pending"          # invitations you haven't accepted yet
curl -s -X POST http://127.0.0.1:8080/api/v1/invites/intigriti/<handle>/accept  # accept one, then refresh that program
curl -s "http://127.0.0.1:8080/api/v1/programs/hackerone/security"     # everything its page shows
curl -s "http://127.0.0.1:8080/api/v1/board"     # the hunt board's summary
curl -s "http://127.0.0.1:8080/api/v1/changes?since=30d&program=hackerone/security"
curl -sO "http://127.0.0.1:8080/api/v1/programs/hackerone/security/scope.burp.json"   # or scope.zap.context, scope.caido.json
curl -s "http://127.0.0.1:8080/api/v1/stats?sort=perreport&bounty=1"     # program stats
curl -s "http://127.0.0.1:8080/api/v1/disclosed?severity=critical&limit=20"  # what others got paid for
curl -s "http://127.0.0.1:8080/healthz"                                     # {"status":"ok"}; the container healthcheck uses this
curl -s "http://127.0.0.1:8080/api/v1/stats/platforms"     # the Stats page: every platform, with a year of months
curl -s "http://127.0.0.1:8080/api/v1/stats/platforms/hackerone?limit=20"     # a platform and its programs
curl -s "http://127.0.0.1:8080/api/v1/changes?since=24h"
curl -s "http://127.0.0.1:8080/api/v1/match?host=app.example.com"
curl -s "http://127.0.0.1:8080/api/v1/targets?kind=wildcards&sort=best"   # newest, most promising first
subfinder -d example.com -silent | curl -s --data-binary @- "http://127.0.0.1:8080/api/v1/match?format=txt"   # keep the in-scope hosts
curl -s "http://127.0.0.1:8080/api/v1/status"
curl -s -X POST "http://127.0.0.1:8080/api/v1/poll/hackerone?mode=quick"
```

Target filters: `kind` (wildcards, domains, urls, ips, cidrs, `roots` for
root domains, `all`, or an asset type such as apps, android, ios or hardware), `scope=out`,
`platform`, `bounty=1`, `private=1`, `include_paused=1`, `tag`,
`include_ignored=1`. Programs take `tag` and `ignored=include|only`.

**Freshness.** `/api/v2/programs`, `/api/v2/invites` and
`/api/v2/programs/{platform}/{handle}` are v1's with each number that can be
estimated or that polls refresh (bounty range, scope counts, everything in
`numbers`) as `{value, estimated, source, updated_at}`: `source` is the
platform, `derived` (counted or worked out by HuntDeck) or `manual` (programs
added by hand), and `updated_at` moves only when the value does (null where
it can't be dated). v1 keeps the flat shape. Both give each program
`updated_at` (anything about it changed) and `last_synced_at` (last read from
its platform). The Methodology page says what's an estimate on each
platform. `since=<RFC 3339 time or 24h/7d>` on the program lists keeps
programs changed after it, on targets those added or edited after it.
Program lists and pages, invites and `GET /api/v1/match` send an `ETag` and `Last-Modified`:
send them back as `If-None-Match` / `If-Modified-Since` for a `304` while
nothing changed. Text exports (targets,
`match?format=txt`, the ZAP context) take `comment=1` for a first line
`# generated_at=…` (an XML comment in ZAP's), the rest unchanged; every
export, Burp's and Caido's JSON too, has the time in `X-HuntDeck-Generated-At`.

```sh
curl -s -D h.txt "http://127.0.0.1:8080/api/v2/programs?since=7d" -o programs.json
curl -s -o /dev/null -w '%{http_code}\n' -H "If-None-Match: $(sed -n 's/^[Ee][Tt]ag: //p' h.txt | tr -d '\r')" \
  "http://127.0.0.1:8080/api/v2/programs?since=7d"   # 304 until something changes
curl -s "http://127.0.0.1:8080/api/v1/targets?kind=wildcards&since=24h&comment=1"
```

## Tokens (`/api` → API tokens)

A script can present a token instead of a browser session:

```sh
curl -s -H "Authorization: Bearer $HUNTDECK_TOKEN" https://huntdeck.example/api/v1/status
```

Mint one on the API page. Only its SHA-256 is stored, so the token is shown
once; revoking it takes effect on the next request and touches nothing else.
A token grants exactly what the access password grants — it is the same gate,
not an account, and there are no scopes or roles.

## The hunt queue (`/api/v1/queue`)

HuntQueue ranks targets by "can I start today, and what is it worth per hour":
the server rebuilds it every half hour from the polls. This is the same list the
board shows five of and `/queue` shows fifty of.

```sh
curl -s "http://127.0.0.1:8080/api/v1/queue?limit=5"
curl -s "http://127.0.0.1:8080/api/v1/queue?limit=1&explain=1"   # with the score breakdown
```

| Parameter | Meaning |
|---|---|
| `limit` | rows, default 20, at most 100 |
| `platform`, `kind`, `added`, `paying`, `one` | the same filters the page's chips set: one platform, one kind of asset, a window like `7d`, only programs that pay, and one row per program. `paying` and `one` accept `1`, `true` or `yes` |
| `explain=1` | each row also carries `factors`: the value every input took and the multiplier it applied. The multipliers of a row multiply back to its score, so `1 - effect` is what that factor cost |

A row has `platform`, `handle`, `kind`, `value`, `score`, `reasons` (plain
sentences: what the payout is, how fast the program accepts, how fresh the
target is) and `computed_at` for the whole answer. `gated` lists what the queue
keeps out and why — a target whose program is paused or waiting on an invite is
not ranked, it is explained.

The same numbers from the command line, for scripting or a quick look:

```sh
huntdeck queue --limit 5
huntdeck queue --explain --limit 3    # the factor breakdown, as text
huntdeck queue --platform intigriti --paying --fresh 7d
```

## Metrics (`/metrics`)

The process exposes Prometheus counters — `huntdeck_uptime_seconds` and
`huntdeck_requests_total{route,status}` — in the exposition format. It sits
behind the same access password as every other page, so a scraper needs a
session; that is deliberate: the route list is not something to hand an
unauthenticated network.

```sh
# with the password in a jar, then:
curl -s -b jar.txt "http://127.0.0.1:8080/metrics" | head -5
```

## Command line

The same data from inside the container (`docker compose exec huntdeck /huntdeck …`):

```sh
huntdeck status                       # what's stored, recent polls
huntdeck export wildcards             # *.example.com, one per line
huntdeck export domains --bounty      # only bounty-eligible scope on paying programs
huntdeck export domains --private     # only your private programs
huntdeck export domains --oos         # out-of-scope list, for filtering
huntdeck export wildcards --tag focus # only programs you tagged "focus"
huntdeck match https://app.example.com/login   # which programs cover this, in or out?
subfinder -d example.com -silent | huntdeck match -       # print only the in-scope hosts
huntdeck match -all - < hosts.txt     # every host: in, out or none, with its programs
huntdeck changes --since 72h          # new programs, invites, scope added/removed
huntdeck score -n 25                  # rescore now and list the top HuntScores
huntdeck poll --limit 20              # poll now (only the first 20 programs)
huntdeck poll --platform bc --refetch-scope   # download every scope now, even unchanged ones
huntdeck stats security               # fetch a HackerOne program's stats now (--platform for others)
huntdeck seed export /data/seed-out.db.gz   # public programs' data, for a new install to start from
huntdeck seed import /data/seed.db.gz       # load one into a new, empty install
```

```sh
docker compose exec -T huntdeck /huntdeck export wildcards | sed 's/^\*\.//' | subfinder -silent
```

Exports skip paused and closed programs unless you pass `--include-paused`.
A wildcard `*.example.com` covers subdomains, not `example.com` itself. Always
check the program's rules before testing.

## Exports of the filtered lists

Every list exports what its filters match, not the page of them. Scripts can
call these directly; each answers `Cache-Control: no-store`, and the pages'
copy and download links use the same URLs.

| Export | URL | One line is | Stops at |
|---|---|---|---|
| Scope entries | `/programs/{platform}/{handle}?tab=scope&format=txt` | an identifier | none (ignores the page's 1,000-row cap) |
| Targets | `/api/v1/targets?kind=domain&…` (text by default) | an identifier | none on your own copy; 5,000 on a read-only one |
| Disclosed reports | `/disclosed?…&format=txt` | a report URL | 5,000, and the page says so |
| Changes | `/changes?…&format=txt` | `date<TAB>program<TAB>what happened<TAB>asset` | 5,000 |
| Programs | `/api/v1/programs?format=csv` | a CSV row, every column | none |

```sh
# a program's in-scope entries added in the last week
curl -s 'http://127.0.0.1:8080/programs/hackerone/fiserv?tab=scope&show=in&since=7d&format=txt'

# domains as text (add format=json for the structured list)
curl -s 'http://127.0.0.1:8080/api/v1/targets?kind=domain&bounty=1&download=1' -o domains.txt

# the last month of scope changes, one a line
curl -s 'http://127.0.0.1:8080/changes?group=scope&since=30d&format=txt'

# programs as a spreadsheet
curl -s 'http://127.0.0.1:8080/api/v1/programs?format=csv&platform=hackerone' -o programs.csv
```

The parameters are the page's own: scope takes `show`, `type`, `q`, `since`
and `order`; changes takes `platform`, `group`, `scope`, `change`, `since`,
`bounty`, `sort` and `rev`; disclosed takes `platform`, `severity`, `weakness`,
`cve`, `since` and `sort`. Targets takes its own filters, and `download=1`
names the file `<kind>s.txt`.

### Programs as a spreadsheet

`GET /api/v1/programs?format=csv` answers the same filters as the JSON list,
but as the page's export: `text/csv`, one row per matching program, every
column, no paging (`cols=` narrows the columns exactly as the page's column
picker does — `hunt`, `bounty`, `ptotal`, `paid90`, `received`, `resolved`,
`response`, `scope`, `hunters`, `new30`, `launched`, `changed`, `disclosed`,
`req`; `platform`, `handle` and `name` always lead). `/api/v2/programs` takes it
too.

```sh
curl -s 'http://127.0.0.1:8080/api/v1/programs?format=csv&platform=hackerone&sort=paid90' -o programs.csv
```
