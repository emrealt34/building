//! Tests for the properties the product claims on the website.
//!
//! Each test is named after a promise, so `cargo test` reads like the marketing page:
//! the claim and the proof live in the same file.

use super::*;
use soroban_sdk::testutils::Address as _;
use soroban_sdk::testutils::Ledger;
use soroban_sdk::token::{StellarAssetClient, TokenClient};
use soroban_sdk::{contract, contractimpl, symbol_short};

// ---------------------------------------------------------------- a stand-in venue

/// Minimal lending venue: holds what the vault sends it, can be told to apply a
/// withdrawal haircut (to simulate slippage) or to accrue extra value (yield).
#[contract]
pub struct MockVenue;

#[contractimpl]
impl MockVenue {
    pub fn init(env: Env, usdc: Address, vault: Address) {
        env.storage().instance().set(&symbol_short!("usdc"), &usdc);
        env.storage().instance().set(&symbol_short!("vault"), &vault);
        env.storage().instance().set(&symbol_short!("pos"), &0i128);
        env.storage().instance().set(&symbol_short!("bonus"), &0i128);
        env.storage().instance().set(&symbol_short!("cut"), &0u32);
    }

    pub fn deposit(env: Env, amount: i128) {
        let pos: i128 = env
            .storage()
            .instance()
            .get(&symbol_short!("pos"))
            .unwrap_or(0);
        env.storage()
            .instance()
            .set(&symbol_short!("pos"), &(pos + amount));
    }

    pub fn withdraw(env: Env, amount: i128) -> i128 {
        let usdc: Address = env.storage().instance().get(&symbol_short!("usdc")).unwrap();
        let vault: Address = env
            .storage()
            .instance()
            .get(&symbol_short!("vault"))
            .unwrap();
        let pos: i128 = env
            .storage()
            .instance()
            .get(&symbol_short!("pos"))
            .unwrap_or(0);
        let cut: u32 = env
            .storage()
            .instance()
            .get(&symbol_short!("cut"))
            .unwrap_or(0);

        let take = if amount > pos { pos } else { amount };
        let out = take - take * cut as i128 / 10_000;
        env.storage()
            .instance()
            .set(&symbol_short!("pos"), &(pos - take));
        if out > 0 {
            token::Client::new(&env, &usdc).transfer(&env.current_contract_address(), &vault, &out);
        }
        out
    }

    /// Position value = principal + accrued "yield" booked by the test.
    pub fn value(env: Env) -> i128 {
        let pos: i128 = env
            .storage()
            .instance()
            .get(&symbol_short!("pos"))
            .unwrap_or(0);
        let bonus: i128 = env
            .storage()
            .instance()
            .get(&symbol_short!("bonus"))
            .unwrap_or(0);
        pos + bonus
    }

    /// Test hooks.
    pub fn set_haircut(env: Env, bps: u32) {
        env.storage().instance().set(&symbol_short!("cut"), &bps);
    }
    pub fn accrue(env: Env, extra: i128) {
        let bonus: i128 = env
            .storage()
            .instance()
            .get(&symbol_short!("bonus"))
            .unwrap_or(0);
        env.storage()
            .instance()
            .set(&symbol_short!("bonus"), &(bonus + extra));
    }
}

// ------------------------------------------------------------------------- fixture

struct Ctx {
    env: Env,
    vault: Address,
    usdc: Address,
    alice: Address,
    bob: Address,
    owner: Address,
    venue: Address,
}

fn policy() -> Policy {
    Policy {
        max_venue_bps: 6_000,
        min_idle_bps: 1_000,
        max_move_bps: 5_000,
        min_improvement_bps: 25,
        cooldown_secs: 0,
        max_slippage_bps: 10,
        max_venues: 4,
    }
}

fn setup(p: Policy) -> Ctx {
    let env = Env::default();
    env.mock_all_auths();

    let owner = Address::generate(&env);
    let alice = Address::generate(&env);
    let bob = Address::generate(&env);

    // USDC
    let usdc_admin = Address::generate(&env);
    let usdc = env.register_stellar_asset_contract_v2(usdc_admin).address();
    StellarAssetClient::new(&env, &usdc).mint(&alice, &1_000_000);
    StellarAssetClient::new(&env, &usdc).mint(&bob, &1_000_000);

    // venue
    let venue = env.register(MockVenue, ());
    let vault = env.register(UserVault, (owner.clone(), usdc.clone(), p));
    MockVenueClient::new(&env, &venue).init(&usdc, &vault);

    let ctx = Ctx { env, vault, usdc, alice, bob, owner, venue };
    UserVaultClient::new(&ctx.env, &ctx.vault).register_adapter(&ctx.venue);
    ctx
}

fn plan(ctx: &Ctx, amount: i128) -> Vec<Allocation> {
    let mut p = Vec::new(&ctx.env);
    p.push_back(Allocation { adapter: ctx.venue.clone(), amount });
    p
}

// --------------------------------------------------------------------------- tests

#[test]
fn deposit_and_withdraw_round_trip() {
    let ctx = setup(policy());
    let v = UserVaultClient::new(&ctx.env, &ctx.vault);
    let token = TokenClient::new(&ctx.env, &ctx.usdc);

    let shares = v.deposit(&ctx.alice, &100_000);
    assert_eq!(shares, 100_000);
    assert_eq!(token.balance(&ctx.alice), 900_000);
    assert_eq!(v.total_assets(), 100_000);

    let returned = v.withdraw(&ctx.alice, &40_000);
    assert_eq!(returned, 40_000);
    assert_eq!(token.balance(&ctx.alice), 940_000);
    assert_eq!(v.shares_of(&ctx.alice), 60_000);
}

#[test]
fn anyone_can_rebalance_and_value_is_conserved() {
    let ctx = setup(policy());
    let v = UserVaultClient::new(&ctx.env, &ctx.vault);
    v.deposit(&ctx.alice, &100_000);
    v.deposit(&ctx.bob, &100_000);

    let moved = v.rebalance(&plan(&ctx, 120_000));
    assert_eq!(moved, 120_000);
    assert_eq!(v.state().idle, 80_000, "idle keeps the 10% floor");
    assert_eq!(v.total_assets(), 200_000, "no value created or destroyed");
}

#[test]
fn a_single_venue_cannot_take_everything() {
    let ctx = setup(policy());
    let v = UserVaultClient::new(&ctx.env, &ctx.vault);
    v.deposit(&ctx.alice, &100_000);

    // 70% into one venue while the policy caps it at 60%.
    let err = v.try_rebalance(&plan(&ctx, 70_000)).unwrap_err().unwrap();
    assert_eq!(err, Error::VenueCap);
}

#[test]
fn the_liquid_floor_is_not_negotiable() {
    let ctx = setup(policy());
    let v = UserVaultClient::new(&ctx.env, &ctx.vault);
    v.deposit(&ctx.alice, &100_000);

    // 60% is within the venue cap but would leave only 40%… still above 10%, so this
    // passes; 95% would not.
    let err = v.try_rebalance(&plan(&ctx, 95_000)).unwrap_err().unwrap();
    assert_eq!(err, Error::VenueCap, "95% also breaks the venue cap");
    assert!(v.try_rebalance(&plan(&ctx, 60_000)).is_ok());
}

#[test]
fn the_cooldown_stops_keeper_churn() {
    let mut p = policy();
    p.cooldown_secs = 2 * 24 * 60 * 60;
    p.max_move_bps = 10_000;
    let ctx = setup(p);
    let v = UserVaultClient::new(&ctx.env, &ctx.vault);
    v.deposit(&ctx.alice, &100_000);

    let mut day = 0u64;
    ctx.env.ledger().set_timestamp(1_000_000 + day);
    v.rebalance(&plan(&ctx, 50_000));

    day += 60;
    ctx.env.ledger().set_timestamp(1_000_000 + day);
    let err = v.try_rebalance(&plan(&ctx, 60_000)).unwrap_err().unwrap();
    assert_eq!(err, Error::Cooldown);

    day += 2 * 24 * 60 * 60;
    ctx.env.ledger().set_timestamp(1_000_000 + day);
    assert!(v.try_rebalance(&plan(&ctx, 60_000)).is_ok());
}

#[test]
fn a_plan_that_loses_value_is_rejected() {
    let mut p = policy();
    p.max_move_bps = 10_000;
    let ctx = setup(p);
    let v = UserVaultClient::new(&ctx.env, &ctx.vault);
    v.deposit(&ctx.alice, &100_000);

    // The venue will only return 95% on exit: the NAV check must refuse the move.
    MockVenueClient::new(&ctx.env, &ctx.venue).set_haircut(&500);
    let err = v.try_rebalance(&plan(&ctx, 50_000)).unwrap_err().unwrap();
    assert_eq!(err, Error::Slippage);
    assert_eq!(v.total_assets(), 100_000, "state rolled back with the error");
}

#[test]
fn freeze_stops_automation_but_never_withdrawals() {
    let ctx = setup(policy());
    let v = UserVaultClient::new(&ctx.env, &ctx.vault);
    v.deposit(&ctx.alice, &100_000);
    v.rebalance(&plan(&ctx, 50_000));

    v.freeze();
    let err = v.try_rebalance(&plan(&ctx, 60_000)).unwrap_err().unwrap();
    assert_eq!(err, Error::Frozen);

    // The user can still get their money out, from the venue if needed.
    let out = v.withdraw(&ctx.alice, &100_000);
    assert_eq!(out, 100_000);
    assert_eq!(TokenClient::new(&ctx.env, &ctx.usdc).balance(&ctx.alice), 1_000_000);
}

#[test]
fn unfreezing_is_delayed_but_freezing_is_instant() {
    let ctx = setup(policy());
    let v = UserVaultClient::new(&ctx.env, &ctx.vault);
    ctx.env.ledger().set_timestamp(1_000_000);
    v.freeze();
    let err = v.try_unfreeze().unwrap_err().unwrap();
    assert_eq!(err, Error::UnfreezeTooEarly);

    ctx.env.ledger().set_timestamp(1_000_000 + UNFREEZE_DELAY_SECS);
    assert!(v.try_unfreeze().is_ok());
}

#[test]
fn a_frozen_venue_takes_no_new_money_but_still_pays_out() {
    let ctx = setup(policy());
    let v = UserVaultClient::new(&ctx.env, &ctx.vault);
    v.deposit(&ctx.alice, &100_000);
    v.rebalance(&plan(&ctx, 50_000));

    v.freeze_venue(&ctx.venue);
    let err = v.try_rebalance(&plan(&ctx, 60_000)).unwrap_err().unwrap();
    assert_eq!(err, Error::Frozen);

    // Withdrawing pulls the money back out of the frozen venue.
    v.withdraw(&ctx.alice, &100_000);
    assert_eq!(TokenClient::new(&ctx.env, &ctx.usdc).balance(&ctx.alice), 1_000_000);
}

#[test]
fn unknown_venues_are_refused() {
    let ctx = setup(policy());
    let v = UserVaultClient::new(&ctx.env, &ctx.vault);
    v.deposit(&ctx.alice, &100_000);

    let stranger = ctx.env.register(MockVenue, ());
    let mut p = Vec::new(&ctx.env);
    p.push_back(Allocation { adapter: stranger, amount: 50_000 });
    let err = v.try_rebalance(&p).unwrap_err().unwrap();
    assert_eq!(err, Error::UnknownVenue);
}

#[test]
fn share_price_rises_with_venue_yield() {
    let ctx = setup(policy());
    let v = UserVaultClient::new(&ctx.env, &ctx.vault);
    v.deposit(&ctx.alice, &100_000);
    v.rebalance(&plan(&ctx, 50_000));
    let before = v.share_price();

    // 5% of the deployed position appears as yield.
    MockVenueClient::new(&ctx.env, &ctx.venue).accrue(&2_500);
    let after = v.share_price();
    assert!(after > before, "share price {after} should exceed {before}");
    assert_eq!(v.total_assets(), 102_500);
}

#[test]
fn pause_blocks_new_money_but_not_exits() {
    let ctx = setup(policy());
    let v = UserVaultClient::new(&ctx.env, &ctx.vault);
    v.deposit(&ctx.alice, &100_000);
    v.pause();

    let err = v.try_deposit(&ctx.bob, &10_000).unwrap_err().unwrap();
    assert_eq!(err, Error::Paused);
    assert!(v.try_withdraw(&ctx.alice, &100_000).is_ok());
}
