// Package risk computes a deterministic, versioned venue risk score.
//
// Design rules:
//   - No machine learning. A reviewer must be able to recompute the score by hand.
//   - Every input is either read on-chain, read from a public API, or read from the
//     versioned safety registry. Nothing depends on an opinion typed at request time.
//   - Higher score = riskier. 0..100.
package risk

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/emrealt34/building/backend/internal/model"
)

const Version = "risk-model-v1.0"

// Weights sum to 1.00.
var Weights = map[string]float64{
	"contract":   0.25, // audit depth, time live, upgrade power, incident history
	"liquidity":  0.20, // can I get my money out today?
	"oracle":     0.15, // bad-debt / price-feed exposure
	"durability": 0.15, // is the yield borrower interest or expiring incentives?
	"contagion":  0.15, // shared dependencies with other venues
	"governance": 0.10, // who can change parameters, and how fast
}

// DepositUSD is used to size the liquidity sub-score: a $50k position in a $200k venue
// is a different risk from the same position in a $20M venue.
func Compute(v model.Venue, depositUSD float64, now time.Time) model.RiskResult {
	subs := map[string]float64{
		"contract":   ScoreContract(v),
		"liquidity":  ScoreLiquidity(v, depositUSD),
		"oracle":     ScoreOracle(v),
		"durability": ScoreDurability(v),
		"contagion":  ScoreContagion(v),
		"governance": ScoreGovernance(v),
	}
	var total float64
	for k, w := range Weights {
		total += subs[k] * w
	}
	return model.RiskResult{
		Score:     round1(clamp(total, 0, 100)),
		Band:      Band(total),
		Subs:      subs,
		Version:   Version,
		Computed:  now,
		InputHash: InputHash(v),
	}
}

// Band maps a score to a letter. Thresholds are part of the published methodology.
func Band(score float64) model.RiskBand {
	switch {
	case score < 45:
		return model.BandA
	case score < 60:
		return model.BandB
	case score < 75:
		return model.BandC
	default:
		return model.BandD
	}
}

func ScoreContract(v model.Venue) float64 {
	s := 40.0
	if v.Tier1Audit {
		s += 15
	}
	if v.Audits > 1 {
		s += min(10, 5*float64(v.Audits-1))
	}
	switch {
	case v.LiveMonths >= 12:
		s += 10
	case v.LiveMonths >= 6:
		s += 5
	}
	if !v.Upgradeable {
		s += 5
	}
	for _, inc := range v.Incidents {
		// A bug in protocol code is worse than a bad configuration in one pool.
		heavy := 8.0
		if inc.Scope == "protocol_code" || inc.Scope == "" {
			heavy = 22.0
		}
		if inc.LossUSD > 1_000_000 {
			s += heavy
		} else if inc.LossUSD > 0 {
			s += heavy / 2
		}
		if inc.MonthsAgo > 18 {
			s -= 5
		}
	}
	return round1(clamp(s, 0, 100))
}

func ScoreLiquidity(v model.Venue, depositUSD float64) float64 {
	s := 20.0
	if v.UtilizationB > 0 {
		s += 60 * (v.UtilizationB / 10_000) // a full pool is hard to exit
	}
	if !v.InstantExit {
		s += 15
	}
	if v.CapacityUSD > 0 {
		s += min(30, 300*(depositUSD/v.CapacityUSD))
	} else {
		s += 30
	}
	return round1(clamp(s, 0, 100))
}

func ScoreOracle(v model.Venue) float64 {
	return round1(clamp(100-100*v.OracleQuality, 0, 100))
}

func ScoreDurability(v model.Venue) float64 {
	s := 25.0
	if v.APY > 0 && v.APYReward > 0 {
		s += 55 * min(1, v.APYReward/v.APY)
	}
	s += 60 * v.RewardDependence
	if v.APYMean30d > 0 {
		s += min(20, 100*mabs(v.APY-v.APYMean30d)/v.APYMean30d)
	}
	if v.APY <= 0.05 {
		s += 10 // suspiciously free money
	}
	if !v.YieldSourceVerified {
		s += 25 // cannot decompose borrower interest vs incentives
	}
	return round1(clamp(s, 0, 100))
}

func ScoreContagion(v model.Venue) float64 {
	extra := len(v.Underlying) - 1
	if extra < 0 {
		extra = 0
	}
	s := 30.0 + 18*float64(extra)
	for _, u := range v.Underlying {
		lu := strings.ToLower(u)
		if strings.Contains(lu, "comet") || strings.Contains(lu, "backstop") {
			s += 25 // the Aug 2026 lesson
		}
	}
	if v.Kind == model.KindVault {
		s += 12 // a layer on top of a layer
	}
	return round1(clamp(s, 0, 100))
}

func ScoreGovernance(v model.Venue) float64 {
	s := 45.0
	if v.Tier1Audit {
		s += 10
	}
	if v.Kind == model.KindManagedVault {
		s -= 10 // discretionary manager
	}
	return round1(clamp(s, 0, 100))
}

// NetScore converts risk into a yield cost, so that "high APY, high risk" does not
// automatically win: every 5 risk points above 50 cost 25 bps of annual yield.
func NetScore(apy float64, score float64) float64 {
	return round2(apy - 0.05*max(0, score-50))
}

// PortfolioRisk adds a correlation penalty: two "different protocols" backed by the same
// collateral are not diversification.
func PortfolioRisk(legs []model.PlanLeg, venues map[string]model.Venue, lambda float64) (total, base, penalty float64) {
	var sumW float64
	for _, l := range legs {
		sumW += l.AmountUSD
	}
	if sumW == 0 {
		return 0, 0, 0
	}
	for _, l := range legs {
		w := l.AmountUSD / sumW
		base += w * l.Risk
	}
	for i := 0; i < len(legs); i++ {
		for j := i + 1; j < len(legs); j++ {
			wi := legs[i].AmountUSD / sumW
			wj := legs[j].AmountUSD / sumW
			shared := sharedDeps(venues[legs[i].VenueID], venues[legs[j].VenueID])
			switch shared {
			case 1:
				penalty += wi * wj * 12
			case 2:
				penalty += wi * wj * 20
			}
		}
	}
	return round1(base + lambda*penalty), round1(base), round1(lambda * penalty)
}

func sharedDeps(a, b model.Venue) int {
	n := 0
	set := map[string]bool{}
	for _, u := range a.Underlying {
		set[strings.ToLower(u)] = true
	}
	for _, u := range b.Underlying {
		lu := strings.ToLower(u)
		for k := range set {
			if strings.Contains(k, lu) || strings.Contains(lu, k) {
				n++
				break
			}
		}
	}
	return n
}

// InputHash is the fingerprint of the curated inputs behind a score, so that a user can
// verify a score was not silently re-weighted.
func InputHash(v model.Venue) string {
	parts := []string{
		v.ID, fmt.Sprint(v.Audits), fmt.Sprint(v.Tier1Audit), fmt.Sprint(v.LiveMonths),
		fmt.Sprint(v.Upgradeable), fmt.Sprintf("%.3f", v.OracleQuality),
		fmt.Sprintf("%.3f", v.RewardDependence), fmt.Sprint(v.YieldSourceVerified),
		strings.Join(v.Underlying, "|"),
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, ";")))
	return hex.EncodeToString(sum[:8])
}

// ---- small helpers (kept local so the model stays dependency-free) ----

func clamp(x, lo, hi float64) float64 { return max(lo, min(hi, x)) }
func min(a, b float64) float64        { if a < b { return a }; return b }
func max(a, b float64) float64        { if a > b { return a }; return b }
func mabs(a float64) float64          { if a < 0 { return -a }; return a }
func round1(x float64) float64        { return float64(int(x*10+0.5)) / 10 }
func round2(x float64) float64        { return float64(int(x*100+0.5)) / 100 }
