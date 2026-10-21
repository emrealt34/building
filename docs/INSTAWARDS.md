# Instawards application pack — Ballast

Prepared 2026-10-21. Everything here is written to be pasted into the SCF/Instawards form; nothing in it claims
something that does not exist in this repository.

---

## 0. What Instawards actually are (so the form is aimed correctly)

* **Instawards up to $15,000 per project**, awarded through **local Stellar Ambassador Chapters**, not through the
  open SCF round. Source: SCF Handbook → *Instawards*; SCF v7 announcement.
* Purpose: **early experimentation and prototyping** that moves a project toward the **Build Award** pipeline.
  It is not a mainnet-launch grant.
* Typical shape: a **30-day, clearly scoped** piece of work with verifiable deliverables (see the Fast-Track pattern
  run by ambassador-led programs: "define a 30-day delivery roadmap, pass proposal evaluation").
* Selection leans on: commitment, collaboration with the local chapter, and **execution readiness** — i.e. does the
  applicant already ship code.
* All awards remain subject to **SDF review and approval**; **KYC/KYB is required** before funds are issued.
* Build Award after Instaward: up to **$150k in XLM**, 10/20/30/40 tranche structure, ≤ 6 months, audit and marketing
  outside the budget.

**Candidate's position (Türkiye):** apply via the Stellar Türkiye / regional Ambassador Chapter; join as a
**Builder** tier member (deployed MVP + working repo qualifies), because Instawards flow through chapters. If the
local chapter is inactive, the fallback is the standard Build Award *Open* track with this same pack.

---

## 1. One-line pitch

> Ballast is a non-custodial USDC savings autopilot on Stellar whose core feature is **refusal**: it shows which
> venues it rejected and why, publishes what routing is really worth, and bounds its own automation with a policy the
> user signs.

## 2. The problem, with a number

Stellar holds roughly **$340M of USDC**, and the yield venues on top of it are few, correlated and unevenly safe.
A saver has three bad options:

1. leave USDC idle at 0%,
2. chase the highest APY and buy undisclosed incentive and liquidity risk,
3. trust a dashboard that only ever shows winners.

Measured on live mainnet data (our own `/evidence` endpoint): moving from idle to a policy-approved allocation is
worth **~550 bps/year**; *switching between the verified venues* is worth only **~27 bps/year**. The value is not
"optimisation". It is getting money working without giving up the ability to leave.

## 3. What is already built (today, in this repo)

* 12 mainnet Stellar USDC venues ingested live (DefiLlama yields + DeFindex discovery + a dated safety registry).
* Deterministic risk model (`risk-model-v1.0`): six sub-scores, bands A–D, `input_hash` per venue, unit tests.
* Policy engine (`policy-v1.2`): per-venue cap, 10% liquid floor, $25 minimum position, utilisation ceiling,
  2-day cooldown, 25 bps improvement threshold.
* Allocation with **water-filling** redistribution and **first-class refusals** with human reasons.
* Read-only keeper loop writing a public decision log, no-ops included.
* Read-only API (11 endpoints) + static Astro site with an evidence page that publishes an unflattering measurement.
* CI: gofmt, vet, tests, API smoke test, static build assertion, GitHub Pages deploy, nightly snapshot refresh.

Nobody has deposited anything. There is no TVL and no user count, and we do not claim any.

## 4. The 30-day scope this award would fund

**Goal: from read-only keeper to a testnet vault that a stranger can use and audit.**

| # | Deliverable | Verifiable by |
| --- | --- | --- |
| 1 | Soroban `UserVault` (per-user, owner-authorised) + `IdleAdapter` | Contract id on testnet, source in repo |
| 2 | On-chain policy check: `max_venue_bps`, `min_idle_bps`, `max_move_bps`, cooldown, NAV before/after | Integration test that fails on a value-losing move |
| 3 | `BlendAdapter`, live on testnet USDC | Deposit → withdraw round trip, hashes published |
| 4 | Permissionless `rebalance(plan)` that reverts on policy or NAV violation | A rejected transaction hash, published |
| 5 | Passkey smart-account deposit/withdraw in the UI (unsigned XDR built client-side) | Working testnet flow + screen recording |
| 6 | Freeze-on-deterioration: blocks new deposits, withdrawals stay open | Test proving withdrawal still works while frozen |
| 7 | Public runbook + threat model page | `docs/` in repo |

Explicitly **out of scope** for 30 days: mainnet, real deposits, audit, marketing.

## 5. Budget: $15,000

| Line | USD | Why |
| --- | --- | --- |
| Contract development (vault, policy module, Blend adapter, tests) | 8,500 | ~3.5 weeks of focused work |
| Testnet deployment + QA + evidence (tx hashes, rejected-move proof) | 2,000 | The artefact reviewers can inspect |
| Passkey smart-account integration (OZ + Stellar Wallets Kit) | 2,500 | The UX that makes "non-custodial" usable |
| Docs, threat model, demo video, chapter demo session | 1,500 | Distribution of the result |
| Contingency | 500 | RPC/provider churn |
| **Total** | **15,000** | |

Deliverable-verified payments preferred; a 3-milestone split (contract / adapter+policy / UX+evidence) is fine.

## 6. Traction and evidence (paste-ready)

* Repository: `https://github.com/emrealt34/building` — MIT, CI green, tests for the risk and policy packages.
* Site: `https://emrealt34.github.io/building/` — static, rebuilt nightly from mainnet by GitHub Actions.
* Live numbers at time of writing: 12 venues observed, 2 eligible, blended **5.67%** at $1,000 with **$100** kept
  liquid, 10 refusals each with a reason.
* Measured routing value: upper bound **+14 bps** over 90 days with perfect hindsight; realistic keeper **+27 bps/yr**,
  capturing 52% of that bound. Published on `/evidence`.
* Chain reads: ledger 65,062,748, protocol 29 — served by `GET /api/v1/network`.

## 7. Why us, why now, why this amount

* The hard, unglamorous half (venue ingestion, risk model, policy engine, refusal UX, CI/CD, honest measurement) is
  **already shipped and public**. The award buys the part that needs a contract and a security story, not a
  prototype that would be thrown away.
* $15k is exactly one tranche of the work above; it converts a read-only keeper into a testnet product. The follow-on
  Build Award ask (Tranche 2) would be the audit + mainnet + passkey distribution.
* The chapter gets a project it can point at: an auditable risk layer, open source, with a published number that
  undercuts its own marketing.

## 8. Questions reviewers will ask, and the answers

**"Meridian already does this."**
Yes — see `docs/COMPETITORS.md`. Meridian is a vault plus a router; Ballast is a policy and refusal layer. The
deliverable here is the on-chain policy module and the audit trail, which is complementary: a neutral policy layer
can serve several vaults. We say this openly rather than pretending the space is empty.

**"Why only 27 bps of alpha?"**
Because with two verified venues, that is the truth. The alternative (claiming 300 bps) requires counting incentive
spikes that a real keeper cannot capture. Our `/evidence` page is built to survive that question.

**"Is it custodial?"**
The backend has no signing path; `grep -rn "Sign\|SubmitTransaction"` returns nothing. Signing happens in the
wallet. The testnet vault will be owner-authorised with a permissionless-but-bounded rebalance.

**"What happens if a venue is hacked?"**
Per-venue cap (60%), max 2% of a venue's exit liquidity, 10% always liquid, and instant freeze on deterioration with
withdrawals left open. Not "no loss" — bounded loss and an exit.

## 9. Compliance checklist before submitting

- [ ] Applicant identity/KYC ready (SDF KYC is mandatory before payment). Individual vs company entity decided.
- [ ] Chapter contacted; application routed through it (or Build Open track if no active chapter).
- [ ] Geographic eligibility checked for Türkiye at submission time.
- [ ] Repo licence (MIT) and public URL confirmed; no secrets in git history.
- [ ] Budget in the form the chapter asks for (USD vs XLM at award date).
- [ ] Deliverable list matches the 30-day scope above, each with an artefact a reviewer can open.
