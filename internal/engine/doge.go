package engine

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fladnagmai/wizard-blocks/internal/address"
	"github.com/fladnagmai/wizard-blocks/internal/merged"
	"github.com/fladnagmai/wizard-blocks/internal/node"
)

// Dogecoin merged mining runs whenever a Dogecoin node is configured. The
// DOGE payout address comes from <data_dir>/payout-doge.json (set from the
// web UI, wins) or the config; without one, Litecoin is mined alone and the
// page says so. A node that is down at startup is not fatal: the aux source
// keeps retrying and Litecoin is mined alone meanwhile.

func (e *Engine) dogePayoutPath() string {
	if e.cfg.DataDir == "" {
		return ""
	}
	return filepath.Join(e.cfg.DataDir, "payout-doge.json")
}

// validateDoge checks a DOGE address locally (for the Litecoin node's
// network) and, when the node answers or requireNode is set, with the
// Dogecoin node (same scriptPubKey).
func (e *Engine) validateDoge(ctx context.Context, rpc *node.Client, addr string, requireNode bool) (string, error) {
	addr = strings.TrimSpace(addr)
	net, err := address.NetworkFor(address.DOGE, e.net.Chain)
	if err != nil {
		return "", err
	}
	a, err := address.Decode(net, addr)
	if err != nil {
		return "", fmt.Errorf("invalid DOGE %s address %q: %w", e.net.Chain, addr, err)
	}
	v, err := rpc.ValidateAddress(ctx, addr)
	if err != nil {
		if requireNode {
			return "", fmt.Errorf("cannot verify DOGE address with the Dogecoin node: %w", err)
		}
		return addr, nil
	}
	if !v.IsValid || !strings.EqualFold(v.ScriptPubKey, hex.EncodeToString(a.Script)) {
		return "", fmt.Errorf("Dogecoin node does not confirm DOGE address %q", addr)
	}
	return addr, nil
}

// initDoge connects to the Dogecoin node (if up, it must be a Dogecoin node
// on the same network) and picks the payout address.
func (e *Engine) initDoge(ctx context.Context) (*merged.Doge, error) {
	d := e.cfg.Doge
	rpc := node.NewClient(d.RPCURL, d.RPCUser, d.RPCPassword, d.RPCCookieFile, time.Duration(e.cfg.Node.RPCTimeoutS)*time.Second)
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ci, err := rpc.GetBlockchainInfo(cctx)
	var ni *node.NetworkInfo
	if err == nil {
		ni, err = rpc.GetNetworkInfo(cctx)
	}
	up := err == nil
	if up {
		if !strings.Contains(strings.ToLower(ni.Subversion), "shibetoshi") {
			return nil, fmt.Errorf("doge.rpc_url points at %q, not a Dogecoin node", ni.Subversion)
		}
		if ci.Chain != e.net.Chain {
			return nil, fmt.Errorf("Dogecoin node is on %q but the Litecoin node is on %q", ci.Chain, e.net.Chain)
		}
		e.log.Info("connected to Dogecoin node", "subversion", ni.Subversion, "chain", ci.Chain, "height", ci.Blocks)
	} else {
		e.log.Warn("Dogecoin node not reachable: mining Litecoin alone until it is", "url", d.RPCURL, "err", err)
	}

	addr := ""
	if p := e.dogePayoutPath(); p != "" {
		var saved struct {
			Address string `json:"address"`
		}
		if b, err := os.ReadFile(p); err == nil && json.Unmarshal(b, &saved) == nil && saved.Address != "" {
			if a, err := e.validateDoge(cctx, rpc, saved.Address, false); err == nil {
				addr = a
				e.log.Info("DOGE payout address (saved from the UI)", "address", addr)
			} else {
				e.log.Error("ignoring saved DOGE payout address", "path", p, "err", err)
			}
		}
	}
	if addr == "" && strings.TrimSpace(d.PayoutAddress) != "" {
		a, err := e.validateDoge(cctx, rpc, d.PayoutAddress, false)
		if err != nil {
			return nil, fmt.Errorf("doge.payout_address: %w", err)
		}
		addr = a
	}
	if addr == "" {
		e.log.Warn("no DOGE payout address set: mining Litecoin only until one is set (web UI Settings)")
	}
	return merged.NewDoge(merged.DogeConfig{
		PayoutAddr: addr, ZMQEndpoint: d.ZMQHashBlock,
		Poll:    time.Duration(d.PollIntervalMs) * time.Millisecond,
		Refresh: time.Duration(d.RefreshS) * time.Second,
		DataDir: e.cfg.DataDir,
	}, rpc, e.st, e.log), nil
}

// DogePayout implements ui.DogeBackend: the DOGE payout address ("" if
// none) and whether merged mining is available at all (a node configured).
func (e *Engine) DogePayout() (string, bool) {
	if e.doge == nil {
		return "", false
	}
	return e.doge.PayoutAddress(), true
}

// SetDogePayout implements ui.DogeBackend: a new address must be confirmed
// by the Dogecoin node; "" turns merged mining off. Saved and applied at
// once (miners get fresh jobs with or without the merged-mining tag).
func (e *Engine) SetDogePayout(ctx context.Context, addr string) (string, error) {
	if e.doge == nil {
		return "", errors.New("Dogecoin merged mining is not configured (no Dogecoin node)")
	}
	addr = strings.TrimSpace(addr)
	if addr != "" {
		var err error
		if addr, err = e.validateDoge(ctx, e.doge.RPC(), addr, true); err != nil {
			return "", err
		}
	}
	if p := e.dogePayoutPath(); p != "" {
		b, _ := json.MarshalIndent(map[string]string{"address": addr}, "", "  ")
		tmp := p + ".tmp"
		if err := os.WriteFile(tmp, b, 0o600); err != nil {
			return "", fmt.Errorf("cannot save DOGE payout address: %w", err)
		}
		if err := os.Rename(tmp, p); err != nil {
			return "", fmt.Errorf("cannot save DOGE payout address: %w", err)
		}
	}
	e.doge.SetPayoutAddress(addr)
	if addr == "" {
		e.log.Info("DOGE payout address cleared from the UI: mining Litecoin only")
	} else {
		e.log.Info("DOGE payout address set from the UI", "address", addr)
	}
	return addr, nil
}
