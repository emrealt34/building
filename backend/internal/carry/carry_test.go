package carry

import (
	"math"
	"testing"
)

func TestCurrencyMoveAndReturn(t *testing.T) {
	// A currency that lost 20% against the dollar while paying 37%:
	//   spotYearAgo = 40, spotNow = 50  →  local currency lost 20%
	//   USD return  = 1.37 × 40/50 − 1  = 9.6%
	spotYearAgo, spotNow, policy := 40.0, 50.0, 37.0
	move := (spotYearAgo/spotNow - 1) * 100
	ret := ((1+policy/100)*(spotYearAgo/spotNow) - 1) * 100
	if math.Abs(move-(-20)) > 1e-9 {
		t.Fatalf("currency move = %v, want -20", move)
	}
	if math.Abs(ret-9.6) > 1e-9 {
		t.Fatalf("usd return = %v, want 9.6", ret)
	}
}

func TestBreakevenDepreciation(t *testing.T) {
	// The rate difference is exactly the depreciation the currency may absorb
	// before the carry stops paying: 37% vs 3.88% → 33.12 percentage points.
	if got := 37.0 - 3.88; math.Abs(got-33.12) > 1e-9 {
		t.Fatalf("breakeven = %v", got)
	}
}

func TestBandNeverRewardsAHighRateAlone(t *testing.T) {
	real := 7.0
	// High policy rate, positive real rate, but a wildly volatile currency: not an A.
	risky := Row{PolicyRate: 37, RealRate: &real, VolAnnual: 30}
	if got := band(risky); got != "D" {
		t.Fatalf("band for vol 30 = %s, want D", got)
	}
	// Same rate, calm currency: better than C.
	calm := Row{PolicyRate: 37, RealRate: &real, VolAnnual: 3}
	if got := band(calm); got != "A" {
		t.Fatalf("band for vol 3 = %s, want A", got)
	}
}

func TestAnnualVolOfFlatSeriesIsZero(t *testing.T) {
	dates := []string{"d1", "d2", "d3", "d4", "d5", "d6", "d7", "d8", "d9", "d10", "d11", "d12"}
	rates := map[string]map[string]float64{}
	for _, d := range dates {
		rates[d] = map[string]float64{"XXX": 42.0}
	}
	if got := annualVol(rates, dates, "XXX"); got != 0 {
		t.Fatalf("flat series vol = %v, want 0", got)
	}
}

func TestRegistryIsDatedAndSourced(t *testing.T) {
	asOf, rows := Registry()
	if asOf.IsZero() {
		t.Fatal("registry must carry a date")
	}
	if len(rows) < 5 {
		t.Fatalf("want at least 5 countries, got %d", len(rows))
	}
	for _, r := range rows {
		if r.Source == "" || r.AsOf == "" {
			t.Fatalf("%s has no source/date", r.Code)
		}
		if r.Policy <= 0 {
			t.Fatalf("%s has a non-positive policy rate", r.Code)
		}
	}
}
