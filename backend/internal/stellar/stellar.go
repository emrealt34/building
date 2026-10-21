// Package stellar is the only place that talks to the Stellar network.
//
// Read-only by design: the backend never holds a key, never signs and never submits.
// It reads network state (RPC + Horizon) and produces unsigned intents for the wallet
// to build and sign. That is what "non-custodial" means at the code level.
package stellar

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/emrealt34/keelson/backend/internal/model"
)

type Client struct {
	RPCURL     string
	HorizonURL string
	HTTP       *http.Client
}

func New(rpcURL, horizonURL string) *Client {
	return &Client{
		RPCURL:     rpcURL,
		HorizonURL: horizonURL,
		HTTP:       &http.Client{Timeout: 20 * time.Second},
	}
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
}

type healthResult struct {
	Status                string `json:"status"`
	LatestLedger          uint32 `json:"latestLedger"`
	LatestLedgerCloseTime string `json:"latestLedgerCloseTime"`
}

// Status reads RPC health + latest ledger, and Horizon fee stats.
func (c *Client) Status(ctx context.Context) model.NetworkStatus {
	out := model.NetworkStatus{CheckedAt: time.Now().UTC()}

	if h, err := c.health(ctx); err == nil {
		out.RPCHealthy = h.Status == "healthy"
		out.LatestLedger = h.LatestLedger
		if secs, err := strconv.ParseInt(h.LatestLedgerCloseTime, 10, 64); err == nil {
			out.CloseTime = secs
		}
	}

	if proto, ok := c.protocol(ctx); ok {
		out.Protocol = proto
	}

	if fee, ok := c.feeStats(ctx); ok {
		out.HorizonOK = true
		out.BaseFeeStroops = fee
	}
	return out
}

func (c *Client) health(ctx context.Context) (healthResult, error) {
	var hr healthResult
	body, _ := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: 1, Method: "getHealth"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.RPCURL, bytes.NewReader(body))
	if err != nil {
		return hr, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return hr, err
	}
	defer resp.Body.Close()
	var payload struct {
		Result healthResult `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return hr, err
	}
	if payload.Error != nil {
		return hr, fmt.Errorf("rpc: %s", payload.Error.Message)
	}
	return payload.Result, nil
}

func (c *Client) protocol(ctx context.Context) (uint32, bool) {
	body, _ := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: 2, Method: "getNetwork"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.RPCURL, bytes.NewReader(body))
	if err != nil {
		return 0, false
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	var payload struct {
		Result struct {
			Protocol uint32 `json:"protocolVersion"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0, false
	}
	return payload.Result.Protocol, payload.Result.Protocol > 0
}

func (c *Client) feeStats(ctx context.Context) (int64, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.HorizonURL+"/fee_stats", nil)
	if err != nil {
		return 0, false
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	var payload struct {
		LastLedgerBaseFee string `json:"last_ledger_base_fee"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0, false
	}
	fee, err := strconv.ParseInt(payload.LastLedgerBaseFee, 10, 64)
	if err != nil {
		return 0, false
	}
	return fee, true
}

// AccountBalance reads a classic account's balances from Horizon. Used by the UI to show
// the user's real position without asking for any credential.
func (c *Client) AccountBalance(ctx context.Context, address string) model.AccountBalance {
	out := model.AccountBalance{Address: address}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.HorizonURL+"/accounts/"+address, nil)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		out.Error = "account not found on this network"
		return out
	}
	var payload struct {
		Balances []struct {
			AssetType      string `json:"asset_type"`
			AssetCode      string `json:"asset_code"`
			Balance        string `json:"balance"`
			LiquidityPools []struct {
				Reserves []struct {
					Asset  string `json:"asset"`
					Amount string `json:"amount"`
				} `json:"reserves"`
			} `json:"liquidity_pools"`
		} `json:"balances"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		out.Error = err.Error()
		return out
	}
	out.Found = true
	for _, b := range payload.Balances {
		amount, _ := strconv.ParseFloat(b.Balance, 64)
		name := b.AssetCode
		if b.AssetType == "native" {
			name = "XLM"
		}
		out.Balances = append(out.Balances, model.AssetBalance{Asset: name, Amount: amount})
	}
	return out
}

// Intent is an unsigned contract call. The frontend (or the user's wallet) turns this
// into a Soroban transaction, simulates it, and signs it locally.
type Intent struct {
	Network      string         `json:"network"`
	ContractID   string         `json:"contract_id"`
	Function     string         `json:"function"`
	Args         map[string]any `json:"args"`
	Description  string         `json:"description"`
	RequiresAuth string         `json:"requires_auth"`
	BuiltBy      string         `json:"built_by"`
}

// UserVaultAddress derives the deterministic vault address label for a user. Real address
// derivation needs the factory's deploy salt; the placeholder is explicit so nobody
// confuses it with a funded address.
func UserVaultAddress(owner string) string {
	return "pending-deploy:" + owner
}
