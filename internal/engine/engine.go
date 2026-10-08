// Package engine wires the node client, work manager, Stratum server and
// stats API together and performs the startup safety checks.
package engine

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/slippybogle/wizard-blocks/internal/address"
	"github.com/slippybogle/wizard-blocks/internal/config"
	"github.com/slippybogle/wizard-blocks/internal/node"
	"github.com/slippybogle/wizard-blocks/internal/stats"
	"github.com/slippybogle/wizard-blocks/internal/stratum"
	"github.com/slippybogle/wizard-blocks/internal/ui"
	"github.com/slippybogle/wizard-blocks/internal/work"
)

// Engine is one running instance (one coin).
type Engine struct {
	cfg config.Config
	log *slog.Logger
	rpc *node.Client
	st  *stats.Collector
	mgr *work.Manager
	srv *stratum.Server
	api *stats.Server
	ui  *ui.Server
	ver string

	settingsMu sync.Mutex
	baseDiff   stratum.DiffSettings // from config/env
	net        *address.Network

	payoutMu sync.RWMutex
	fixed    *stratum.Payout // nil in fixed mode until a payout address is set

	cacheMu sync.Mutex
	cache   map[string]*stratum.Payout
}

// New creates an engine. It performs no I/O.
func New(cfg config.Config, version string, log *slog.Logger) *Engine {
	return &Engine{
		cfg:   cfg,
		log:   log,
		rpc:   node.NewClient(cfg.Node.RPCURL, cfg.Node.RPCUser, cfg.Node.RPCPassword, cfg.Node.RPCCookieFile, time.Duration(cfg.Node.RPCTimeoutS)*time.Second),
		st:    stats.New(cfg.Coin, version, cfg.DataDir),
		ver:   version,
		cache: map[string]*stratum.Payout{},
	}
}

// Stats exposes the collector (used by tests and the API).
func (e *Engine) Stats() *stats.Collector { return e.st }

// StratumAddr returns the bound Stratum address (after Start).
func (e *Engine) StratumAddr() string { return e.srv.Addr() }

// APIAddr returns the bound API address (after Start).
func (e *Engine) APIAddr() string { return e.api.Addr() }

// UIAddr returns the bound web UI address ("" if disabled).
func (e *Engine) UIAddr() string {
	if e.ui == nil {
		return ""
	}
	return e.ui.Addr()
}

// Manager exposes the work manager (used by tests).
func (e *Engine) Manager() *work.Manager { return e.mgr }

// connect waits for the node RPC to answer and returns chain/network info.
func (e *Engine) connect(ctx context.Context) (*node.ChainInfo, *node.NetworkInfo, error) {
	var last error
	for i := 0; ; i++ {
		ci, err := e.rpc.GetBlockchainInfo(ctx)
		if err == nil {
			ni, err := e.rpc.GetNetworkInfo(ctx)
			if err == nil {
				return ci, ni, nil
			}
			last = err
		} else {
			last = err
		}
		if strings.Contains(last.Error(), "unauthorized") {
			return nil, nil, last
		}
		if i%10 == 0 {
			e.log.Warn("waiting for node RPC", "url", e.cfg.Node.RPCURL, "err", last)
		}
		select {
		case <-ctx.Done():
			return nil, nil, fmt.Errorf("node not reachable: %w", last)
		case <-time.After(2 * time.Second):
		}
	}
}

// checkCoin refuses obviously mismatched coin/node combinations.
func checkCoin(coin, subversion string) error {
	s := strings.ToLower(subversion)
	bchLike := strings.Contains(s, "cash") || strings.Contains(s, "bch") || strings.Contains(s, "flowee")
	btcLike := strings.Contains(s, "/satoshi:")
	switch {
	case coin == "btc" && bchLike:
		return fmt.Errorf("coin is btc but node is %q (a Bitcoin Cash node)", subversion)
	case coin == "bch" && btcLike && !bchLike:
		return fmt.Errorf("coin is bch but node is %q (a Bitcoin node)", subversion)
	}
	return nil
}

// ValidatePayout decodes addr locally and with the node's validateaddress;
// both must produce the identical scriptPubKey.
func (e *Engine) ValidatePayout(ctx context.Context, addr string) (*stratum.Payout, error) {
	a, err := address.Decode(e.net, addr)
	if err != nil {
		return nil, fmt.Errorf("invalid %s %s address %q: %w", e.net.Coin, e.net.Chain, addr, err)
	}
	nodeAddr := strings.TrimSpace(addr)
	if e.net.Coin == address.BCH && !strings.Contains(nodeAddr, ":") && a.Type != "" {
		if _, _, _, err := address.CashAddrDecode(nodeAddr, e.net.CashPrefix); err == nil {
			nodeAddr = e.net.CashPrefix + ":" + strings.ToLower(nodeAddr)
		}
	}
	v, err := e.rpc.ValidateAddress(ctx, nodeAddr)
	if err != nil {
		return nil, fmt.Errorf("cannot verify address %q with node: %w", addr, err)
	}
	if !v.IsValid {
		return nil, fmt.Errorf("node rejects address %q as invalid", addr)
	}
	if !strings.EqualFold(v.ScriptPubKey, hex.EncodeToString(a.Script)) {
		return nil, fmt.Errorf("address %q: node scriptPubKey %s differs from decoded %x; refusing", addr, v.ScriptPubKey, a.Script)
	}
	return &stratum.Payout{Script: a.Script, Address: nodeAddr}, nil
}

// resolve implements stratum.PayoutResolver.
func (e *Engine) resolve(ctx context.Context, username string) (*stratum.Payout, error) {
	if e.cfg.Payout.Mode == "fixed" {
		if p := e.fixedPayout(); p != nil {
			return p, nil
		}
		return nil, errNoPayout
	}
	addr, _, _ := strings.Cut(strings.TrimSpace(username), ".")
	e.cacheMu.Lock()
	p := e.cache[addr]
	e.cacheMu.Unlock()
	if p != nil {
		return p, nil
	}
	p, err := e.ValidatePayout(ctx, addr)
	if err != nil {
		return nil, err
	}
	e.cacheMu.Lock()
	if len(e.cache) > 10000 {
		clear(e.cache)
	}
	e.cache[addr] = p
	e.cacheMu.Unlock()
	return p, nil
}

// Start performs startup checks, binds listeners and starts background
// work. It returns an error (and the engine must not run) if the node is
// the wrong coin or the payout address is not valid.
func (e *Engine) Start(ctx context.Context) error {
	ci, ni, err := e.connect(ctx)
	if err != nil {
		return err
	}
	if err := checkCoin(e.cfg.Coin, ni.Subversion); err != nil {
		return err
	}
	if e.net, err = address.NetworkFor(address.Coin(e.cfg.Coin), ci.Chain); err != nil {
		return err
	}
	e.log.Info("connected to node", "subversion", ni.Subversion, "chain", ci.Chain, "height", ci.Blocks)
	e.st.SetNode(func(n *stats.NodeStatus) { n.Subversion, n.Chain, n.Connected = ni.Subversion, ci.Chain, true })

	if e.cfg.Payout.Mode == "fixed" {
		if err := e.initPayout(ctx); err != nil {
			return err
		}
	} else if e.cfg.Payout.Address != "" {
		return errors.New("payout.address must be empty in miner mode (each miner's username is its payout address)")
	}

	params, err := work.ParamsFor(address.Coin(e.cfg.Coin))
	if err != nil {
		return err
	}
	e.mgr = work.NewManager(work.Config{
		Params: params, Chain: ci.Chain, Tag: []byte(e.cfg.Payout.CoinbaseTag),
		Extranonce2Size: e.cfg.Stratum.Extranonce2Size,
		PollInterval:    time.Duration(e.cfg.Node.PollIntervalMs) * time.Millisecond,
		RefreshInterval: time.Duration(e.cfg.Node.TemplateRefreshS) * time.Second,
		ZMQEndpoint:     e.cfg.Node.ZMQHashBlock,
	}, e.rpc, e.st, e.log)

	// Fail fast on a coinbase that can never be valid (e.g. tag too long).
	if _, err := work.BuildCoinbase(work.CoinbaseParams{
		Height: 1 << 30, Tag: []byte(e.cfg.Payout.CoinbaseTag), Extranonce2Size: e.cfg.Stratum.Extranonce2Size,
		PayoutScript: make([]byte, 40), Value: 1, WitnessCommitment: make([]byte, 38), MinTxSize: params.MinTxSize,
	}); err != nil {
		return err
	}

	v := e.cfg.Vardiff
	e.srv = stratum.NewServer(stratum.Config{
		Listen: e.cfg.Stratum.Listen, Extranonce2Size: e.cfg.Stratum.Extranonce2Size,
		VersionRollingMask: e.cfg.VersionMask(),
		MaxConnections:     e.cfg.Stratum.MaxConnections, MaxConnsPerIP: e.cfg.Stratum.MaxConnsPerIP,
		AuthTimeout:  time.Duration(e.cfg.Stratum.AuthTimeoutS) * time.Second,
		IdleTimeout:  time.Duration(e.cfg.Stratum.IdleTimeoutS) * time.Second,
		MaxLineBytes: e.cfg.Stratum.MaxLineBytes, MsgRate: e.cfg.Stratum.MsgRatePerS, MsgBurst: e.cfg.Stratum.MsgBurst,
		Vardiff: stratum.VardiffConfig{
			Initial: v.Initial, Min: v.Min, Max: v.Max,
			TargetShare: time.Duration(v.TargetShareS * float64(time.Second)),
			FixedDiff:   v.FixedDiff,
			Retarget:    time.Duration(v.RetargetS * float64(time.Second)),
			VariancePct: v.VariancePct,
		},
	}, e.mgr, e.resolve, e.st, e.log)
	e.baseDiff = e.srv.DiffSettings()
	e.loadSavedSettings()
	if err := e.srv.Listen(); err != nil {
		return fmt.Errorf("stratum listen: %w", err)
	}
	e.api = &stats.Server{C: e.st, Prometheus: e.cfg.API.Prometheus, Log: e.log}
	if err := e.api.Listen(e.cfg.API.Listen); err != nil {
		return fmt.Errorf("api listen: %w", err)
	}
	if e.cfg.UI.Listen != "" {
		e.ui = ui.New(ui.Config{
			Coin: e.cfg.Coin, Version: e.ver, StratumPort: e.cfg.StratumPort(), PayoutMode: e.cfg.Payout.Mode,
			Extranonce2Size: e.cfg.Stratum.Extranonce2Size,
			VersionMask:     e.cfg.Stratum.VersionRollingMask, DataDir: e.cfg.DataDir,
			AdminPassword: e.cfg.UI.AdminPassword,
		}, e.st, e.rpc, e.log)
		e.ui.SetSettingsBackend(e)
		if err := e.ui.Listen(e.cfg.UI.Listen); err != nil {
			return fmt.Errorf("ui listen: %w", err)
		}
	}
	e.log.Info("listening", "stratum", e.srv.Addr(), "api", e.api.Addr(), "ui", e.UIAddr(), "payout_mode", e.cfg.Payout.Mode)
	return nil
}

// Run serves until ctx is cancelled, then shuts down gracefully: miners are
// disconnected, in-flight block submissions are allowed to finish, and state
// is persisted.
func (e *Engine) Run(ctx context.Context) error {
	errc := make(chan error, 4)
	if e.ui != nil {
		go func() { errc <- e.ui.Serve() }()
		go e.ui.Run(ctx)
	}
	go func() { errc <- e.srv.Serve(ctx) }()
	go func() { errc <- e.api.Serve() }()
	go func() { errc <- e.mgr.Run(ctx) }()

	saveTick := time.NewTicker(time.Minute)
	defer saveTick.Stop()
	var runErr error
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case err := <-errc:
			if err != nil && ctx.Err() == nil {
				runErr = err
				break loop
			}
		case <-saveTick.C:
			if err := e.st.Save(); err != nil {
				e.log.Warn("saving state failed", "err", err)
			}
		}
	}
	e.log.Info("shutting down")
	e.srv.Close()
	wctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	e.mgr.Wait(wctx)
	cancel()
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = e.api.Shutdown(sctx)
	if e.ui != nil {
		_ = e.ui.Shutdown(sctx)
	}
	cancel()
	if err := e.st.Save(); err != nil {
		e.log.Warn("saving state failed", "err", err)
	}
	e.log.Info("stopped")
	return runErr
}
