# Who already builds this, and what is actually left

Researched 2026-10-21. The honest answer to "has someone done this idea?" is **yes, several times, and some of them
are live on Stellar mainnet.** Pretending otherwise in an SCF/Instawards application is the fastest way to be
rejected by reviewers who know the ecosystem.

What follows is what exists, what it does not do, and the one seam Ballast is aiming at.

---

## 1. Direct match: Meridian

Stablecoin yield aggregator on Stellar, non-custodial signing, Blend + DeFindex, keeper-based rebalancing.
Multiple public forks exist (`BobbyRichie2/meridian`, `Netty-kun/meridian`, `Ayomide3271/meridian`, …), which tells
you it is a popular reference architecture rather than an obscure one.

* **Live on mainnet** per its README: USDC → `MeridianVault` → `BlendAdapter` → Blend pool, mUSDC shares (SEP-41).
* Non-custodial by construction: API returns unsigned XDR, wallet signs.
* `migrate_adapter` moves the whole vault position atomically behind a timelock.
* Keeper: rate comparison + migration built and tested; on at least one fork the production key is not yet granted
  admin authority, so migration is not actually running.
* Honest self-assessment in its own README: no independent audit yet, single admin key, DeFindex adapter built but
  not wired.

**Overlap with Ballast:** ~85%. Non-custodial vault, adapter pattern, keeper, Blend + DeFindex, risk labels.

## 2. Adjacent, same category

| Project | What it is | Gap |
| --- | --- | --- |
| **Nester** | "Diversified yield strategies… routes across lending pools and AMMs on Stellar, non-custodial vaults" | Marketing site; no public audit trail of decisions |
| **StellarYield** (edehvictor, Amas-01) | AI-assisted aggregator, Freighter + Soroban vault, one-click rebalance | "AI advisor" replaces a reproducible model; no published metric |
| **soroban-yield-vault** (stellar-zklab) | ERC-4626-style vault, Yearn-V3 debt allocator, live strategy allocation table | Single-asset, no cross-protocol risk screen |
| **Stellar-Defi-Vault** (hardcordev) | Share-based non-custodial vault on Soroban | Vault only; no venue discovery or policy |
| **Wirex × Ultra Stellar** (Apr 2026) | Native stablecoin yield "up to 6% APY, full liquidity, no lock-ups" inside 7M-user infrastructure | A distribution giant; they do not need to be non-custodial in the same sense |

## 3. What none of them do well

Read the READMEs closely and a pattern appears. Everyone shows:

* the APY table,
* the deposit flow,
* the "we route to the best" sentence.

Almost nobody publishes:

1. **Which venues were refused, and why.** The most useful output of a risk model is the rejection, and it is the
   first thing every dashboard hides.
2. **What the routing is actually worth.** Not one of the projects above publishes a counterfactual: *how much would
   a keeper have earned versus never moving?* It is cheap to compute and it usually produces an embarrassing number
   (ours: ~27 bps/year between the two verified venues), which is exactly why it is worth publishing.
3. **A model you can re-derive.** Weights are typically private heuristics or an LLM call. Ballast hashes its inputs
   so a score can be checked (`input_hash`), and versions every model change.
4. **The refusal path in the protocol.** Freeze-on-deterioration that blocks new deposits while keeping withdrawals
   open — implemented in the contract, not in a dashboard.

## 4. The seam Ballast is aiming at

> Not another aggregator. A **policy and refusal layer** that any aggregator could sit on top of.

Concretely:

* **Refusal-first UX** — the dashboard's headline block is what was rejected and why.
* **Auditable determinism** — versioned model + registry + policy, `input_hash` per venue, tests for the scoring
  rules. Reviewers and users can recompute the number.
* **Measured claims** — every marketing line has a number behind it in `/evidence`, including the unflattering ones.
* **Tranche 2**: the same policy expressed on-chain in a Soroban vault (`max_venue_bps`, `min_idle_bps`,
  `max_move_bps`, cooldown, NAV check) with instant freeze and a timelocked unfreeze.

That is a defensible difference for a $15k Instaward and a later $150k Build Award: not "we do yield", but
"we are the part of yield that can be audited".

## 5. Risk of the position

* Meridian has a head start on contracts and mainnet. If they ship an audit and a policy layer, the seam narrows.
* Reviewers may reasonably ask "why not contribute the risk layer to Meridian?" — the answer has to be product
  strategy (a neutral layer serves multiple vaults), not pride.
* Stellar's verified-venue set is small (2 venues above the safety floor at the time of writing), so routing alpha is
  small by construction. The product has to earn its place on safety and exit-ability, not on APY.
