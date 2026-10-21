package policy

import (
	"testing"

	"github.com/emrealt34/building/backend/internal/model"
)

func venue(id string, apy float64, cap float64, verified bool) model.Venue {
	return model.Venue{
		ID: id, Label: id, Kind: model.KindVault, APY: apy, APYBase: apy, APYMean30d: apy,
		TVLUSD: cap, CapacityUSD: cap, InstantExit: true, Audits: 2, Tier1Audit: true,
		LiveMonths: 18, OracleQuality: 0.7, RewardDependence: 0.1,
		YieldSourceVerified: verified, Underlying: []string{"Blend strategy"},
	}
}

func TestUnverifiedYieldIsRefusedEvenWhenItPaysMost(t *testing.T) {
	p := model.DefaultPolicy()
	venues := []model.Venue{
		venue("managed-10pct", 10.0, 25_000_000, false),
		venue("verified-6pct", 6.0, 20_000_000, true),
	}
	plan := Build(venues, 1_000, p)

	for _, leg := range plan.Legs {
		if leg.VenueID == "managed-10pct" {
			t.Fatal("a venue with unverifiable yield must never receive money")
		}
	}
	if len(plan.Refusals) == 0 || plan.Refusals[0].VenueID != "managed-10pct" {
		t.Fatal("the refusal must be published, with the highest-APY venue first")
	}
}

func TestIdleSleeveIsAlwaysKept(t *testing.T) {
	p := model.DefaultPolicy()
	plan := Build([]model.Venue{venue("v", 6.0, 20_000_000, true)}, 1_000, p)
	minIdle := 1_000 * float64(p.MinIdleBps) / 10_000
	if plan.IdleUSD < minIdle {
		t.Fatalf("idle sleeve %.2f is below the %.2f policy floor", plan.IdleUSD, minIdle)
	}
}

func TestSingleVenueCapIsRespected(t *testing.T) {
	p := model.DefaultPolicy()
	venues := []model.Venue{
		venue("a", 6.0, 20_000_000, true),
		venue("b", 5.9, 20_000_000, true),
	}
	plan := Build(venues, 10_000, p)
	for _, leg := range plan.Legs {
		if leg.WeightBps > p.MaxVenueBps {
			t.Fatalf("venue %s got %d bps, above the %d bps cap", leg.VenueID, leg.WeightBps, p.MaxVenueBps)
		}
	}
}

func TestSmallVenueIsRefusedForExitLiquidity(t *testing.T) {
	p := model.DefaultPolicy()
	plan := Build([]model.Venue{venue("tiny", 7.5, 7_500, true)}, 1_000, p)
	if len(plan.Legs) != 0 {
		t.Fatal("a $7.5k venue cannot absorb a savings position safely")
	}
	if len(plan.Refusals) == 0 {
		t.Fatal("the refusal must be explained to the user")
	}
}

func TestOutlierRateIsTreatedAsDistress(t *testing.T) {
	p := model.DefaultPolicy()
	v := venue("spike", 60.0, 20_000_000, true)
	v.APYMean30d = 6.0 // 10x the median
	ok, reason := eligible(v, p)
	policy := Build([]model.Venue{v}, 1_000, p)
	if ok {
		t.Fatal("a rate 10x its 30-day median must be refused")
	}
	if reason == "" || len(policy.Refusals) == 0 {
		t.Fatal("refusal must carry a reason")
	}
}

func TestShouldMoveRespectsCooldownAndThreshold(t *testing.T) {
	p := model.DefaultPolicy()
	cur := venue("cur", 5.0, 20_000_000, true)
	best := venue("best", 5.2, 20_000_000, true) // +20 bps

	if move, _ := ShouldMove(cur, best, 30, p); move {
		t.Fatal("20 bps is below the 25 bps threshold: must not move")
	}

	best.APY = 5.5 // +50 bps
	if move, _ := ShouldMove(cur, best, 1, p); move {
		t.Fatal("cooldown must block an early move")
	}
	if move, reason := ShouldMove(cur, best, 30, p); !move {
		t.Fatalf("50 bps after cooldown should move, got: %s", reason)
	}
}

func TestNoOpIsAValidOutcome(t *testing.T) {
	p := model.DefaultPolicy()
	v := venue("only", 6.0, 20_000_000, true)
	if move, reason := ShouldMove(v, v, 30, p); move {
		t.Fatalf("staying put must be allowed, got: %s", reason)
	}
}
