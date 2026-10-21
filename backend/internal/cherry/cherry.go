// Package cherry answers the question the product is actually built on: if you
// kept your money where the *net* return is highest — after the currency moved,
// after local inflation, after our fee — what would you have earned?
//
// It exists because the obvious version of "chase the highest rate" is wrong in a
// way that costs people money. Türkiye pays 37% and its inflation is 29.7%; India
// pays 5.5% with 5.5% inflation. The headline ranks Türkiye first and India last.
// The net number, expressed in the dollars the saver actually spends, ranks them
// differently. This package computes that number from public data and simulates
// three ways of choosing: by headline rate, by net rate, and by perfect hindsight.
//
// Nothing here is an offer. It is the measurement that tells us whether the product
// idea is worth building, and the page it feeds says exactly that.
package cherry

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/emrealt34/keelson/backend/internal/carry"
)

const (
	Version   = "cherry-model-v1.0"
	FXSource  = "ECB reference rates via frankfurter.dev"
	FeeBps    = 50 // our fee: 0.50% a year, charged on what the money earns, never on the principal
	Benchmark = 0  // idle dollars earn nothing
)

// Market is one country's rate, its inflation, and what a dollar actually earned
// there over the window once the currency moved.
type Market struct {
	Code       string  `json:"code"`
	Country    string  `json:"country"`
	Currency   string  `json:"currency"`
	RateName   string  `json:"rate_name"`
	Source     string  `json:"source"`
	AsOf       string  `json:"as_of"`
	PolicyRate float64 `json:"policy_rate"`
	Inflation  float64 `json:"inflation_pct"`

	// FXMove is the currency's move against the dollar over the window: positive
	// means it bought more dollars at the end than at the start.
	FXMove float64 `json:"fx_move_pct"`
	// GrossUSD is the headline rate earned in local currency, then converted back
	// to dollars at the rate that actually prevailed.
	GrossUSD float64 `json:"gross_usd_pct"`
	// NetUSD is GrossUSD less our fee, prorated to the window.
	NetUSD float64 `json:"net_usd_pct"`
	// RealUSD is NetUSD measured against US inflation: the purchasing power a
	// dollar saver actually gained.
	RealUSD float64 `json:"real_usd_pct"`
	// RealLocal is the local rate minus local inflation: what the number in the
	// advertisement means to somebody who lives there.
	RealLocal float64 `json:"real_local_pct"`

	Days int `json:"days"`
}

// Leg is one month of a strategy: which market was held, and what it returned.
type Leg struct {
	From        string  `json:"from"`
	To          string  `json:"to"`
	Code        string  `json:"code"`
	Country     string  `json:"country"`
	ReturnPct   float64 `json:"return_pct"`
	ChosenOnPct float64 `json:"chosen_on_pct"` // what the rule saw when it made the choice
}

// Strategy is one way of choosing, replayed over the same year.
type Strategy struct {
	Key       string  `json:"key"`
	Label     string  `json:"label"`
	ReturnPct float64 `json:"return_pct"` // for the whole window, in dollars
	Note      string  `json:"note"`
	Legs      []Leg   `json:"legs,omitempty"`
}

type Report struct {
	Version     string     `json:"version"`
	WindowDays  int        `json:"window_days"`
	Start       string     `json:"start"`
	End         string     `json:"end"`
	FeeBps      int        `json:"fee_bps"`
	USInflation float64    `json:"us_inflation_pct"`
	Markets     []Market   `json:"markets"`
	Strategies  []Strategy `json:"strategies"`
	Note        string     `json:"note"`
	Registry    string     `json:"registry"`
}

type fxSeries struct {
	Rates map[string]map[string]float64 `json:"rates"`
}

// Analyse fetches the FX path and replays the year. now is passed in so the result
// is reproducible in a snapshot build.
func Analyse(ctx context.Context, c *http.Client, now time.Time) (Report, error) {
	reg, rows := carry.Registry()
	inflation := map[string]float64{}
	byCode := map[string]carry.CountryRate{}
	currencies := make([]string, 0, len(rows))
	for _, r := range rows {
		byCode[r.Code] = r
		currencies = append(currencies, r.Currency)
	}

	// September 2026 CPI, year on year. Hand-maintained and dated for the same
	// reason the rate registry is: a wrong number with a plausible timestamp is
	// worse than a number a human signed.
	cpi := map[string]struct {
		v      float64
		source string
	}{
		"US": {3.70, "BLS CPI, Sep 2026"},
		"EU": {3.80, "Eurostat HICP, Sep 2026"},
		"TR": {29.73, "TÜİK CPI, Sep 2026"},
		"BR": {4.58, "IBGE IPCA, Sep 2026"},
		"MX": {3.45, "INEGI CPI, Sep 2026"},
		"IN": {5.54, "MoSPI CPI, Sep 2026"},
	}
	for code, v := range cpi {
		inflation[code] = v.v
	}
	usInflation := inflation["US"]

	start := now.AddDate(-1, 0, 0).Format("2006-01-02")
	end := now.Format("2006-01-02")
	series, err := fetchFX(ctx, c, start, end, currencies)
	if err != nil {
		return Report{}, err
	}

	dates := make([]string, 0, len(series.Rates))
	for d := range series.Rates {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	if len(dates) < 60 {
		return Report{}, fmt.Errorf("only %d quotes returned, need at least 60", len(dates))
	}
	first, last := dates[0], dates[len(dates)-1]
	days := int(math.Round(mustParse(last).Sub(mustParse(first)).Hours() / 24))
	years := float64(days) / 365

	markets := make([]Market, 0, len(rows))
	for _, r := range rows {
		m := Market{
			Code: r.Code, Country: r.Country, Currency: r.Currency,
			RateName: r.RateName, Source: r.Source, AsOf: r.AsOf,
			PolicyRate: r.Policy, Inflation: inflation[r.Code], Days: days,
		}
		a, b := 1.0, 1.0
		if r.Currency != "USD" { // the FX feed quotes everything against the dollar, itself excluded
			var ok1, ok2 bool
			a, ok1 = series.Rates[first][r.Currency]
			b, ok2 = series.Rates[last][r.Currency]
			if !ok1 || !ok2 || a <= 0 || b <= 0 {
				markets = append(markets, m)
				continue
			}
			m.FXMove = round2((a/b - 1) * 100)
		}
		// Local rate compounded for the window, multiplied by the currency move.
		local := math.Pow(1+r.Policy/100, years)
		fx := a / b
		m.GrossUSD = round2((local*fx - 1) * 100)
		m.NetUSD = round2(m.GrossUSD - float64(FeeBps)/100*years)
		m.RealUSD = round2(((1+m.NetUSD/100)/(1+usInflation/100) - 1) * 100)
		m.RealLocal = round2(r.Policy - inflation[r.Code])
		markets = append(markets, m)
	}

	// Highest net return first: the ranking the product is built on.
	sort.Slice(markets, func(i, j int) bool {
		if markets[i].NetUSD != markets[j].NetUSD {
			return markets[i].NetUSD > markets[j].NetUSD
		}
		return markets[i].Code < markets[j].Code
	})

	rep := Report{
		Version:     Version,
		WindowDays:  days,
		Start:       first,
		End:         last,
		FeeBps:      FeeBps,
		USInflation: usInflation,
		Markets:     markets,
		Registry:    reg.Format("2006-01-02"),
		Note: "Net return is what a dollar actually earned: the local central bank rate, multiplied by the currency's " +
			"actual move against the dollar, minus our fee, measured against US inflation. This is a measurement of " +
			"public data, not a product: we cannot open a deposit in another country for you, and nothing here is an offer.",
	}

	rep.Strategies = []Strategy{
		chase(series, dates, byCode, "month", "nominal", "Chase the headline rate",
			"Every month, move to whichever country advertises the highest rate. The intuitive strategy, and the one the advert wants."),
		chase(series, dates, byCode, "month", "net", "Chase the net return, monthly",
			"Every month, move to whichever country actually paid the most in dollars the month before — after the currency moved."),
		chase(series, dates, byCode, "quarter", "net", "Chase the net return, quarterly",
			"The same rule, but only allowed to move once a quarter. Fewer moves, less whipsaw, and the number we would actually ship."),
		commit(series, dates, byCode),
		bestSingle(markets, years),
		stayPut(series, dates, byCode, years),
	}

	return rep, nil
}

// chase replays a monthly rule. It never looks at the month it is about to earn:
// it decides using the previous month's result, which is all anyone can know.
func chase(series fxSeries, dates []string, byCode map[string]carry.CountryRate, period, mode, label, note string) Strategy {
	months := buckets(dates, period)
	years := mustParse(dates[len(dates)-1]).Sub(mustParse(dates[0])).Hours() / 24 / 365
	st := Strategy{Key: mode, Label: label, Note: note}
	value := 1.0
	for i := 1; i < len(months); i++ {
		prev, cur := months[i-1], months[i]
		bestCode, bestSeen := "", math.Inf(-1)
		for code, r := range byCode {
			if _, ok := series.Rates[prev.last][r.Currency]; !ok {
				continue
			}
			ret, ok := windowReturn(series, prev.first, prev.last, r)
			if !ok {
				continue
			}
			score := ret
			if mode == "nominal" {
				score = r.Policy
			}
			if score > bestSeen {
				bestSeen, bestCode = score, code
			}
		}
		if bestCode == "" {
			continue
		}
		r := byCode[bestCode]
		ret, ok := windowReturn(series, cur.first, cur.last, r)
		if !ok {
			continue
		}
		value *= 1 + ret
		st.Legs = append(st.Legs, Leg{
			From: cur.first, To: cur.last, Code: bestCode, Country: r.Country,
			ReturnPct: round2(ret * 100), ChosenOnPct: round2(bestSeen),
		})
	}
	st.ReturnPct = round2((value-1)*100 - float64(FeeBps)/100*years)
	return st
}

// commit is the only strategy here that could be run for real: rank the countries on
// the first quarter of data, commit to the winner, and hold it for the rest of the
// window without touching it. No lookahead, no monthly churn.
func commit(series fxSeries, dates []string, byCode map[string]carry.CountryRate) Strategy {
	months := monthBuckets(dates)
	st := Strategy{Key: "commit", Label: "Rank once, then leave it alone",
		Note: "Rank the countries on the first three months, put the money in the winner, and do not touch it again. No hindsight, no churn — the honest test of whether the ranking is useful."}
	if len(months) < 5 {
		return st
	}
	rankFrom, rankTo := months[0].first, months[2].last
	bestCode, bestSeen := "", math.Inf(-1)
	for code, r := range byCode {
		ret, ok := windowReturn(series, rankFrom, rankTo, r)
		if !ok {
			continue
		}
		if ret > bestSeen {
			bestSeen, bestCode = ret, code
		}
	}
	if bestCode == "" {
		return st
	}
	r := byCode[bestCode]
	holdFrom := months[3].first
	ret, ok := windowReturn(series, holdFrom, months[len(months)-1].last, r)
	if !ok {
		return st
	}
	st.ReturnPct = round2(ret * 100)
	st.Note = fmt.Sprintf("%s won the first quarter on net return (%.2f%%) and was then left alone for the rest of the year. %s",
		r.Country, bestSeen*100, st.Note)
	st.Legs = []Leg{{From: holdFrom, To: months[len(months)-1].last, Code: bestCode, Country: r.Country, ReturnPct: st.ReturnPct, ChosenOnPct: round2(bestSeen * 100)}}
	return st
}

func bestSingle(markets []Market, years float64) Strategy {
	st := Strategy{Key: "best_single", Label: "Pick the winner in hindsight",
		Note: "One country, chosen at the start with knowledge of how the year ended. Nobody can do this; it is the ceiling."}
	best := math.Inf(-1)
	var pick Market
	for _, m := range markets {
		if m.NetUSD > best {
			best, pick = m.NetUSD, m
		}
	}
	st.ReturnPct = pick.NetUSD
	st.Note = fmt.Sprintf("%s, held for the whole window. Impossible in advance; the number to beat.", pick.Country)
	return st
}

func stayPut(series fxSeries, dates []string, byCode map[string]carry.CountryRate, years float64) Strategy {
	st := Strategy{Key: "usd", Label: "Leave the dollars alone",
		Note: "The baseline: dollars earning the US rate. No currency risk, nothing to watch."}
	r, ok := byCode["US"]
	if !ok {
		return st
	}
	ret, ok := windowReturn(series, dates[0], dates[len(dates)-1], r)
	if !ok {
		return st
	}
	st.ReturnPct = round2((math.Pow(1+r.Policy/100, years)-1)*100 - float64(FeeBps)/100*years)
	_ = ret
	return st
}

type bucket struct{ first, last string }

func monthBuckets(dates []string) []bucket { return buckets(dates, "month") }

// buckets splits the series by calendar period. "quarter" is deliberately one of the
// options: the monthly rule turns out to trade a real ranking advantage for churn.
func buckets(dates []string, period string) []bucket {
	var out []bucket
	var cur string
	for _, d := range dates {
		m := d[:7]
		if period == "quarter" {
			mo, _ := strconv.Atoi(d[5:7])
			m = d[:4] + "-Q" + strconv.Itoa((mo-1)/3+1)
		}
		if m != cur {
			out = append(out, bucket{first: d, last: d})
			cur = m
			continue
		}
		out[len(out)-1].last = d
	}
	return out
}

// windowReturn is the return of holding `r`'s currency between two dates, in dollars.
func windowReturn(series fxSeries, from, to string, r carry.CountryRate) (float64, bool) {
	a, ok1 := series.Rates[from][r.Currency]
	b, ok2 := series.Rates[to][r.Currency]
	if !ok1 || !ok2 || a <= 0 || b <= 0 {
		return 0, false
	}
	d1, d2 := mustParse(from), mustParse(to)
	span := d2.Sub(d1).Hours() / 24 / 365
	if span <= 0 {
		return 0, false
	}
	local := math.Pow(1+r.Policy/100, span)
	return local*(a/b) - 1, true
}

func fetchFX(ctx context.Context, c *http.Client, start, end string, currencies []string) (fxSeries, error) {
	symbols := ""
	for i, c := range currencies {
		if i > 0 {
			symbols += ","
		}
		symbols += c
	}
	u := fmt.Sprintf("https://api.frankfurter.dev/v1/%s..%s?base=USD&symbols=%s", start, end, url.QueryEscape(symbols))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fxSeries{}, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return fxSeries{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fxSeries{}, fmt.Errorf("fx provider returned %d", resp.StatusCode)
	}
	var s fxSeries
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return fxSeries{}, err
	}
	return s, nil
}

func mustParse(d string) time.Time {
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return time.Time{}
	}
	return t
}

func round2(x float64) float64 { return math.Round(x*100) / 100 }
