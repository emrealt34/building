// Package api exposes the read-only HTTP surface. No endpoint holds a key or mutates
// chain state; the only write-shaped endpoints return unsigned intents for the wallet.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/emrealt34/building/backend/internal/alpha"
	"github.com/emrealt34/building/backend/internal/carry"
	"github.com/emrealt34/building/backend/internal/model"
	"github.com/emrealt34/building/backend/internal/policy"
	"github.com/emrealt34/building/backend/internal/providers"
	"github.com/emrealt34/building/backend/internal/risk"
	"github.com/emrealt34/building/backend/internal/stellar"
	"github.com/emrealt34/building/backend/internal/store"
)

type Server struct {
	Store   *store.Store
	Stellar *stellar.Client
	LLama   *providers.DefiLlama
	Policy  model.Policy
	Network string // "mainnet" | "testnet"
	VaultID string // UserVault contract id, empty until Tranche 2
	Version string
	HTTP    *http.Client // used by the cross-border carry measurement
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.health)
	mux.HandleFunc("GET /api/v1/network", s.networkStatus)
	mux.HandleFunc("GET /api/v1/policy", s.policyDoc)
	mux.HandleFunc("GET /api/v1/meta", s.meta)
	mux.HandleFunc("GET /api/v1/venues", s.venues)
	mux.HandleFunc("GET /api/v1/venues/{id}", s.venue)
	mux.HandleFunc("GET /api/v1/venues/{id}/history", s.venueHistory)
	mux.HandleFunc("GET /api/v1/plan", s.plan)
	mux.HandleFunc("GET /api/v1/decisions", s.decisions)
	mux.HandleFunc("GET /api/v1/alpha", s.alphaReport)
	mux.HandleFunc("GET /api/v1/carry", s.carryReport)
	mux.HandleFunc("GET /api/v1/accounts/{address}", s.account)
	mux.HandleFunc("POST /api/v1/intents/deposit", s.intentDeposit)
	mux.HandleFunc("POST /api/v1/intents/withdraw", s.intentWithdraw)
	return withCORS(withLogging(mux))
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	_, updated, srcErr := s.Store.Venues()
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"version":        s.Version,
		"network":        s.Network,
		"venues_cached":  s.Store.Count(),
		"venues_updated": updated,
		"provider_error": srcErr,
		"time":           time.Now().UTC(),
	})
}

func (s *Server) meta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"product":         "Compass",
		"chain":           "Stellar",
		"asset":           "USDC",
		"custody":         "non-custodial: the backend holds no key and signs nothing",
		"automation":      "policy-bounded: the vault enforces caps, cooldowns and a NAV check",
		"risk_model":      risk.Version,
		"safety_registry": providers.RegistryVersion,
		"policy_version":  s.Policy.Version,
		"api_version":     s.Version,
		"vault_contract":  s.VaultID,
	})
}

func (s *Server) networkStatus(w http.ResponseWriter, r *http.Request) {
	n := s.Store.Network()
	if n.CheckedAt.IsZero() {
		n = s.Stellar.Status(r.Context())
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) policyDoc(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"policy":  s.Policy,
		"explain": "These parameters are what a user signs once. The vault cannot exceed them, and the keeper cannot bypass them.",
		"chosen_by_measurement": map[string]string{
			"cooldown_secs":          "average leadership between the two verified venues was ~6.7 days; a 7-day cooldown missed most switches",
			"min_improvement_bps":    "below 25 bps the switch costs more attention than it earns",
			"min_capacity_usd":       "venues under $100k cannot absorb our exit without moving the rate against us",
			"max_venue_share_of_cap": "capped at 2% of a venue so our own exit does not become the risk",
		},
	})
}

func (s *Server) venues(w http.ResponseWriter, r *http.Request) {
	venues, updated, srcErr := s.Store.Venues()
	deposit := queryFloat(r, "deposit", 1_000)
	policy.Annotate(venues, deposit, s.Policy, time.Now().UTC())
	writeJSON(w, http.StatusOK, map[string]any{
		"as_of":          updated,
		"deposit_usd":    deposit,
		"provider_error": srcErr,
		"count":          len(venues),
		"venues":         store.TopByNetScore(venues),
	})
}

func (s *Server) venue(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	venues, updated, _ := s.Store.Venues()
	policy.Annotate(venues, queryFloat(r, "deposit", 1_000), s.Policy, time.Now().UTC())
	for _, v := range venues {
		if v.ID == id {
			writeJSON(w, http.StatusOK, map[string]any{"as_of": updated, "venue": v})
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "venue not found"})
}

func (s *Server) venueHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	days := queryInt(r, "days", 90)
	venues, _, _ := s.Store.Venues()
	for _, v := range venues {
		if v.ID == id && v.PoolID != "" {
			pts, err := s.LLama.History(r.Context(), v.PoolID, days)
			if err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"venue_id": id, "days": days, "points": pts})
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "venue not found or no history source"})
}

func (s *Server) plan(w http.ResponseWriter, r *http.Request) {
	deposit := queryFloat(r, "deposit", 1_000)
	if deposit < 1 || deposit > 10_000_000 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "deposit must be between 1 and 10,000,000 USD"})
		return
	}
	venues, _, _ := s.Store.Venues()
	p := policy.Build(venues, deposit, s.Policy)
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) decisions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"note":      "Every keeper evaluation, including the ones where nothing happened. A decision log that only shows successful moves is marketing, not a log.",
		"decisions": s.Store.Decisions(),
	})
}

func (s *Server) alphaReport(w http.ResponseWriter, r *http.Request) {
	days := queryInt(r, "days", 90)
	principal := queryFloat(r, "principal", 1000)
	rep, err := alpha.Analyse(r.Context(), s.LLama, days, principal)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) account(w http.ResponseWriter, r *http.Request) {
	addr := r.PathValue("address")
	if len(addr) < 10 || !strings.HasPrefix(addr, "G") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expected a Stellar account address (G…)"})
		return
	}
	writeJSON(w, http.StatusOK, s.Stellar.AccountBalance(r.Context(), addr))
}

type intentReq struct {
	AmountUSD float64 `json:"amount_usd"`
	VenueID   string  `json:"venue_id"`
	Caller    string  `json:"caller"`
}

func (s *Server) intentDeposit(w http.ResponseWriter, r *http.Request) {
	var req intentReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if req.AmountUSD <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "amount_usd must be positive"})
		return
	}
	writeJSON(w, http.StatusOK, stellar.Intent{
		Network:      s.Network,
		ContractID:   s.VaultID,
		Function:     "deposit",
		RequiresAuth: req.Caller,
		Args: map[string]any{
			"from":       req.Caller,
			"amount":     int64(req.AmountUSD * 1e7), // USDC has 7 decimals on Stellar
			"venue_hint": req.VenueID,
		},
		Description: "Unsigned intent. The wallet must build the Soroban transaction, simulate it, " +
			"and sign it locally. The backend never sees a secret key and never submits.",
		BuiltBy: "compass-backend (read-only)",
	})
}

func (s *Server) intentWithdraw(w http.ResponseWriter, r *http.Request) {
	var req intentReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	writeJSON(w, http.StatusOK, stellar.Intent{
		Network:      s.Network,
		ContractID:   s.VaultID,
		Function:     "withdraw",
		RequiresAuth: req.Caller,
		Args: map[string]any{
			"amount": int64(req.AmountUSD * 1e7),
		},
		Description: "Unsigned intent. Withdrawal is always callable by the owner; illiquid parts " +
			"return as `pending` and are shown to the user, never hidden.",
		BuiltBy: "compass-backend (read-only)",
	})
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=30")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		_ = start
	})
}

func queryFloat(r *http.Request, key string, def float64) float64 {
	if v := r.URL.Query().Get(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func queryInt(r *http.Request, key string, def int) int {
	if v := r.URL.Query().Get(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

// carryReport measures what a dollar deposit in another currency actually paid,
// after the currency moved. It is the honest counterweight to headline rates.
func (s *Server) carryReport(w http.ResponseWriter, r *http.Request) {
	client := s.HTTP
	if client == nil {
		client = &http.Client{Timeout: 25 * time.Second}
	}

	// Benchmark: what the same dollar would earn inside Ballast's current plan.
	benchmark := 0.0
	if venues, _, _ := s.Store.Venues(); len(venues) > 0 {
		benchmark = policy.Build(venues, 1000, s.Policy).BlendedAPY
	}

	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	report, err := carry.Analyse(ctx, client, benchmark, time.Now().UTC())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "fx or rate data unavailable: " + err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, report)
}
