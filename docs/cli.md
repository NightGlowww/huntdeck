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

- **`huntdeck poll [--platform h1] [--mode full|quick] [--limit N] [--force-removals] [--refetch-scope]`**
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

- **`huntdeck doctor`** — the data directory, schema, integrity, key, space,
  backup freshness, whether the proxy answers, and how fresh the polls, the queue
  and the score history are. `FAIL` lines mean something is wrong.
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
