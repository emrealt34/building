package providers

import "github.com/emrealt34/building/backend/internal/model"

// USDC is the Stellar Asset Contract address for USDC on mainnet.
const USDC = "CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75"

// XLM is the Stellar Asset Contract address for lumen on mainnet.
const XLM = "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC"

// SafetyProfile is the curated, versioned part of a venue: everything that cannot
// be read from a price feed. It is intentionally kept separate from live numbers so
// that a reviewer can diff it over time.
type SafetyProfile struct {
	Kind                model.VenueKind
	Audits              int
	Tier1Audit          bool
	LiveMonths          int
	Upgradeable         bool
	Oracle              string
	OracleQuality       float64
	RewardDependence    float64
	YieldSourceVerified bool
	InstantExit         bool
	Underlying          []string
	Incidents           []model.Incident
	Label               string
}

// RegistryVersion must be bumped whenever a safety profile changes.
const RegistryVersion = "safety-registry-2026-10-21"

var blendIncidents = []model.Incident{
	{
		Type:      "oracle_manipulation",
		LossUSD:   10_900_000,
		MonthsAgo: 8,
		Scope:     "pool_config",
		Note:      "Feb 2026: YieldBlox pool oracle fed from a shallow SDEX market was manipulated; ~$10.9M. Not a bug in Blend core code, but the same oracle plumbing.",
	},
}

var blend2026Comet = model.Incident{
	Type:      "shared_dependency",
	LossUSD:   717_000,
	MonthsAgo: 2,
	Scope:     "protocol_code",
	Note:      "Aug 2026: Comet BLND-USDC pool (the backstop collateral for every Blend pool) was exploited; all backstops were impaired simultaneously and Stellar DeFi TVL fell ~60% intraday. This is why we never enter backstops or BLND-LP collateral.",
}

// Registry maps a provider+pool identity to a curated safety profile.
func Registry(project, poolMeta, address string) (SafetyProfile, bool) {
	switch project {
	case "blend-pools-v2":
		label := "Blend v2 — " + orDefault(poolMeta, "Pool") + " / USDC reserve"
		p := SafetyProfile{
			Kind:                model.KindLendingReserve,
			Audits:              3,
			Tier1Audit:          true,
			LiveMonths:          26,
			Upgradeable:         true,
			Oracle:              "Reflector / SEP-40 (per-pool configuration)",
			OracleQuality:       0.65,
			RewardDependence:    0.0,
			YieldSourceVerified: true, // borrower interest, read from reserve state
			InstantExit:         false,
			Underlying:          []string{"Blend pool", "Comet backstop LP (indirect)"},
			Incidents:           append([]model.Incident{blend2026Comet}, blendIncidents...),
			Label:               label,
		}
		if containsFold(poolMeta, "yieldblox") {
			p.Oracle = "SDEX-fed price (shallow market)"
			p.OracleQuality = 0.25
		}
		return p, true

	case "gami-labs":
		return SafetyProfile{
			Kind:                model.KindManagedVault,
			Audits:              1,
			Tier1Audit:          false,
			LiveMonths:          7,
			Upgradeable:         true,
			Oracle:              "n/a (vault NAV)",
			OracleQuality:       0.70,
			RewardDependence:    0.50,
			YieldSourceVerified: false, // a flat 10% with no published decomposition
			InstantExit:         true,
			Underlying:          []string{"Blend", "Aquarius", "managed strategy"},
			Label:               "Gami earnUSDC (managed vault)",
		}, true

	case "defindex":
		return SafetyProfile{
			Kind:                model.KindVault,
			Audits:              2,
			Tier1Audit:          false,
			LiveMonths:          14,
			Upgradeable:         true,
			Oracle:              "n/a (share price)",
			OracleQuality:       0.70,
			RewardDependence:    0.15,
			YieldSourceVerified: true,
			InstantExit:         true,
			Underlying:          []string{"Blend strategy (autocompound)"},
			Label:               "DeFindex vault",
		}, true
	}
	return SafetyProfile{}, false
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func containsFold(s, sub string) bool {
	return len(s) >= len(sub) && (indexFold(s, sub) >= 0)
}

func indexFold(s, sub string) int {
	ls, lsub := lower(s), lower(sub)
	for i := 0; i+len(lsub) <= len(ls); i++ {
		if ls[i:i+len(lsub)] == lsub {
			return i
		}
	}
	return -1
}

func lower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}
