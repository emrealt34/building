// Package model holds the domain types shared by providers, risk, policy and API.
package model

import "time"

// VenueKind describes how a venue earns yield.
type VenueKind string

const (
	KindLendingReserve VenueKind = "lending_reserve"
	KindVault          VenueKind = "vault"
	KindManagedVault   VenueKind = "managed_vault"
)

// RiskBand is the letter grade shown to users.
type RiskBand string

const (
	BandA RiskBand = "A"
	BandB RiskBand = "B"
	BandC RiskBand = "C"
	BandD RiskBand = "D"
)

// Incident is a historical safety event. Scope matters: a bug in protocol code is
// worse than a bad configuration in a different pool.
type Incident struct {
	Type      string  `json:"type"`
	LossUSD   float64 `json:"loss_usd"`
	MonthsAgo int     `json:"months_ago"`
	Scope     string  `json:"scope"` // "protocol_code" | "pool_config"
	Note      string  `json:"note"`
}

// Venue is a place where USDC can earn yield.
type Venue struct {
	ID       string    `json:"id"`
	Label    string    `json:"label"`
	Kind     VenueKind `json:"kind"`
	Project  string    `json:"project"`
	PoolID   string    `json:"pool_id,omitempty"`
	Address  string    `json:"address,omitempty"` // Soroban contract address, when the source exposes it
	Asset    string    `json:"asset"`
	Source   string    `json:"source"` // "defillama" | "defindex"
	Chain    string    `json:"chain"`
	Protocol string    `json:"protocol,omitempty"`

	// Live numbers (refreshed by the provider layer).
	APY          float64 `json:"apy"`
	APYBase      float64 `json:"apy_base"`
	APYReward    float64 `json:"apy_reward"`
	APYMean30d   float64 `json:"apy_mean_30d,omitempty"`
	TVLUSD       float64 `json:"tvl_usd"`
	CapacityUSD  float64 `json:"capacity_usd"` // what we consider safely withdrawable
	UtilizationB float64 `json:"utilization_bps,omitempty"` // basis points, lending only
	InstantExit  bool    `json:"instant_exit"`

	// Safety profile (curated, versioned: see risk registry).
	Audits              int        `json:"audits"`
	Tier1Audit          bool       `json:"tier1_audit"`
	LiveMonths          int        `json:"live_months"`
	Upgradeable         bool       `json:"upgradeable"`
	Oracle              string     `json:"oracle,omitempty"`
	OracleQuality       float64    `json:"oracle_quality"`       // 0..1, higher is better
	RewardDependence    float64    `json:"reward_dependence"`    // 0..1 share of yield from incentives
	YieldSourceVerified bool       `json:"yield_source_verified"`
	Underlying          []string   `json:"underlying"`
	Incidents           []Incident `json:"incidents,omitempty"`

	// Derived (filled by the risk/policy layers).
	Risk      *RiskResult `json:"risk,omitempty"`
	NetScore  float64     `json:"net_score"`
	MaxPosUSD float64     `json:"max_position_usd,omitempty"`
	Eligible  bool        `json:"eligible"`
	Refusal   string      `json:"refusal,omitempty"`
}

// RiskResult is a deterministic, versioned score. Higher means riskier.
type RiskResult struct {
	Score     float64            `json:"score"`
	Band      RiskBand           `json:"band"`
	Subs      map[string]float64 `json:"subs"`
	Version   string             `json:"version"`
	Computed  time.Time          `json:"computed_at"`
	Stale     bool               `json:"stale"`
	InputHash string             `json:"input_hash"`
}

// Policy is what a user signs. The vault enforces only this.
type Policy struct {
	Version                string  `json:"version"`
	MaxVenueBps            int     `json:"max_venue_bps"`
	MinIdleBps             int     `json:"min_idle_bps"`
	MaxMoveBps             int     `json:"max_move_bps"`
	MinImprovementBps      int     `json:"min_improvement_bps"`
	CooldownSecs           int     `json:"cooldown_secs"`
	MaxSlippageBps         int     `json:"max_slippage_bps"`
	MaxVenueUtilizationBps int     `json:"max_venue_utilization_bps"`
	StabilityDays          int     `json:"stability_days"`
	MinCapacityUSD         float64 `json:"min_capacity_usd"`
	MaxVenueShareOfCap     float64 `json:"max_venue_share_of_capacity"`
	MinPositionUSD         float64 `json:"min_position_usd"`
	RebalanceEnabled       bool    `json:"rebalance_enabled"`
}

// DefaultPolicy is the "balanced" profile. Parameters were chosen by measurement
// (see /api/v1/alpha), not by taste: average leadership between the two verified
// venues was ~6.7 days, so a 7-day cooldown misses most switches.
func DefaultPolicy() Policy {
	return Policy{
		Version:                "policy-v1.2",
		MaxVenueBps:            6000,
		MinIdleBps:             1000,
		MaxMoveBps:             5000,
		MinImprovementBps:      25,
		CooldownSecs:           2 * 24 * 3600,
		MaxSlippageBps:         10,
		MaxVenueUtilizationBps: 8500,
		StabilityDays:          1,
		MinCapacityUSD:         100_000,
		MaxVenueShareOfCap:     0.02,
		MinPositionUSD:         25,
		RebalanceEnabled:       true,
	}
}

type PlanLeg struct {
	VenueID  string   `json:"venue_id"`
	Label    string   `json:"label"`
	AmountUSD float64 `json:"amount_usd"`
	WeightBps int     `json:"weight_bps"`
	APY      float64  `json:"apy"`
	RiskBand RiskBand `json:"risk_band"`
	Risk     float64  `json:"risk"`
}

type Refusal struct {
	VenueID string  `json:"venue_id"`
	Label   string  `json:"label"`
	APY     float64 `json:"apy"`
	Reason  string  `json:"reason"`
}

type Plan struct {
	DepositUSD        float64    `json:"deposit_usd"`
	Legs              []PlanLeg  `json:"legs"`
	IdleUSD           float64    `json:"idle_usd"`
	BlendedAPY        float64    `json:"blended_apy"`
	PortfolioRisk     float64    `json:"portfolio_risk"`
	PortfolioRiskBase float64    `json:"portfolio_risk_base"`
	CorrelationPenalty float64   `json:"correlation_penalty"`
	Refusals          []Refusal  `json:"refusals"`
	PolicyVersion     string     `json:"policy_version"`
	GeneratedAt       time.Time  `json:"generated_at"`
}

// Decision is one keeper evaluation, stored for the public decision log.
type Decision struct {
	At        time.Time `json:"at"`
	Vault     string    `json:"vault"`
	Action    string    `json:"action"` // "noop" | "proposed" | "skipped"
	Reason    string    `json:"reason"`
	PlanHash  string    `json:"plan_hash,omitempty"`
	BestAPY   float64   `json:"best_apy,omitempty"`
	CurrentAPY float64  `json:"current_apy,omitempty"`
	GapBps    float64   `json:"gap_bps,omitempty"`
}

// NetworkStatus is read live from Stellar RPC + Horizon.
type NetworkStatus struct {
	RPCHealthy     bool   `json:"rpc_healthy"`
	LatestLedger   uint32 `json:"latest_ledger"`
	Protocol       uint32 `json:"protocol"`
	CloseTime      int64  `json:"close_time"`
	BaseFeeStroops int64  `json:"base_fee_stroops"`
	HorizonOK      bool   `json:"horizon_ok"`
	CheckedAt      time.Time `json:"checked_at"`
}

// AccountBalance is read from Horizon (classic account, non-custodial read only).
type AccountBalance struct {
	Address  string             `json:"address"`
	Found    bool               `json:"found"`
	Balances []AssetBalance     `json:"balances"`
	Error    string             `json:"error,omitempty"`
}

type AssetBalance struct {
	Asset  string  `json:"asset"`
	Amount float64 `json:"amount"`
}
