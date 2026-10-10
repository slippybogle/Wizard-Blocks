// Package nodestatus polls the Litecoin and Dogecoin nodes over RPC and
// serves one plain page with both nodes' live status (the page of the
// Litecoin + Dogecoin Node Umbrel app).
package nodestatus

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/fladnagmai/wizard-blocks/internal/node"
)

// Node is one node to watch.
type Node struct {
	Name string // "Litecoin Node"
	RPC  *node.Client
}

// Status is one node's state at the last check. Fields a node does not
// report stay zero.
type Status struct {
	Name         string  `json:"name"`
	State        string  `json:"state"` // "synced", "syncing", "starting", "down"
	Error        string  `json:"error,omitempty"`
	Version      string  `json:"version,omitempty"`
	Chain        string  `json:"chain,omitempty"`
	Blocks       int64   `json:"blocks"`
	Headers      int64   `json:"headers"`
	Progress     float64 `json:"progress"`             // verificationprogress, 0..1
	Peers        *int    `json:"peers,omitempty"`      // nil: getnetworkinfo failed
	MempoolTx    *int64  `json:"mempool_tx,omitempty"` // nil: getmempoolinfo failed
	MempoolBytes int64   `json:"mempool_bytes"`
	TipTime      int64   `json:"tip_time,omitempty"` // unix time of the best block
	Difficulty   float64 `json:"difficulty"`
	Pruned       bool    `json:"pruned"`
	SizeOnDisk   int64   `json:"size_on_disk,omitempty"`
	Checked      int64   `json:"checked"` // unix time of this check
}

// Poller checks every node each interval and keeps the latest statuses.
type Poller struct {
	nodes    []Node
	interval time.Duration

	mu   sync.Mutex
	last []Status
}

// NewPoller creates a poller for nodes (checked in this order).
func NewPoller(nodes []Node, interval time.Duration) *Poller {
	p := &Poller{nodes: nodes, interval: interval, last: make([]Status, len(nodes))}
	for i, n := range nodes {
		p.last[i] = Status{Name: n.Name, State: "starting", Error: "not checked yet"}
	}
	return p
}

// Snapshot returns the latest statuses.
func (p *Poller) Snapshot() []Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Status(nil), p.last...)
}

// Run checks all nodes now and then every interval until ctx ends. Nodes
// are checked concurrently so a slow or down node does not delay the other.
func (p *Poller) Run(ctx context.Context) {
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		p.CheckAll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// CheckAll checks every node once.
func (p *Poller) CheckAll(ctx context.Context) {
	var wg sync.WaitGroup
	for i, n := range p.nodes {
		wg.Add(1)
		go func(i int, n Node) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			s := Check(cctx, n)
			cancel()
			p.mu.Lock()
			p.last[i] = s
			p.mu.Unlock()
		}(i, n)
	}
	wg.Wait()
}

// chainInfo is the part of getblockchaininfo shown (Litecoin Core 0.21 and
// Dogecoin Core 1.14 both answer these fields).
type chainInfo struct {
	Chain         string  `json:"chain"`
	Blocks        int64   `json:"blocks"`
	Headers       int64   `json:"headers"`
	BestBlockHash string  `json:"bestblockhash"`
	Difficulty    float64 `json:"difficulty"`
	Progress      float64 `json:"verificationprogress"`
	Pruned        bool    `json:"pruned"`
	SizeOnDisk    int64   `json:"size_on_disk"`
}

// Check asks one node for its state.
func Check(ctx context.Context, n Node) Status {
	s := Status{Name: n.Name, Checked: time.Now().Unix()}
	var ci chainInfo
	if err := n.RPC.Call(ctx, "getblockchaininfo", nil, &ci); err != nil {
		var re *node.RPCError
		if errors.As(err, &re) && re.Code == -28 {
			// RPC_IN_WARMUP: loading the block index, verifying blocks...
			s.State, s.Error = "starting", strings.TrimSpace(re.Message)
		} else {
			s.State, s.Error = "down", err.Error()
		}
		return s
	}
	s.Chain, s.Blocks, s.Headers, s.Difficulty = ci.Chain, ci.Blocks, ci.Headers, ci.Difficulty
	s.Progress, s.Pruned, s.SizeOnDisk = ci.Progress, ci.Pruned, ci.SizeOnDisk
	var ni struct {
		Subversion  string `json:"subversion"`
		Connections int    `json:"connections"`
	}
	if n.RPC.Call(ctx, "getnetworkinfo", nil, &ni) == nil {
		s.Version, s.Peers = ni.Subversion, &ni.Connections
	}
	var mi struct {
		Size  int64 `json:"size"`
		Bytes int64 `json:"bytes"`
	}
	if n.RPC.Call(ctx, "getmempoolinfo", nil, &mi) == nil {
		s.MempoolTx, s.MempoolBytes = &mi.Size, mi.Bytes
	}
	if ci.BestBlockHash != "" {
		var h struct {
			Time int64 `json:"time"`
		}
		if n.RPC.Call(ctx, "getblockheader", []any{ci.BestBlockHash}, &h) == nil {
			s.TipTime = h.Time
		}
	}
	// Synced: has every header it knows of and has verified (nearly) the
	// whole chain. A node with no headers yet is still finding peers.
	if s.Headers > 0 && s.Blocks == s.Headers && s.Progress >= 0.9999 {
		s.State = "synced"
	} else {
		s.State = "syncing"
	}
	return s
}
