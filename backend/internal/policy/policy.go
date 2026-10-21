// Package policy turns venue data into an allocation. This is the "cherry picker":
// eligibility -> ranking -> capacity-aware weighting -> refusals.
//
// It is deliberately dumb and explainable. Every refusal carries a human sentence,
// because the refusal list is the product's main trust signal.
package policy

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/emrealt34/keelson/backend/internal/model"
	"github.com/emrealt34/keelson/backend/internal/risk"
)

// Annotate fills the derived fields (risk, net score, cap, eligibility) on each venue.
func Annotate(venues []model.Venue, depositUSD float64, p model.Policy, now time.Time) {
	for i := range venues {
		v := &venues[i]
		r := risk.Compute(*v, depositUSD, now)
		v.Risk = &r
		v.NetScore = risk.NetScore(v.APY, r.Score)
		cap := p.MaxVenueShareOfCap * v.CapacityUSD
		if cap > 250_000 {
			cap = 250_000 // operational ceiling per venue, regardless of size
		}
		v.MaxPosUSD = round0(cap)
		v.Eligible, v.Refusal = eligible(*v, p)
	}
}

func eligible(v model.Venue, p model.Policy) (bool, string) {
	switch {
	case !v.YieldSourceVerified:
		return false, "We cannot explain where the interest comes from — part of it looks like a temporary promotion rather than borrowers paying. Until we can tell the two apart, no new money goes in."
	case v.Risk == nil:
		return false, "We do not have enough information to judge how safe it is yet."
	case v.Risk.Band == model.BandD:
		return false, "It fails our safety check, so no new money goes in."
	case v.APY <= 0:
		return false, "It is not paying anything right now."
	case v.CapacityUSD < p.MinCapacityUSD:
		return false, fmt.Sprintf("Too small to be worth it: only $%.0f is available to take out, which is not enough to guarantee you could leave quickly.", v.CapacityUSD)
	case v.MaxPosUSD < p.MinPositionUSD:
		return false, fmt.Sprintf("The most we would ever put here ($%.0f) is below the smallest position worth opening ($%.0f).", v.MaxPosUSD, p.MinPositionUSD)
	case int(v.UtilizationB) > p.MaxVenueUtilizationBps:
		return false, fmt.Sprintf("%.0f%% of the money lent here is already out the door, which is more than we allow — getting your money back could take longer than we promise.", float64(v.UtilizationB)/100)
	case v.APYMean30d > 0 && v.APY > 3*v.APYMean30d:
		return false, fmt.Sprintf("%.2f%% is more than three times this place's own normal rate. That usually means a shortage of supply, not free money.", v.APY)
	}
	return true, ""
}

// Build creates a plan for a deposit. The engine never promises a rate; it returns a
// transparent allocation plus every venue it refused and why.
func Build(venues []model.Venue, depositUSD float64, p model.Policy) model.Plan {
	Annotate(venues, depositUSD, p, time.Now().UTC())

	plan := model.Plan{
		DepositUSD:    depositUSD,
		PolicyVersion: p.Version,
		GeneratedAt:   time.Now().UTC(),
		Legs:          []model.PlanLeg{},
		Refusals:      []model.Refusal{},
	}

	byID := map[string]model.Venue{}
	var cands []model.Venue
	for _, v := range venues {
		byID[v.ID] = v
		if v.Eligible {
			cands = append(cands, v)
		} else {
			plan.Refusals = append(plan.Refusals, model.Refusal{
				VenueID: v.ID, Label: v.Label, APY: v.APY, Reason: v.Refusal,
			})
		}
	}

	sort.SliceStable(cands, func(i, j int) bool { return cands[i].NetScore > cands[j].NetScore })
	sort.SliceStable(plan.Refusals, func(i, j int) bool { return plan.Refusals[i].APY > plan.Refusals[j].APY })

	// At least min_idle_bps stays liquid, always.
	idle := depositUSD * float64(p.MinIdleBps) / 10_000
	invest := depositUSD - idle

	// Allocate by net score, then cap each venue and redistribute the excess.
	// Concentration in the single best venue is what a naive router does; diversification
	// is what makes an automated product safe to leave running.
	alloc := make(map[string]float64, len(cands))
	caps := make(map[string]float64, len(cands))
	var scoreSum float64
	for _, v := range cands {
		if v.NetScore > 0 {
			scoreSum += v.NetScore
		} else {
			scoreSum += 0.01
		}
	}
	for _, v := range cands {
		w := v.NetScore
		if w <= 0 {
			w = 0.01
		}
		c := v.MaxPosUSD
		if capByPolicy := depositUSD * float64(p.MaxVenueBps) / 10_000; c > capByPolicy {
			c = capByPolicy
		}
		caps[v.ID] = c
		alloc[v.ID] = invest * w / scoreSum
	}
	for iter := 0; iter < 6; iter++ {
		var excess float64
		var freeSum float64
		for _, v := range cands {
			if alloc[v.ID] > caps[v.ID] {
				excess += alloc[v.ID] - caps[v.ID]
				alloc[v.ID] = caps[v.ID]
			} else if alloc[v.ID] < caps[v.ID] {
				freeSum += caps[v.ID] - alloc[v.ID]
			}
		}
		if excess < 0.5 || freeSum <= 0 {
			break
		}
		for _, v := range cands {
			if alloc[v.ID] < caps[v.ID] {
				room := caps[v.ID] - alloc[v.ID]
				alloc[v.ID] += excess * room / freeSum
			}
		}
	}

	var total float64
	for _, v := range cands {
		a := round2(alloc[v.ID])
		if a < p.MinPositionUSD {
			if a > 0 {
				plan.Refusals = append(plan.Refusals, model.Refusal{
					VenueID: v.ID, Label: v.Label, APY: v.APY,
					Reason: fmt.Sprintf("Allocation ($%.0f) is below the $%.0f minimum order size.", a, p.MinPositionUSD),
				})
			}
			continue
		}
		plan.Legs = append(plan.Legs, model.PlanLeg{
			VenueID: v.ID, Label: v.Label, AmountUSD: a,
			WeightBps: int(10000 * a / depositUSD),
			APY:       v.APY, RiskBand: v.Risk.Band, Risk: v.Risk.Score,
		})
		total += a
	}

	plan.IdleUSD = round2(depositUSD - total)
	if total > 0 {
		var wsum float64
		for _, l := range plan.Legs {
			wsum += l.AmountUSD * l.APY
		}
		plan.BlendedAPY = round2(wsum / total)
	}
	plan.PortfolioRisk, plan.PortfolioRiskBase, plan.CorrelationPenalty =
		risk.PortfolioRisk(plan.Legs, byID, 0.5)
	return plan
}

// ImprovementBps is the yield difference between two venues, in basis points.
func ImprovementBps(from, to float64) float64 { return round2((to - from) * 100) }

// ShouldMove applies the anti-churn rules that make automation safe to leave running.
// It returns a decision plus a human-readable reason; "no-op" is a valid, logged outcome.
func ShouldMove(currentVenue, bestVenue model.Venue, daysSinceMove int, p model.Policy) (bool, string) {
	if !p.RebalanceEnabled {
		return false, "Automatic moving is switched off for this account."
	}
	if daysSinceMove*86400 < p.CooldownSecs {
		return false, fmt.Sprintf("We moved money recently; we wait %d days between moves and only %d have passed.", p.CooldownSecs/86400, daysSinceMove)
	}
	gap := ImprovementBps(currentVenue.APY, bestVenue.APY)
	if gap < float64(p.MinImprovementBps) {
		return false, fmt.Sprintf("The other place pays only %.2f%% more a year, which is not worth the move.", gap/100)
	}
	if strings.EqualFold(currentVenue.ID, bestVenue.ID) {
		return false, "Your money is already where it pays best."
	}
	return true, fmt.Sprintf("The other place pays %.2f%% more a year — worth the move.", gap/100)
}

func round0(x float64) float64 { return float64(int(x + 0.5)) }
func round2(x float64) float64 { return float64(int(x*100+0.5)) / 100 }
