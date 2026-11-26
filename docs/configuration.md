# Configuration

Environment variables. Every one can also be read from a file via
`NAME_FILE=/path`, for Docker secrets. See [.env.example](../.env.example).

**Settings › Accounts** sets the platforms' credentials, rates and public-only
switches, and the proxy, without editing `.env`; **Email › HuntMail › Setup** does
the same for `HUNTMAIL_*`. A value saved there takes
precedence over the environment's until you press "Reset to .env". Saved
values are encrypted in the database with a key HuntDeck keeps beside it
(`secret.key` in the data directory, readable by HuntDeck only), so a copy of
the database doesn't give them away; keep the data directory itself private.
Passwords, tokens and 2FA secrets are never shown again, only whether they're
set, and seeds never include any of it. Saving restarts HuntDeck in place, once
no poll is running, and then polls that platform so you see whether it signed
in. The listen address, data directory, allowed hosts, logging and VNC stay in
`.env`, as HuntDeck needs them before it starts.

The `*_INTERVAL` variables are defaults. Intervals changed on the Settings page
take precedence until you press "Reset" there, and Settings can also disable a
platform: no scheduled or manual polls and no logins, while its stored programs
stay browsable.

| Variable | Default | Purpose |
|---|---|---|
| `HUNTDECK_LISTEN` | `0.0.0.0:8080` | HTTP listen address inside the container |
| `HUNTDECK_DATA_DIR` | `/data` | Database and backups |
| `HUNTDECK_ALLOWED_HOSTS` | — | Extra hostnames you open HuntDeck by, comma-separated (IPs and `localhost` always work) |
| `HUNTDECK_CLIENT_IP_HEADER` | — | Behind a reverse proxy, the header carrying the visitor's address, so login budgets and rate limits count per person instead of per proxy: `X-Real-IP` (nginx, 1Panel), `CF-Connecting-IP` (Cloudflare). Unset when nothing proxies HuntDeck |
| `HUNTDECK_SEED_URL` | the public starter seed | Where a new install downloads its starter seed on its first start, once, into an empty database (see [seeds](seeds.md)); `off` to start empty. An https URL, with its SHA-256 at the same URL plus `.sha256` |
| `HUNTDECK_SOURCES` | — | Platforms HuntDeck has no client for, as custom sources: `name=location` pairs separated by spaces, each location an absolute file path or an http(s) address of a JSON document your own script writes (see [custom sources](custom-sources.md)). Polled like a platform under that name; refused on a read-only copy |
| `HUNTDECK_SOURCES_INTERVAL` | `1h` | How often each custom source is read in full; new programs every quarter of that |
| `HUNTDECK_TIMEZONE` | `America/New_York` | Time zone the UI and CLI show dates and times in (an IANA name such as `Australia/Brisbane` or `UTC`), until you pick one in Settings |
| `HUNTDECK_LANGUAGE` | `auto` | The interface language when a visitor hasn't picked one: `auto` (the browser's `Accept-Language`), `en`, or any language with a catalog such as `zh`. Each visitor's choice in the sidebar's switcher wins over it |
| `HUNTDECK_PASSWORD` | — | Ask for this password before showing anything (the login page): one password for the whole install, still no user accounts. At least 8 characters; changing it signs every browser out. Put it behind TLS in front of HuntDeck — it travels in a cookie, not in the clear page |
| `HUNTDECK_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `HUNTDECK_LOG_FORMAT` | `text` | `text` or `json` |
| `HUNTDECK_PROXY` | — | Proxy for all platform traffic, e.g. Burp on your machine: `http://host.docker.internal:8080` (on Linux, add `extra_hosts: ["host.docker.internal:host-gateway"]` to the compose service; `127.0.0.1` inside the container is HuntDeck itself): every platform's requests, custom sources read over http(s), and the login browser (one Chrome, used by every platform's login). With any platform login set it must be `http://` (`socks5://` works for API tokens only); it may carry credentials (`http://user:password@host:port`). Notifications and the starter seed download don't use it |
| `H1_USERNAME`, `H1_TOKEN` | — | HackerOne API credentials ([create a token](https://hackerone.com/settings/api_token/edit)); they also read your own reports (Reports, Analytics) |
| `H1_EMAIL`, `H1_PASSWORD`, `H1_TOTP_SECRET` | — | Optional HackerOne website login (Chrome, like Intigriti's), for private programs' stats and pending invitations, which HuntDeck lists and can accept. `H1_OTP` is read as `H1_TOTP_SECRET` too |
| `H1_FULL_INTERVAL` | `6h` | Refresh every program's scope (`0` disables) |
| `H1_QUICK_INTERVAL` | `15m` | List programs and fetch new ones only (`0` disables) |
| `H1_RATE` | `5` | Requests per second (HackerOne allows 10) |
| `H1_CONCURRENCY` | `4` | Parallel scope fetches |
| `BC_EMAIL`, `BC_PASSWORD` | — | Bugcrowd login, for private programs and pending invitations (listed with their scope, and accepted from HuntDeck). It runs in the login browser (a Bugcrowd tab in Settings › Browser), ticking Okta's "remember me" boxes; the session is saved in the data directory, and HuntDeck signs in again by itself when it ends; it also reads your own reports (Reports, Analytics) |
| `BC_TOTP_SECRET` | — | Base32 secret of your Bugcrowd authenticator app (needed if 2FA is on) |
| `BC_SESSION` | — | Alternative to a password: the `_bugcrowd_session` cookie from your browser (lasts about 14 days) |
| `BC_PUBLIC_ONLY` | `false` | Poll Bugcrowd's public programs without credentials |
| `BC_FULL_INTERVAL` | `6h` | Refresh every Bugcrowd program's scope (`0` disables) |
| `BC_QUICK_INTERVAL` | `30m` | List Bugcrowd programs and fetch new ones only (`0` disables) |
| `BC_RATE` | `1` | Bugcrowd requests per second (its WAF blocks faster clients for hours) |
| `BC_CONCURRENCY` | `2` | Parallel Bugcrowd scope fetches |
| `IT_TOKEN` | — | Intigriti personal access token |
| `IT_FULL_INTERVAL`, `IT_QUICK_INTERVAL` | `6h`, `15m` | Intigriti polling (`0` disables) |
| `IT_RATE`, `IT_CONCURRENCY` | `2`, `2` | Intigriti request rate and parallel fetches |
| `IT_EMAIL`, `IT_PASSWORD`, `IT_TOTP_SECRET` | — | Optional Intigriti web login, for what the API lacks (hunters, response times, payouts, updates, the brief's out-of-scope, severity and FAQ sections, test credentials, terms and collaboration, and which invitations you haven't accepted yet). It runs as a browser sync, once a day by default (Settings › Polling, the browser sync under Intigriti). The first login runs a windowed Chrome; use a residential `HUNTDECK_PROXY`, as the login page scores visitors with reCAPTCHA. The session is saved in the data directory (`sessions/`, owner-only), so restarts don't log in again; changing `IT_EMAIL` clears it, and the new account signs in afresh. `IT_TOKEN` must be the same account's. Quote values containing `#` or `$` in single quotes; it is also what reads your own reports (Reports, Analytics) |
| `HUNTDECK_CHROME` | on `PATH` | The Chrome or Chromium for that login (the image includes Google Chrome; Chromium when built for arm64) |
| `HUNTDECK_CHROME_TIMEZONE` | `HUNTDECK_TIMEZONE` | The browser's clock: match it to the proxy's country |
| `HUNTDECK_CHROME_HEADLESS` | `false` | Run the browser without a window (login pages trust that less) |

### The login browser and its sandbox

The login browser runs **without Chrome's sandbox** inside the container: measured on this
image, Chrome aborts (exit 134) when it is on, because a container without user
namespaces cannot give the renderer the privileges it needs. That is a real
blast-radius decision, so know it: a renderer bug runs with the same user that
can read `/data` — the database, `secret.key`, and every platform session.

What limits it: the container is non-root with `cap_drop: ALL` and a read-only
root filesystem; the profile directory (`<data>/chrome/browser`) is created
`0700`; the browser only ever opens the platforms' own login pages, and the
proxy is the same one API calls use. If you run HuntDeck in a setup that does
provide user namespaces, give the browser its sandbox back by adding
`--no-sandbox`'s opposite to your deployment (Chrome has no flag for that: it is
on unless passed).
| `HUNTDECK_VNC` | `false` | Show the login browser (one Chrome, a tab per platform) live in Settings > Browser and at `/novnc/` (noVNC, in the image), to see a login or a platform's session, or finish a login by hand. The VNC server only listens inside the container; anyone who can reach HuntDeck can watch |
| `HUNTDECK_NOVNC_DIR` | `/usr/share/novnc` (the image's) | Where the noVNC client for `HUNTDECK_VNC` is served from; only needed outside the image |
| `HUNTDECK_PUBLIC_URL` | — | Where a **read-only** copy is published, such as `https://huntdeck.example`: with it the copy may be indexed, and its pages carry canonical links, a sitemap and link previews. A full install is never indexed |
| `HUNTDECK_FEEDBACK_EMAIL` | — | Where the UI's "Send feedback" links mail to; unset hides them |
| `HUNTDECK_AI_BASE_URL`, `HUNTDECK_AI_MODEL` | — | An OpenAI-compatible endpoint and model for optional AI features (`https://api.deepseek.com/v1` with `deepseek-chat`, an Azure OpenAI base, a local vLLM or Ollama). Set both or neither: with neither, every page works exactly as it does without AI. `huntdeck ai-check` verifies them, and `huntdeck ai-extract` asks the model to read each program's policy and caches what it finds by the policy's hash — the program page then shows those rules beside the ones quoted from the policy, marked as a paraphrase |
| `HUNTDECK_AI_KEY` | — | The endpoint's API key. Prompted for a local model with no key, leave it unset |
| `HUNTDECK_X_ACCOUNT` | — | An X account to follow for news (e.g. `huntdeck`): a read-only copy links to it and its link previews name it |
| `HUNTDECK_REPO_URL` | the project's | HuntDeck's source repository as the UI links it, for a fork that would rather point home |
| `YWH_EMAIL`, `YWH_PASSWORD` | — | YesWeHack login. It runs in the login browser (a YesWeHack tab in Settings › Browser); HuntDeck takes the web app's token, saves it in the data directory and renews it as the web app does, signing in again only when it can't; it also reads your own reports (Reports, Analytics) |
| `YWH_TOTP_SECRET` | — | Base32 secret of your YesWeHack authenticator app (if 2FA is on) |
| `YWH_TOKEN` | — | Alternative: a bearer token from your browser (expires within hours); also reads your own reports (Reports, Analytics) |
| `YWH_PUBLIC_ONLY` | `false` | Poll YesWeHack's public programs without an account |
| `YWH_FULL_INTERVAL`, `YWH_QUICK_INTERVAL` | `6h`, `30m` | YesWeHack polling (`0` disables) |
| `YWH_RATE`, `YWH_CONCURRENCY` | `2`, `2` | YesWeHack request rate and parallel fetches |
| `HUNTMAIL_URL`, `HUNTMAIL_API_KEY` | — | Your [HuntMail](https://github.com/mdpsec/huntmail) inbox, for the Email page and each program's Email tab: the address with its base path and the API key, as HuntMail's setup printed them (`https://<worker>.<subdomain>.workers.dev/<base path>`), or as HuntDeck saved them when it set HuntMail up (Email › HuntMail › Setup). Both or neither; the address is kept secret like the key, since its path is one. HuntDeck reads it from the server only, straight to Cloudflare (not through `HUNTDECK_PROXY`), when you open a page that shows mail |
| `HUNTMAIL_DOMAINS` | HuntMail's | Domains to make new addresses on, comma-separated (`example.com, example.net`); empty: the ones HuntMail was set up for, the busiest first |

**Setting HuntMail up from HuntDeck** (Email › HuntMail › Setup)
needs a domain on Cloudflare and an API token with: Account › Workers
Scripts: Edit, Account › D1: Edit, Account › Account Settings: Read,
Zone › Zone: Read, Zone › Zone Settings: Edit and Zone › Email Routing
Rules: Edit. HuntDeck uses it for that run only and never stores it; the
address and key it saves are the two variables above.


In `.env`, wrap any value containing `$` in single quotes
(`BC_PASSWORD='pa$$word#1'`): docker compose and shells otherwise treat `$` as
a variable and silently change the value. Plain `docker run --env-file` is
different: it keeps quotes as part of the value, so there either leave the
value unquoted or put it in a file and use `BC_PASSWORD_FILE`.

## Data and backups

Data lives in the `/data` volume (`huntdeck.db` plus pre-upgrade backups in
`backups/`). The container runs as a non-root user (UID 65532). If you
bind-mount a host directory instead of using a named volume, make it writable
by that UID. HuntDeck backs the database up before every schema upgrade.

## Plain Docker

Without Compose:

```sh
docker build -t huntdeck .
docker run -d --name huntdeck --init --env-file .env \
  -p 127.0.0.1:8080:8080 \
  -v huntdeck-data:/data \
  --read-only --tmpfs /tmp --cap-drop ALL --security-opt no-new-privileges \
  huntdeck
```

Give the container its own network (the default), not `--network host`: the
Intigriti login's Chrome drops its requests whenever the host's network
interfaces change, which on a busy Docker host is constantly.

## How polling stays polite

HackerOne has no "what changed since" query, so a full poll costs one request
per program (about 600 for a typical account). Scope is read from the program
endpoint (600 requests/minute). The `structured_scopes` endpoint has been
limited to 50/minute since June 2026, so it is used only for the roughly 1% of
programs whose scope exceeds the 850 entries the program endpoint returns.
Those few programs (some list thousands of assets) are what make a full poll
take several minutes.

HackerOne's disclosed reports come from the official Hacktivity endpoint:
each poll reads only what was disclosed since the last one (usually one
request), and a program's history is fetched once, the first time (about 600
requests for a typical account, spread over that full poll).

Intigriti, YesWeHack and Bugcrowd scope is only downloaded again when the
platform says it changed: Intigriti's program activity feed, YesWeHack's
`last_update_at`, Bugcrowd's brief changelog. Quick polls pick up Intigriti and
YesWeHack changes within 15 minutes. As a safety net, every scope is
downloaded at least once a day anyway. Poll history shows how many were
skipped as unchanged.

Bugcrowd has no API, so HuntDeck reads the JSON the bugcrowd.com web app uses
(see [platforms](platforms.md)). A Bugcrowd full poll costs one small
request per program, plus the full brief only for programs whose brief
changed since the last download (or a day has passed); at 1 request/second
that's about 9 minutes for the ~520 public programs.

HuntDeck paces requests before sending them and honours `429`/`Retry-After`
with bounded retries. Unchanged programs cost one request and no database
writes. A poll that fails partway never marks programs or scope as removed.
If a platform suddenly reports most of your programs gone, or a program's
scope as empty, HuntDeck assumes a platform glitch and removes nothing; the
poll shows as partial with the reason. If it's real, run
`huntdeck poll --platform <name> --force-removals`.

## POJIA

- **HUNTDECK_POJIA_KEY** - a key for cve.pojia.ai, the disclosed-bug-bounty
  library. With one set, a program page shows what the program has
  historically paid for (report count, bounty total, class distribution),
  fetched from POJIA and cached a week.
- **HUNTDECK_POJIA_URL** - its endpoint; https://cve.pojia.ai/mcp by default.


## Scan handoff

- **HUNTDECK_SCAN_URL** - a ScopeSentry instance (its web address).
- **HUNTDECK_SCAN_KEY** - an ssk_ key from that instance, scoped to
  task:write, task:read, node:read and template:read.
- **HUNTDECK_SCAN_READ_BATCH** - how many tasks one read-back pass asks the
  scanner about (default 8, 1–100). Its MCP allows about ten calls a minute, so
  eight fits; a self-hosted instance with a higher limit can raise it.

With both set, the Scan page lists the instance's templates and nodes;
turning the handoff on creates one scan task per batch of new in-scope
targets and records each target so a re-poll does not queue it twice.


## Decision model

- **HUNTDECK_JEV_KEY** - a key for typesafe.ai (Jev). With one set, the scan
  handoff asks for a typed judgement before creating tasks: proceed,
  confirm_first, split_smaller or block. Block and confirm stand the handoff
  down. A model that cannot be reached is not a judgement on the batch, so
  the handoff goes ahead on the deterministic checks alone and the failure is
  logged; a model that answers with block, or that answers nothing at all
  while reachable, stops it.
- **HUNTDECK_JEV_MODEL** - jev-latest (default) or jev-preview.
- **HUNTDECK_JEV_URL** - https://api.typesafe.ai unless you proxy it.
- **HUNTDECK_JEV_PROXY** - an HTTP proxy the call leaves through, for a host
  whose direct egress TypeSafe refuses (it answers 451 by region).

It is advisory: the deterministic checks (the memory floor) run first, and a
decision model can only narrow what they allow.

### Decision service providers

Four services answer the same three question types. Three of them speak one
protocol, so only the address and the credentials change; OpenAI has its own body
shape, translated in `internal/jev/openai.go`.

| Provider | Endpoint | Credentials |
|---|---|---|
| `typesafe` (default) | `POST {url}/v1/systemone` | `HUNTDECK_JEV_KEY` |
| `bailian` | `POST {url}` (workspace host) | key + `HUNTDECK_BAILIAN_WORKSPACE_ID` |
| `cloudflare` | `POST {url}/accounts/{id}/ai/run/@cf/cloudflare/clef-flash` | key + `HUNTDECK_CF_ACCOUNT_ID` |
| `openai` | `POST {url}/v1/decisions`, model `gpt-6-luna` | key |

- **HUNTDECK_DECISION_PROVIDER** - which one answers: `typesafe`, `bailian`,
  `cloudflare` or `openai`.
- **HUNTDECK_CF_ACCOUNT_ID** - Cloudflare's account, for `cloudflare`.
- **HUNTDECK_BAILIAN_WORKSPACE_ID** - Bailian's workspace, for `bailian`.

The question ceiling is the smallest documented one, sixteen: a caller cannot send
a request that one provider accepts and another rejects. Without a key a provider
is wired but untested against the live service; the tests check the request and
response shapes each provider documents, including OpenAI's refusal type, which is
an error rather than a zero probability.
