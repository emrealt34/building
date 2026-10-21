//! Ballast `BlendAdapter` — the piece that turns "the vault holds dollars" into
//! "the vault is lending dollars on Blend".
//!
//! Design rules:
//!
//! * **The vault is the only counterparty.** `deposit` is called by the vault *after* it
//!   has transferred USDC here; `withdraw` always sends the proceeds back to the vault
//!   address fixed at construction. A stranger calling either function can therefore only
//!   push this contract's own idle balance into the pool, or send it home to the vault.
//!   There is no path that pays a third party.
//! * **No collateral, no borrowing.** The adapter only ever files a `Supply` request, so
//!   a fall in the pool cannot cascade into a liquidation. The worst case is a yield that
//!   stops, not a position that gets called.
//! * **`value()` is read from the pool, never cached.** The vault checks its net asset
//!   value before and after every rebalance; a cached number would let a bad move pass.
#![no_std]

use blend_contract_sdk::pool::{self, Request};
use soroban_sdk::{contract, contracterror, contractimpl, contracttype, token, Address, Env, Vec};

/// Blend's request kinds. Supply and withdraw are the only two this adapter uses.
const REQUEST_SUPPLY: u32 = 0;
const REQUEST_WITHDRAW: u32 = 1;
/// Blend tracks b-tokens and their exchange rate with 12 decimals.
const SCALAR_12: i128 = 1_000_000_000_000;
/// How long the allowance handed to the pool survives, in ledgers (~5s each).
const ALLOWANCE_LEDGERS: u32 = 100;

#[contracttype]
pub enum DataKey {
    Vault,
    Usdc,
    Pool,
    ReserveId,
}

#[contracterror]
#[derive(Copy, Clone, Debug, Eq, PartialEq)]
#[repr(u32)]
pub enum Error {
    NotInitialized = 1,
    InvalidAmount = 2,
    NothingToWithdraw = 3,
}

#[contract]
pub struct BlendAdapter;

#[contractimpl]
impl BlendAdapter {
    pub fn __constructor(env: Env, vault: Address, usdc: Address, pool: Address, reserve_id: u32) {
        let store = env.storage().instance();
        store.set(&DataKey::Vault, &vault);
        store.set(&DataKey::Usdc, &usdc);
        store.set(&DataKey::Pool, &pool);
        store.set(&DataKey::ReserveId, &reserve_id);
    }

    /// Lend `amount` USDC that the vault has already sent to this contract.
    /// Returns the amount supplied.
    pub fn deposit(env: Env, amount: i128) -> Result<i128, Error> {
        if amount <= 0 {
            return Err(Error::InvalidAmount);
        }
        let me = env.current_contract_address();
        let usdc = Self::usdc(&env);
        let pool_addr = Self::pool(&env);

        let token = token::Client::new(&env, &usdc);
        // Never lend money we do not have: the vault may have called us after a partial
        // transfer, and the pool would fail with a much less clear error.
        if token.balance(&me) < amount {
            return Err(Error::InvalidAmount);
        }
        token.approve(
            &me,
            &pool_addr,
            &amount,
            &(env.ledger().sequence() + ALLOWANCE_LEDGERS),
        );

        let requests = Vec::from_array(
            &env,
            [Request {
                address: usdc.clone(),
                amount,
                request_type: REQUEST_SUPPLY,
            }],
        );
        pool::Client::new(&env, &pool_addr).submit(&me, &me, &me, &requests);
        Ok(amount)
    }

    /// Pull `amount` USDC out of the pool and send it to the vault.
    /// Returns what actually came out, which is what the vault must account for.
    pub fn withdraw(env: Env, amount: i128) -> Result<i128, Error> {
        if amount <= 0 {
            return Err(Error::InvalidAmount);
        }
        let me = env.current_contract_address();
        let usdc = Self::usdc(&env);
        let pool_addr = Self::pool(&env);
        let token = token::Client::new(&env, &usdc);

        let before = token.balance(&me);
        let requests = Vec::from_array(
            &env,
            [Request {
                address: usdc.clone(),
                amount,
                request_type: REQUEST_WITHDRAW,
            }],
        );
        pool::Client::new(&env, &pool_addr).submit(&me, &me, &me, &requests);

        // The pool pays this contract; trust the balance, not the request.
        let received = token.balance(&me) - before;
        if received <= 0 {
            return Ok(0);
        }
        token.transfer(&me, &Self::vault(&env), &received);
        Ok(received)
    }

    /// What the position is worth right now, in USDC, straight from the pool.
    pub fn value(env: Env) -> i128 {
        let me = env.current_contract_address();
        let usdc = Self::usdc(&env);
        let pool_client = pool::Client::new(&env, &Self::pool(&env));

        let positions = pool_client.get_positions(&me);
        let b_tokens = positions
            .supply
            .get(Self::reserve_id(&env))
            .unwrap_or(0);
        if b_tokens <= 0 {
            return 0;
        }
        let reserve = pool_client.get_reserve(&usdc);
        b_tokens * reserve.data.b_rate / SCALAR_12
    }

    // ------------------------------------------------------------- read-only surface

    pub fn vault(env: Env) -> Address {
        env.storage()
            .instance()
            .get(&DataKey::Vault)
            .expect("vault not set")
    }

    pub fn usdc(env: Env) -> Address {
        env.storage()
            .instance()
            .get(&DataKey::Usdc)
            .expect("usdc not set")
    }

    pub fn pool(env: Env) -> Address {
        env.storage()
            .instance()
            .get(&DataKey::Pool)
            .expect("pool not set")
    }

    pub fn reserve_id(env: Env) -> u32 {
        env.storage()
            .instance()
            .get(&DataKey::ReserveId)
            .expect("reserve id not set")
    }
}

#[cfg(test)]
mod test;
