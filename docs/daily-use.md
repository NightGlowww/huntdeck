# A day with HuntDeck

The pages exist; this is the order to open them in, and what to do with what
they say. It assumes nothing beyond the install in the README.

## 1. The board (/, first thing)

- **Hunt queue** at the top: five targets ranked by "can I start today, and what
  is it worth per hour". If one of them looks like your kind of work, click it.
- **Platforms last checked**: if a platform says hours rather than minutes, a
  poll is failing — `/settings` shows why (a refused credential, a blocked
  address).
- Everything else on the board answers "what changed", not "what should I do":
  fresh scope, invites, scope diffs, top picks.

## 2. The queue (/queue)

The board's five rows are the head of this page. What the controls mean:

| Control | What it does |
|---|---|
| Each platform | Only that platform's targets |
| Paying only | Only programs that pay bounties |
| Added in 7 days | Only targets added (or new to you) in the last week |
| One per program | A program's best target only — a plan for the day, not five rows from one company |
| Score | Click it to unfold the factors: what each input did to the number |

The filters combine: two chips set means both apply, and the count in the
heading is the number that match, not the number on screen.

The page says **"N of 8 snapshots"** at the bottom, and N grows by one a day:
the server writes a snapshot within half an hour of every UTC midnight, with no
one running a command. That is the queue's own honesty — it is not judged
against what the platforms actually paid until there are eight days of history
to judge it on — and it is why the number is not written down here: it changes.

## 3. A program page

Read **Before you test** first — it is the part that costs money to ignore:

- requirements the platform states (Signal, residence, application)
- what only the policy text says: a header to send, a User-Agent, a rate, the
  platform email alias, and **what the program forbids** (denial of service,
  customer data, leaked credentials …), each quoting the program's own sentence
- the reporting window, when the text states one

Then the tabs: **Scope** (what to attack, with the platform's own notes),
**Stats** (how fast this program pays and how crowded it is), **Changes** (what
moved since you last looked), **Notes** (yours).

Copy targets with the button, or export Burp/ZAP/Caido configs from the same
menu.

## 4. While you work

- **Watch** a program to get its changes in Notifications; **Ignore** one to
  drop it out of the queue and the board.
- Tags groups programs your way ("paid well", "needs a VPN").
- The top bar says **Polling** while a poll runs and **New data: reload** when
  one finished after you opened the page.

## 5. Weekly

- `/disclosed`: what others got paid for, per program — the fastest way to see
  what a program actually accepts.
- `/stats`: which platforms pay, how fast, and how crowded they are.
- Backups: the app takes one before every schema upgrade, and nothing else
  on its own. Schedule `huntdeck backup --to /data/backups/daily.tar.gz` (a
  cron job, or the host's scheduler) and keep a copy somewhere else; moving
  the database off the host is what makes a backup a backup.

## When something looks wrong

1. `huntdeck doctor` — data directory, database integrity, schema, key, disk,
   and **freshness**: it says when each platform was last read and when the
   queue was last rebuilt, so a sound database with a stalled poll is visible
   in one command rather than three pages.
2. `/settings` — is the credential accepted? did the last poll fail, and why?
3. `huntdeck queue` on the command line — the same ranking without the browser.
4. `docs/audit/health-2026-11.md` — what is known to be rough, stated by the
   people who built it.

### Is a poll stuck, or just slow?

A poll that is working and a poll that is hung look the same in the run's
counters: they are written when it ends. Three things tell them apart, and all
three exist because a working poll was once diagnosed as hung:

- `huntdeck doctor` prints every running poll with the time since its **last
  activity**, and warns after three minutes of silence. The heartbeat is a
  column in the database (`poll_runs.last_activity`), so it survives a restart
  and a reader in another process sees it too.
- `/api/v1/status` carries the same run under `progress`: the step it is on,
  how far through that step, the requests made, and the seconds since the last
  one. `"idle_seconds"` is the field to watch.
- `/settings` shows the idle time next to a running poll, from the heartbeat
  when this process's in-memory progress is gone.

A poll that a restart cut short is retried in **five minutes**, not a whole
interval: an interrupted run is one we stopped. A poll that ran past its limit
(45 minutes full, 20 quick, 30 web) **fails** with its own reason, because
retrying a hung poll in five minutes would hang again in five minutes. A poll
that failed for any other reason keeps its interval - an auth error retried
every five minutes would hammer the platform.

### A platform says it is not configured, but the credential is saved

The settings form sends every field, so the page cannot hit this; a script
posting a single field can. Since the fix, a field the request does not mention
is **left alone** rather than emptied, and clearing is what the `clear_` button
sends. If a credential looks missing after a script ran against
`/settings/accounts`, that script posted before the fix.

### Everything fails at once

One dependency, not six integrations: `huntdeck doctor` now checks the proxy
with a request of its own (exit address and a platform request through it), the
decision model, the scanner's nodes and queue, and each platform's website login
with the state of its session. A dead proxy, an empty session and a refused
login each say so on their own line.

## A known cost: the login browser stays open

A website login runs in a headful Chrome with its own Xvfb inside the container
(`/data/chrome/browser`, `--window-position=0,0 --window-size=1920,1080`).
After a successful sign-in the browser **stays running** so the next login can
reuse it, and `HUNTDECK_VNC` is off, so nothing displays it.

Measured on the production host on 2026-11-21: one Chrome and one Xvfb had been
up for six minutes with no login in progress, at about 3% of a core between them
(1.4% each in `ps -o pcpu`). On a 4-core host shared with a scanner that is a
real, permanent cost for nothing.

The fix is small and unwritten: close the host after a login when VNC is off, and
let the next login open a new browser. It is not done because it cannot be
verified tonight - a login has to happen to prove the browser closes and the
session survives, and the host is too loaded to run one.
