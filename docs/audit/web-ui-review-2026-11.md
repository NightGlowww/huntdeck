# HuntDeck web layer: behaviour review (2026-11)

Scope: `internal/web/templates/**/*.html`, `internal/web/static/*.js`,
`internal/web/static/css/*.css`, and the handlers that feed them
(`internal/web/*.go`, read-only). Everything here is a **behaviour** defect a
user hits — a form that posts the wrong thing, a control that does nothing, text
that states a number the server did not send, a broken reference, dead code that
was supposed to be live. No style opinions.

Reviewed at commit `1aa6d33` (working tree `1aa6d33db261-dirty`), against the
running instance `deploy-202611190415` on 2026-11-19 ~04:15–04:30 CST, signed in
through the access password. Nothing in the repository was modified except this
file; no commits.

## How this was verified

- **Code**: every finding cites the line that causes it and the line that would
  fix it. Where a template and a script disagree, both sides are quoted.
- **Live**: 36 pages fetched from the authenticated instance (dashboard,
  programs, targets, changes, disclosed, stats, stats/<platform>, queue,
  invites, settings on every tab, notifications, the new-channel forms, `/api`,
  `/methodology`, one program page and its Changes tab, `/programs/independent/new`),
  plus 314 unique internal links crawled and 9 queue filter combinations probed.
- **Differential probes** where a 500 could have one of several causes: every
  queue filter that resolves against a table the query *does* join returns 200;
  every filter that resolves against the one it does not join returns 500
  (finding 1).
- Anything I could not reach (multi-platform browser-sync rows, a second saved
  view, a phone-width layout) is marked **UNVERIFIED** with what would settle it.

## Findings at a glance

| # | Severity | Finding |
|---|---|---|
| 1 | high | `/queue?one=1&added=7d` (and every `added=` value) answers **500 Internal Server Error** |
| 2 | medium | The Hunt board's Fresh attack surface says "40 targets shown" while listing 30 rows, 12 of them visible |
| 3 | medium | On Targets, the count line is untranslated English in the Chinese UI ("2,076 urls", "379 cidr ranges") |
| 4 | medium | A program's change feed has its dates rewritten into "9d ago" by the relative-time ticker |
| 5 | medium | The Export menu on Targets never closes: outside click and Escape are ignored |
| 6 | medium | The notification channel filter cannot be applied without JavaScript: its Apply button is `hidden`, and the hook meant to reveal it is used by no template |
| 7 | medium | The queue's filter chips cannot be combined, and "All platforms" loses its selected state whenever another filter is on |
| 8 | low | The saved-views dialog's name field has the literal string `' + t("View name") + '` as its accessible name |
| 9 | low | `/stats/<platform>`'s pager links end in `#list`, an id that page does not have |
| 10 | low | A notification channel's "Never programs tagged" field shows the placeholder **"none placeholder"** |
| 11 | low | `id="websync"` is emitted once per platform with a browser sync, so two platforms give a duplicate id |
| 12 | low | Script-generated status text bypasses `t()`, so the Chinese UI shows English sentences ("Saved “x”", "9 new since your last visit") |

---

## 1. `/queue?one=1&added=7d` answers 500 (high)

**Files:** `internal/store/queue.go:299-307` (`QueueFilter.where`),
`:326-336` (`HuntQueueTop`, the one-per-program branch), `:571-578`
(`huntQueueRows`); reached from `internal/web/queuepage.go:54-61,76`.

`where()` adds, for the "added in the last N days" filter:

```go
clauses = append(clauses, "ts.added_at >= ?")
```

`ts` is the `target_scores` alias, and only `HuntQueueTop`'s *paged* query
declares it (`:369`). The one-per-program path calls `huntQueueRows`, whose
query (`:573-578`) is

```sql
FROM hunt_queue q JOIN programs p ON p.id = q.program_id
WHERE q.gate = '' <where>
```

— no `LEFT JOIN target_scores ts`. The predicate therefore references a table
that is not in the statement.

**What a user sees:** the queue page replaced by *Internal Server Error*. The
error is produced before rendering, so there is no page chrome to recover with
except the sidebar.

**Verified** (authenticated instance, 2026-11-19):

```
200  one=1&kind=domain          # q.kind: in scope
200  one=1&paying=1             # p.offers_bounty: in scope
200  one=1&platform=hackerone   # p.platform: in scope
500  one=1&added=7d             # ts.added_at: NOT in scope
500  one=1&added=14d
500  one=1&added=30d
200  added=7d                   # the paged query, which does join ts
```

Every other `one=1` filter works, so the dedupe path itself is fine: only the
predicate that needs `ts` fails. This is a regression from the fix recorded in
`docs/audit/queue-review-2026-11.md` §3 ("one-per-program is no longer a WHERE
clause … dedupes in Go"): the new `huntQueueRows` query did not carry the join
the shared `where()` still assumes.

**Reachable by hand, not by click:** the chips in finding 7 never set two
filters at once, so this needs an edited or bookmarked URL. It is still one
keystroke away from the two chips a user most wants together ("one per program"
+ "added in 7 days"), and it is a hard 500 rather than an empty list.

**Fix:** give `huntQueueRows` the same join `HuntQueueTop` has —

```go
FROM hunt_queue q JOIN programs p ON p.id = q.program_id
LEFT JOIN target_scores ts ON ts.program_id = q.program_id AND ts.kind = q.kind AND ts.value = q.value
```

— or, better, build the added-window predicate against `q.computed_at`-free
data in a subquery so `where()` never needs an alias the caller might not have.
A test that runs `HuntQueueTop` with `OnePerProgram: true, Added: "7d"` would
have caught it; the existing queue tests cover the two filters separately.

## 2. The board says "40 targets shown" while showing 12 (medium)

**Files:** `internal/web/templates/dashboard.html:98`,
`internal/web/board.go:20-21,377-386`, `internal/web/static/css/12-board.css:117-118,22`.

```html
<span class="muted">{{num .TargetTotal}} {{plural .TargetTotal "target shown" "targets shown"}}</span>
```

`TargetTotal` is the number of targets *matching the filter*, not the number on
the page: `boardTargets` (`board.go:377-386`) fetches at most
`boardTargets = 30` rows and returns the true total separately, and the
stylesheet hides everything past `boardRows = 12` unless the screen is ≥2200 px:

```css
tr.xl, li.xl, .dcard.xl { display: none; }
@media (min-width: 2200px) { tr.xl { display: table-row; } … }
```

**What a user sees:** on the home page, under *Fresh attack surface*, the footer
of the table reads "**40 targets shown**" while the table lists 30 rows and a
laptop actually paints the first 12. The adjacent "All 40 →" link offers the same
number, so the two are indistinguishable from a statement about what is on screen.

**Verified:** the live dashboard contains 30 `<tr data-href>` rows inside
`#fresh`, 18 of them `class="xl"` (hidden at the sampled width), and the
selection bar reads `40 targets shown`; the "All" link reads `All 40 →`.

**Fix:** say what is on the page — `{{num (len .Targets)}} {{plural (len .Targets) "target shown" "targets shown"}}`
— and keep `TargetTotal` for the "All N →" link, which is already correct. Or
label it "40 match" and let the pager-less table stay honest about the 30 it
carries.

## 3. Targets' count line is English in the Chinese UI (medium)

**File:** `internal/web/templates/targets.html:17`

```html
<span class="total">{{num .Pager.Total}} {{T (lower (kindLabel .KindParam))}}</span>
```

`kindLabel` (`internal/web/display.go:66-87`) returns capitalised plural labels
— `"URLs"`, `"IPs"`, `"CIDR ranges"`, `"Root domains"`, `"All kinds"` — and the
template lowercases them before looking them up. The Chinese catalogue only
carries three of the lowercase forms, which are exactly the three that work:

```
zh.json:134  "wildcards": "通配符"
zh.json:442  "domains": "域名"
zh.json:72   "apps": "应用"
zh.json:145  "CIDR ranges": "网段"      # capitalised: never matched
zh.json:147  "IPs": "IP"
zh.json:148  "URLs": "URL"
zh.json:150  "Root domains": "根域名"
zh.json:151  "All kinds": "全部类型"
```

**What a user sees** (`hd_lang=zh`, live):

```
/targets?kind=url    -> 2,076 urls
/targets?kind=ip     -> 533 ips
/targets?kind=cidr   -> 379 cidr ranges
/targets?kind=root   -> 15,521 root domains
/targets?kind=app    -> 1,677 应用      (correct)
```

The chip bar directly above each of those lines is translated, so the page
disagrees with itself. The same English also reads oddly in English: every other
heading on the page is a capitalised label, this one is a lowercased one.

**Fix:** translate the label as `kindLabel` returns it — `{{T (kindLabel .KindParam)}}`
— and drop the five `lower`-keyed duplicates that do exist (or add the missing
lowercase keys; the first is less to maintain).

## 4. A program's change feed loses its dates to the relative-time ticker (medium)

**Files:** `internal/web/templates/partials/program-panels.html:100` and `:110`,
`internal/web/static/app.js:704-730`.

The feed rows and the "busy day" summary render a **date**:

```html
<li data-at="{{iso .OccurredAt}}"><span class="when" …>{{date .OccurredAt}}</span>
…
<li class="dayfold" data-at="{{iso .OccurredAt}}"><details><summary><span class="when" …>{{date .OccurredAt}}</span>
```

The ticker selects `[data-at]`, finds the inner `.when` and overwrites its
text with a relative phrase, immediately on load (`tick()` is called at
`app.js:728`):

```js
const cell = el.matches(".when") ? el : el.querySelector(".when");
if (cell) { cell.textContent = text; … }
```

**What a user sees:** a program's Changes tab lists rows dated "2026-11-10" with
no scripts, and "9d ago" with them — including the day-group heading, which is
the only place the date appears for a folded group. The full timestamp survives
only in the `title` tooltip. The Changes *list* page renders `{{ago}}` in the
same `.when` (`changes.html:35`) and is unaffected, so the same list shape
reads differently on two pages.

**Verified:** live `/programs/bugcrowd/openai?tab=changes` serves
`<li data-at="2026-11-10T04:03:12Z"><span class="when" title="2026-11-10 12:03 CST">2026-11-10</span>`;
`app.js` rewrites that span to "9d ago" at load.

**Fix:** let the ticker own only spans it painted. Either mark the relative ones
(`<span class="when" data-ago>`, which `dashboard.html:52` already uses for
exactly this) and have `app.js` select `[data-ago]`, or give the date spans a
class the ticker does not touch (`.when.date`) and skip them.

## 5. The Export menu on Targets never closes (medium)

**Files:** `internal/web/templates/targets.html:1`, `internal/web/static/app.js:390-401`.

```html
<details class="tb-menu" data-tb-menu>
```

The menu closer in `app.js` only looks at `details[data-menu][open]`:

```js
const menus = () => document.querySelectorAll("details[data-menu][open]");
document.addEventListener("click", (e) => { for (const m of menus()) if (!m.contains(e.target)) m.open = false; });
document.addEventListener("keydown", (e) => { if (e.key !== "Escape") return; for (const m of menus()) { m.open = false; m.querySelector("summary")?.focus(); } });
```

`data-tb-menu` is written by **no other template** and read by **no script**: the
only other selector for these menus is `details.tb-menu` in the `toggle`
handler (`app.js:405`), which nudges an open menu back on screen. Every other
menu in the app — the three in `program-head.html`, the one in `myviews.html` —
carries `data-menu` and closes properly.

**What a user sees:** the Export dropdown on Targets stays open when you click
anywhere else on the page, and Escape does nothing to it; you have to hit the
summary again. Every other dropdown on the site behaves the other way.

**Verified:** live `/targets` serves `<details class="tb-menu" data-tb-menu>`;
`grep` finds `data-tb-menu` only at `targets.html:1`, and `data-menu` in the
four other menus.

**Fix:** `<details class="tb-menu" data-menu>`.

## 6. The notification channel filter needs JavaScript, though every page promises it does not (medium)

**Files:** `internal/web/templates/notifications.html:60-66`,
`internal/web/static/app.js:382-388`.

```html
<select name="channel" data-autosubmit aria-label="{{T "Channel"}}">…</select>
<button type="submit" class="small" hidden>{{T "Apply"}}</button>
```

The fallback button is hard-hidden in the markup. The script that is supposed to
hide it (**only** when it takes over) has nothing to hide:

```js
// Selects with data-autosubmit apply at once; their form's button is only
// needed without scripts.
for (const sel of document.querySelectorAll("select[data-autosubmit]")) {
  const btn = sel.form.querySelector("[data-autosubmit-button]");
  if (btn) btn.hidden = true;
  sel.addEventListener("change", () => sel.form.requestSubmit());
}
```

`[data-autosubmit-button]` appears in no template, so the hook is dead code, and
the one place that needs it ships the button already hidden.

**What a user sees:** with scripts blocked or broken — the stated baseline,
`app.js:1`: *"Progressive enhancements only: every page works without
JavaScript."* — picking a channel in *Recent messages* changes nothing, and
there is no visible control to submit it. The status chips next to it are plain
links and still work, so the filter row looks functional.

**Verified:** code (`grep -rn 'data-autosubmit' internal/web/templates
internal/web/static/*.js` → the select and the script only); live page carries
`<button type="submit" class="small" hidden>Apply</button>`.
**UNVERIFIED** by disabling JS in a browser; it follows from the markup.

**Fix:** drop `hidden` and add `data-autosubmit-button` to the button, so the
script hides it only when `requestSubmit()` is wired.

## 7. The queue's filter chips cannot be combined, and one of them lies (medium)

**Files:** `internal/web/templates/queue.html:11-15`, `internal/web/queuepage.go:54-61`.

Each chip is a fixed URL, so it replaces the whole filter rather than adding to it:

```html
<a href="/queue"                  …>{{T "All platforms"}}</a>
<a href="/queue?paying=1"         …>{{T "Paying only"}}</a>
<a href="/queue?added=7d"         …>{{T "Added in 7 days"}}</a>
<a href="/queue?one=1"            …>{{T "One per program"}}</a>
<a href="/queue?platform={{.}}"   …>{{platformLabel .}}</a>
```

`QueueFilter` (`store/queue.go:270-279`) supports all four at once, and the page
header tells the reader that combining is expected. Clicking "Paying only" while
filtering by platform silently drops the platform; there is no way to reach
"paying + one per program" at all — which is also why finding 1 is not on a
click path.

The selected-state condition for the first chip is wrong as well:

```html
{{if and (not .Filter.Paying) (eq .Filter.Platform "")}} class="chip on"
```

The chip means "platform = all". It loses its `on` state as soon as *any other*
filter is set, so the row shows no platform selected even when none is.

**Verified** live:

```
/queue?paying=1        -> All platforms: (not on), Paying only: ON
/queue?platform=hackerone -> All platforms: (not on), HackerOne: ON
/queue                 -> All platforms: ON
```

**Fix:** build the chip hrefs from the current filter (the pattern `pills` uses
on Programs/Targets/Changes: keep every other parameter, toggle this one), and
mark "All platforms" on when `eq .Filter.Platform ""` alone.

## 8. The saved-views dialog names its field `' + t("View name") + '` (low)

**File:** `internal/web/static/views.js:60-63` (line 61).

```js
dlg.innerHTML = `<form method="dialog"><h2></h2><p class="muted small"></p>
  <input type="text" maxlength="${NAME_MAX}" autocomplete="off" spellcheck="false" aria-label="' + t("View name") + '">
  …
```

The `aria-label` value is a leftover from a string-concatenation version and sits
*inside* a template literal, so it is written to the DOM verbatim.

**What a user sees:** nothing visually; a screen reader announces the field as
"quote-plus-t-open-paren-quote-View name-quote-close-paren-plus-quote" instead of
"View name". The dialog itself ("Save view" / "Rename view") is otherwise the
only way to name a saved view, so this is the whole input.

**Verified:** code; the literal is unambiguous. **UNVERIFIED** with a screen
reader.

**Fix:** `aria-label="${t("View name")}"`.

## 9. `/stats/<platform>`'s pager links to an anchor that page does not have (low)

**Files:** `internal/web/templates/statsplatform.html:22,51`, `internal/web/pager.go:57-66`.

`pager.url` appends `#list` so that paging returns the scroll position and the
keyboard focus to the top of the list — the comment says so:

```go
// Back to the list itself: the anchor puts the scroll and the keyboard focus
// at its top, not at the top of the page above the filters.
return p.path + "?" + q.Encode() + "#list"
```

On the five list pages the target exists (`<div class="table-wrap" id="list" tabindex="-1">`
in `programs.html:15`, `targets.html:28`, `changes.html:20`, `disclosed.html:22`,
`queue.html:22`). `statsplatform.html` uses the same pager but wraps its table in a
bare `<div class="table-wrap">` (`:22`).

**What a user sees:** paging a platform's stats page jumps to the top of the
document instead of the list, and keyboard focus stays wherever the pager click
left it — the behaviour the other five pages were fixed for.

**Verified:** live `/stats/hackerone` contains `href="/stats/hackerone?page=2#list"`
and zero occurrences of `id="list"`.

**Fix:** `<div class="table-wrap" id="list" tabindex="-1">` at
`statsplatform.html:22` (the markup the other five use).

## 10. "Never programs tagged" shows the placeholder "none placeholder" (low)

**File:** `internal/web/templates/notification.html:83`; catalogue entry
`internal/i18n/catalog/zh.json:908`.

```html
<input type="text" name="exclude_tags" value="{{.ExcludeText}}" placeholder="{{T "none placeholder"}}" maxlength="700">
```

The neighbouring field ("Only programs tagged") says `placeholder="any"`. This
one carries a string that reads like a note to the translator, and the catalogue
confirms it was treated as user copy: `"none placeholder": "无"`.

**What a user sees:** adding a notification channel, the field under
*Never programs tagged* is greyed with the words **"none placeholder"** — on the
page for every channel kind.

**Verified** live (`/settings/notifications/new?kind=webhook|discord|telegram`,
all three): `placeholder="none placeholder"`. In Chinese it renders "无", i.e.
correctly by accident.

**Fix:** `placeholder="{{T "none"}}"` (and remove the now-unused catalogue key),
or drop the placeholder to match the field's optional nature.

## 11. Two platforms with a browser sync give the page two `id="websync"` (low)

**File:** `internal/web/templates/settings.html:37-38`, inside the
`{{range $p := .Platforms}}` at `:16-56`.

```html
{{with $w := $p.Web}}
<tr class="sub…" id="websync">
```

The row is emitted once per platform that has a web login, with a fixed id. The
id is not referenced by any `aria-controls` or `href`, so nothing breaks
functionally; it is a duplicate id in the document, which invalidates anchor
navigation to it and is the one duplicate-id class the rest of the app avoids
(the crawl of 36 pages found no other duplicate).

**Verified:** code; live `/settings` has exactly one `id="websync"` because one
platform currently has a browser sync configured. **UNVERIFIED** with two:
Intigriti and YesWeHack both support it, so enabling both would produce the
duplicate.

**Fix:** `id="websync-{{$w.Name}}"` (or drop the id if nothing targets it).

## 12. Script status text bypasses the catalogue (low)

**Files:** `internal/web/static/views.js:118,174,176,185,210,248`;
`internal/web/static/watch.js:83`.

`i18n.js` states the contract — *"window.HD_I18N holds the message catalog … a
message the catalog doesn't have reads as the English source"* — and the scripts
are supposed to ask for every sentence through `t()`. These do not:

```js
a.title = v.name + "\nLoad: " + (clean(v.query) || "the page's defaults") + (v.cols ? ", with its columns" : "");   // views.js:118
say(`At most ${PER_PAGE} views a page: delete one first`);                                                          // :174
say(`Saved “${name}”`);                                                                                            // :176
say(`Renamed to “${name}”`);                                                                                       // :185
say(`Exported ${n} ${n === 1 ? "view" : "views"}`);                                                                // :210
say(`Imported ${added}${kept ? `, kept ${kept} newer here` : ""}${bad ? `, skipped ${bad}` : ""}`);           // :248
note.textContent = `${n}${more} new since your last visit`;                                                        // watch.js:83
```

Because they never call `t()`, the completeness test that covers the scripts
cannot see them either.

**What a user sees:** in the Chinese interface, saving a view answers
"Saved “my view”", deleting one answers "Deleted “…”", importing a file answers
"Imported 3", and the Changes page's since-your-last-visit note reads
"9 new since your last visit" — all in English inside an otherwise translated page.

**Verified:** code. **UNVERIFIED** rendered in `zh`: it needs a saved view (a
localStorage interaction). The strings are unambiguously outside `t()`.

**Fix:** wrap each in `t()` (with `TF`-style substitution — `t()` already
replaces `%s`/`%d`) and add the keys to the scripts' catalogue, which is what
the existing test expects to find.

---

## Checked and found sound

Worth recording, because these are the classes that usually fail and did not:

- **No dead links or buttons.** 314 unique internal `href` targets from the 36
  pages were fetched: none answered 404 or 405. 34 answered 502 with the
  instance taking ten requests a second; four of those were retried
  individually a minute later and returned 200, and the rest were the same tail
  (no other status appeared). Every POST form's action exists in
  `server.go:222-279`.
- **No CSP violations.** The policy is `default-src 'self'; img-src 'self' data:;
  style-src 'self'; script-src 'self' 'nonce-…'` (`server.go:385-386`). No
  rendered page carries a `style="…"` attribute or an inline event handler, and
  the only `<script>` without `src` is `application/ld+json`, which does not
  execute. The scripts set styles through CSSOM, which the policy allows.
- **No duplicate ids and no broken `aria-controls`/`aria-describedby`/
  `aria-labelledby`/`for`/`href="#…"`** on the 36 pages loaded (the two
  exceptions are findings 11 and 9).
- **The skip link and the pager's focus target** work on the five list pages:
  `#main` and `#list` both exist with `tabindex="-1"`.
- **`aria-sort`** is present on the sorted column of the Programs table and absent
  elsewhere, which is correct (it is not a per-column label).
- **Forms whose inputs live outside their `<form>`** (the Settings interval
  table, `form="sched"`; the Remove button of a hand-added program,
  `form="remove-program"`) do submit, and `saveSchedule` reads the repeated
  `platform` field positionally-safe (`settings.go:205-238`).

## What I could not verify

- Findings 11 and 6's no-JS behaviour need a browser state I could not set from
  here (two web-sync platforms; scripts disabled).
- Finding 12's text needs a saved view in localStorage; the code is quoted instead.
- Layout at other widths: 2 (12 rows) is asserted from the stylesheet's
  `tr.xl { display: none }` and the live markup, not from a rendered screenshot
  at each breakpoint.
- The queue page's "Blocked" panel is unfiltered by design and prints gate keys
  through `huntqueue.Render`; with nothing gated on the instance today
  (`Nothing is blocked right now`) it is unexercised, so I left it to
  `queue-review-2026-11.md` §13 rather than re-reporting it.
