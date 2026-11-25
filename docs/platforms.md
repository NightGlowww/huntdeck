# Platforms

What HuntDeck reads from each platform, and how.

- **HackerOne** uses the official Hacker API with your API token. Program
  stats (bounties paid, reports resolved, the share answered on time, bounty table)
  aren't in that API, so they come from the GraphQL endpoint behind
  hackerone.com program pages, asked anonymously about once a day (three
  programs per request, one request a second). That endpoint is unofficial:
  if HackerOne changes it, stats stop updating and polls carry on.
  Bugcrowd (rewarded reports, average payout, validation time) and
  YesWeHack (reports submitted, first response) stats come from their
  program pages, one request per program a day. Intigriti's API has none,
  so its stats (total paid, average payout, reports submitted and accepted)
  come from the public program pages on app.intigriti.com, read without your
  token: one request for the list plus one per public program a day (about
  170, roughly 250 KB each). That is unofficial too; programs you have to
  apply to have no public page. Each day's numbers are kept, and program
  pages chart them. Where a platform publishes only all-time totals
  (YesWeHack, Intigriti), the 90-day figures are worked out from the saved
  totals: estimates (≈) until there are 90 days of them. With the Intigriti
  website login, each program's leaderboards (last 90 days and all time; two
  more requests per program a day) give those estimates from the first day:
  the all-time totals × the share of the program's reputation points earned
  in the last 90 days. Past months' leaderboards, a year back (one request a
  month per program, spread over the first few days, then one a month),
  estimate valid reports per month from before HuntDeck saved the program's
  totals. Only the sums are kept, never who is on them.
- **Bugcrowd** has no researcher API. HuntDeck signs in in its login browser, as you
  would (Bugcrowd's Okta login, with your password and authenticator code), and
  reads the same JSON the website loads. This is unofficial: it can break when
  Bugcrowd changes their site, and automated access may not be covered by
  Bugcrowd's terms. Keep `BC_RATE` low and use it for your own account only.
  HuntDeck checks the session before and after each listing, so an expired
  session can never make your private programs look removed. Some programs
  (many US-government VDPs, for example) withhold scope from logged-in
  accounts until you accept their terms on bugcrowd.com; HuntDeck lists them
  without scope until you do.
- **Intigriti** uses the official researcher API with a personal access token,
  plus its public program pages for stats (see above). With `IT_EMAIL` and
  `IT_PASSWORD`, a daily browser sync reads the logged-in web app instead,
  with your account, as the website does: unofficial in the same way as
  Bugcrowd, about three requests per program a day. If its Chrome login
  fails (the login page now and then takes Chrome for a bot), the sync runs
  again after 1 hour, then 2, 4, 8 and 16 hours, until it signs in; a
  refused password isn't retried before a restart.
- **YesWeHack** uses the API its website uses (`api.yeswehack.com`), logging in
  with your email, password and authenticator code like the website does.
  Unofficial in the same way as Bugcrowd.
- **Your own reports** are read in polls, at most once an hour (Settings ›
  Polling switches them off per platform). HackerOne: the Hacker API's list
  of your reports (100 a page), a report's activities when it changed, and
  your earnings. Bugcrowd: your submissions as the website lists them (25 a
  page), and a submission and its activity feed when it changed. Intigriti:
  the logged-in web app's submissions (so they need `IT_EMAIL` and
  `IT_PASSWORD`), a submission when it changed, and with the API token your
  payouts' dates. At most 100 reports' details a poll, so a long history is
  read over the first few. Only titles, states, severities, dates, who spoke
  last, the number of comments and payments are kept; never the write-up or
  what anyone wrote. YesWeHack reports aren't read yet: add them by hand.
- **Other platforms** (Inspectiv, HackenProof, a company's own portal…) can be
  added as custom sources: a script of yours reads the platform and writes a
  JSON document, which HuntDeck polls (`HUNTDECK_SOURCES`;
  [custom sources](custom-sources.md)). How the script reads the
  platform, and whether its terms allow it, is up to you; HuntDeck only reads
  the document. To build a full client instead, see
  [adding a platform](adding-a-platform.md).
- **The public starter seed** holds only what each platform shows anyone
  without signing in (a signed-out seed: programs that are and always were
  public, with their scope, stats and history as HuntDeck collected them).
  It's offered so new installs don't start blank; whether reusing that data
  suits a platform's terms is yours to judge, like the polling itself.
