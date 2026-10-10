// Package merged connects aux chains to the engine for merged mining. Doge
// is a Dogecoin node used through createauxblock/submitauxblock
// (dogecoin src/rpc/mining.cpp, v1.14.9).
package merged

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/fladnagmai/wizard-blocks/internal/auxpow"
	"github.com/fladnagmai/wizard-blocks/internal/bitcoin"
	"github.com/fladnagmai/wizard-blocks/internal/logging"
	"github.com/fladnagmai/wizard-blocks/internal/node"
	"github.com/fladnagmai/wizard-blocks/internal/pow"
	"github.com/fladnagmai/wizard-blocks/internal/stats"
	"github.com/fladnagmai/wizard-blocks/internal/work"
)

// DogeChainID is Dogecoin's AuxPoW chain ID (consensus.nAuxpowChainId).
const DogeChainID = 0x62

// DogeConfig configures the Dogecoin aux source.
type DogeConfig struct {
	PayoutAddr  string
	ZMQEndpoint string
	Poll        time.Duration // tip check without ZMQ
	Refresh     time.Duration // createauxblock refresh (new mempool txs)
	DataDir     string        // rejected proofs are saved here
}

// Doge is a work.AuxSource backed by a Dogecoin node.
type Doge struct {
	cfg DogeConfig
	rpc *node.Client
	zmq *node.ZMQSubscriber
	st  *stats.Collector
	log *slog.Logger

	mu      sync.Mutex
	addr    string // DOGE payout address ("" = merged mining off)
	poke    chan struct{}
	cur     *work.AuxBlock
	prev    string // previousblockhash of cur
	lastErr string
	changed chan struct{}
	wg      sync.WaitGroup
}

// NewDoge creates the Dogecoin aux source.
func NewDoge(cfg DogeConfig, rpc *node.Client, st *stats.Collector, log *slog.Logger) *Doge {
	d := &Doge{cfg: cfg, rpc: rpc, st: st, log: log.With("chain", "doge"), changed: make(chan struct{}, 1),
		addr: cfg.PayoutAddr, poke: make(chan struct{}, 1)}
	if cfg.ZMQEndpoint != "" {
		d.zmq = &node.ZMQSubscriber{Endpoint: cfg.ZMQEndpoint, Topics: []string{"hashblock"}, Log: d.log}
	}
	return d
}

// Current implements work.AuxSource.
func (d *Doge) Current() []work.AuxBlock {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cur == nil {
		return nil
	}
	return []work.AuxBlock{*d.cur}
}

// Changed implements work.AuxSource.
func (d *Doge) Changed() <-chan struct{} { return d.changed }

// PayoutAddress returns the DOGE payout address ("" when not set).
func (d *Doge) PayoutAddress() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.addr
}

// SetPayoutAddress changes the DOGE payout address ("" turns merged mining
// off). The caller has verified it. Work follows within moments.
func (d *Doge) SetPayoutAddress(addr string) {
	d.mu.Lock()
	d.addr = addr
	d.mu.Unlock()
	select {
	case d.poke <- struct{}{}:
	default:
	}
}

// RPC returns the Dogecoin node client.
func (d *Doge) RPC() *node.Client { return d.rpc }

// ZMQConnected reports the ZMQ session state (false without ZMQ).
func (d *Doge) ZMQConnected() bool { return d.zmq != nil && d.zmq.Connected() }

// LastError is the last error talking to the node ("" when healthy).
func (d *Doge) LastError() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastErr
}

func (d *Doge) signal() {
	select {
	case d.changed <- struct{}{}:
	default:
	}
}

// auxBlockResult is createauxblock's answer.
type auxBlockResult struct {
	Hash          string `json:"hash"`
	ChainID       uint32 `json:"chainid"`
	PreviousBlock string `json:"previousblockhash"`
	CoinbaseValue int64  `json:"coinbasevalue"`
	Bits          string `json:"bits"`
	Height        int64  `json:"height"`
	Target        string `json:"target"`
}

// parseAuxBlock checks createauxblock's answer and converts it.
func parseAuxBlock(r *auxBlockResult, payout string) (*work.AuxBlock, error) {
	if r.ChainID != DogeChainID {
		return nil, fmt.Errorf("chain ID %d, want %d (not a Dogecoin node?)", r.ChainID, DogeChainID)
	}
	h, err := bitcoin.HashFromDisplay(r.Hash)
	if err != nil {
		return nil, fmt.Errorf("hash: %w", err)
	}
	bits, err := strconv.ParseUint(r.Bits, 16, 32)
	if err != nil || len(r.Bits) != 8 {
		return nil, fmt.Errorf("bits %q invalid", r.Bits)
	}
	target, err := bitcoin.CompactToTarget(uint32(bits))
	if err != nil || target.Sign() <= 0 {
		return nil, fmt.Errorf("bits %q: %v", r.Bits, err)
	}
	// "target" is the same value in reversed (little-endian) byte order.
	if r.Target != "" {
		tb, err := hex.DecodeString(r.Target)
		if err != nil || len(tb) != 32 {
			return nil, fmt.Errorf("target %q invalid", r.Target)
		}
		for i, j := 0, len(tb)-1; i < j; i, j = i+1, j-1 {
			tb[i], tb[j] = tb[j], tb[i]
		}
		if new(big.Int).SetBytes(tb).Cmp(target) != 0 {
			return nil, fmt.Errorf("target %s disagrees with bits %s", r.Target, r.Bits)
		}
	}
	if r.Height <= 0 || r.CoinbaseValue <= 0 {
		return nil, fmt.Errorf("height %d / coinbasevalue %d invalid", r.Height, r.CoinbaseValue)
	}
	return &work.AuxBlock{Chain: "doge", ChainID: r.ChainID, Hash: h, Target: target,
		Height: r.Height, Reward: r.CoinbaseValue, PayoutAddr: payout}, nil
}

// fetch asks the node for the current aux block and publishes changes.
func (d *Doge) fetch(ctx context.Context, why string) {
	addr := d.PayoutAddress()
	if addr == "" {
		d.mu.Lock()
		lost := d.cur != nil
		d.cur, d.prev, d.lastErr = nil, "", ""
		d.mu.Unlock()
		d.report(false, ReasonNoAddress, nil, d.nodeUp(ctx))
		if lost {
			d.log.Warn("no DOGE payout address: mining Litecoin only")
			d.signal()
		}
		return
	}
	var r auxBlockResult
	err := d.rpc.Call(ctx, "createauxblock", []any{addr}, &r)
	var b *work.AuxBlock
	if err == nil {
		b, err = parseAuxBlock(&r, addr)
	}
	d.mu.Lock()
	if err != nil {
		msg := err.Error()
		lost := d.cur != nil
		first := d.lastErr != msg
		d.cur, d.prev, d.lastErr = nil, "", msg
		d.mu.Unlock()
		reason, up := ReasonNodeDown, false
		var rpcErr *node.RPCError
		if errors.As(err, &rpcErr) {
			up = true
			reason = ReasonNodeError
			if rpcErr.Code == -10 || rpcErr.Code == -9 { // in initial download / not connected
				reason = ReasonNodeSyncing
			}
		}
		d.report(false, reason, nil, up)
		if lost || first {
			d.log.Warn("Dogecoin aux work unavailable: mining Litecoin alone until it is back", "err", msg, "trigger", why)
		}
		if lost {
			d.signal()
		}
		return
	}
	same := d.cur != nil && d.cur.Hash == b.Hash
	back := d.cur == nil
	d.cur, d.prev, d.lastErr = b, r.PreviousBlock, ""
	d.mu.Unlock()
	d.report(true, "", b, true)
	if same {
		return
	}
	if back {
		d.log.Info("Dogecoin aux work available", "height", b.Height, "hash", b.Hash.String(), "trigger", why)
	} else {
		d.log.Debug("Dogecoin aux block changed", "height", b.Height, "hash", b.Hash.String(), "trigger", why)
	}
	d.signal()
}

// Reasons why the aux chain is not being merged.
const (
	ReasonNoAddress   = "no address"
	ReasonNodeDown    = "node down"
	ReasonNodeSyncing = "node syncing"
	ReasonNodeError   = "node error"
)

// nodeUp reports whether the node answers at all.
func (d *Doge) nodeUp(ctx context.Context) bool {
	_, err := d.rpc.GetBestBlockHash(ctx)
	return err == nil
}

// report publishes the chain's status to the stats.
func (d *Doge) report(merged bool, reason string, b *work.AuxBlock, connected bool) {
	addr := d.PayoutAddress()
	zmq := d.ZMQConnected()
	d.st.SetAux("doge", func(a *stats.AuxStatus) {
		a.Merged, a.Reason, a.Connected, a.ZMQ, a.Address = merged, reason, connected, zmq, addr
		if b != nil {
			a.Height = b.Height
			a.NetworkDiff = pow.Scrypt.Difficulty(b.Target)
		}
	})
}

// Run keeps the aux block current until ctx ends: on each new Dogecoin
// block (ZMQ, or a poll of the tip) and periodically for new transactions.
func (d *Doge) Run(ctx context.Context) {
	zmqCh := make(chan node.ZMQMessage, 8)
	if d.zmq != nil {
		go d.zmq.Run(ctx, zmqCh)
	}
	poll := time.NewTicker(d.cfg.Poll)
	defer poll.Stop()
	refresh := time.NewTicker(d.cfg.Refresh)
	defer refresh.Stop()
	d.fetch(ctx, "startup")
	for {
		select {
		case <-ctx.Done():
			d.wg.Wait()
			return
		case <-zmqCh:
			d.fetch(ctx, "zmq")
		case <-d.poke:
			d.fetch(ctx, "address")
		case <-poll.C:
			best, err := d.rpc.GetBestBlockHash(ctx)
			d.mu.Lock()
			stale := err != nil || d.cur == nil || best != d.prev
			d.mu.Unlock()
			if stale {
				d.fetch(ctx, "poll")
			}
		case <-refresh.C:
			d.fetch(ctx, "refresh")
		}
	}
}

// Submit implements work.AuxSource: submitauxblock, then confirm the block
// is on Dogecoin's active chain.
func (d *Doge) Submit(b work.AuxBlock, ap *auxpow.AuxPow, worker string, shareDiff float64, parentHash bitcoin.Hash) {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.submit(b, ap, worker, shareDiff, parentHash)
	}()
}

func (d *Doge) submit(b work.AuxBlock, ap *auxpow.AuxPow, worker string, shareDiff float64, parentHash bitcoin.Hash) {
	hash := b.Hash.String()
	rec := stats.BlockRecord{
		Chain: "doge", Height: b.Height, Hash: hash, Worker: worker, Address: b.PayoutAddr, Reward: b.Reward,
		Time: time.Now(), Status: "pending", ShareDiff: shareDiff, NetworkDiff: pow.Scrypt.Difficulty(b.Target),
		ParentHash: parentHash.String(),
	}
	d.log.Log(context.Background(), logging.LevelBlock, "*** DOGE BLOCK FOUND — submitting ***",
		"height", b.Height, "hash", hash, "worker", worker, "payout", b.PayoutAddr, "reward_koinu", b.Reward, "parent", rec.ParentHash)
	d.st.AuxBlockSubmitted(rec)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	auxHex := hex.EncodeToString(ap.Serialize())
	var ok bool
	var err error
	for attempt := 1; attempt <= 6; attempt++ {
		err = d.rpc.Call(ctx, "submitauxblock", []any{hash, auxHex}, &ok)
		var rpcErr *node.RPCError
		if err == nil || errors.As(err, &rpcErr) {
			break
		}
		d.log.Error("submitauxblock transport error; retrying", "attempt", attempt, "hash", hash, "err", err)
		time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
	}
	status := "pending"
	switch {
	case err != nil:
		status, rec.Reason = "rejected", err.Error()
	case !ok:
		status, rec.Reason = "rejected", "submitauxblock returned false (the reason is in the Dogecoin node's log)"
	}
	if status == "rejected" {
		d.keepRejected(b, rec.Reason, auxHex)
	}
	for i := 0; i < 20 && status != "rejected"; i++ {
		if hdr, herr := d.rpc.GetBlockHeader(ctx, hash); herr == nil {
			if hdr.Confirmations >= 1 {
				status = "accepted"
				break
			}
			if hdr.Confirmations == -1 && i >= 5 {
				status = "orphaned"
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if status == "pending" {
		status, rec.Reason = "rejected", "block unknown to the Dogecoin node after submit"
	}
	rec.Status = status
	d.st.AuxBlockSubmitted(rec)
	if status == "accepted" {
		d.log.Log(context.Background(), logging.LevelBlock, "*** DOGE BLOCK ACCEPTED — on active chain ***",
			"height", b.Height, "hash", hash, "worker", worker, "payout", b.PayoutAddr, "reward_koinu", b.Reward)
	} else {
		d.log.Error("DOGE block not on active chain", "height", b.Height, "hash", hash, "status", status, "reason", rec.Reason)
	}
}

// keepRejected logs the full AuxPoW of a refused aux block and saves it.
func (d *Doge) keepRejected(b work.AuxBlock, reason, auxHex string) {
	path := ""
	if d.cfg.DataDir != "" {
		dir := filepath.Join(d.cfg.DataDir, "rejected-blocks")
		p := filepath.Join(dir, fmt.Sprintf("doge-%d-%s.auxpow.hex", b.Height, b.Hash.String()))
		if os.MkdirAll(dir, 0o700) == nil && os.WriteFile(p, []byte(auxHex+"\n"), 0o600) == nil {
			path = p
		}
	}
	d.log.Error("rejected DOGE auxpow hex", "height", b.Height, "hash", b.Hash.String(), "reason", reason, "saved", path, "hex", auxHex)
}
