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
