// Command snapshot freezes the read API into a single JSON file so the site can be
// built statically (GitHub Pages) without a backend running.
//
// It deliberately runs the *real* HTTP handlers through httptest instead of
// re-serialising models: the snapshot schema can then never drift from the live API.
// Whatever is in the file is exactly what GET /api/v1/... returned at generation
// time, and the file records that time so the UI can say "as of".
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"github.com/emrealt34/keelson/backend/internal/api"
	"github.com/emrealt34/keelson/backend/internal/model"
	"github.com/emrealt34/keelson/backend/internal/policy"
	"github.com/emrealt34/keelson/backend/internal/providers"
	"github.com/emrealt34/keelson/backend/internal/stellar"
	"github.com/emrealt34/keelson/backend/internal/store"
)

// Deposit presets the static site renders one page for each.
var deposits = []int{100, 1000, 10000, 50000}

type snapshot struct {
	GeneratedAt time.Time                  `json:"generated_at"`
	Note        string                     `json:"note"`
	Deposits    []int                      `json:"deposits"`
	Responses   map[string]json.RawMessage `json:"responses"`
}

func main() {
	out := flag.String("out", "../frontend/src/data/snapshot.json", "output file")
	network := flag.String("network", "mainnet", "mainnet | testnet")
	flag.Parse()

	rpcURL, horizonURL := "https://mainnet.sorobanrpc.com", "https://horizon.stellar.org"
	if *network == "testnet" {
		rpcURL, horizonURL = "https://soroban-testnet.stellar.org", "https://horizon-testnet.stellar.org"
	}

	st := store.New()
	llama := providers.NewDefiLlama()
	defindex := providers.NewDeFindex()
	pol := model.DefaultPolicy()
	sc := stellar.New(rpcURL, horizonURL)

	srv := &api.Server{
		Store:   st,
		Stellar: sc,
		LLama:   llama,
		Policy:  pol,
		Network: *network,
		Version: "1.0.0",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	log.Printf("snapshot: refreshing venues from %s", *network)
	var venues []model.Venue
	if v, err := llama.Venues(ctx); err == nil {
		venues = append(venues, v...)
	} else {
		log.Printf("snapshot: defillama: %v", err)
	}
	if v, err := defindex.Venues(ctx); err == nil {
		venues = append(venues, v...)
	} else {
		log.Printf("snapshot: defindex: %v", err)
	}
	if len(venues) == 0 {
		log.Fatal("snapshot: no venues fetched; refusing to write an empty snapshot")
	}
	// One scoring pass, the same one the keeper does, so every endpoint agrees.
	policy.Annotate(venues, 1000, pol, time.Now().UTC())
	st.SetVenues(venues, nil)
	st.SetNetwork(sc.Status(ctx))
	log.Printf("snapshot: %d venues ready", len(venues))

	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	// get returns nil when the endpoint legitimately has nothing to say (a venue
	// with no public APY series answers 404). The site treats nil as "no data".
	get := func(path string) json.RawMessage {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			log.Fatalf("snapshot: GET %s: %v", path, err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			log.Printf("snapshot: GET %s: status %d (skipped)", path, res.StatusCode)
			return nil
		}
		var raw json.RawMessage
		if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
			log.Fatalf("snapshot: decode %s: %v", path, err)
		}
		return raw
	}

	snap := snapshot{
		GeneratedAt: time.Now().UTC(),
		Note:        "Frozen read API. This static site renders this snapshot; run the Go backend for live data.",
		Deposits:    deposits,
		Responses:   map[string]json.RawMessage{},
	}

	for _, p := range []string{
		"/api/v1/health", "/api/v1/meta", "/api/v1/network",
		"/api/v1/policy", "/api/v1/decisions", "/api/v1/alpha?days=90", "/api/v1/carry", "/api/v1/cherry",
	} {
		snap.Responses[p] = get(p)
		log.Printf("snapshot: %s ok", p)
	}

	for _, d := range deposits {
		for _, p := range []string{
			fmt.Sprintf("/api/v1/plan?deposit=%d", d),
			fmt.Sprintf("/api/v1/venues?deposit=%d", d),
		} {
			snap.Responses[p] = get(p)
		}
		log.Printf("snapshot: deposit %d ok", d)
	}

	for _, v := range venues {
		for _, p := range []string{
			fmt.Sprintf("/api/v1/venues/%s?deposit=1000", v.ID),
			fmt.Sprintf("/api/v1/venues/%s/history?days=90", v.ID),
		} {
			snap.Responses[p] = get(p)
		}
	}
	log.Printf("snapshot: %d venue pages ok", len(venues))

	blob, err := json.MarshalIndent(snap, "", " ")
	if err != nil {
		log.Fatalf("snapshot: marshal: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		log.Fatalf("snapshot: mkdir: %v", err)
	}
	if err := os.WriteFile(*out, append(blob, '\n'), 0o644); err != nil {
		log.Fatalf("snapshot: write: %v", err)
	}
	log.Printf("snapshot: wrote %s (%.1f KB, %d responses)", *out, float64(len(blob))/1024, len(snap.Responses))
}
