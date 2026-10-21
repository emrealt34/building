// Package keeper is the automation loop: refresh data, evaluate the policy, log a
// decision. In this MVP the keeper is read-only (it proposes, it does not submit);
// submitting requires a signed transaction, which is wired in Tranche 2 together with
// the UserVault contract.
//
// Read-only is a feature, not a shortcut: it lets the decision logic run in production,
// in public, before any contract can move a user's money.
package keeper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/emrealt34/keelson/backend/internal/model"
	"github.com/emrealt34/keelson/backend/internal/policy"
	"github.com/emrealt34/keelson/backend/internal/providers"
	"github.com/emrealt34/keelson/backend/internal/stellar"
	"github.com/emrealt34/keelson/backend/internal/store"
)

type Keeper struct {
	LLama        *providers.DefiLlama
	DeFindex     *providers.DeFindex
	Stellar      *stellar.Client
	Store        *store.Store
	Policy       model.Policy
	RefreshEvery time.Duration
	TickEvery    time.Duration
	TrackedVault string // demo vault label shown in the decision log
	StartVenueID string // the venue the demo vault currently sits in
}

// Run starts both loops and blocks until ctx is cancelled.
func (k *Keeper) Run(ctx context.Context) {
	k.refresh(ctx)
	k.tick(ctx)

	refresh := time.NewTicker(k.RefreshEvery)
	tick := time.NewTicker(k.TickEvery)
	defer refresh.Stop()
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-refresh.C:
			k.refresh(ctx)
			k.network(ctx)
		case <-tick.C:
			k.tick(ctx)
		}
	}
}

func (k *Keeper) refresh(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()

	var venues []model.Venue
	if v, err := k.LLama.Venues(cctx); err == nil {
		venues = append(venues, v...)
	} else {
		log.Printf("keeper: defillama refresh failed: %v", err)
	}
	if v, err := k.DeFindex.Venues(cctx); err == nil {
		venues = append(venues, v...)
	} else {
		log.Printf("keeper: defindex refresh failed: %v", err)
	}

	if len(venues) == 0 {
		k.Store.SetVenues(nil, fmt.Errorf("all venue providers failed"))
		return
	}
	// Annotate once so every endpoint serves the same scoring pass.
	policy.Annotate(venues, 1_000, k.Policy, time.Now().UTC())
	k.Store.SetVenues(venues, nil)
	log.Printf("keeper: %d venues refreshed", len(venues))
}

func (k *Keeper) network(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	k.Store.SetNetwork(k.Stellar.Status(cctx))
}

// tick is one decision cycle for the tracked (demo) vault.
func (k *Keeper) tick(ctx context.Context) {
	venues, _, _ := k.Store.Venues()
	if len(venues) == 0 {
		return
	}
	var current *model.Venue
	for i := range venues {
		if venues[i].ID == k.StartVenueID {
			current = &venues[i]
			break
		}
	}
	if current == nil {
		// Fall back to the largest verified venue so the log is never empty.
		for i := range venues {
			if venues[i].YieldSourceVerified && (current == nil || venues[i].CapacityUSD > current.CapacityUSD) {
				current = &venues[i]
			}
		}
	}
	if current == nil {
		return
	}

	// Best eligible venue by net score.
	var best *model.Venue
	for i := range venues {
		if !venues[i].Eligible {
			continue
		}
		if best == nil || venues[i].NetScore > best.NetScore {
			best = &venues[i]
		}
	}
	if best == nil {
		k.Store.AddDecision(model.Decision{
			At: time.Now().UTC(), Vault: k.TrackedVault, Action: "noop",
			Reason: "No suitable place to move to this time.", CurrentAPY: current.APY,
		})
		return
	}

	move, reason := policy.ShouldMove(*current, *best, 30, k.Policy) // 30 days since last move
	action := "noop"
	if move {
		action = "proposed"
	}
	planHash := ""
	if move {
		raw, _ := json.Marshal(struct {
			From, To string
			Amount   float64
			At       time.Time
		}{current.ID, best.ID, 250, time.Now().UTC()})
		sum := sha256.Sum256(raw)
		planHash = hex.EncodeToString(sum[:8])
	}
	k.Store.AddDecision(model.Decision{
		At: time.Now().UTC(), Vault: k.TrackedVault, Action: action, Reason: reason,
		PlanHash: planHash, BestAPY: best.APY, CurrentAPY: current.APY,
		GapBps: policy.ImprovementBps(current.APY, best.APY),
	})
}
