// Package carry measures the one thing a dollar saver is never shown: what a
// deposit in another currency actually paid, in dollars, after the currency moved.
//
// This is the honest version of "put your money where interest is high". A 37%
// lira deposit is not a 37% return to someone who spends dollars; it is 37% minus
// whatever the lira lost. The package computes the realised number from public FX
// data and states the break-even depreciation, so the trade can be judged instead
// of sold.
//
// Nothing here is a product we offer. It is a measurement, published because the
// alternative — quoting headline rates — is how people lose money.
package carry

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"time"
)

// Version of the model and of the rate registry that ships with it.
const (
	Version         = "carry-model-v1.0"
	RegistryVersion = "rates-2026-10-21"
	FXSource        = "ECB reference rates via frankfurter.dev"
)

// CountryRate is a policy rate plus context. Every row carries its own source and
// date: a rate without a date is a rumour.
type CountryRate struct {
	Code      string   `json:"code"`
	Country   string   `json:"country"`
	Currency  string   `json:"currency"`
	Policy    float64  `json:"policy_rate"`
	Inflation *float64 `json:"inflation,omitempty"` // nil = not published in this build
	RateName  string   `json:"rate_name"`
	Source    string   `json:"source"`
	AsOf      string   `json:"as_of"`
}

// Registry is the dated set of policy rates. Hand-maintained on purpose: central
// bank APIs are inconsistent, and a wrong number with a plausible timestamp is
// worse than a number a human signed.
func Registry() (asOf time.Time, rows []CountryRate) {
	tr := 29.73
	return time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC), []CountryRate{
		{Code: "US", Country: "United States", Currency: "USD", Policy: 3.88, Inflation: nil,
			RateName: "Fed funds rate", Source: "Federal Reserve H.15, 2026-10-20", AsOf: "2026-10-20"},
		{Code: "EU", Country: "Euro area", Currency: "EUR", Policy: 2.50, Inflation: nil,
			RateName: "ECB rate", Source: "ECB, effective 2026-09-16", AsOf: "2026-09-16"},
		{Code: "TR", Country: "Türkiye", Currency: "TRY", Policy: 37.00, Inflation: &tr,
			RateName: "CBRT rate", Source: "CBRT MPC 2026-09-10; TÜİK CPI Sep 2026", AsOf: "2026-09-10"},
		{Code: "BR", Country: "Brazil", Currency: "BRL", Policy: 13.75, Inflation: nil,
			RateName: "Selic rate", Source: "BCB Copom, 2026-09-16", AsOf: "2026-09-16"},
		{Code: "MX", Country: "Mexico", Currency: "MXN", Policy: 6.50, Inflation: nil,
			RateName: "Banxico rate", Source: "Banxico, 2026-09-24", AsOf: "2026-09-24"},
		{Code: "IN", Country: "India", Currency: "INR", Policy: 5.50, Inflation: nil,
			RateName: "RBI rate", Source: "RBI MPC, 2026-10-07", AsOf: "2026-10-07"},
	}
}

// Row is one country, measured.
type Row struct {
	Code       string   `json:"code"`
	Country    string   `json:"country"`
	Currency   string   `json:"currency"`
	PolicyRate float64  `json:"policy_rate"`
	Inflation  *float64 `json:"inflation,omitempty"`
	RealRate   *float64 `json:"real_rate,omitempty"`
	RateName   string   `json:"rate_name"`

	SpotNow     float64 `json:"spot_now"`      // local units per USD today
	SpotYearAgo float64 `json:"spot_year_ago"` // local units per USD a year ago
	// CurrencyMove is the % change of the local currency against the dollar:
	// negative = the currency lost value.
	CurrencyMove float64 `json:"currency_move_pct"`

	// LocalReturnUSD is what USD 1 became: (1+policy) × spotYearAgo/spotNow − 1.
	// Realised, not promised: it uses the actual path of the currency.
	LocalReturnUSD float64 `json:"local_return_usd_pct"`

	// BreakevenDepreciation is how much the currency may fall before the carry
	// stops beating holding dollars: policy − USD policy rate.
	BreakevenDepreciation float64 `json:"breakeven_depreciation_pct"`

	// VolAnnual is the annualised volatility of the daily rate moves.
	VolAnnual float64 `json:"vol_annual_pct"`
	// MaxDrawdown is the worst peak-to-trough move of the currency over the year.
	// A managed currency can look calm for months and then step: volatility alone
	// would hand it a good grade, this column is what stops that.
	MaxDrawdown float64 `json:"max_drawdown_pct"`

	Band      string  `json:"band"`    // A–D, currency risk
	Verdict   string  `json:"verdict"` // paid / did not pay / not enough data
	ExcessBps float64 `json:"excess_vs_usd_bps"`
	Days      int     `json:"days"`
	Source    string  `json:"source"`
	AsOf      string  `json:"as_of"`
}

// Report is the whole board.
type Report struct {
	Version      string    `json:"version"`
	Registry     string    `json:"registry"`
	GeneratedAt  time.Time `json:"generated_at"`
	WindowDays   int       `json:"window_days"`
	BenchmarkAPY float64   `json:"benchmark_apy"` // Stellar USDC blended, for context
	Rows         []Row     `json:"rows"`
	Note         string    `json:"note"`
}

type fxSeries struct {
	Base  string                        `json:"base"`
	Rates map[string]map[string]float64 `json:"rates"`
}

// Analyse builds the report. benchmark is the USDC yield the user would otherwise
// earn; it is only used in the note and in the excess column.
func Analyse(ctx context.Context, c *http.Client, benchmark float64, now time.Time) (Report, error) {
	reg, rates := Registry()
	currencies := make([]string, 0, len(rates))
	for _, r := range rates {
		currencies = append(currencies, r.Currency)
	}

	start := now.AddDate(-1, 0, 0).Format("2006-01-02")
	end := now.Format("2006-01-02")
	url := fmt.Sprintf("https://api.frankfurter.dev/v1/%s..%s?base=USD&symbols=%s",
		start, end, join(currencies))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Report{}, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return Report{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Report{}, fmt.Errorf("fx provider returned %d", resp.StatusCode)
	}
	var series fxSeries
	if err := json.NewDecoder(resp.Body).Decode(&series); err != nil {
		return Report{}, err
	}

	dates := make([]string, 0, len(series.Rates))
	for d := range series.Rates {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	if len(dates) < 30 {
		return Report{}, fmt.Errorf("not enough fx history: %d days", len(dates))
	}
	first, last := dates[0], dates[len(dates)-1]

	usdPolicy := 0.0
	for _, r := range rates {
		if r.Code == "US" {
			usdPolicy = r.Policy
		}
	}

	report := Report{
		Version:      Version,
		Registry:     RegistryVersion,
		GeneratedAt:  now.UTC(),
		WindowDays:   len(dates),
		BenchmarkAPY: benchmark,
		Note: "Real one-year return of a dollar deposited at each country's central bank rate, " +
			"converted back to dollars at today's rate. Uses the actual currency path, not a forecast. " +
			"A managed currency can post a low volatility and still step sharply; the drawdown column exists for that. " +
			"Not an offer, not advice, and not a product: no cross-border deposit is available through Keelson.",
	}

	for _, r := range rates {
		row := Row{
			Code: r.Code, Country: r.Country, Currency: r.Currency,
			PolicyRate: r.Policy, RateName: r.RateName,
			Source: r.Source, AsOf: r.AsOf,
			Days: len(dates),
		}
		if r.Inflation != nil {
			v := r.Policy - *r.Inflation
			row.Inflation = r.Inflation
			row.RealRate = &v
		}

		spotYearAgo, ok1 := series.Rates[first][r.Currency]
		spotNow, ok2 := series.Rates[last][r.Currency]
		if !ok1 || !ok2 || spotNow <= 0 || spotYearAgo <= 0 {
			row.Band, row.Verdict = "D", "not enough data"
			report.Rows = append(report.Rows, row)
			continue
		}
		row.SpotNow, row.SpotYearAgo = spotNow, spotYearAgo
		row.CurrencyMove = round2((spotYearAgo/spotNow - 1) * 100)
		row.LocalReturnUSD = round2(((1+r.Policy/100)*(spotYearAgo/spotNow) - 1) * 100)
		row.BreakevenDepreciation = round2(r.Policy - usdPolicy)
		if r.Currency == "USD" {
			row.CurrencyMove, row.LocalReturnUSD, row.VolAnnual, row.MaxDrawdown = 0, 0, 0, 0
			row.Band, row.Verdict, row.ExcessBps = "—", "base", 0
			report.Rows = append(report.Rows, row)
			continue
		}
		row.VolAnnual = round2(annualVol(series.Rates, dates, r.Currency) * 100)
		row.MaxDrawdown = round2(maxDrawdown(series.Rates, dates, r.Currency) * 100)

		switch {
		case row.LocalReturnUSD > benchmark:
			row.Verdict = "paid"
		default:
			row.Verdict = "did not pay"
		}
		row.ExcessBps = round0((row.LocalReturnUSD - benchmark) * 100)
		row.Band = band(row)
		report.Rows = append(report.Rows, row)
	}

	// Riskiest first: the whole point is that the highest rate is not the best one.
	sort.Slice(report.Rows, func(i, j int) bool {
		if report.Rows[i].PolicyRate != report.Rows[j].PolicyRate {
			return report.Rows[i].PolicyRate > report.Rows[j].PolicyRate
		}
		return report.Rows[i].Code < report.Rows[j].Code
	})
	_ = reg
	return report, nil
}

// band grades currency risk. Volatility alone is not enough: a managed currency can
// be calm for months and then step 20% in a week, so the worst drawdown counts as much
// as the day-to-day noise. Nor is drawdown enough on its own: a currency that is walked
// down steadily — a couple of basis points every single day — shows almost no drawdown
// and almost no volatility, yet still hands the user a 15% loss over the year. So the
// twelve-month slide is a third gate: a smooth crawl is not a calm currency.
func band(r Row) string {
	vol, dd := r.VolAnnual, r.MaxDrawdown
	slide := -r.CurrencyMove // positive = the currency bought fewer dollars over the year
	real := 0.0
	if r.RealRate != nil {
		real = *r.RealRate
	}
	switch {
	case vol <= 5 && dd <= 10 && slide <= 5 && real >= 1:
		return "A"
	case vol <= 8 && dd <= 18 && slide <= 12 && real >= -1:
		return "B"
	case vol <= 15 && dd <= 30 && slide <= 25:
		return "C"
	default:
		return "D"
	}
}

// maxDrawdown is the worst peak-to-trough depreciation of the currency over the
// window, as a positive fraction of the peak.
func maxDrawdown(rates map[string]map[string]float64, dates []string, ccy string) float64 {
	peak, worst := 0.0, 0.0
	for _, d := range dates {
		v, ok := rates[d][ccy]
		if !ok || v <= 0 {
			continue
		}
		if v > peak {
			peak = v
		}
		if peak > 0 {
			if dd := (peak - v) / peak; dd > worst {
				worst = dd
			}
		}
	}
	return worst
}

// annualVol is the annualised standard deviation of daily log returns.
func annualVol(rates map[string]map[string]float64, dates []string, ccy string) float64 {
	rets := make([]float64, 0, len(dates))
	for i := 1; i < len(dates); i++ {
		a, ok1 := rates[dates[i-1]][ccy]
		b, ok2 := rates[dates[i]][ccy]
		if !ok1 || !ok2 || a <= 0 || b <= 0 {
			continue
		}
		rets = append(rets, math.Log(b/a))
	}
	if len(rets) < 10 {
		return 0
	}
	mean := 0.0
	for _, r := range rets {
		mean += r
	}
	mean /= float64(len(rets))
	varsum := 0.0
	for _, r := range rets {
		varsum += (r - mean) * (r - mean)
	}
	sd := math.Sqrt(varsum / float64(len(rets)-1))
	// ~252 business days of ECB quotes per year
	return sd * math.Sqrt(252)
}

func join(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ","
		}
		out += x
	}
	return out
}

func round0(x float64) float64 { return math.Round(x) }
func round2(x float64) float64 { return math.Round(x*100) / 100 }
