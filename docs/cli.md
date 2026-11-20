# huntdeck on the command line

The container runs `huntdeck serve`; everything else is a one-off you run with
`docker compose exec huntdeck /huntdeck <command>` (or `/huntdeck <command>` inside
the container). Every command reads the data directory from `HUNTDECK_DATA_DIR`,
and the ones that can take `--data` say so.

## Looking at what is stored

- **`huntdeck status`** — programs and scope per platform, and the last polls.
- **`huntdeck changes [--since 24h] [--limit 200]`** — recent changes, newest first.
- **`huntdeck score [-n 25]`** — recompute HuntsScores now and print the best.
- **`huntdeck queue [-limit 40] [-explain] [-candidates 2000] [-platform P] [-kind K] [-paying] [-fresh]`** —
  the HuntQueue ranking: which targets can start today and what the work is
  worth. `-explain` prints each factor, computing rather than reading the
  saved table (which carries scores, not factors); `-fresh` does the same
  for timing; `-kind` and `-paying` narrow the candidates.
- **`huntdeck audit-texts [-limit 10] [-json]`** - ask the decision model whether
  the sentences it writes about a target are supported by the numbers behind
  them. The claims come from the renderer and the evidence from the stored
  inputs: two paths meant to agree. Exits 1 when a sentence reads as
  unsupported, so a scheduled run can catch a formatting drift. Needs
  `HUNTDECK_JEV_KEY`.
- **`huntdeck intigriti [-json]`** - your own state on Intigriti: reputation,
  rank, whether the identity check is done, pending invitations and
  applications, and the payouts the researcher API exposes (marked BETA
  there). Reputation and rank exist only in the web app, so this needs a web
  login as well as a token.
- **`huntdeck triage [-file finding.json] [-json]`** - ask the decision model whether a
  finding would survive triage, before spending a submission. It answers two
  questions: would a triager accept this, and do the supplied artefacts prove
  the impact. The two numbers separate the cases a single verdict collapses:
  out of scope scores low on both axes, a real but unproven bug scores high on
  the first and low on the second. It also rates the finding's severity on a
  five-level scale (0 informational to 4 critical), which is a Score rather
  than a choice: the answer can sit between two levels. Needs `HUNTDECK_JEV_KEY`.
- **`huntdeck backtest [-top 10] [-horizon 4320h]`** — what the top ten by
  HuntScore, and by the queue, would have captured at each past cut. Needs a few
  days of history: it says how many cuts it has.

## Getting data out

- **`huntdeck export KIND`** — targets as plain text: `wildcards`, `domains`,
  `urls`, `ips` or `cidrs`. `--platform h1`, `--bounty`, `--private`,
  `--include-paused`, `--include-ignored`, `--tag NAME`, `--oos` for the
  out-of-scope list. This is what feeds recon tools.
- **`huntdeck match HOST`** — which programs cover a host, IP or URL. With `-` it
  reads a list from stdin and prints the in-scope ones (`-all` keeps every line
  with its verdict).

## Polling

- **`huntdeck poll [--platform h1] [--mode full|quick] [--limit N] [--force-removals] [--refetch-scope] [--handle HANDLE]`**
  — poll now instead of waiting for the scheduler. `--limit` never removes
  anything; `--force-removals` is for a listing that really did shrink.
- **`huntdeck stats [HANDLE…] [--platform h1]`** — fetch a program's public stats
  without a full poll.

## Backups

- **`huntdeck backup [--to FILE] [--keep N] [--no-sessions] [--verify=false]`** — one file holding
  the database, the settings key and the sessions; it reads the archive back
  before reporting success.
- **`huntdeck restore --from FILE [--data DIR] [--dry-run] [--force] [--no-sessions]`**
  — unpack such a file. Stop HuntDeck first, or point `--data` at a scratch
  directory. `scripts/restore-drill.sh` does exactly that and checks the copy.
- **`huntdeck compact [--dry-run]`** — VACUUM, to return the space deleted rows
  leave inside the file. Stop HuntDeck first.

## Health and setup

- **`huntdeck doctor [--short]`** — the data directory, schema, integrity, key, space,
  backup freshness, whether the proxy answers, and how fresh the polls, the queue
  and the score history are. `FAIL` lines mean something is wrong. `--short`
  prints only the failures and warnings, for a cron job that should stay quiet
  until there is something to say; the exit status is the same either way.
- **`huntdeck scan-handoff [--targets a.example.com,b.example.com]`** - send named
  targets to the scanner instead of the next batch off the top of the list: one
  task per program, named after it, recorded so a re-handoff does not queue it
  again.
- **`huntdeck scan-clear [--task ID,...]`** - forget the scan handoffs HuntDeck
  recorded, so those targets are candidates again. The scanner's own tasks are
  deleted on its page: the key HuntDeck holds has five tools and none deletes.
- **`huntdeck account [--platform h1]`** - your standing on each configured
  platform: HackerOne reputation, signal and impact; Bugcrowd weekly submission
  allowance and acceptance rate; Intigriti reputation and rank. Each lives
  somewhere different, so the command asks each platform for what it has.
- **`huntdeck bugcrowd-caps [--engagement HANDLE]`** - what Bugcrowd allows this
  account to submit: the weekly cap and what is left of it, the acceptance rate
  against the 50% threshold that lifts the cap, and the next reassessment. It
  reads one engagement's submission page, because that page is where Bugcrowd
  renders the numbers; there is no API for them.
- **`huntdeck seed export FILE [--signed-out]`** / **`huntdeck seed import FILE`**
  — write the public programs for someone else to start from, or load such a file
  into an empty install.

## The optional model

- **`huntdeck ai-check`** — asks the configured endpoint one question and reports
  whether the key, the model and the endpoint agree.
- **`huntdeck ai-extract [--platform h1] [--limit 25] [--force] [--clear]`** —
  asks the model for the rules each program's policy states, cached against the
  policy's hash and shown beside the quoted rules. `--clear` drops every cached
  reading. Without `HUNTDECK_AI_*` it says so and does nothing.

## Running inside the container

- **`huntdeck healthcheck`** — asked by the image's Healthcheck: it calls
  `/healthz` on this instance and exits non-zero when it does not answer.
  Nothing for a person to run.
- **`huntdeck migrate`** — applies the database migrations and exits. `serve`
  does the same on start, having taken a backup first; this is for running
  them on their own, with the server stopped.

## Serving

- **`huntdeck serve`** — the web UI, the API and the scheduled polls. The
  container's default command.
