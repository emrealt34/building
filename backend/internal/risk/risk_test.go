package risk

import (
	"testing"
	"time"

	"github.com/emrealt34/building/backend/internal/model"
)

func baseVenue() model.Venue {
	return model.Venue{
		ID: "test:vault", Label: "Test vault", Kind: model.KindVault,
		APY: 6.0, APYBase: 6.0, APYMean30d: 6.0,
		TVLUSD: 20_000_000, CapacityUSD: 20_000_000,
		InstantExit: true, Audits: 2, Tier1Audit: true, LiveMonths: 18,
		OracleQuality: 0.7, RewardDependence: 0.1,
		YieldSourceVerified: true, Underlying: []string{"Blend strategy"},
	}
}

func TestScoreIsBoundedAndStable(t *testing.T) {
	v := baseVenue()
	now := time.Now()
	a := Compute(v, 1_000, now)
	b := Compute(v, 1_000, now)
	if a.Score != b.Score {
		t.Fatalf("score is not deterministic: %v vs %v", a.Score, b.Score)
	}
	if a.Score < 0 || a.Score > 100 {
		t.Fatalf("score out of range: %v", a.Score)
	}
	if a.InputHash == "" {
		t.Fatal("input hash must be published")
	}
}

func TestUnverifiedYieldRaisesDurabilityRisk(t *testing.T) {
	verified := baseVenue()
	unverified := baseVenue()
	unverified.YieldSourceVerified = false
	unverified.RewardDependence = 0.5

	r1 := Compute(verified, 1_000, time.Now())
	r2 := Compute(unverified, 1_000, time.Now())

	if r2.Subs["durability"] <= r1.Subs["durability"] {
		t.Fatalf("unverifiable yield must be scored riskier: %.1f vs %.1f",
			r2.Subs["durability"], r1.Subs["durability"])
	}
}

func TestProtocolCodeIncidentWeighsMoreThanPoolConfig(t *testing.T) {
	code := baseVenue()
	code.Incidents = []model.Incident{{Type: "exploit", LossUSD: 5_000_000, MonthsAgo: 2, Scope: "protocol_code"}}
	config := baseVenue()
	config.Incidents = []model.Incident{{Type: "oracle", LossUSD: 5_000_000, MonthsAgo: 2, Scope: "pool_config"}}

	c1 := ScoreContract(code)
	c2 := ScoreContract(config)
	if c1 <= c2 {
		t.Fatalf("protocol-code incident must be penalised more: %.1f vs %.1f", c1, c2)
	}
}

func TestLiquidityRiskGrowsWithOurShareOfTheVenue(t *testing.T) {
	small := baseVenue()
	small.CapacityUSD = 200_000
	small.TVLUSD = 200_000
	big := baseVenue()

	rSmall := ScoreLiquidity(small, 100_000) // 50% of the venue
	rBig := ScoreLiquidity(big, 100_000)     // 0.5% of the venue
	if rSmall <= rBig {
		t.Fatalf("a position that is a large share of a venue must be riskier: %.1f vs %.1f", rSmall, rBig)
	}
}

func TestBackstopExposureIsPunishedForContagion(t *testing.T) {
	safe := baseVenue()
	risky := baseVenue()
	risky.Underlying = []string{"Blend pool", "Comet backstop LP (indirect)"}
	if ScoreContagion(risky) <= ScoreContagion(safe) {
		t.Fatal("backstop dependency must raise contagion risk (Aug 2026 lesson)")
	}
}

func TestNetScoreLetsSafetyBeatAPY(t *testing.T) {
	// 6.9% at risk 70 vs 5.9% at risk 47: the safer venue must win on net score.
	risky := NetScore(6.92, 70)
	safe := NetScore(5.94, 47)
	if safe <= risky {
		t.Fatalf("safe venue should outrank the risky one on net score: %.2f vs %.2f", safe, risky)
	}
}

func TestPortfolioRiskAddsCorrelationPenalty(t *testing.T) {
	a := baseVenue()
	a.ID = "a"
	a.Underlying = []string{"Blend strategy"}
	b := baseVenue()
	b.ID = "b"
	b.Underlying = []string{"Blend strategy"} // same dependency

	legs := []model.PlanLeg{
		{VenueID: "a", AmountUSD: 500, Risk: 50},
		{VenueID: "b", AmountUSD: 500, Risk: 50},
	}
	total, base, penalty := PortfolioRisk(legs, map[string]model.Venue{"a": a, "b": b}, 0.5)
	if penalty <= 0 {
		t.Fatal("shared dependency must produce a correlation penalty")
	}
	if total <= base {
		t.Fatalf("total must exceed base: %.2f vs %.2f", total, base)
	}
}
