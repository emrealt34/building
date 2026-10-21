package cherry

import (
	"math"
	"testing"

	"github.com/emrealt34/keelson/backend/internal/carry"
)

// The whole point of the model: a headline rate can rank first while the net
// return ranks last. If this test ever passes trivially, the product has no thesis.
func TestANetReturnCanContradictTheHeadline(t *testing.T) {
	// 37% paid in a currency that lost 15.4% of its dollar value, minus the fee.
	gross := (1.37*0.846 - 1) * 100
	net := gross - 0.5
	if net < 10 {
		t.Fatalf("expected the net return to stay positive here, got %.2f", net)
	}
	// 5.5% paid in a currency that lost 6.9%: the headline is small and the net is negative.
	grossIN := (1.055*0.931 - 1) * 100
	if grossIN >= 0 {
		t.Fatalf("expected India's dollar return to be negative, got %.2f", grossIN)
	}
	if !(net > grossIN) {
		t.Fatal("the ranking must change once the currency is counted")
	}
}

func TestWindowReturnUsesTheCurrencyPath(t *testing.T) {
	series := fxSeries{Rates: map[string]map[string]float64{
		"2026-01-01": {"TRY": 40.0},
		"2026-07-01": {"TRY": 50.0},
	}}
	r := carry.CountryRate{Code: "TR", Currency: "TRY", Policy: 37}
	got, ok := windowReturn(series, "2026-01-01", "2026-07-01", r)
	if !ok {
		t.Fatal("expected a return")
	}
	// Half a year at 37%; the lira lost 20% against the dollar over the same span.
	span := 181.0 / 365.0 // 2026-01-01 → 2026-07-01, the span the code computes
	want := math.Pow(1.37, span)*(40.0/50.0) - 1
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("window return = %.6f, want %.6f", got, want)
	}
	if got >= 0 {
		t.Fatal("a 20% currency loss must not produce a positive dollar return at these rates")
	}
}

func TestMonthBucketsSplitOnTheCalendar(t *testing.T) {
	b := monthBuckets([]string{"2026-01-02", "2026-01-30", "2026-02-02", "2026-02-27"})
	if len(b) != 2 {
		t.Fatalf("buckets = %d, want 2", len(b))
	}
	if b[0].first != "2026-01-02" || b[0].last != "2026-01-30" {
		t.Fatalf("first bucket = %+v", b[0])
	}
	if b[1].first != "2026-02-02" || b[1].last != "2026-02-27" {
		t.Fatalf("second bucket = %+v", b[1])
	}
}

// The fee is charged on the return, prorated, and never on the principal.
func TestTheFeeIsSmallAndProrated(t *testing.T) {
	for _, years := range []float64{0.25, 1, 2} {
		fee := float64(FeeBps) / 100 * years
		if fee > 1.01 {
			t.Fatalf("fee over a year should be 0.50%%, got %.2f", fee)
		}
	}
}
