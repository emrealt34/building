package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/emrealt34/building/backend/internal/model"
)

const defindexBase = "https://api.defindex.io"

// DeFindex is the SCF Integration List building block we anchor on. The discovery
// endpoint is public (no API key); the per-vault endpoints require a partner key, so
// the backend treats discovery as the source of truth for the MVP and documents the
// key as a Tranche-1 dependency.
type DeFindex struct {
	HTTP *http.Client
}

func NewDeFindex() *DeFindex {
	return &DeFindex{HTTP: &http.Client{Timeout: 25 * time.Second}}
}

type defindexVault struct {
	Address           string `json:"address"`
	APY               float64 `json:"apy"`
	TotalManagedFunds []struct {
		Asset       string `json:"asset"`
		TotalAmount string `json:"total_amount"`
	} `json:"totalManagedFunds"`
}

// Venues returns USDC-only DeFindex vaults. Non-USDC vaults are skipped: a savings
// product that mixes a tokenised bond into a dollar balance is a different product.
func (d *DeFindex) Venues(ctx context.Context) ([]model.Venue, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		defindexBase+"/vault/discover?network=mainnet", nil)
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
		return nil, fmt.Errorf("defindex: status %d", resp.StatusCode)
	}
	var payload struct {
		TotalVaults int             `json:"totalVaults"`
		Vaults      []defindexVault `json:"vaults"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	profile, _ := Registry("defindex", "", "")
	out := make([]model.Venue, 0, len(payload.Vaults))
	for _, v := range payload.Vaults {
		var size float64
		for _, f := range v.TotalManagedFunds {
			if f.Asset != USDC {
				continue
			}
			amount, err := strconv.ParseFloat(f.TotalAmount, 64)
			if err != nil {
				continue
			}
			size += amount / 1e7 // 7 decimals
		}
		if size <= 0 {
			continue
		}
		label := profile.Label + " " + short(v.Address, 6) + "…"
		switch {
		case size > 5_000_000:
			label = fmt.Sprintf("%s %s… (large, $%.1fM)", profile.Label, short(v.Address, 6), size/1e6)
		case size > 100_000:
			label = fmt.Sprintf("%s %s… ($%.0fK)", profile.Label, short(v.Address, 6), size/1e3)
		default:
			label = fmt.Sprintf("%s %s… (small, $%.0f)", profile.Label, short(v.Address, 6), size)
		}
		out = append(out, model.Venue{
			ID:                  "defindex:" + short(v.Address, 10),
			Label:               label,
			Kind:                model.KindVault,
			Project:             "defindex",
			Address:             v.Address,
			Asset:               USDC,
			Source:              "defindex-discovery",
			Chain:               "Stellar",
			Protocol:            "defindex",
			APY:                 v.APY,
			APYBase:             v.APY, // DeFindex does not publish a reward split today
			TVLUSD:              size,
			CapacityUSD:         size,
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
		})
	}
	return out, nil
}
