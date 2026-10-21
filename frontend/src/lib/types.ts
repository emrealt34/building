export interface Incident {
  type: string;
  loss_usd: number;
  months_ago: number;
  scope: string;
  note: string;
}

export interface RiskResult {
  score: number;
  band: 'A' | 'B' | 'C' | 'D';
  subs: Record<string, number>;
  version: string;
  computed_at: string;
  stale: boolean;
  input_hash: string;
}

export interface Venue {
  id: string;
  label: string;
  kind: string;
  project: string;
  pool_id?: string;
  address?: string;
  asset: string;
  source: string;
  chain: string;
  apy: number;
  apy_base: number;
  apy_reward: number;
  apy_mean_30d?: number;
  tvl_usd: number;
  capacity_usd: number;
  utilization_bps?: number;
  instant_exit: boolean;
  audits: number;
  tier1_audit: boolean;
  live_months: number;
  upgradeable: boolean;
  oracle?: string;
  oracle_quality: number;
  reward_dependence: number;
  yield_source_verified: boolean;
  underlying: string[];
  incidents?: Incident[];
  risk: RiskResult;
  net_score: number;
  max_position_usd: number;
  eligible: boolean;
  refusal?: string;
}

export interface PlanLeg {
  venue_id: string;
  label: string;
  amount_usd: number;
  weight_bps: number;
  apy: number;
  risk_band: string;
  risk: number;
}

export interface Refusal {
  venue_id: string;
  label: string;
  apy: number;
  reason: string;
}

export interface Plan {
  deposit_usd: number;
  legs: PlanLeg[];
  idle_usd: number;
  blended_apy: number;
  portfolio_risk: number;
  portfolio_risk_base: number;
  correlation_penalty: number;
  refusals: Refusal[];
  policy_version: string;
  generated_at: string;
}

export interface Decision {
  at: string;
  vault: string;
  action: 'noop' | 'proposed' | 'skipped';
  reason: string;
  plan_hash?: string;
  best_apy?: number;
  current_apy?: number;
  gap_bps?: number;
}

export interface NetworkStatus {
  rpc_healthy: boolean;
  latest_ledger: number;
  protocol: number;
  close_time: number;
  base_fee_stroops: number;
  horizon_ok: boolean;
  checked_at: string;
}

export interface Policy {
  version: string;
  max_venue_bps: number;
  min_idle_bps: number;
  max_move_bps: number;
  min_improvement_bps: number;
  cooldown_secs: number;
  max_slippage_bps: number;
  max_venue_utilization_bps: number;
  stability_days: number;
  min_capacity_usd: number;
  max_venue_share_of_capacity: number;
  min_position_usd: number;
  rebalance_enabled: boolean;
}

export interface PolicyDoc {
  policy: Policy;
  explain: string;
  chosen_by_measurement: Record<string, string>;
}

export interface Meta {
  product: string;
  chain: string;
  asset: string;
  custody: string;
  automation: string;
  risk_model: string;
  safety_registry: string;
  policy_version: string;
  api_version: string;
  vault_contract: string;
}

export interface SimResult {
  principal_usd: number;
  threshold_bps: number;
  cooldown_days: number;
  switches: number;
  final_usd: number;
  gain_usd: number;
  passive_venue: string;
  passive_final_usd: number;
  alpha_usd: number;
  alpha_bps_per_year: number;
  capture_of_upper_bound_pct: number;
}

export interface VenueSeries {
  venue_id: string;
  label: string;
  days: number;
  mean_apy: number;
  min_apy: number;
  max_apy: number;
  series: HistoryPoint[];
}

export interface AlphaReport {
  window_days: number;
  generated_at: string;
  venues: VenueSeries[];
  upper_bound_usd: number;
  best_single_label: string;
  best_single_usd: number;
  perfect_hindsight_usd: number;
  sweeps: SimResult[];
  best: SimResult | null;
  days_gap_over_threshold: number;
  avg_gap_bps: number;
  note: string;
}

export interface HistoryPoint {
  date: string;
  apy: number;
  tvl_usd: number;
}

export interface CarryRow {
  code: string;
  country: string;
  currency: string;
  policy_rate: number;
  inflation?: number;
  real_rate?: number;
  rate_name: string;
  spot_now: number;
  spot_year_ago: number;
  currency_move_pct: number;
  local_return_usd_pct: number;
  breakeven_depreciation_pct: number;
  vol_annual_pct: number;
  max_drawdown_pct: number;
  band: string;
  verdict: string;
  excess_vs_usd_bps: number;
  days: number;
  source: string;
  as_of: string;
}

export interface CarryReport {
  version: string;
  registry: string;
  generated_at: string;
  window_days: number;
  benchmark_apy: number;
  rows: CarryRow[];
  note: string;
}

// ---- cherry picking (net return across countries) ------------------------------

export interface CherryMarket {
  code: string; country: string; currency: string;
  rate_name: string; source: string; as_of: string;
  policy_rate: number; inflation_pct: number;
  fx_move_pct: number;
  gross_usd_pct: number;
  net_usd_pct: number;
  real_usd_pct: number;
  real_local_pct: number;
  days: number;
}

export interface CherryLeg {
  from: string; to: string; code: string; country: string;
  return_pct: number; chosen_on_pct: number;
}

export interface CherryStrategy {
  key: string; label: string; return_pct: number; note: string; legs?: CherryLeg[];
}

export interface CherryReport {
  version: string;
  window_days: number;
  start: string;
  end: string;
  fee_bps: number;
  us_inflation_pct: number;
  markets: CherryMarket[];
  strategies: CherryStrategy[];
  note: string;
  registry: string;
}
