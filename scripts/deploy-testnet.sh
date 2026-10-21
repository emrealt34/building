#!/usr/bin/env bash
# Deploy Keelson to Stellar testnet and prove the money rails work.
#
# Requires stellar-cli (>= 28) and a wasm built by CI, because the point is to deploy
# the exact bytes the tests ran against:
#   gh run download --name wasm -R emrealt34/keelson
#   ./scripts/deploy-testnet.sh keelson_vault.wasm
#
# Nothing here touches mainnet, and every key it creates is a throwaway testnet key.
set -euo pipefail
WASM="${1:-keelson_vault.wasm}"
NET=testnet
POLICY='{"max_venue_bps":6000,"min_idle_bps":1000,"max_move_bps":5000,"min_improvement_bps":25,"cooldown_secs":172800,"max_slippage_bps":50,"max_venues":4}'

stellar keys generate deployer --network "$NET" --fund 2>/dev/null || true
stellar keys generate issuer   --network "$NET" --fund 2>/dev/null || true
stellar keys generate user     --network "$NET" --fund 2>/dev/null || true
OWNER=$(stellar keys address user)
ISSUER=$(stellar keys address issuer)

# A token we can mint: testnet USDC is Circle's to issue, so an end-to-end run needs ours.
stellar contract asset deploy --asset "KEEL:$ISSUER" --source issuer --network "$NET"
SAC=$(stellar contract id asset --asset "KEEL:$ISSUER" --network "$NET")

VAULT=$(stellar contract deploy --wasm "$WASM" --source deployer --network "$NET" -- \
  --owner "$OWNER" --usdc "$SAC" --policy "$POLICY")
echo "vault: $VAULT"

stellar tx new change-trust --source user --line "KEEL:$ISSUER" --network "$NET"
stellar contract invoke --id "$SAC" --source issuer --network "$NET" -- \
  mint --to "$OWNER" --amount 10000000000
stellar contract invoke --id "$VAULT" --source user --network "$NET" -- \
  deposit --from "$OWNER" --amount 1000000000
stellar contract invoke --id "$VAULT" --source user --network "$NET" -- \
  withdraw --holder "$OWNER" --shares 400000000
stellar contract invoke --id "$VAULT" --source user --network "$NET" -- state

# Expected below: Error(Contract, #13) = UnknownVenue. The refusal is the feature.
stellar contract invoke --id "$VAULT" --source user --network "$NET" --send=yes -- \
  rebalance --plan "[{\"adapter\":\"$SAC\",\"amount\":\"100000000\"}]" || true
