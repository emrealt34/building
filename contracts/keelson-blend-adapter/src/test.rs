use crate::{BlendAdapter, BlendAdapterClient, Error};
use blend_contract_sdk::pool::{Positions, Request, Reserve, ReserveConfig, ReserveData};
use soroban_sdk::{
    contract, contractimpl,
    testutils::Address as _,
    token::{StellarAssetClient, TokenClient},
    Address, Env, Map, Vec,
};

/// One b-token is worth 1.05 USDC here, so a scaling mistake cannot hide behind a
/// 1:1 exchange rate.
const B_RATE: i128 = 1_050_000_000_000;
const SCALAR_12: i128 = 1_000_000_000_000;
const DECIMALS: u32 = 7;
const UNIT: i128 = 10_000_000; // 1 USDC

/// A pool that behaves like Blend for the two calls the adapter makes, with a fixed
/// exchange rate. The real pool is exercised on testnet; this exists to pin our side
/// of the ABI: same types, same auth, same token pulls.
#[contract]
pub struct MockPool;

#[contractimpl]
impl MockPool {
    pub fn submit(
        env: Env,
        from: Address,
        _spender: Address,
        to: Address,
        requests: Vec<Request>,
    ) -> Positions {
        let me = env.current_contract_address();
        let mut b_tokens: i128 = env.storage().persistent().get(&to).unwrap_or(0);

        for req in requests.iter() {
            let token = TokenClient::new(&env, &req.address);
            match req.request_type {
                0 => {
                    // Supply: the pool pulls from the funder using the allowance the
                    // adapter granted it, exactly as Blend does.
                    token.transfer_from(&me, &from, &me, &req.amount);
                    b_tokens += req.amount * SCALAR_12 / B_RATE;
                }
                1 => {
                    // Withdraw: convert b-tokens back to underlying, rounded up so the
                    // user is never underpaid, and capped at what the position holds.
                    let mut want = (req.amount * SCALAR_12 + B_RATE - 1) / B_RATE;
                    if want > b_tokens {
                        want = b_tokens;
                    }
                    b_tokens -= want;
                    token.transfer(&me, &to, &(want * B_RATE / SCALAR_12));
                }
                _ => panic!("unsupported request"),
            }
        }
        env.storage().persistent().set(&to, &b_tokens);
        Self::positions_for(&env, to)
    }

    pub fn get_positions(env: Env, address: Address) -> Positions {
        Self::positions_for(&env, address)
    }

    pub fn get_reserve(env: Env, asset: Address) -> Reserve {
        let _ = env;
        let config = ReserveConfig {
            index: 0,
            decimals: DECIMALS,
            c_factor: 0_7500000,
            l_factor: 0_7500000,
            util: 0_7500000,
            max_util: 0_9500000,
            r_base: 0_0100000,
            r_one: 0_0500000,
            r_two: 0_5000000,
            r_three: 1_5000000,
            reactivity: 0_0000020,
            supply_cap: 1_000_000 * UNIT,
            enabled: true,
        };
        let data = ReserveData {
            d_rate: SCALAR_12,
            b_rate: B_RATE,
            ir_mod: SCALAR_12,
            b_supply: 0,
            d_supply: 0,
            backstop_credit: 0,
            last_time: 0,
        };
        Reserve {
            asset,
            config,
            data,
            scalar: 1,
        }
    }
}

impl MockPool {
    fn positions_for(env: &Env, address: Address) -> Positions {
        let b_tokens: i128 = env.storage().persistent().get(&address).unwrap_or(0);
        let mut supply = Map::new(env);
        supply.set(0u32, b_tokens);
        Positions {
            collateral: Map::new(env),
            liabilities: Map::new(env),
            supply,
        }
    }
}

struct Fixture {
    env: Env,
    vault: Address,
    usdc: Address,
    pool: Address,
    adapter: Address,
}

fn setup() -> Fixture {
    let env = Env::default();
    env.mock_all_auths();

    let admin = Address::generate(&env);
    let usdc = env
        .register_stellar_asset_contract_v2(admin.clone())
        .address();
    let vault = Address::generate(&env);
    StellarAssetClient::new(&env, &usdc).mint(&vault, &(100_000 * UNIT));

    let pool = env.register(MockPool, ());
    let adapter = env.register(
        BlendAdapter,
        (vault.clone(), usdc.clone(), pool.clone(), 0u32),
    );

    Fixture {
        env,
        vault,
        usdc,
        pool,
        adapter,
    }
}

fn fund_adapter(f: &Fixture, amount: i128) {
    TokenClient::new(&f.env, &f.usdc).transfer(&f.vault, &f.adapter, &amount);
}

#[test]
fn supplied_money_lands_in_the_pool_and_is_worth_what_it_says() {
    let f = setup();
    let client = BlendAdapterClient::new(&f.env, &f.adapter);
    let token = TokenClient::new(&f.env, &f.usdc);

    fund_adapter(&f, 1_000 * UNIT);
    assert_eq!(client.deposit(&(1_000 * UNIT)), 1_000 * UNIT);

    assert_eq!(token.balance(&f.pool), 1_000 * UNIT);
    assert_eq!(token.balance(&f.adapter), 0);

    // Valued through the pool and its exchange rate, not from our own bookkeeping.
    let value = client.value();
    assert!(value > 999 * UNIT && value <= 1_000 * UNIT, "value {value}");
}

#[test]
fn withdrawing_pays_the_vault_and_leaves_the_rest_working() {
    let f = setup();
    let client = BlendAdapterClient::new(&f.env, &f.adapter);
    let token = TokenClient::new(&f.env, &f.usdc);

    fund_adapter(&f, 1_000 * UNIT);
    client.deposit(&(1_000 * UNIT));
    let vault_before = token.balance(&f.vault);

    let out = client.withdraw(&(400 * UNIT));
    assert!(out > 399 * UNIT && out <= 400 * UNIT, "out {out}");
    assert_eq!(token.balance(&f.vault), vault_before + out);
    assert!(client.value() < 601 * UNIT);
    assert!(client.value() > 598 * UNIT);
}

#[test]
fn a_stranger_cannot_redirect_a_withdrawal() {
    let f = setup();
    let client = BlendAdapterClient::new(&f.env, &f.adapter);
    let token = TokenClient::new(&f.env, &f.usdc);
    let stranger = Address::generate(&f.env);

    fund_adapter(&f, 1_000 * UNIT);
    client.deposit(&(1_000 * UNIT));
    let vault_before = token.balance(&f.vault);

    // Anyone may call withdraw; the money still goes to the vault fixed at construction.
    let out = client.withdraw(&(100 * UNIT));
    assert_eq!(token.balance(&stranger), 0);
    assert_eq!(token.balance(&f.vault), vault_before + out);
}

#[test]
fn a_stranger_can_only_push_idle_balance_into_the_pool() {
    let f = setup();
    let client = BlendAdapterClient::new(&f.env, &f.adapter);
    let token = TokenClient::new(&f.env, &f.usdc);

    fund_adapter(&f, 50 * UNIT);
    // No deposit has been called yet: the money is idle in the adapter.
    client.deposit(&(50 * UNIT));
    assert_eq!(token.balance(&f.pool), 50 * UNIT);
    let value = client.value();
    assert!(value > 49 * UNIT && value <= 50 * UNIT, "value {value}");
}

#[test]
fn depositing_more_than_it_holds_is_refused() {
    let f = setup();
    let client = BlendAdapterClient::new(&f.env, &f.adapter);

    fund_adapter(&f, 10 * UNIT);
    let res = client.try_deposit(&(11 * UNIT));
    assert_eq!(res, Err(Ok(Error::InvalidAmount)));
}

#[test]
fn empty_amounts_are_refused() {
    let f = setup();
    let client = BlendAdapterClient::new(&f.env, &f.adapter);

    assert_eq!(client.try_deposit(&0), Err(Ok(Error::InvalidAmount)));
    assert_eq!(client.try_withdraw(&0), Err(Ok(Error::InvalidAmount)));
}

#[test]
fn the_constructor_pins_the_counterparties() {
    let f = setup();
    let client = BlendAdapterClient::new(&f.env, &f.adapter);
    assert_eq!(client.vault(), f.vault);
    assert_eq!(client.usdc(), f.usdc);
    assert_eq!(client.pool(), f.pool);
    assert_eq!(client.reserve_id(), 0);
}
