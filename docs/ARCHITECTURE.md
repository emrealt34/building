# Architecture notes

Short version lives in the README. This file holds the parts a reviewer or an auditor needs: what is trusted, what can
go wrong, what the contract must guarantee, and what is deliberately deferred.

---

## 1. Trust boundaries

| Component | Can it move money? | If it is compromised |
| --- | --- | --- |
| Astro site | No | Someone changes a number on a page. Nobody's funds move. |
| Go backend | No — no signing path exists | A bad *proposal*. The vault rejects it on policy or NAV. |
| Keeper bot | Only inside the signed policy | A bad move within the caps, and only if NAV does not fall. |
| Risk registry (curated) | No | A venue is scored wrong. Mitigated by versioning + `input_hash`, not by trust. |
| `UserVault` contract | Yes, for one user | The one real target. Hence immutable-by-default, 2-of-3 registry, timelock. |

The design rule: *the component that touches the network is the dumbest one*. `backend/internal/stellar` performs
`getHealth`, `getNetwork`, Horizon `/fee_stats` and `/accounts`. There is no `Sign`, no `SubmitTransaction`, no secret
in the repo — that is checkable with one `grep`, which is the point.

## 2. Determinism and versioning

Three independent versions ship with every number:

* `risk-model-v1.0` — six sub-scores (`contract`, `liquidity`, `oracle`, `durability`, `contagion`, `governance`) with
  fixed weights `0.25 / 0.20 / 0.15 / 0.15 / 0.15 / 0.10`, plus `input_hash` over the sorted inputs.
* `safety-registry-2026-10-21` — curated audits, incidents, oracle facts. Dated, because "audited" decays.
* `policy-v1.2` — the allocation and move rules.

A score that changes without a version bump is a bug, and the API makes that visible (`risk.version`, `risk.input_hash`).

## 3. The allocation engine, precisely

```
net(v)      = apy(v) − 0.05 × max(0, risk(v) − 50)
eligible(v) = yield_source_verified ∧ capacity ≥ 100k ∧ utilisation ≤ 85%
              ∧ stability ≥ 1 day ∧ capacity ≥ min_position / max_venue_share
weights     ∝  net
cap         =  min(60% of deposit, 2% of venue exit liquidity, max_position)
redistribute until no weight exceeds its cap (6 passes), then
residual    <  $25  →  refusal ("below minimum order size"), idle stays in the vault
floor       idle ≥ 10% of deposit
```

Refusals are returned, not dropped: the API's `plan.refusals` is the same length as the venue list minus the legs.

## 4. Invariants the Tranche 2 contract must hold

1. No arbitrary transfer entry point. The vault can only call registered adapters; adapters return funds only to the
   vault.
2. `rebalance(plan)` is permissionless, but rejects any plan that breaks `max_venue_bps`, `min_idle_bps`,
   `max_move_bps`, the cooldown, or a NAV decrease beyond 10 bps.
3. Owner withdrawal always works, including while a venue is frozen; illiquid parts surface as `pending`, never as a
   silent failure.
4. Keeper bounties are paid by the app treasury, never from principal.
5. Vaults are immutable by default; upgrades require a new version and per-user opt-in migration.
6. Freezing is instant; unfreezing is timelocked.

These are the six items rendered on `/how-it-works`; the page and the contract must never disagree.

## 5. Threat model (condensed)

| # | Threat | Mitigation | Residual |
| --- | --- | --- | --- |
| T1 | Adapter exploit | Caps, 10% liquid, freeze, audits | Loss within the cap |
| T2 | Stablecoin depeg/freeze | Same-asset only (USDC), issuer risk disclosed | Real, unpriced |
| T3 | Oracle/bad debt | `oracle` sub-score, registry incidents | Zero-day |
| T4 | Unsustainable APY | durability score, incentive-dependence penalty, refusal on unverifiable yield | Misclassification |
| T5 | Keeper compromise | Policy + NAV bound on-chain | One bad bounded move |
| T6 | Registry capture | 2-of-3, timelock, versioned, public diffs | Collusion |
| T7 | Liquidity illusion | capacity = withdrawable share, not TVL | Fast-moving runs |
| T8 | Provider outage | Multi-source ingestion, last-good snapshot, honest staleness | Stale scores |
| T9 | Data/schema drift | Snapshot generated from the real handlers, asserted in CI | Low |
| T10 | MEV/sandwich on moves | Soroban ordering, max move size, no flash liquidity | Low |
| T11 | Regulatory (custody) | No custody, no key, no fiat on-ramp of our own | Jurisdiction-dependent |
| T12 | Key loss (user) | Passkey smart account, recovery via guardian | User error |

## 6. Roadmap

* **Tranche 1 (done, this repo):** ingestion, risk model, policy engine, refusals, read-only keeper, API, static site,
  evidence, CI/CD.
* **Tranche 2 (the Instaward ask):** Soroban `UserVault` + `BlendAdapter` on testnet, on-chain policy module, passkey
  flow, rejected-move evidence, runbook.
* **Tranche 3:** audit, mainnet with a staged TVL cap (10k → 50k → 250k), DeFindex adapter, second keeper operator.

## 7. Deliberate non-goals

* Not a yield maximiser; the measured ceiling (~27 bps) is published to keep the promise small.
* No token, no points programme, no referral incentives.
* No cross-chain bridging we operate ourselves (CCTP only, where the issuer is the custodian).
* No fiat custody, no KYC as a product feature — an anchor does that, under its own licence.
