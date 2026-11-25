# HuntMail bundle

HuntMail's code, which HuntDeck deploys to the user's Cloudflare account
from Email › HuntMail › Setup. Copy it in from a HuntMail
release (its `bundle` script writes `dist/huntmail-bundle/`); never edit it
by hand. `manifest.json` lists every file with its SHA-256, and HuntDeck
refuses a bundle that doesn't match.

    manifest.json        name, version, modules, compatibility date and flags,
                         crons, the D1 binding, default vars, secrets, migrations
    index.js …           the Worker's modules, as wrangler would upload them
    migrations/*.sql     the database's migrations, applied in name order
    LICENSE, NOTICES     HuntMail's licence and its bundled dependencies'

Without `manifest.json`, HuntDeck builds without it, and Settings says how to
set HuntMail up on its own instead.
