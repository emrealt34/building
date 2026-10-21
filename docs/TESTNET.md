# Testnet deployment — what is actually live, and how to check it

Deployed 2026-10-21 by `scripts/deploy-testnet.sh`. Every hash below can be opened in
[stellar.expert](https://stellar.expert/explorer/testnet) or
[lab.stellar.org](https://lab.stellar.org/r/testnet). Nothing here holds real money.

## Contracts

| What | Address |
| --- | --- |
| `keelson-vault` (against testnet USDC) | `CDRMO2YM2KORLKINFSEUK6W3EKGFUUAS2FWEGVSPF73LJQGT5I26KMCU` |
| `keelson-vault` (against a token we can mint) | `CDXAAVRFWPUNJRTTGBK66LLG2CZZYNHVCQ7P5AZZSQDTEMLP7M5DVTY6` |
| testnet USDC (SAC, Circle's issuer) | `CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA` |
| `KEEL` test token (we issue it, so we can mint) | `CCYMEOA5HV3QSLWJIEL3V32NTGVLDNGOAGUVKHXR3LUL5G5SXTXOX3BL` |

Two vaults on purpose: testnet USDC can only be minted by Circle, so the end-to-end
deposit/withdraw run uses a token we control. The USDC vault is deployed and running
with the same code, and is the one a real testnet deposit would use.

## Transactions

| Step | What it proves | Hash |
| --- | --- | --- |
| deploy vault | the wasm CI built is the wasm on chain | `d18af20c8e67f54d9fbf0a26d42bc754369ee79b38b773f1a3d40533a3d9233c` |
| mint 1000 KEEL | the test asset exists and is ours | see account history of `GD6NB7DZ2K7R7CAYB2S2QC5JDK56IUNQ7RD2ZNIUNQNFP62SB7HPMZER` |
| deposit 100 | shares are issued 1:1 on the first deposit | `02218da803ba7e389a2da1018dda1f239b7288d8cc67cb82db4472f21121673a` |
| withdraw 40 | the owner can always take money out | `b21cb35b6d39df5cd160fc5cec6ed90d79aa8f7483d73c57987b78fd45019cd0` |
| rebalance to an unregistered venue | **refused** with `Error(Contract, #13)` = `UnknownVenue` | simulation reverted, no transaction |

After the run: `total_assets = total_shares = 1600000000` (160 units of 7 decimals) —
deposit 200, withdraw 40, and the accounting still reconciles exactly.

## What is *not* on testnet yet

* The Blend adapter is deployed-able but not wired: it needs a live Blend testnet pool
  and a reserve to point at, which is the remaining half of the adapter work.
* No passkey smart account yet. The vault owner on testnet is a plain Stellar key.
* No keeper: these calls were made by hand, which is honest — automation is bounded by
  policy, and the policy is what the refusal above demonstrates.
