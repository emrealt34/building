package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/emrealt34/building/backend/internal/model"
)

const llamaBase = "https://yields.llama.fi"

// DefiLlama is the public, key-less venue feed. It is our discovery source for
// lending reserves and third-party vaults on Stellar.
type DefiLlama struct {
	HTTP *http.Client
}

func NewDefiLlama() *DefiLlama {
	return &DefiLlama{HTTP: &http.Client{Timeout: 25 * time.Second}}
}

type llamaPool struct {
	Chain      string   `json:"chain"`
	Project    string   `json:"project"`
	Symbol     string   `json:"symbol"`
	Pool       string   `json:"pool"`
	PoolMeta   string   `json:"poolMeta"`
	TVLUsd     float64  `json:"tvlUsd"`
	APY        *float64 `json:"apy"`
	APYBase    *float64 `json:"apyBase"`
	APYReward  *float64 `json:"apyReward"`
	APYMean30d *float64 `json:"apyMean30d"`
}

// Venues returns every Stellar USDC venue we can classify (USDC or a USDC-denominated
// managed vault share). Venues with an unknown safety profile are dropped rather than
// guessed at: an unclassified venue cannot be risk-scored, so it cannot receive money.
func (d *DefiLlama) Venues(ctx context.Context) ([]model.Venue, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, llamaBase+"/pools", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "compass-backend/1.0")
	resp, err := d.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("defillama: status %d", resp.StatusCode)
	}

	var payload struct {
		Data []llamaPool `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	out := make([]model.Venue, 0, 16)
	for _, p := range payload.Data {
		if p.Chain != "Stellar" {
			continue
		}
		isUSDC := p.Symbol == "USDC" || (p.Project == "gami-labs" && p.Symbol == "EARNUSDC")
		if !isUSDC || p.TVLUsd < 3_000 {
			continue
		}
		profile, ok := Registry(p.Project, p.PoolMeta, "")
		if !ok {
			continue
		}
		v := model.Venue{
			ID:                  p.Project + ":" + short(p.Pool, 8),
			Label:               profile.Label,
			Kind:                profile.Kind,
			Project:             p.Project,
			PoolID:              p.Pool,
			Asset:               USDC,
			Source:              "defillama",
			Chain:               "Stellar",
			Protocol:            p.Project,
			APY:                 deref(p.APY),
			APYBase:             deref(p.APYBase),
			APYReward:           deref(p.APYReward),
			APYMean30d:          deref(p.APYMean30d),
			TVLUSD:              p.TVLUsd,
			CapacityUSD:         p.TVLUsd, // available (not borrowed) balance: our exit liquidity proxy
			InstantExit:         profile.InstantExit,
			Audits:              profile.Audits,
			Tier1Audit:          profile.Tier1Audit,
			LiveMonths:          profile.LiveMonths,
			Upgradeable:         profile.Upgradeable,
			Oracle:              profile.Oracle,
			OracleQuality:       profile.OracleQuality,
			RewardDependence:    profile.RewardDependence,
			YieldSourceVerified: profile.YieldSourceVerified,
			Underlying:          profile.Underlying,
			Incidents:           profile.Incidents,
		}
		out = append(out, v)
	}
	return out, nil
}

// APYPoint is one daily observation.
type APYPoint struct {
	Date      string  `json:"date"`
	APY       float64 `json:"apy"`
	TVLUsd    float64 `json:"tvl_usd"`
}

// History returns the daily APY series for a pool id.
func (d *DefiLlama) History(ctx context.Context, poolID string, days int) ([]APYPoint, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, llamaBase+"/chart/"+poolID, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "compass-backend/1.0")
	resp, err := d.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("defillama chart: status %d", resp.StatusCode)
	}
	var payload struct {
		Data []struct {
			Timestamp string   `json:"timestamp"`
			APY       *float64 `json:"apy"`
			TVLUsd    float64  `json:"tvlUsd"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	points := make([]APYPoint, 0, days+5)
	for _, row := range payload.Data {
		ts, err := time.Parse(time.RFC3339, row.Timestamp)
		if err != nil || ts.Before(cutoff) || row.APY == nil {
			continue
		}
		points = append(points, APYPoint{Date: ts.UTC().Format("2006-01-02"), APY: *row.APY, TVLUsd: row.TVLUsd})
	}
	return points, nil
}

func deref(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}

func short(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
