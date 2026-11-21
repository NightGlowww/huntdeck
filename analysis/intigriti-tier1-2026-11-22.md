# Intigriti Tier1 full analysis - 2026-11-22

Source: the local HuntDeck database (Intigriti API, own token, quick 15m / full 6h).
Universe: 89 open programs with in-scope assets and bounties; 23 carry >=3 Tier 1 assets.
Tier 1 is the platform's top payout tier - the mark that says these assets are expected to yield high severity.

## Full table (sorted by Tier1 count)

| # | program | Tier1 | assets | web | top bounty | score | family |
|---|---|---|---|---|---|---|---|
| 1 | **Watsons** | 72 | 123 | 93 | $8500 | 32.3 | A.S. Watson group |
| 2 | **Marionnaud** | 24 | 77 | 61 | $8500 | 35.6 | A.S. Watson group |
| 3 | **Axel Springer National Media &** | 19 | 41 | 40 | $2500 | 15.9 | standalone |
| 4 | **Nexo** | 16 | 18 | 18 | $4800 | 36.4 | standalone |
| 5 | **Intergamma** | 15 | 33 | 25 | $5500 | 24.7 | standalone |
| 6 | **ICI PARIS XL** | 14 | 37 | 35 | $8500 | 32.2 | A.S. Watson group |
| 7 | **Lightspark Public Bug Bounty** | 14 | 22 | 13 | $5000 | 20.5 | standalone |
| 8 | **Kruidvat** | 13 | 50 | 46 | $8500 | 34.6 | A.S. Watson group |
| 9 | **Superdrug** | 12 | 28 | 24 | $8500 | 32.1 | A.S. Watson group |
| 10 | **UZ Leuven** | 11 | 34 | 33 | $5000 | 31.2 | standalone |
| 11 | **Adobe Public** | 9 | 42 | 20 | $15000 | 35.5 | standalone |
| 12 | **Bank J.Van Breda & C°** | 9 | 14 | 10 | $10000 | 40.0 | Belgian banking |
| 13 | **Veriff Bug Bounty** | 8 | 13 | 11 | $4000 | 30.1 | standalone |
| 14 | **Too Good To Go - Marketplace** | 7 | 7 | 4 | $3500 | 30.7 | standalone |
| 15 | **The Perfume Shop** | 6 | 10 | 8 | $8500 | 33.2 | standalone |
| 16 | **Port of Antwerp-Bruges VIP** | 5 | 7 | 7 | $5000 | 34.2 | standalone |
| 17 | **Dropbox Bug Bounty** | 4 | 26 | 19 | $10000 | 28.8 | standalone |
| 18 | **RIPE NCC** | 4 | 10 | 3 | $2000 | 11.4 | standalone |
| 19 | **Rivian Bug Bounty** | 4 | 9 | 7 | $5000 | 25.8 | standalone |
| 20 | **Social Deal** | 4 | 8 | 6 | $1000 | 21.5 | standalone |
| 21 | **BMW Group** | 3 | 7 | 6 | $6000 | 23.6 | standalone |
| 22 | **Mobile Vikings** | 3 | 18 | 18 | $5000 | 27.8 | standalone |
| 23 | **Ninja Kiwi Games Bug Bounty pr** | 3 | 15 | 15 | $4125 | 41.0 | standalone |

## Corporate families (research once, reuse across)


## Tier1 details, top programs

### Watsons (Tier1 72 / 123 assets / top $8500)

Pattern: every Tier1 is `api.<country-domain>` - one e-commerce API deployed per country. A finding on one replica validates on 12+ more, each billed as its own asset. Start with a quiet region (lookatme.com.ph, drogas.lt).

```
domain    api.drogas.lt
domain    api.drogas.lv
domain    api.fortress.com.hk
domain    api.lookatme.com.ph
domain    api.pns.hk
domain    api.watsons.co.id
domain    api.watsons.co.th
domain    api.watsons.com.hk
domain    api.watsons.com.my
domain    api.watsons.com.ph
domain    api.watsons.com.sg
domain    api.watsons.com.tr
domain    api.watsons.com.tw
domain    api.watsons.vn
domain    app.drogas.lt
domain    app.drogas.lv
... and 56 more of the same pattern
```

### Marionnaud (Tier1 24 / 77 assets / top $8500)

Pattern: same family as Watsons - `api.marionnaud.{at,ch,fr,it}` paired with `app.marionnaud.{...}`. The authorization model studied on Watsons applies here.

```
domain    api.marionnaud.at
domain    api.marionnaud.ch
domain    api.marionnaud.fr
domain    api.marionnaud.it
domain    app.marionnaud.at
domain    app.marionnaud.ch
domain    app.marionnaud.fr
domain    app.marionnaud.it
android   Marionnaud Austria Android
ios       Marionnaud Austria iOS
android   Marionnaud France Android
ios       Marionnaud France iOS
... and 12 more
```

### Axel Springer National Media & (Tier1 19 / 41 assets / top $2500)

```
wildcard  *.asadcdn.com
wildcard  *.auth.bild.de
wildcard  *.bild.tv
wildcard  *.computerbild.de
wildcard  *.hey.bild.de
wildcard  *.sportbild.de
ip        18.184.198.198, 18.185.214.59, 18.194.109.179, 3.121.117.72, 3.1
domain    adtechnology.axelspringer.com
domain    bild.de
domain    cancellation.prod.ps.welt.de
```

### Nexo (Tier1 16 / 18 assets / top $4800)

Pattern: every money-movement endpoint is marked Tier1. Classic plays: the quote-to-order race, amount and currency tampering, object-level access on withdrawal ids, deposit parameter tampering.

```
url       https://platform.nexo.com/api/1/create_recurring_payment
url       https://platform.nexo.com/api/1/exchange_order
url       https://platform.nexo.com/api/1/exchange_request_quote
url       https://platform.nexo.com/api/1/get_pay_to_card_fee
url       https://platform.nexo.com/api/1/request_bwc_quote
url       https://platform.nexo.com/api/1/request_capture_payment
url       https://platform.nexo.com/api/1/request_crypto_withdrawal
url       https://platform.nexo.com/api/1/request_pay_to_card
url       https://platform.nexo.com/api/1/term/deposits/user/create
url       https://platform.nexo.com/api/platform/exchange/v1/booster/quote
url       https://platform.nexo.com/api/platform/exchange/v2/booster/order
url       https://platform.nexo.com/api/platform/exchange/v2/exchange/orde
url       https://platform.nexo.com/api/trading/advanced/facade/api/v1/dep
url       https://platform.nexo.com/api/trading/advanced/facade/api/v1/fee
url       https://platform.nexo.com/api/trading/advanced/facade/api/v1/ord
url       https://platform.nexo.com/api/trading/advanced/facade/api/v1/wit
```

### Intergamma (Tier1 15 / 33 assets / top $5500)

```
ios       949829216
ios       950680989
ios       950693949
android   be.gamma.app.android
wildcard  kassa.gamma.be/*
wildcard  kassa.gamma.nl/*
wildcard  kassa.karwei.nl/*
wildcard  mijn.gamma.be/*
wildcard  mijn.gamma.nl/*
wildcard  mijn.karwei.nl/*
```

### ICI PARIS XL (Tier1 14 / 37 assets / top $8500)

```
domain    api.iciparisxl.be
domain    api.iciparisxl.lu
domain    api.iciparisxl.nl
domain    app.iciparisxl.be
domain    app.iciparisxl.lu
domain    app.iciparisxl.nl
android   ICI Paris XL Android
ios       ICI Paris XL iOS
domain    media.iciparisxl.be
domain    media.iciparisxl.lu
```

## How to play it

1. **Watsons, main effort**: one-finding-many-assets structure + its failed scan targets were already released. Study one quiet region, then verify horizontally.
2. **Nexo, in parallel**: pure API, hand-test all 16 endpoints for races, tampering, object access.
3. **A.S. Watson horizontal**: the Watsons authorization model maps onto Marionnaud / Kruidvat / Superdrug / ICI PARIS XL.
4. **Scanner**: Watsons (123 assets) and Marionnaud (77) queued once node memory frees; Watsons's old targets are already scan-cleared.
5. **Skip**: Adobe (strict), Axel Springer ($2.5k), Capture Our Flag (CTF event).

---
Generated 2026-11-22 - HuntDeck /api/v1/programs, full query - 23 of 89 programs selected
