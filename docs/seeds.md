# Seeds

A new install would have to backfill months of history itself: when each
program and scope entry first appeared, the change log, stats trends,
disclosed and resolved reports. A seed skips that wait. It holds every **public**
program's platform data from another install, and nothing of that user's: no
programs that are or ever were private (or their scope), no programs added
by hand unless shared (see Independent programs), no logins, settings of yours, tags,
ignores, notifications, poll history, own stats or scores, reports, HuntMail
addresses or mail, and no hunters' handles.

**By default a new install loads the public starter seed by itself**: on its
first start, with an empty database, it downloads
`https://github.com/mdpsec/huntdeck/releases/download/seed/seed.db.gz`
(refreshed daily, about 35 MB), checks it against the SHA-256 published
beside it and loads it, then polls for itself as usual. It does that once:
from then on the install keeps its own data and never asks again. If the
download fails it starts empty and fills up from its polls. Set
`HUNTDECK_SEED_URL=off` to start empty, or point it at another seed's https
URL (its SHA-256 at the same URL plus `.sha256`).

To load a seed of your own instead, put it in the data directory as
`seed.db.gz` (or `seed.db`) before the first start; it's used in place of
the download:

```sh
docker compose create
docker compose cp seed.db.gz huntdeck:/data/seed.db.gz
docker compose up -d
```

(With `HUNTDECK_DATA_PATH`, copy it into that directory instead.) HuntDeck
loads it once, into an empty database only, renames it to
`seed.db.gz.loaded`, and its polls carry on from there: scope changes since
the seed are listed as changes; your private invites are added quietly, like
a first import. Or run `huntdeck seed import FILE` on a new install. A seed
from a newer HuntDeck is refused; one from an older one is brought up to
date as it loads.

`huntdeck seed export [--signed-out] FILE.db.gz` writes one from your own install (about
35 MB gzipped for ~1,400 programs). `--signed-out` leaves out programs a
platform shows only to signed-in accounts too, for a seed other people
will see.
