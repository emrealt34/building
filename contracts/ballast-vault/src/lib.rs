//! Ballast `UserVault`.
//!
//! The vault is the only component in Ballast that can move money, so it is also the
//! smallest and the most constrained. Its whole job is to refuse things:
//!
//! * a rebalance is **permissionless** — anyone may propose one — but it is rejected if
//!   it breaks the policy the owner signed (per-venue cap, liquid floor, move cap,
//!   cooldown) or if it loses value (NAV before vs NAV after);
//! * a venue can be frozen instantly by the owner; new money stops going there while
//!   withdrawals from it stay open;
//! * the owner can always withdraw. There is no queue and no permission from us.
//!
//! The keeper that proposes these plans holds no key. If it is compromised the worst it
//! can do is propose a bad plan, and the checks below reject it.
#![no_std]

use soroban_sdk::{
    contract, contracterror, contractimpl, contracttype, symbol_short, token, Address, Env, IntoVal,
    Symbol, Val, Vec,
};

const BPS: i128 = 10_000;
/// Unfreezing is deliberately slow: freezing protects the user, unfreezing protects
/// them from a stolen owner key.
const UNFREEZE_DELAY_SECS: u64 = 48 * 60 * 60;

/// What the owner signs once. Every field is enforced on-chain in `rebalance`.
#[contracttype]
#[derive(Clone, Debug, PartialEq)]
pub struct Policy {
    /// Max share of the vault that may sit in a single venue, in bps.
    pub max_venue_bps: u32,
    /// Minimum share that must stay liquid in the vault, in bps.
    pub min_idle_bps: u32,
    /// Max share of the vault a single rebalance may move, in bps.
    pub max_move_bps: u32,
    /// Minimum rate improvement the keeper should require before proposing, in bps.
    /// Advisory on-chain (the contract cannot see rates); recorded for auditability.
    pub min_improvement_bps: u32,
    /// Seconds that must pass between two rebalances.
    pub cooldown_secs: u64,
    /// How much value a rebalance may lose before it is rejected, in bps.
    pub max_slippage_bps: u32,
    /// Hard cap on how many venues one plan may touch.
    pub max_venues: u32,
}

#[contracttype]
#[derive(Clone, Debug)]
pub struct Allocation {
    pub adapter: Address,
    pub amount: i128,
}

#[contracttype]
#[derive(Clone, Debug)]
pub struct Position {
    pub adapter: Address,
    pub value: i128,
}

#[contracttype]
#[derive(Clone, Debug)]
pub struct VaultState {
    pub idle: i128,
    pub total_assets: i128,
    pub total_shares: i128,
    pub policy: Policy,
    pub frozen: bool,
    pub paused: bool,
    pub last_rebalance: u64,
    pub venues: u32,
}

#[contracterror]
#[derive(Copy, Clone, Debug, Eq, PartialEq)]
#[repr(u32)]
pub enum Error {
    NotInitialized = 1,
    Unauthorized = 2,
    Frozen = 3,
    Paused = 4,
    Cooldown = 5,
    VenueCap = 6,
    IdleFloor = 7,
    MoveCap = 8,
    Slippage = 9,
    InsufficientShares = 10,
    InvalidAmount = 11,
    TooManyVenues = 12,
    UnknownVenue = 13,
    UnfreezeTooEarly = 14,
    InvalidPolicy = 15,
}

#[contracttype]
pub enum DataKey {
    Owner,
    Usdc,
    Policy,
    TotalShares,
    Shares(Address),
    AdapterList,
    FrozenVenue(Address),
    LastRebalance,
    Frozen,
    Paused,
    FrozenAt,
}

#[contract]
pub struct UserVault;

#[contractimpl]
impl UserVault {
    pub fn __constructor(env: Env, owner: Address, usdc: Address, policy: Policy) {
        validate_policy(&policy);
        let store = env.storage().instance();
        store.set(&DataKey::Owner, &owner);
        store.set(&DataKey::Usdc, &usdc);
        store.set(&DataKey::Policy, &policy);
        store.set(&DataKey::TotalShares, &0i128);
        store.set(&DataKey::AdapterList, &Vec::<Address>::new(&env));
        store.set(&DataKey::Frozen, &false);
        store.set(&DataKey::Paused, &false);
        store.set(&DataKey::LastRebalance, &0u64);
    }

    // ---------------------------------------------------------------- money in/out

    /// Deposit USDC, receive shares. Shares are priced on total assets, so a deposit
    /// after a yield accrual buys fewer shares and a withdrawal returns the gains.
    pub fn deposit(env: Env, from: Address, amount: i128) -> Result<i128, Error> {
        from.require_auth();
        if amount <= 0 {
            return Err(Error::InvalidAmount);
        }
        if Self::paused(&env) {
            return Err(Error::Paused);
        }

        let total_assets = Self::total_assets(&env);
        let total_shares = Self::total_shares(&env);
        let shares = if total_shares == 0 || total_assets == 0 {
            amount
        } else {
            amount * total_shares / total_assets
        };
        if shares <= 0 {
            return Err(Error::InvalidAmount);
        }

        token::Client::new(&env, &Self::usdc(&env)).transfer(
            &from,
            &env.current_contract_address(),
            &amount,
        );

        let held = Self::shares_of(env.clone(), from.clone());
        env.storage()
            .persistent()
            .set(&DataKey::Shares(from.clone()), &(held + shares));
        env.storage()
            .instance()
            .set(&DataKey::TotalShares, &(total_shares + shares));

        env.events().publish(
            (symbol_short!("deposit"), from),
            (amount, shares),
        );
        Ok(shares)
    }

    /// Burn shares, receive USDC. Pulls from idle first, then from venues until the
    /// requested amount is covered. Never blocked by a venue freeze — only by a pause.
    pub fn withdraw(env: Env, holder: Address, shares: i128) -> Result<i128, Error> {
        holder.require_auth();
        if shares <= 0 {
            return Err(Error::InvalidAmount);
        }
        let held = Self::shares_of(env.clone(), holder.clone());
        if shares > held {
            return Err(Error::InsufficientShares);
        }

        let total_assets = Self::total_assets(&env);
        let total_shares = Self::total_shares(&env);
        if total_shares == 0 {
            return Err(Error::InsufficientShares);
        }
        // Round down: the vault never pays out more than the shares are worth.
        let mut amount = shares * total_assets / total_shares;
        if amount <= 0 {
            return Err(Error::InvalidAmount);
        }

        let mut idle = Self::idle(&env);
        if idle < amount {
            let needed = amount - idle;
            for adapter in Self::adapters(&env) {
                if needed <= 0 {
                    break;
                }
                let pulled = Self::adapter_withdraw(&env, &adapter, needed);
                if pulled > 0 {
                    idle += pulled;
                }
            }
        }
        if idle < amount {
            // Not enough liquid value; return what can be honoured rather than revert,
            // and let the caller see the shortfall.
            amount = idle;
            if amount <= 0 {
                return Err(Error::InvalidAmount);
            }
        }

        env.storage()
            .persistent()
            .set(&DataKey::Shares(holder.clone()), &(held - shares));
        env.storage()
            .instance()
            .set(&DataKey::TotalShares, &(total_shares - shares));

        token::Client::new(&env, &Self::usdc(&env)).transfer(
            &env.current_contract_address(),
            &holder,
            &amount,
        );

        env.events()
            .publish((symbol_short!("withdraw"), holder), (shares, amount));
        Ok(amount)
    }

    // ------------------------------------------------------------------- the keeper

    /// Propose a set of target positions. Permissionless, bounded, and value-checked.
    ///
    /// `plan` is a list of (adapter, target amount). The vault computes the deltas,
    /// withdraws first, then deposits, and rejects the whole call if the policy or the
    /// NAV check fails — so a keeper cannot half-execute a bad idea.
    pub fn rebalance(env: Env, plan: Vec<Allocation>) -> Result<i128, Error> {
        if Self::frozen(&env) {
            return Err(Error::Frozen);
        }
        if Self::paused(&env) {
            return Err(Error::Paused);
        }

        let policy = Self::policy(&env);
        let now = env.ledger().timestamp();
        let last: u64 = env
            .storage()
            .instance()
            .get(&DataKey::LastRebalance)
            .unwrap_or(0);
        if last != 0 && now < last + policy.cooldown_secs {
            return Err(Error::Cooldown);
        }
        if plan.len() > policy.max_venues {
            return Err(Error::TooManyVenues);
        }

        let registered = Self::adapters(&env);
        let nav_before = Self::total_assets(&env);

        // Target per adapter, and the per-venue cap.
        let venue_cap = nav_before * policy.max_venue_bps as i128 / BPS;
        let mut moved: i128 = 0;
        let mut target_sum: i128 = 0;
        for alloc in plan.iter() {
            if alloc.amount < 0 {
                return Err(Error::InvalidAmount);
            }
            if !registered.contains(&alloc.adapter) {
                return Err(Error::UnknownVenue);
            }
            if Self::venue_frozen(&env, alloc.adapter.clone()) && alloc.amount > 0 {
                return Err(Error::Frozen);
            }
            if alloc.amount > venue_cap {
                return Err(Error::VenueCap);
            }
            target_sum += alloc.amount;
            let current = Self::adapter_value(&env, &alloc.adapter);
            moved += if alloc.amount > current {
                alloc.amount - current
            } else {
                current - alloc.amount
            };
        }

        // Move cap: count gross movement, not net, because every leg costs a fill.
        if moved > nav_before * policy.max_move_bps as i128 / BPS {
            return Err(Error::MoveCap);
        }

        // Liquid floor after the plan: idle + (current positions - targets).
        let mut current_total: i128 = 0;
        for a in registered.iter() {
            current_total += Self::adapter_value(&env, &a);
        }
        let idle = Self::idle(&env);
        let idle_after = idle + current_total - target_sum;
        if idle_after < nav_before * policy.min_idle_bps as i128 / BPS {
            return Err(Error::IdleFloor);
        }

        // Execute: free liquidity first, then deploy it.
        for alloc in plan.iter() {
            let current = Self::adapter_value(&env, &alloc.adapter);
            if alloc.amount < current {
                let _ = Self::adapter_withdraw(&env, &alloc.adapter, current - alloc.amount);
            }
        }
        for alloc in plan.iter() {
            let current = Self::adapter_value(&env, &alloc.adapter);
            if alloc.amount > current {
                let delta = alloc.amount - current;
                let token = token::Client::new(&env, &Self::usdc(&env));
                token.transfer(&env.current_contract_address(), &alloc.adapter, &delta);
                let _: Val = env.invoke_contract(
                    &alloc.adapter,
                    &Symbol::new(&env, "deposit"),
                    (delta,).into_val(&env),
                );
            }
        }

        // Value check: the plan must not have destroyed value on the way through.
        let nav_after = Self::total_assets(&env);
        let floor = nav_before * (BPS - policy.max_slippage_bps as i128) / BPS;
        if nav_after < floor {
            return Err(Error::Slippage);
        }

        env.storage()
            .instance()
            .set(&DataKey::LastRebalance, &now);
        env.events()
            .publish((symbol_short!("rebalance"),), (moved, nav_after));
        Ok(moved)
    }

    // ------------------------------------------------------------------- owner rails

    /// Freeze everything: no rebalances. Deposits and withdrawals still work.
    pub fn freeze(env: Env) {
        Self::require_owner(&env);
        env.storage().instance().set(&DataKey::Frozen, &true);
        env.storage()
            .instance()
            .set(&DataKey::FrozenAt, &env.ledger().timestamp());
    }

    /// Unfreeze after the timelock, so a stolen key cannot quietly re-enable automation.
    pub fn unfreeze(env: Env) -> Result<(), Error> {
        Self::require_owner(&env);
        let frozen_at: u64 = env
            .storage()
            .instance()
            .get(&DataKey::FrozenAt)
            .unwrap_or(0);
        if frozen_at != 0 && env.ledger().timestamp() < frozen_at + UNFREEZE_DELAY_SECS {
            return Err(Error::UnfreezeTooEarly);
        }
        env.storage().instance().set(&DataKey::Frozen, &false);
        Ok(())
    }

    /// Stop new deposits (a venue incident, a depeg). Withdrawals stay open.
    pub fn pause(env: Env) {
        Self::require_owner(&env);
        env.storage().instance().set(&DataKey::Paused, &true);
    }

    pub fn unpause(env: Env) {
        Self::require_owner(&env);
        env.storage().instance().set(&DataKey::Paused, &false);
    }

    /// Freeze one venue: no new money in, withdrawals from it still allowed.
    pub fn freeze_venue(env: Env, adapter: Address) {
        Self::require_owner(&env);
        env.storage()
            .persistent()
            .set(&DataKey::FrozenVenue(adapter.clone()), &true);
        env.events()
            .publish((symbol_short!("venuefrz"),), adapter);
    }

    pub fn unfreeze_venue(env: Env, adapter: Address) {
        Self::require_owner(&env);
        env.storage()
            .persistent()
            .set(&DataKey::FrozenVenue(adapter.clone()), &false);
    }

    pub fn register_adapter(env: Env, adapter: Address) {
        Self::require_owner(&env);
        let mut list = Self::adapters(&env);
        if !list.contains(&adapter) {
            list.push_back(adapter.clone());
            env.storage().instance().set(&DataKey::AdapterList, &list);
            env.events().publish((symbol_short!("adapter"),), adapter);
        }
    }

    pub fn set_policy(env: Env, policy: Policy) {
        Self::require_owner(&env);
        validate_policy(&policy);
        env.storage().instance().set(&DataKey::Policy, &policy);
    }

    // ------------------------------------------------------------------------ views

    pub fn owner(env: Env) -> Address {
        env.storage()
            .instance()
            .get(&DataKey::Owner)
            .expect("owner not set")
    }

    pub fn policy(env: Env) -> Policy {
        env.storage()
            .instance()
            .get(&DataKey::Policy)
            .expect("policy not set")
    }

    pub fn state(env: Env) -> VaultState {
        VaultState {
            idle: Self::idle(&env),
            total_assets: Self::total_assets(&env),
            total_shares: Self::total_shares(&env),
            policy: Self::policy(&env),
            frozen: Self::frozen(&env),
            paused: Self::paused(&env),
            last_rebalance: env
                .storage()
                .instance()
                .get(&DataKey::LastRebalance)
                .unwrap_or(0),
            venues: Self::adapters(&env).len(),
        }
    }

    pub fn shares_of(env: Env, who: Address) -> i128 {
        env.storage()
            .persistent()
            .get(&DataKey::Shares(who))
            .unwrap_or(0)
    }

    /// Total value: idle USDC plus every venue position, as reported by the adapters.
    pub fn total_assets(env: Env) -> i128 {
        let mut total = Self::idle(&env);
        for adapter in Self::adapters(&env) {
            total += Self::adapter_value(&env, &adapter);
        }
        total
    }

    /// Price of one share, scaled by 1e6 for a readable integer view.
    pub fn share_price(env: Env) -> i128 {
        let shares = Self::total_shares(&env);
        if shares == 0 {
            return 1_000_000;
        }
        Self::total_assets(&env) * 1_000_000 / shares
    }

    pub fn positions(env: Env) -> Vec<Position> {
        let mut out = Vec::new(&env);
        for adapter in Self::adapters(&env) {
            let value = Self::adapter_value(&env, &adapter);
            out.push_back(Position { adapter, value });
        }
        out
    }

    // --------------------------------------------------------------------- internals

    fn usdc(env: &Env) -> Address {
        env.storage()
            .instance()
            .get(&DataKey::Usdc)
            .expect("usdc not set")
    }

    fn idle(env: &Env) -> i128 {
        token::Client::new(env, &Self::usdc(env))
            .balance(&env.current_contract_address())
    }

    fn adapters(env: &Env) -> Vec<Address> {
        env.storage()
            .instance()
            .get(&DataKey::AdapterList)
            .unwrap_or(Vec::new(env))
    }

    fn total_shares(env: &Env) -> i128 {
        env.storage()
            .instance()
            .get(&DataKey::TotalShares)
            .unwrap_or(0)
    }

    fn frozen(env: &Env) -> bool {
        env.storage()
            .instance()
            .get(&DataKey::Frozen)
            .unwrap_or(false)
    }

    fn paused(env: &Env) -> bool {
        env.storage()
            .instance()
            .get(&DataKey::Paused)
            .unwrap_or(false)
    }

    fn venue_frozen(env: &Env, adapter: Address) -> bool {
        env.storage()
            .persistent()
            .get(&DataKey::FrozenVenue(adapter))
            .unwrap_or(false)
    }

    fn require_owner(env: &Env) {
        let owner: Address = env
            .storage()
            .instance()
            .get(&DataKey::Owner)
            .expect("owner not set");
        owner.require_auth();
    }

    fn adapter_value(env: &Env, adapter: &Address) -> i128 {
        env.invoke_contract::<i128>(adapter, &Symbol::new(env, "value"), Vec::new(env))
    }

    fn adapter_withdraw(env: &Env, adapter: &Address, amount: i128) -> i128 {
        env.invoke_contract::<i128>(
            adapter,
            &Symbol::new(env, "withdraw"),
            (amount,).into_val(env),
        )
    }
}

fn validate_policy(p: &Policy) {
    assert!(p.max_venue_bps > 0 && p.max_venue_bps <= BPS as u32);
    assert!(p.min_idle_bps <= BPS as u32);
    assert!(p.max_move_bps > 0 && p.max_move_bps <= BPS as u32);
    assert!(p.max_slippage_bps <= 1_000);
    assert!(p.max_venues > 0 && p.max_venues <= 16);
    assert!(p.cooldown_secs <= 30 * 24 * 60 * 60);
}

#[cfg(test)]
mod test;
