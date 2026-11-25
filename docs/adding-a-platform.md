# Adding a platform in Go

A built-in platform client polls a platform directly, and can bring
everything HuntDeck does with the big four: invites, program stats,
disclosed reports, logins with an authenticator code.

For programs only (no stats), a [custom source](custom-sources.md) is
quicker: a script of yours writes a JSON document, with no Go and no
rebuild. Many custom sources can graduate to a client later; keep the
source's name and the stored programs carry over.

The ground rules (the rest are in [AGENTS.md](../AGENTS.md)):

- platform code never touches the database;
- all HTTP goes through `internal/httpclient`;
- removals happen only from a complete listing;
- fixtures are sanitised.

Use `internal/platform/hackerone` as the reference, and
`internal/platform/custom` as the smallest complete one.

## 1. The package

Create `internal/platform/<name>/`. Its package comment says how it reaches
the platform (official API or the website), its request budget per poll,
and its rate limits.

Implement `platform.Platform` (`internal/platform/platform.go`):

```go
// Name is the platform's stored name: lowercase, stable forever.
func (c *Client) Name() string { return Name }

// ListPrograms returns every program the account can see, with
// Complete only when every page succeeded. A failed page returns what
// was read, Complete false, and the error: nothing is then removed.
func (c *Client) ListPrograms(ctx context.Context) (platform.Listing, error)

// FetchScope returns one program's whole scope; an error means
// "unknown", never "empty".
func (c *Client) FetchScope(ctx context.Context, p platform.Program) ([]platform.ScopeEntry, error)

// Stats reports the HTTP client's totals for the poll's record.
func (c *Client) Stats() (requests, rateLimited int64) { return c.http.Stats() }
```

Map the platform's asset types to the normalised ones in `internal/scope`
(`scope.AssetWildcard`, `scope.AssetURL`, …), and keep the platform's own
in `PlatformAssetType`. Give each scope entry the platform's ID where it has
one, so edits stay edits.

## 2. Optional interfaces

Each adds a feature; the syncer and server find them by type assertion.

| Interface | Gives |
|---|---|
| `ScopeDetailFetcher` | Rules and bounty table with the scope download. |
| `ScopeChangeChecker` | A cheap "did the scope change?", so full polls skip unchanged ones. |
| `StatsFetcher` | Program stats: payouts, reports, response times (Stats page, HuntScore). |
| `InviteLister`, `InviteAccepter` | Pending invitations, and accepting them from HuntDeck. |
| `DisclosureFetcher`, `ResolutionFetcher` | Disclosed and resolved reports. |
| `CredentialReporter` | Whether each credential works, under Settings › Accounts. |
| `KeepAliver` | Keeps an idle login session alive between polls. |
| `ReportFetcher` | The user's own reports (Reports, Analytics, report notifications). |
| `WebSyncer` | A separate logged-in browser sync for data the API lacks. |

## 3. Wiring it in

| Where | What |
|---|---|
| `internal/config` | Its environment variables (credentials as `Secret`), intervals and rate; `readOnlyConflicts` refuses them on a read-only copy. |
| `.env.example`, `docs/configuration.md`, `docs/platforms.md`, README | The variables in `.env.example` and the configuration table; how the client reads the platform and any terms-of-service caveats in `docs/platforms.md`; the platform in the README's Platforms table. |
| `cmd/huntdeck/platforms.go` | Build it in `buildPlatforms`. Add a short alias to `platformAliases` and its place in `heldPlatforms`' order. |
| `internal/web/display.go` | `platformLabel` ("HackerOne") and `platformShort` ("H1"); `canonicalPlatform` in `pages.go` for the alias. |
| `internal/web/static/css/00-theme.css`, `07-stats.css` | A `--pf-<name>` colour in each palette, light and dark, and its `.pf-<name>` class. |
| `internal/web/openapi.json` | The name in the `platform` enums of the path parameters. |

## 4. Tests

- **Client tests:** against `httptest` servers replaying sanitised fixtures
  in `internal/platform/<name>/testdata/` (fake handles, `example.com`
  domains). Cover pagination, 429 with `Retry-After`, 5xx, refused
  credentials, and a failed page leaving the listing incomplete.
- **Live tests:** opt-in, behind `HUNTDECK_LIVE_TESTS=1`, never in `make check`.

Run `make check`, and `make docker` for the container.
