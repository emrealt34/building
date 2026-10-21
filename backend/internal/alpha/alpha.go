// Package alpha answers the question the whole product depends on:
// "how much is picking the best venue actually worth?"
//
// It is intentionally part of the backend rather than a spreadsheet: the number that
// justifies (or refuses) automation should be reproducible by anyone, from the same
// public data, at any time.
package alpha

import (
	"context"
	"sort"
	"time"

	"github.com/emrealt34/keelson/backend/internal/providers"
)

// series is one venue's daily APY history.
type series struct {
	label string
	byDay map[string]float64
}

type VenueSeries struct {
	VenueID string               `json:"venue_id"`
	Label   string               `json:"label"`
	Days    int                  `json:"days"`
	Mean    float64              `json:"mean_apy"`
	Min     float64              `json:"min_apy"`
	Max     float64              `json:"max_apy"`
	Series  []providers.APYPoint `json:"series"`
}

type SimResult struct {
	PrincipalUSD    float64 `json:"principal_usd"`
	ThresholdBps    float64 `json:"threshold_bps"`
	CooldownDays    int     `json:"cooldown_days"`
	Switches        int     `json:"switches"`
	FinalUSD        float64 `json:"final_usd"`
	GainUSD         float64 `json:"gain_usd"`
	PassiveVenue    string  `json:"passive_venue"`
	PassiveFinalUSD float64 `json:"passive_final_usd"`
	AlphaUSD        float64 `json:"alpha_usd"`
	AlphaBpsYear    float64 `json:"alpha_bps_per_year"`
	CapturePct      float64 `json:"capture_of_upper_bound_pct"`
}

type Report struct {
	WindowDays    int           `json:"window_days"`
	GeneratedAt   time.Time     `json:"generated_at"`
	Venues        []VenueSeries `json:"venues"`
	UpperBoundUSD float64       `json:"upper_bound_usd"` // perfect hindsight minus best single
	BestSingle    string        `json:"best_single_label"`
	BestSingleUSD float64       `json:"best_single_usd"`
	PerfectUSD    float64       `json:"perfect_hindsight_usd"`
	Sweeps        []SimResult   `json:"sweeps"`
	Best          *SimResult    `json:"best"`
	GapDays       int           `json:"days_gap_over_threshold"`
	AvgGapBps     float64       `json:"avg_gap_bps"`
	Note          string        `json:"note"`
}

// Analyse downloads history for the given pools and runs the keeper simulation.
func Analyse(ctx context.Context, llama *providers.DefiLlama, days int, principal float64) (Report, error) {
	rep := Report{WindowDays: days, GeneratedAt: time.Now().UTC()}

	pools := []struct{ id, label string }{
		{"ecf788e3-d2ef-4fdd-9ece-8a2d96226ddf", "Blend fixed pool"},
		{"48c597dc-9367-4b4a-aa10-49b9755c4c2e", "Blend YieldBlox pool"},
	}

	data := map[string]*series{}
	daySet := map[string]bool{}

	for _, p := range pools {
		pts, err := llama.History(ctx, p.id, days+5)
		if err != nil || len(pts) < days*3/4 {
			continue // a venue with patchy history cannot enter a fair comparison
		}
		s := &series{label: p.label, byDay: map[string]float64{}}
		sum, mn, mx := 0.0, 1e9, 0.0
		for _, pt := range pts {
			s.byDay[pt.Date] = pt.APY
			daySet[pt.Date] = true
			sum += pt.APY
			if pt.APY < mn {
				mn = pt.APY
			}
			if pt.APY > mx {
				mx = pt.APY
			}
		}
		s.byDay = cleanOutliers(s.byDay)
		data[p.id] = s
		rep.Venues = append(rep.Venues, VenueSeries{
			VenueID: p.id, Label: p.label, Days: len(pts),
			Mean: round2(sum / float64(len(pts))), Min: round2(mn), Max: round2(mx),
			Series: pts,
		})
	}
	if len(data) < 2 {
		rep.Note = "Not enough venue history to compare. A single venue makes routing alpha undefined."
		return rep, nil
	}

	daysList := make([]string, 0, len(daySet))
	for d := range daySet {
		daysList = append(daysList, d)
	}
	sort.Strings(daysList)

	// 1) best single venue, no switching
	bestID, bestFinal := "", 0.0
	for id := range data {
		bal := compound(data, id, daysList, principal)
		if bal > bestFinal {
			bestID, bestFinal = id, bal
		}
	}
	rep.BestSingle = data[bestID].label
	rep.BestSingleUSD = round2(bestFinal)

	// 2) perfect hindsight: every day in the best venue
	perfect := principal
	for _, d := range daysList {
		best := 0.0
		first := true
		for _, s := range data {
			if v, ok := s.byDay[d]; ok && (first || v > best) {
				best, first = v, false
			}
		}
		perfect *= 1 + best/100/365
	}
	rep.PerfectUSD = round2(perfect)
	rep.UpperBoundUSD = round2(perfect - bestFinal)

	// 3) realistic keeper sweep
	for _, cd := range []int{0, 1, 2, 3, 5, 7} {
		for _, th := range []float64{10, 25, 50, 100} {
			final, switches := simulate(data, daysList, principal, th, cd)
			r := SimResult{
				PrincipalUSD: principal, ThresholdBps: th, CooldownDays: cd,
				Switches: switches, FinalUSD: round2(final),
				GainUSD:      round2(final - principal),
				PassiveVenue: data[bestID].label, PassiveFinalUSD: round2(bestFinal),
				AlphaUSD:     round2(final - bestFinal),
				AlphaBpsYear: round2((final - bestFinal) / principal * 10_000 * 365 / float64(len(daysList))),
			}
			if rep.UpperBoundUSD > 0 {
				r.CapturePct = round2(100 * r.AlphaUSD / rep.UpperBoundUSD)
			}
			// Only 2-3 venues listed above; keep the sweep readable.
			rep.Sweeps = append(rep.Sweeps, r)
		}
	}
	sort.SliceStable(rep.Sweeps, func(i, j int) bool { return rep.Sweeps[i].AlphaUSD > rep.Sweeps[j].AlphaUSD })
	if len(rep.Sweeps) > 0 {
		b := rep.Sweeps[0]
		rep.Best = &b
	}

	// 4) how often was there a gap worth acting on?
	var sumGap float64
	for _, d := range daysList {
		mn, mx, n := 1e9, 0.0, 0
		for _, s := range data {
			if v, ok := s.byDay[d]; ok {
				n++
				if v < mn {
					mn = v
				}
				if v > mx {
					mx = v
				}
			}
		}
		if n < 2 {
			continue
		}
		gap := (mx - mn) * 100
		sumGap += gap
		if gap >= 25 {
			rep.GapDays++
		}
	}
	if len(daysList) > 0 {
		rep.AvgGapBps = round2(sumGap / float64(len(daysList)))
	}
	rep.Note = "Upper bound is perfect hindsight minus the best single venue, i.e. the entire value of routing with a crystal ball. A realistic keeper captures a fraction of it."
	return rep, nil
}

// cleanOutliers drops observations above 3x the trailing 30-day median.
//
// Why: a single 193% print is a liquidity squeeze, not a yield opportunity, and a keeper
// reading it would be reading a broken measurement. Removing it is the difference between
// an honest alpha number and a flattering one.
func cleanOutliers(byDay map[string]float64) map[string]float64 {
	dates := make([]string, 0, len(byDay))
	for d := range byDay {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	out := make(map[string]float64, len(byDay))
	window := make([]float64, 0, 31)
	for _, d := range dates {
		v := byDay[d]
		window = append(window, v)
		if len(window) > 30 {
			window = window[1:]
		}
		med := median(window)
		if med > 0 && v > 3*med {
			continue // dropped: treated as missing data, not as alpha
		}
		out[d] = v
	}
	return out
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	cp := make([]float64, len(xs))
	copy(cp, xs)
	sort.Float64s(cp)
	n := len(cp)
	if n%2 == 1 {
		return cp[n/2]
	}
	return (cp[n/2-1] + cp[n/2]) / 2
}

func compound(data map[string]*series, id string, days []string, principal float64) float64 {
	bal := principal
	for _, d := range days {
		if apy, ok := data[id].byDay[d]; ok {
			bal *= 1 + apy/100/365
		}
	}
	return bal
}

func simulate(data map[string]*series, days []string, principal, thresholdBps float64, cooldownDays int) (float64, int) {
	ids := make([]string, 0, len(data))
	for id := range data {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	cur := ids[0]
	// start in the venue with the best full-window mean, so the comparison is fair
	bestMean, bestID := -1.0, ids[0]
	for _, id := range ids {
		var sum float64
		var n int
		for _, d := range days {
			if v, ok := data[id].byDay[d]; ok {
				sum += v
				n++
			}
		}
		if n > 0 && sum/float64(n) > bestMean {
			bestMean, bestID = sum/float64(n), id
		}
	}
	cur = bestID

	bal := principal
	lastSwitch := -1 << 30
	switches := 0
	for i, d := range days {
		// Decision uses yesterday's observation and applies today. A keeper that reacts
		// within the same day it sees a rate is measuring hindsight, not strategy.
		if i > 0 {
			prev := days[i-1]
			curApy, okCur := data[cur].byDay[prev]
			if okCur {
				bestApy, bestCand := curApy, cur
				for _, id := range ids {
					if v, ok := data[id].byDay[prev]; ok && v > bestApy {
						bestApy, bestCand = v, id
					}
				}
				if bestCand != cur && (bestApy-curApy)*100 >= thresholdBps && i-lastSwitch >= cooldownDays {
					cur, lastSwitch = bestCand, i
					switches++
				}
			}
		}
		apy := 0.0
		if v, ok := data[cur].byDay[d]; ok {
			apy = v
		}
		bal *= 1 + apy/100/365
	}
	return bal, switches
}

func round2(x float64) float64 { return float64(int(x*100+0.5)) / 100 }
