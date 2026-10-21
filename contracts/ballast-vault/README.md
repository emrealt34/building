# ballast-vault (Soroban)

The `UserVault`: the only Ballast component that can move money, and the smallest one.

## Adapter interface

Any contract the owner registers must expose three functions. That is the entire
integration surface, which is why an adapter can be replaced without touching user funds:

```rust
fn deposit(amount: i128);          // vault has already transferred USDC to the adapter
fn withdraw(amount: i128) -> i128; // adapter sends USDC back to the vault, returns the amount sent
fn value() -> i128;                // adapter's own valuation of the position
```

## What the contract refuses

| Check | Error | Why |
| --- | --- | --- |
| plan exceeds `max_venue_bps` in one venue | `VenueCap` | one venue cannot take the whole balance |
| plan leaves less than `min_idle_bps` liquid | `IdleFloor` | "withdraw any time" must be true |
| plan moves more than `max_move_bps` | `MoveCap` | a bounded move, even if the keeper is wrong |
| two rebalances inside `cooldown_secs` | `Cooldown` | churn costs more than it earns |
| NAV after < NAV before − `max_slippage_bps` | `Slippage` | a value-destroying move reverts atomically |
| a frozen venue receives new money | `Frozen` | freeze stops inflow, not outflow |
| any rebalance while the vault is frozen | `Frozen` | instant stop |
| `unfreeze` before the 48h timelock | `UnfreezeTooEarly` | a stolen owner key cannot re-arm automation |

`rebalance` is **permissionless**: no signature is required to propose one. The policy above
is the only thing standing between a keeper and the user's money, which is exactly why it
lives on-chain rather than in the keeper's configuration file.

## Tests

`cargo test` runs the promises as tests: deposit/withdraw round trip, value conservation,
venue cap, liquid floor, cooldown, NAV slippage rejection, freeze-vs-withdraw, delayed
unfreeze, frozen venue, unknown venue, share price growth, pause.

## Not built yet

The Blend adapter (Tranche 3) and the passkey smart account. The venue used in tests is a
mock that implements the interface above, so the vault's checks are exercised against a real
contract call, not a mocked function.
