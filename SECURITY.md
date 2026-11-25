# Security

## Reporting a vulnerability

Use the repository's private security advisory ("Report a vulnerability"
under the Security tab). Please don't open a public issue for a
vulnerability.

Describe the problem with HuntDeck itself, with steps to reproduce it on a
fresh install. Use made-up data only: never send

- your HuntDeck database, seeds exported from it, or logs;
- private program names, scope, briefs or anything under a platform's NDA;
- platform credentials, session cookies, API tokens, TOTP secrets or
  notification webhook URLs;
- details of a vulnerability you reported to a bug bounty program.

You'll get an answer within a week. Fixes go into the next release, and the
advisory is published once a release with the fix is out.

## Supported versions

The latest release. HuntDeck upgrades its database by itself on start (with
a backup first), so staying current is a matter of pulling the new image.

## Security model

HuntDeck is a personal tool, and its design assumes it is reached only by
you:

- **There is no login, by design.** The compose file publishes it on
  `127.0.0.1` only (the binary itself listens on every interface, so run it
  behind the same restriction); reach it from elsewhere through a private network such as Tailscale or a
  VPN, never by publishing the port to the internet.
- The `Host` header of every request but `/healthz` is checked against IPs,
  `localhost` (and `*.localhost`) and `HUNTDECK_ALLOWED_HOSTS`, so a website you visit can't reach it by DNS
  rebinding; cross-site form posts and API calls are refused.
- Pages are served with a strict Content-Security-Policy (scripts, styles
  and connections from HuntDeck itself only; no third-party sources),
  `frame-ancestors 'none'` and `Referrer-Policy: no-referrer`.
- The image runs as a non-root user; the compose file adds a read-only root
  filesystem, no capabilities and `no-new-privileges`.
- Credentials come from the environment, `*_FILE` secrets or Settings, where
  saved values are encrypted at rest; they're never logged or shown again.
- Nothing is sent anywhere but the platforms you configure, the notification
  channels you add, your HuntMail inbox if you set one up, Cloudflare's API
  (`api.cloudflare.com`) while HuntDeck sets HuntMail up or updates it when
  you ask, and a new install's one download of the public starter seed
  (`HUNTDECK_SEED_URL=off` turns it off).
- Setting HuntMail up takes a Cloudflare API token, used for that run only:
  it's never saved or logged, and you can delete it afterwards. The key and
  private address the setup makes are saved encrypted, like any other
  credential saved in Settings.
- With HuntMail set up, anyone who can reach HuntDeck can read your mail
  through it, as they can read everything else in it. Its key stays on the
  server; mail is shown as text, never as HTML, and attachments only
  download.

So "anyone who can reach the port can use it" is expected behaviour, not a
vulnerability. What is in scope: anything that lets a website, a platform's
data (program names, scope, policy text), a seed or a custom source run
code, read or change your data, or reach HuntDeck despite the checks above;
credentials or private program data leaking into logs, exports, seeds or
a read-only copy (`HUNTDECK_READ_ONLY`, as HuntDeck Public runs); and anything that makes HuntDeck break a
platform's rate limits.
