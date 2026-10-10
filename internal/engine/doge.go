package engine

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/fladnagmai/wizard-blocks/internal/address"
	"github.com/fladnagmai/wizard-blocks/internal/merged"
	"github.com/fladnagmai/wizard-blocks/internal/node"
)

// initDoge connects to the Dogecoin node and checks the DOGE payout address
// locally and with that node. It returns nil (Litecoin only) when no
// address is configured. A configured but unreachable node is not fatal:
// the aux source keeps retrying and Litecoin is mined alone meanwhile.
func (e *Engine) initDoge(ctx context.Context) (*merged.Doge, error) {
	d := e.cfg.Doge
	rpc := node.NewClient(d.RPCURL, d.RPCUser, d.RPCPassword, d.RPCCookieFile, time.Duration(e.cfg.Node.RPCTimeoutS)*time.Second)
	if strings.TrimSpace(d.PayoutAddress) == "" {
		e.log.Warn("no DOGE payout address set: mining Litecoin only (no Dogecoin merged mining)")
		return nil, nil
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	addr := strings.TrimSpace(d.PayoutAddress)
	ci, ciErr := rpc.GetBlockchainInfo(cctx)
	var ni *node.NetworkInfo
	if ciErr == nil {
		ni, ciErr = rpc.GetNetworkInfo(cctx)
	}
	if ciErr != nil {
		// Not fatal: mine Litecoin alone and keep retrying. The address is
		// checked locally now and by the node (createauxblock) once it is up.
		net, err := address.NetworkFor(address.DOGE, e.net.Chain)
		if err != nil {
			return nil, err
		}
		if _, err := address.Decode(net, addr); err != nil {
			return nil, fmt.Errorf("invalid DOGE %s payout address %q: %w", e.net.Chain, addr, err)
		}
		e.log.Warn("Dogecoin node not reachable: mining Litecoin alone until it is", "url", d.RPCURL, "err", ciErr)
		return e.newDoge(rpc, addr), nil
	}
	if !strings.Contains(strings.ToLower(ni.Subversion), "shibetoshi") {
		return nil, fmt.Errorf("doge.rpc_url points at %q, not a Dogecoin node", ni.Subversion)
	}
	if ci.Chain != e.net.Chain {
		return nil, fmt.Errorf("Dogecoin node is on %q but the Litecoin node is on %q", ci.Chain, e.net.Chain)
	}
	net, err := address.NetworkFor(address.DOGE, ci.Chain)
	if err != nil {
		return nil, err
	}
	a, err := address.Decode(net, addr)
	if err != nil {
		return nil, fmt.Errorf("invalid DOGE %s payout address %q: %w", ci.Chain, addr, err)
	}
	v, err := rpc.ValidateAddress(cctx, addr)
	if err != nil {
		return nil, fmt.Errorf("cannot verify DOGE address with the Dogecoin node: %w", err)
	}
	if !v.IsValid || !strings.EqualFold(v.ScriptPubKey, hex.EncodeToString(a.Script)) {
		return nil, fmt.Errorf("Dogecoin node does not confirm DOGE address %q", addr)
	}
	e.log.Info("connected to Dogecoin node", "subversion", ni.Subversion, "chain", ci.Chain, "height", ci.Blocks, "payout", addr)
	return e.newDoge(rpc, addr), nil
}

func (e *Engine) newDoge(rpc *node.Client, addr string) *merged.Doge {
	d := e.cfg.Doge
	return merged.NewDoge(merged.DogeConfig{
		PayoutAddr: addr, ZMQEndpoint: d.ZMQHashBlock,
		Poll:    time.Duration(d.PollIntervalMs) * time.Millisecond,
		Refresh: time.Duration(d.RefreshS) * time.Second,
		DataDir: e.cfg.DataDir,
	}, rpc, e.st, e.log)
}
