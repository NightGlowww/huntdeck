# HuntDeck over MCP

`huntdeck-mcp` serves a running instance to an agent over the Model Context
Protocol on stdio: programs, targets, scope checks, changes, disclosed reports
and a poll trigger, without the agent needing to know the REST API.

The instance lists every tool with its arguments at /mcp, rendered from the
server's own registry, so the page and the server cannot disagree.

It adds **no accounts** of its own. It signs in with the instance's access
password the way a browser does (the same `hd_session` cookie), keeps that
cookie in memory, and signs in again if the session goes away.

## Running it

```sh
make build                       # bin/huntdeck and bin/huntdeck-mcp
HUNTDECK_URL=http://127.0.0.1:8080 \
HUNTDECK_PASSWORD='…' ./bin/huntdeck-mcp
```

`HUNTDECK_URL` defaults to `http://127.0.0.1:8080`; leave
`HUNTDECK_PASSWORD` out when the instance has no login, and set
`HUNTDECK_MCP_READONLY=1` to offer only the look-ups (no `huntdeck_poll`,
and even a client that asks for it by name is refused). Nothing is written to
disk: the password is read from the environment and never printed.


### The interface, and what a failure means

`huntdeck-mcp` reads **only** environment variables — there are no command-line
flags. `--url` and `--password` are silently absent: the server falls back to
`http://127.0.0.1:8080` and an empty password, and every tool then reports a
session problem. If a tool says the password is wrong, the first thing to check
is that the variables are set at all.

| What happened | What the tool says |
|---|---|
| the instance answered, refused the login | `sign in: HTTP 401 (is HUNTDECK_PASSWORD right?)` |
| no password was given, and the instance needs one | `the instance refused the session (is HUNTDECK_PASSWORD right?)` |
| nothing listens on the port | `GET http://…/api/v1/status: dial tcp …: connect: connection refused` |
| the host does not resolve, and a proxy is configured | `…: proxyconnect tcp: dial tcp …: connect: connection refused` — the proxy is named, so it is not mistaken for the instance |
| the instance is too old for a tool | the API's own 404 or 400, with the path |

A tool failure is `isError: true` with one text line: the MCP server does not
invent error codes of its own. A large answer is refused rather than truncated
(8 MiB), because half a JSON document parses as a broken one.
### Claude Desktop / other clients

```json
{
  "mcpServers": {
    "huntdeck": {
      "command": "/path/to/bin/huntdeck-mcp",
      "env": {
        "HUNTDECK_URL": "https://huntdeck.example",
        "HUNTDECK_PASSWORD": "…"
      }
    }
  }
}
```

## Tools

| Tool | What it answers |
|---|---|
| `huntdeck_status` | Programs and scope entries per platform, last poll |
| `huntdeck_invites` | Private programs you were invited to (`platform`, `sort`, `limit`, `offset`) |
| `huntdeck_programs` | Search programs (`q`, `platform`, `state`, `bounty`, `sort`, `limit`, `offset`) |
| `huntdeck_program` | One program in full, by `platform` and `handle`, `parts` picks the projection (default `facts,brief,rules`; `scope_limit`/`scope_offset` page the scope: a full program with 5,000 disclosures is a megabyte) |
| `huntdeck_programs_csv` | The matching programs as CSV text, every column |
| `huntdeck_targets` | In-scope targets (`kind`, `platform`, `bounty`, `added`, `limit`) |
| `huntdeck_match` | Which programs list a host, IP or URL in scope |
| `huntdeck_match_many` | The same for a list of hosts (one per line, up to 5,000): recon output straight in, the in-scope subset out |
| `huntdeck_changes` | What changed (`since`, `platform`, `kind`, `program`, `limit`) |
| `huntdeck_changes_text` | The same changes as text: date, program, what happened, asset |
| `huntdeck_disclosed` | Disclosed reports (`q`, `platform`, `severity`, `cve`, `sort`) |
| `huntdeck_scope_text` | A program's scope entries as plain text, one per line |
| `huntdeck_export` | The scope as a Burp, ZAP or Caido import file (the body to save) |
| `huntdeck_queue` | HuntQueue: targets ranked by "can I start today, what is it worth", with reasons (`limit`); `platform`, `kind`, `added`, `paying`, `one` are the page's chips; `explain` adds each row's factor breakdown |
| `huntdeck_board` | The Hunt board in one answer: totals, this week's churn, reopened and paused programs, top hunts |
| `huntdeck_stats` | Platform totals and how they moved (`q`, `bounty`, `sort`, `rev`, `limit`, `offset`) |
| `huntdeck_poll` | Ask the instance to poll a platform now (at most once a minute per session) |

Each returns the API's JSON as text; a failure (wrong password, unknown
program) comes back as a tool error the agent can read, not as a crash.

### A session, in the order an agent makes it

Walked through against a live instance, to check the tools are enough on their
own:

| Step | Call | What comes back |
|---|---|---|
| what is worth today | `huntdeck_queue {limit: 5, explain: true}` | five rows with score and reasons; `factors` names what held each one back (`quality x0.78, personal x0.90` on the first) |
| is it safe to start | `huntdeck_program {platform, handle, parts: "facts,brief,rules"}` | `rules` as structured items — a `header` with the exact string to send, an `email` alias, a `warn` for what the policy forbids; when a model has read the policy the answer also carries `ai_rules` (a paraphrase, with `ai_model`); `facts` for triage, safe harbour, industry; `brief` for the policy text |
| what do I attack | the same answer's `scope` (summary) or `parts: "scope"` with `scope_limit` | in-scope assets, with the program's own notes |

That is enough to go from "which target" to "ready to test" without opening a
browser. The payloads are 0.7 KB for the queue and about 64 KB for one program's
facts, brief and rules (the full program is 155 KB).

## What it does not do

A session may make 120 calls a minute; a loop is told to wait rather than
left to hammer the instance's database.

It does not change settings, credentials, channels or scope — the only
write is `huntdeck_poll`, which asks for a poll the scheduler could have run
anyway, and even that is capped at one a minute: a tool an agent can loop is
a load test on platforms we share with everyone else. Everything else is the
same read the web UI does.

### From inside the container

The image carries the server too, so an agent that can run a command on the
host reaches the same instance without copying anything:

```sh
# the container's own HUNTDECK_PASSWORD and localhost:8080 are already set
docker exec -i huntdeck /huntdeck-mcp

# read-only, for an agent that should only look
docker exec -i -e HUNTDECK_MCP_READONLY=1 huntdeck /huntdeck-mcp
```

A client that wants that as its MCP command uses `docker` as the command and
`exec -i huntdeck /huntdeck-mcp` as the arguments.

### huntdeck_triage

Ask the decision model whether a finding would survive triage, before it is
submitted. Two probabilities come back: would a triager accept it, and do the
supplied artefacts prove the impact. Needs `HUNTDECK_JEV_KEY` on the server.

Arguments: `title`, `in_scope`, `impact`, `evidence` (required), `target`,
`steps` (optional). It changes nothing on the instance.

