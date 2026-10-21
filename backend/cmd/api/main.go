// Compass API — read-only, non-custodial backend.
//
// Environment:
//
//	PORT             listen port (default 8080)
//	STELLAR_NETWORK  mainnet | testnet (default mainnet)
//	RPC_URL          Soroban RPC endpoint
//	HORIZON_URL      Horizon endpoint
//	VAULT_CONTRACT   UserVault contract id (empty until Tranche 2)
//	REFRESH_SECONDS  venue refresh interval (default 300)
//	TICK_SECONDS     keeper decision interval (default 120)
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/emrealt34/keelson/backend/internal/api"
	"github.com/emrealt34/keelson/backend/internal/keeper"
	"github.com/emrealt34/keelson/backend/internal/model"
	"github.com/emrealt34/keelson/backend/internal/providers"
	"github.com/emrealt34/keelson/backend/internal/stellar"
	"github.com/emrealt34/keelson/backend/internal/store"
)

const version = "1.0.0"

func main() {
	port := env("PORT", "8080")
	network := env("STELLAR_NETWORK", "mainnet")

	rpcURL, horizonURL := "https://mainnet.sorobanrpc.com", "https://horizon.stellar.org"
	if network == "testnet" {
		rpcURL, horizonURL = "https://soroban-testnet.stellar.org", "https://horizon-testnet.stellar.org"
	}
	rpcURL = env("RPC_URL", rpcURL)
	horizonURL = env("HORIZON_URL", horizonURL)

	st := store.New()
	sc := stellar.New(rpcURL, horizonURL)
	llama := providers.NewDefiLlama()
	defindex := providers.NewDeFindex()

	k := &keeper.Keeper{
		LLama:        llama,
		DeFindex:     defindex,
		Stellar:      sc,
		Store:        st,
		Policy:       model.DefaultPolicy(),
		RefreshEvery: time.Duration(envInt("REFRESH_SECONDS", 300)) * time.Second,
		TickEvery:    time.Duration(envInt("TICK_SECONDS", 120)) * time.Second,
		TrackedVault: "demo-vault-001",
		StartVenueID: "blend-pools-v2:ecf788e3",
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go k.Run(ctx)

	srv := &api.Server{
		Store:   st,
		Stellar: sc,
		LLama:   llama,
		Policy:  model.DefaultPolicy(),
		Network: network,
		VaultID: env("VAULT_CONTRACT", ""),
		Version: version,
	}

	httpSrv := &http.Server{
		Addr:              "0.0.0.0:" + port,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("compass api v%s listening on :%s (network=%s, rpc=%s)", version, port, network, rpcURL)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}
