# The web UI

| Page | Path | What it is for |
|---|---|---|
| Hunt board | `/` | today at a glance: the head of the hunt queue, what changed, invites |
| Hunt queue | `/queue` | targets ranked by "can I start today, what is it worth"; the full section is below |
| Programs | `/programs` | every program, filterable by platform, status and bounty |
| Targets | `/targets` | every in-scope asset, one row per wildcard, domain, URL or CIDR |
| Changes | `/changes` | what moved: scope added or removed, policies edited, bounties changed |
| Disclosed | `/disclosed` | reports other people got paid for, per program and severity |
| Stats | `/stats` | which platforms pay, how fast, and how crowded they are |
| Invites | `/invites` | private programs you have been invited to |
| Methodology | `/methodology` | how HuntScore and HuntQueue are computed, and what they are not |
| Settings | `/settings` | platforms and intervals, accounts, the login browser, theme |
| Notifications | `/settings/notifications` | where changes are sent, and what was sent |
| API | `/api` | every endpoint, with a filter box and a send button |
| Metrics | `/metrics` | Prometheus counters for this process, behind the login |

## Languages

The interface ships in English (the source language) and Chinese, and a
visitor can switch in the sidebar's footer; the choice is kept in a cookie,
and without one `HUNTDECK_LANGUAGE` decides (`auto`, the default, asks the
browser through `Accept-Language`).

Messages are keyed by their English source text (gettext-style), so a page
reads `{{T "Hunt board"}}` and the catalog `internal/i18n/catalog/zh.json`
maps that exact sentence to `狩猎面板`. A message a catalog doesn't have
falls back to the English, which is why pages can be translated one at a
time: a half-translated language still renders correctly. Template sets are
built once per language at startup (`loadTemplates`), and `render` picks the
set for the request's language — a page missing from a language's set falls
back to English.

Rules for a new string in a template: wrap it in `{{T "..."}}` (or `{{TF "..." args}}`
for a format string), use the English sentence as the key, and add the
Chinese to the catalog. Page titles in Go are translated only from the fixed
list in `pageTitleKeys` (`internal/web/lang.go`), so a title naming a program
keeps the program's name.

`plural n "one" "many"` picks a form and translates it, so a language
without plural forms (Chinese) keeps the same words in both catalog entries.
Relative times go through the catalog too (`"%dd ago"` / `"%d 天前"`,
`internal/web/timefmt.go`), which is why the dashboard says `3 天前`.

The scripts' words are a separate, small catalog (`internal/i18n/catalog/js/zh.json`),
served as `/i18n.js` for the visitor's language: `window.HD_I18N`, which
`static/i18n.js`'s `t("...", args)` reads (a file rather than an inline
script, since the page's CSP gives inline scripts no nonce). `TestScriptMessagesCovered`
fails when a script asks for a message the catalog lacks.

A long page written out in full — Methodology, whose prose is the page —
has one file per language instead of a message per sentence:
`templates/<page>.<lang>.html` (currently `methodology.zh.html`). The loader
picks it for that language and the English `templates/<page>.html` otherwise,
so anchors, classes and markup stay in step by copying the file.

Change sentences are built per request in `internal/web/changelang.go`
(`Scope added: x` / `范围新增：x`, `Status: open → paused` / `状态：在架 → 已暂停`),
so the change list, the dashboard's feed and the change page all read in the
page's language.

Everything the interface writes is translated: the pages, the scripts'
messages (a small catalog served as `/i18n.js`) and the sentences HuntDeck
stores when it scores (HuntScore's reasons read back in the page's language
from the shapes scoring writes). What stays English is the platforms' own
data: program names, policy text, weakness names. `make i18n` lists any
interface copy a template still writes in English.

Counts put the measure word in the sentence, not in the noun: the catalog
holds bare nouns (`program` → 项目, `invite` → 邀请) and the phrases that
count them add 个 ("%s 个%s" → 1,128 个项目). Sentences that use the noun
without a number ("没有匹配该搜索的%s。") then read naturally too. Change a
noun only together with the formats that count it, or both go wrong at once
("1,128 个个项目").

## The login page

With `HUNTDECK_PASSWORD` set, every page redirects to `/login` until the
visitor signs in; the API answers `401` instead. It is a page of its own
(`templates/login.html`, no sidebar or top bar), rendered by `renderLogin`.
Sessions are one signed cookie (`hd_session`, 30 days, `HttpOnly`, `Secure`
over TLS, `SameSite=Lax`) whose key is derived from the password, so changing
the password signs every browser out and nothing is stored server-side. One
address may try 8 wrong passwords per 10 minutes. Signing out clears the
cookie; `next` only ever redirects to a path on this site (`safeNext`).

| Page | What it shows |
|---|---|
| Hunt board | Headline counts, each with the last 7 days' new programs, invites, wildcards, domains and scope; the fresh attack surface: targets added in the last 7, 14 or 30 days, or new to you there (in scope when you got access to a private program, up to 3 per program with a link to the rest) with their score next to the program, and the bounty range and total paid their program gives, newest first or sorted by score or bounty (filter by type, several at once, bounty and platform, and leave out targets or programs by word; copy them); the week's scope diffs per program, with programs that keep flipping between paused and open muted; top picks by HuntScore with their bounty and scope size. Press **Columns** to show, hide or reorder the fresh targets' columns (the same picker as on Programs), and **Customize** to drag the boxes around, resize them by dragging their sides, bottom or corner (widths snap to a 12-column grid, heights to 20px steps; a box shorter than its content scrolls) or hide them; the layout is saved in your browser, separately for ultrawide screens |
| Invites | The private programs you're invited to, laid out like Programs with the same filters (no Visibility, every one is private) plus Invitation: not accepted yet, or accepted. Waiting invitations come first, then the latest invited or joined; one waiting is marked "pending" here and on Programs. Intigriti, HackerOne (with its website login) and Bugcrowd (logged in) say which you haven't accepted; on YesWeHack an invite shows once you can see the program. Their invitations have an **Accept** button (far right, here, on Programs and on the hunt board): HuntDeck accepts it on the platform with its signed-in session (signing in first if it has to), then refreshes just that program, its listing entry, scope and stats, in the background. The hunt board's Invites box lists the waiting ones and those joined in the last 30 days |
| Programs | Built for HackerOne's numbers, with the other platforms filling in what they share. Opens best to hunt first, by HuntScore. A filter rail headed by a name search (a word starting with `-` leaves out names containing it, as does the Exclude field below it) and views (Best to hunt, Fresh scope, Big bounties, Quiet programs, Fast response, New launches), then filters with counts (platform, bounty or VDP, top bounty (or an amount of your own; views keep it, as Quiet programs paying 5k or more), wildcards, apps, APIs, test credentials, new scope in 7/30 days, launched in 90 days, back open, no requirements, status, visibility, ignored, tags); columns for HuntScore (a bar and the number, as on the hunt board; its parts on hover), bounty range, total paid (HackerOne and Intigriti, and a marked estimate for Bugcrowd), average bounty, paid in 90 days (also estimated for Bugcrowd, from CrowdStream, and worked out from Intigriti's saved totals), paid, reports received (HackerOne; worked out for YesWeHack and Intigriti from their saved report totals) and valid reports in 90 days, response time, in-scope assets (the breakdown on hover), new scope in 30 days and notes: requirements and test credentials (**Customize**, top right, shows, hides and reorders them by dragging; the program's column stays first). Paid, reports received and valid reports show their last 90 days, under one "Last 90 days" heading (a dash where a platform has no like-for-like number), as a stock-site style chart instead of the number (the number is in its tooltip), green when it went up and red when it went down. **CSV** downloads the list as filtered and sorted, every page, with the columns the table shows |
| Programs added by hand | **Add program** on Programs: programs that aren't on a supported platform, such as ones companies run themselves, added by hand: **Add program** on Programs opens a form for the name, the program's page, paying or VDP and its bounty range, invite-only, status, scope (one asset per line, in and out: domains, wildcards, URLs, IPs, CIDR ranges) and rules or notes in Markdown. They show under the platform "Independent" everywhere a platform's programs do: Programs, the program page (**Edit** there), Targets and its exports, the scope check, Changes and the API. Saving records what you added, removed or edited on the program's Changes tab; there's no feed, so HuntDeck only knows what you enter: it never fetches the program's page, polls nothing for them, sends no notifications for your edits and gives them no HuntScore (they sort last by it). Removing one keeps its history under Status › Removed. They stay out of seeds unless you tick "Include in seeds" (and never when invite-only) |
| Methodology | Where each Programs column comes from on each platform, what is estimated (marked ≈), the limits, and how HuntScore is scored |
| Program | Tabs. On every tab: the headline numbers as on the Programs list (HuntScore, bounty, total paid, paid / received / resolved in 90 days with charts, response, assets in scope), Copy targets (wildcards, domains or all in scope), "Burp scope", "ZAP context" and "Caido scope" downloads (in-scope targets included, out-of-scope ones excluded; load in Burp under Settings › Project › Scope, ZAP's File › Import Context, or start Caido with `caido-cli --config huntdeck-<platform>-<handle>-caido.json`, a Caido configuration file holding the scope as a preset of host globs, since Caido's Scope page can't import files yet; the menu lists what Caido's file leaves out: apps, hardware, out-of-scope paths and IP ranges a host glob can't express), tags and Ignore (ignored programs are left out of lists, target exports, HuntScore picks and best targets; the scope check still shows them). **Overview**: why its HuntScore is what it is, the public stats the headline doesn't show, the newest assets in scope, the latest disclosed reports, recent changes, the bounty table (by severity with average bounty and share of reports, and by asset), **Before you test** (requirements, rate limits, the header or User-Agent to send, the platform email, test credentials, some read from the policy text), program facts with your own numbers. **Scope**: in scope, out of scope or removed, by asset type, with a filter, when each asset was added (HackerOne's own date; Bugcrowd, Intigriti and YesWeHack dates rebuilt from their scope version history, "≤" when the history only shows it was there by then; otherwise when HuntDeck saw it appear) and new ones flagged, optionally newest first. **Policy**: the program's texts (introduction, policy, scope notes, excluded findings, severity, FAQ, reward notes) with a contents list. **Stats**: per month for a year from HackerOne Hacktivity or Bugcrowd CrowdStream (resolved or accepted, submitted, bounties, median days to resolve), the platform's own numbers over every day HuntDeck has saved them, and disclosed reports (title linking to the report, severity, weakness, bounty, votes, reporter) with a severity breakdown and the most common weaknesses. **Changes**: the program's change history. **Notes**: your own notes on the program (endpoints, ideas, credentials to try, what you've tested), saved as you type in your browser only, never sent to HuntDeck; programs with notes get a ✎ wherever they're listed, Programs' **Has notes** keeps to them, and **Export all notes** / **Import** move them as a JSON file (an imported note replaces yours only if it's newer). Press **n** on a program for its notes |
| Stats | Compare programs by public stats: bounties paid and reports received in the last 90 days, reports received in 90 days (all of them, duplicates included) charted by day and, once there are three months of snapshots, by month; YesWeHack reports per calendar month; reports accepted per month (Bugcrowd CrowdStream), reports resolved per month and valid reports submitted per month over the last year (HackerOne Hacktivity, Bugcrowd CrowdStream) as small bars, paid per report, average and top bounty, the share answered on time, last resolved report **All · Public · Private** beside the title counts every program, only public ones, or only the private ones you were invited to (`?visibility=`, also on the API's `/api/v1/stats/platforms`); a program counts as it is now. |
| Disclosed | Every disclosed report across your programs, newest first (or by bounty or votes), with a filter rail like the Programs page's (severity, weakness, date, bounty, each with its count) and a search by title or program; a row opens the report; `/api/v1/disclosed` takes the same filters |
| Targets | Every wildcard/domain/URL/IP/CIDR across programs, newest first, plus root domains of web targets for recon (`kind=roots`; a root is often not in scope itself), with each target's bounty range (sortable, highest first), a filter rail like the Programs page's (kind, scope, platform, "added in the last 7/30/90/365 days", bounty, each with its count) and a `.txt` download; "Best new targets" ranks targets added in the last 90 days by asset score (see HuntScore), its reasons in the tooltip; the API takes `added=7d` or `added=YYYY-MM-DD` too |
| Changes | With a filter rail (period, kind, platform, with counts; program changes split into new, removed, paused or closed, opened or resumed, bounty, visibility and renames) and sorted by date, program name or platform: new programs, removed programs, scope added/removed/edited, and edits to programs' rules: the policy (all four platforms), reward notes and exclusions (HackerOne), the brief's in-scope notes, out of scope, severity and FAQ (Intigriti, logged in) and the bounty table (HackerOne, Bugcrowd, YesWeHack), each with a diff page that marks the changed words or the changed amounts. **Feed** gives the list as filtered as an Atom feed for feed readers (`/changes.atom`, the newest 50); each program has its own too (`/programs/<platform>/<handle>/changes.atom`, linked from its Changes tab) |
| Scope check | Paste a host or URL and see which programs cover it, in or out of scope, and which used to (scope programs removed, and programs that have gone, with when). A wildcard covers subdomains, not the domain itself, so the check also lists what's under the host: wildcards on it (`*.example.com` for `example.com`) and its listed subdomains, with a link to them all on Targets. Part of a name works too: when nothing is the host or under it, the check lists scope that contains the text (`canva-apps` finds `*.canva-apps.cn` and `*.canva-apps.com`), and the top search bar goes there when no program's name has the text. `/api/v1/match` returns these as `matches`, `past`, `under` and `mentioning`. For a whole list (recon output), POST it one per line to `/api/v1/match` (JSON verdicts: in, out or none, with the programs; `format=txt` for just the in-scope ones, up to 20,000 lines) or pipe it into `huntdeck match -` |
| Notifications | Discord, Telegram, Bark, ServerChan and webhook channels, a row each with what it sends and when, its Test button and last message; examples of real messages from the last week; recent messages and errors. A channel's form groups its events (opportunities, watching, HuntDeck), sends a test before saving, and previews what it would have sent in the last week as you tick — as cards for Discord, Telegram and webhooks, and as the message's own text for Bark and ServerChan (see Notifications below) |
| Settings | Tabs. **Polling**: one table of platforms: on/off and intervals, saved together; **Sign-in**, each credential (API token, login, website login) with whether the platform accepted it the last time it was used, or why it refused it; last polls and "poll now"; and under Intigriti its browser sync (logged-in website reads, separate from polls) with its own on/off, interval, last run, "run now" and what it pulls for every program. Then poll history. **Accounts**: each platform's credentials, rates and public-only switch, and the proxy, saved encrypted over `.env`'s (passwords and tokens are never shown again). **Browser**: the login browser (one Chrome, a tab per platform), live when `HUNTDECK_VNC` is on, to watch a login or finish one by hand. **HuntScore**: how its parts are weighted. **Display**: the time zone dates and times are shown in, and the theme |
| API | Every endpoint, by group, with its parameters, a copyable curl example and a sample response; a GET can be edited and sent from the page to see this HuntDeck's answer. The OpenAPI 3.1 description downloads from there (`/api/v1/openapi.json`) for Postman, Insomnia, Bruno or a client generator |

**Watch** on a program's page adds it to your watchlist, kept in your browser (no account needed): **Your watchlist** at the top of the Programs and Changes filters then shows only those programs, and the Changes feed can follow them. Changes newer than your last visit (half an hour without a page view ends one) get a bar down their left edge, and Changes says how many there are.

**Save view** on the Hunt board, Programs and Targets keeps the page's filters, sort and (on the hunt board and Programs) columns under a name, in your browser too; **My views**, beside it, loads, renames or deletes that page's views, and **Export views** / **Import views** move them as a JSON file (an imported view replaces one of the same name only if it's newer). A view saved before a filter went away loads without it.

Keyboard: **/** searches, **j** and **k** move through a list's rows, **Enter** or **o** opens one (Shift: in a new tab), **x** ticks its checkbox, and **g** then a letter goes to a page (**g p** Programs, **g c** Changes, …), **n** opens a program's notes, **v** your saved views. **?** lists them all.

The search bar at the top of every page (press `/`) checks scope when you paste a host, IP or URL. Anything else searches program names and scope: the Programs list when a program's name has it (saying how many scope rules mention it too, with a link), otherwise the scope check, which lists the scope that mentions it (`canva-apps` finds `*.canva-apps.cn`). The **Theme** menu at the top right, next to when the latest poll finished, picks light, dark or your system's, and the colours: Midnight (navy and lavender, the default), Nord (slate and frost blue) or Graphite (neutral greys), each in light and dark (Settings → Display → Theme picks the colours too, with a preview); the choice is kept in your browser.


## After an action: the flash

Pages that change something answer with a redirect and a flash line at the
top of the page that follows: \`flashMessages\` and \`flashErrors\` in
\`internal/web/server.go\` map a short key to its sentence, and the redirect
carries it (\`?msg=program-ignored\`, \`?err=poll-running\`). The layout renders
both through \`T\`, so they read in the page's language: add the English
sentence to the catalog when adding a key.

```go
http.Redirect(w, r, programPath(p)+"?msg=program-ignored", http.StatusSeeOther)
```

That is how ignoring a program, saving tags, exporting the demo seed or
starting a poll say what happened. The selection bar's bulk actions use the
same pattern: they append \`msg=\` to the page they return to.

## The hunt queue (`/queue`)

The board shows the first five targets of this list; the page shows fifty at a
time and is where the ranking is meant to be read.

Each row is one target of one program, with the score and the sentence that
explains it (`pays up to $15.0k · accepted in a median of 16 days · target added
8 days ago`). Clicking the score unfolds the factors behind it: the value each
input took and the multiplier it applied, so the number stops being a verdict
and becomes an argument you can disagree with. The same breakdown is in the
API (`?explain=1`), the CLI (`huntdeck queue --explain`) and the MCP tool.

### The chips

| Chip | What it does |
|---|---|
| each platform | that platform's targets only |
| Paying only | programs that pay bounties |
| Added in 7 days | targets added, or new to you, in the last week |
| One per program | that program's best target only |
| Clear filters | appears once any chip is on; drops them all |

Chips combine: two on means both apply, and the heading counts what matches
rather than what fits on screen.

### What the page is honest about

- **The footer's snapshot count.** The queue is a claim about the future
  ("this is worth your hour"), so it is judged against what the platforms
  actually paid at each past day it was built. `N of 8 snapshots` means that
  judgement has not started; until it does, HuntScore is the number with a track
  record and the queue is a well-argued guess.
- **The gated list.** Targets the queue keeps out are listed with the reason
  (a paused program, a pending invite, a program with no scope) rather than
  silently dropped.
- **The build time.** "Built 14m ago" keeps ticking while the page is open, so
  a stale page says it is stale.
- **The score drifts between rebuilds.** Freshness fades in real time, so the
  same target reads 68.1 at one rebuild and 68.0 at the next. The API and the
  page always agree because both read the same row; two screenshots an hour
  apart need not.

### Filters are links

Every chip is a plain link, so a filtered queue can be bookmarked, shared or
opened by a script without any JavaScript having run.

## HuntScore

HuntScore (0–100) is HuntDeck's estimate of how worth hunting a program is right
now, worked out from the data it already stores, after every poll. It has four parts:

| Part | What raises it |
|---|---|
| Fresh | Assets added in the last 30 days (wildcards and new root domains count most, bounty-eligible more than not, halving every 14 days), a recent invite, launch or reopening, a raised bounty table |
| Competition | Few reports for the size of the scope compared with the platform's other programs, invite-only programs (a strong boost), programs that are new |
| Payout | Top bounty, bounties paid in 90 days and average bounty, compared with the platform's other programs |
| Response | Response efficiency, time to first response and time to validate |

Programs are compared only with others on the same platform, since each shares
different numbers. A part the platform gives nothing for scores a neutral 45
and lowers the confidence shown; programs without bounties are scaled down.
Paused and closed programs keep a score but aren't picked. A program's
requirements (Signal, ID check, residence) are shown but don't change its
score or picks. Settings chooses how the parts are weighted:
Balanced, New surface, Big payouts or Low competition. Your own reports aren't
used. Fresh only counts assets with a known date: one only known to have been
there by some date ("≤") may be much older.

Targets get an **asset score** too (shown as "Target score"), for "Best new targets" on the Targets page
and `sort=best` in the API: in-scope targets added in the last 90 days on
programs you can hunt, scored by how new they are (halving about monthly),
what they're worth (wildcards first; bounty eligibility, severity, a root
domain new to the program), whether other programs list the same target, and
their program's competition, payout and response.
## Selecting rows, copying and bulk actions

The Programs, Targets and Disclosed lists (and the hunt board's targets) have a
tick column and a select-all box in the header. Ticking anything raises a
selection bar that stays at the bottom of the screen while the list scrolls:
it counts what's ticked ("已选 3 项" in Chinese, "3 selected" in English),
copies the ticked values as plain text, one per line — `platform/handle` for
programs, the identifier for targets, the report URL for disclosed reports —
and, on the Programs list, ignores them in one go. Selection is per page: a
filter, sort or page change starts a new page and an empty selection.

On a touch screen, press and hold a row (about half a second) to tick it: the
box alone is a small target. Moving the finger cancels it (that's a scroll),
and the tap that ends the hold doesn't open the row. The selection bar's
buttons work the same as with a mouse.

## Feedback and keyboard

Copying says so on the button ("Copied" / "已复制"), and a download (CSV,
Atom feed, targets as text) shows a short "Download started" line at the
bottom of the screen; both are announced to screen readers (role="status").
The layout editor on the hunt board announces entering and leaving, confirms
resetting the layout, and keeps the keyboard on the box handles: the arrow
keys move a box, Shift with the arrow keys resizes it, Esc leaves the editor.
`?` lists every keyboard shortcut; `/` focuses the search; `g` then a letter
opens a page.

**Phone width is part of the check.** The audit captures 390 px as well as
1400 px by default: at that width the tables become stacked cards (each row
carries its own labels) rather than a squeezed grid, which is a layout nobody
sees on a laptop. Checked 2026-11-19 on the board, queue, a program page and
Settings: the sidebar becomes a Menu button, the queue list and the program
header stack, and Settings' platform table turns into one labelled block per
platform.

**Alignment is measured, not eyeballed.** `scripts/layout-audit.mjs` walks every
page at two widths and reports sibling boxes that sit side by side, have the same
height and yet do not share a top: the class of bug where two chips are a few
pixels apart because one is an inline-block and the other a flex item, with no
differing property to point at. It found the board's kind chip and amount 5px
apart (offsetTop 75 against 70, identical computed styles); after the row became
a flex line both measure 70. Run it after touching a table or a chip row:

```sh
HD_PASSWORD='…' node scripts/layout-audit.mjs --base https://huntdeck.example --sizes 390,1400
```
