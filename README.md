# Ballast

**A non-custodial USDC savings autopilot on Stellar.** Your money sits in a vault you own; a published risk model
decides which audited venues may hold it; a policy you sign bounds what a permissionless keeper may do; and the
backend that scores everything holds no key at all.

Live site (static build, refreshed daily from mainnet): **https://emrealt34.github.io/building/**

```
Go backend (read-only, no keys)  →  frozen JSON snapshot  →  Astro static site  →  GitHub Pages
        ↑ Stellar RPC · Horizon · DefiLlama · DeFindex discovery
```

---

## Status: what exists today, stated plainly

| | |
| --- | --- |
| ✅ Working | Venue ingestion (12 Stellar USDC venues, live), deterministic risk scoring, policy-scoped allocation with refusals, keeper decision loop, full read API, static site, evidence page with a measured (not promised) value of routing. |
| ✅ Working | **`UserVault` (Soroban)** — policy enforced on-chain, permissionless rebalance, NAV check, freeze/timelock, 12 tests, wasm built in CI. Not deployed to testnet yet, and the keeper still proposes off-chain only. |
| ✅ Working | **Cross-border carry measurement** (`/borders`): what a dollar actually earned in TR/BR/MX/IN/EU after the currency moved, with volatility and worst drawdown. Real data, published as a measurement, not a product. |
| ⚠️ Partly | Adapter interface is proven against a mock venue contract in tests; the **Blend adapter** and testnet deployment are next. |
| ❌ Not built | Passkey smart account, signed-XDR wallet flow in the UI, third-party audit. The site says so on `/how-it-works`. |

No fake traction: there are no deposits, no TVL, no users. What exists is a working, auditable pipeline and a
measurement of what the product is worth.

---

## Why this is not "highest APY, automated"

The category is crowded (see [`docs/COMPETITORS.md`](docs/COMPETITORS.md)). Ballast is deliberately the
**refusal-first** version of it:

* **A venue that cannot justify its yield is refused, not ranked.** The 10.00% venue in the list is refused today
  because borrower interest and incentives cannot be separated. Five small vaults are refused for exit liquidity.
  Refusals are a first-class API object with a human reason and they render in the UI.
* **The measurement caps the promise.** `/evidence` computes the perfect-hindsight upper bound of routing between
  the verified venues, then how much a realistic keeper captures: **~27 bps/year**, not 300. The honest pitch is
  0% → working capital (~550 bps), not "beat the market".
* **Safety can outrank yield.** Scores are net: `apy − 0.05 × max(0, risk − 50)`.
* **Interest is high in some countries — and their currencies move.** `/borders` publishes what a dollar actually
  earned across a border (Turkey: +37% rate, +16% in dollars after the lira fell; India: positive rate, negative
  return). The page exists to make "chase the highest rate" answerable, not to sell it.
* **Everything is versioned and hash-addressed** (`risk-model-v1.0`, `safety-registry-2026-10-21`, `policy-v1.2`,
  `input_hash` per venue), so a score can be re-derived instead of trusted.
* **The backend is the least powerful component.** `internal/stellar` is the only package that opens a socket, and
  it has no signing path: `grep -rn "func.*Sign\|SubmitTransaction\|sendTransaction" backend` returns nothing.

---

## Architecture

```
Stellar mainnet ───────────────┐
  Soroban RPC  getHealth · getNetwork · getLatestLedger
  Horizon      /fee_stats · /accounts
DefiLlama yields API ──────────┤
DeFindex discovery API ────────┘
                │
                ▼
        backend/  (Go 1.23, stdlib only)
        ├── providers   normalise venues: registry + DefiLlama + DeFindex
        ├── risk        six sub-scores → 0–100, band A–D, deterministic
        ├── policy      eligibility → net-score weighting → caps → redistribution
        ├── keeper      refresh + tick loop, writes decisions (incl. no-ops)
        ├── alpha       what routing was worth, with yesterday's signal only
        └── api         read-only JSON over HTTP
                │
                ├── cmd/api        live server (:8080)
                └── cmd/snapshot   freezes every endpoint into one file
                                   │
                                   ▼
                     frontend/  (Astro 5, `output: 'static'`)
                     └── prerendered pages → GitHub Pages
```

### Two things we deliberately publish against our own interest

1. **Routing is worth ~27 bps.** `/evidence` computes the perfect-hindsight ceiling and how little of it a realistic
   keeper captures. It would be easier to quote the ceiling.
2. **High rates are currency bets.** `/borders` shows that a 37% lira rate became +15.9% in dollars over the last
   year and that India's 5.5% became −1.8%, because the rupee fell more than the rate paid. No product is offered
   there — the page exists so the claim "put money where interest is high" can be judged instead of sold.

### Why the site is static

GitHub Pages serves files, so the site builds against either a live backend (`API_URL=…`) or the frozen snapshot in
`frontend/src/data/snapshot.json`. The snapshot is generated by running the **real handlers** through `httptest`, so
it can never drift from the API schema. A nightly Action re-runs the whole thing against mainnet and commits the new
snapshot; the commit redeploys the site. No server to operate, no secret in the browser, nothing to key-manage.

---

## Run it

Prerequisites: Go ≥ 1.23, Node ≥ 20.

```bash
# live backend
cd backend
go run ./cmd/api                     # :8080, mainnet reads, no keys

# frontend against the live backend
cd ../frontend
npm install
API_URL=http://127.0.0.1:8080 npm run dev        # http://localhost:4321

# or build the static site from the committed snapshot
npm run build && npx serve dist
```

Regenerate the snapshot (needs network):

```bash
cd backend && go run ./cmd/snapshot -out ../frontend/src/data/snapshot.json
```

Tests:

```bash
cd backend && gofmt -l . && go vet ./... && go test ./...
```

Environment (`backend`): `PORT`, `STELLAR_NETWORK` (`mainnet|testnet`), `RPC_URL`, `HORIZON_URL`,
`VAULT_CONTRACT`, `REFRESH_SECONDS`, `TICK_SECONDS`.
Frontend: `API_URL` (build/dev only), `BASE_PATH` (e.g. `/building`), `SITE_URL`.

---

## API

All `GET`, JSON, no auth, read-only by construction.

| Route | Returns |
| --- | --- |
| `/api/v1/health` | liveness, cached venue count, version |
| `/api/v1/meta` | product/chain/asset/custody/automation + model + registry versions |
| `/api/v1/network` | RPC health, latest ledger, protocol version, base fee, Horizon status |
| `/api/v1/venues?deposit=` | every observed venue with risk result, capacity, eligibility |
| `/api/v1/venues/{id}?deposit=` | one venue |
| `/api/v1/venues/{id}/history?days=` | daily APY/TVL series (404 when a venue has no public series) |
| `/api/v1/plan?deposit=` | legs, idle amount, blended APY, portfolio risk, refusals |
| `/api/v1/policy` | the policy + why each value was chosen |
| `/api/v1/decisions` | keeper log, including no-ops |
| `/api/v1/alpha?days=` | measured value of routing: upper bound, realistic keeper, sweep |
| `/api/v1/carry` | cross-border carry: policy rate, currency move, realised USD return, break-even, drawdown |
| `/api/v1/accounts/{address}` | read-only USDC balance for an account |
| `POST /api/v1/intents/{deposit,withdraw}` | unsigned intent for a wallet to build and sign |

---

## The allocation engine

```
eligible(v)  →  net = apy − 0.05 × max(0, risk − 50)
weight       ∝  net
cap          →  max 60% per venue, max 2% of a venue's exit liquidity
redistribute →  water-filling, 6 passes
residual     →  below the $25 minimum order → refusal, not a dust position
idle         →  ≥ 10% of the balance, by policy
```

Policy (`policy-v1.2`): `MaxVenueBps 6000`, `MinIdleBps 1000`, `MaxMoveBps 5000`, `MinImprovementBps 25`,
`CooldownSecs 2d`, `MaxSlippageBps 10`, `MaxVenueUtilizationBps 8500`, `MinCapacityUSD 100k`,
`MaxVenueShareOfCap 2%`, `MinPositionUSD 25`.

---

## Repository layout

```
contracts/
  ballast-vault/    Soroban UserVault: on-chain policy, NAV check, freeze rails (12 tests)
backend/
  cmd/api/          live HTTP server
  cmd/snapshot/     freezes the API into frontend/src/data/snapshot.json
  internal/
    providers/      registry + DefiLlama + DeFindex
    risk/           scoring (tested)
    policy/         eligibility + allocation + keepers' move rules (tested)
    alpha/          counterfactual value of routing
    carry/          cross-border carry: local rate vs currency move, in dollars
    keeper/         refresh + tick loop
    stellar/        the only package that touches the network; read-only
    store/ api/ model/
frontend/
  src/pages/        index, d/[deposit], borders, venues, venues/[id], evidence, how-it-works
  src/components/   BalanceCard, AllocationBar, RefusalList, DecisionLog, VenueTable, RiskBars
  src/data/         snapshot.json (regenerated daily)
docs/
  COMPETITORS.md    who already builds this, and what is left
  INSTAWARDS.md     the $15k application pack
  ARCHITECTURE.md   deeper notes: threat model, invariants, roadmap
```

CI: `.github/workflows/ci.yml` (gofmt, vet, tests, API smoke test, static build assertion),
`contracts.yml` (rustfmt, cargo test, wasm build with a size cap), `pages.yml` (deploy),
`refresh-snapshot.yml` (daily mainnet refresh).

---

## Risks, stated

Contract risk (an audited adapter can still be exploited), stablecoin depeg or issuer freeze, incentive-driven APY
that decays, bridge risk for non-native deposits, and the plain fact that a 6% venue can pay 3% next month. The risk
model prices what is observable and dated; it cannot see a zero-day bug.

**Not investment or legal advice.** No deposits are being accepted; there is no mainnet vault yet.

MIT licensed.
