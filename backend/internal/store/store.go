// Package store keeps the refreshable snapshot of venues plus the public decision log.
// It is in-memory on purpose: the source of truth for money is the Stellar ledger, not
// this process. Losing the cache costs one refresh, not a balance.
package store

import (
	"sort"
	"sync"
	"time"

	"github.com/emrealt34/building/backend/internal/model"
)

type Store struct {
	mu        sync.RWMutex
	venues    []model.Venue
	updated   time.Time
	sourceErr string
	decisions []model.Decision
	network   model.NetworkStatus
}

const maxDecisions = 200

func New() *Store { return &Store{} }

func (s *Store) SetVenues(v []model.Venue, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.sourceErr = err.Error()
		return
	}
	s.venues = v
	s.updated = time.Now().UTC()
	s.sourceErr = ""
}

func (s *Store) Venues() ([]model.Venue, time.Time, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.Venue, len(s.venues))
	copy(out, s.venues)
	return out, s.updated, s.sourceErr
}

func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.venues)
}

func (s *Store) AddDecision(d model.Decision) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.decisions = append([]model.Decision{d}, s.decisions...)
	if len(s.decisions) > maxDecisions {
		s.decisions = s.decisions[:maxDecisions]
	}
}

func (s *Store) Decisions() []model.Decision {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.Decision, len(s.decisions))
	copy(out, s.decisions)
	return out
}

func (s *Store) SetNetwork(n model.NetworkStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.network = n
}

func (s *Store) Network() model.NetworkStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.network
}

// TopByNetScore returns venues sorted for display.
func TopByNetScore(v []model.Venue) []model.Venue {
	out := make([]model.Venue, len(v))
	copy(out, v)
	sort.SliceStable(out, func(i, j int) bool { return out[i].NetScore > out[j].NetScore })
	return out
}
