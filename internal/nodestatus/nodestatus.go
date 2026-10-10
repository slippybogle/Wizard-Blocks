// Package nodestatus polls the Litecoin and Dogecoin nodes over RPC and
// serves one plain page with both nodes' live status (the page of the
// Litecoin + Dogecoin Node Umbrel app).
package nodestatus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fladnagmai/wizard-blocks/internal/node"
)

// Node is one node to watch.
type Node struct {
	Key      string // "ltc", "doge": used in the API
	Name     string // "Litecoin Node"
	RPC      *node.Client
	Pausable bool // its sync (p2p network) can be paused from the page
}

// Status is one node's state at the last check. Fields a node does not
// report stay zero.
type Status struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	State string `json:"state"` // "synced", "syncing", "starting", "down"
	// Pausable: the page offers pause/resume. Paused: a pause is in force
	// (kept across node restarts). NetworkActive: the node's own p2p switch
	// (nil if unknown).
	Pausable      bool    `json:"pausable"`
	Paused        bool    `json:"paused"`
	NetworkActive *bool   `json:"network_active,omitempty"`
	Error         string  `json:"error,omitempty"`
	Version       string  `json:"version,omitempty"`
	Chain         string  `json:"chain,omitempty"`
	Blocks        int64   `json:"blocks"`
	Headers       int64   `json:"headers"`
	Progress      float64 `json:"progress"`             // verificationprogress, 0..1
	Peers         *int    `json:"peers,omitempty"`      // nil: getnetworkinfo failed
	MempoolTx     *int64  `json:"mempool_tx,omitempty"` // nil: getmempoolinfo failed
	MempoolBytes  int64   `json:"mempool_bytes"`
	TipTime       int64   `json:"tip_time,omitempty"` // unix time of the best block
	Difficulty    float64 `json:"difficulty"`
	Pruned        bool    `json:"pruned"`
	SizeOnDisk    int64   `json:"size_on_disk,omitempty"`
	Checked       int64   `json:"checked"` // unix time of this check
}

// Poller checks every node each interval and keeps the latest statuses.
type Poller struct {
	nodes     []Node
	interval  time.Duration
	stateFile string // where pauses are kept ("" = memory only)

	mu     sync.Mutex
	last   []Status
	paused map[string]bool // by node key
}

// NewPoller creates a poller for nodes (checked in this order). Pauses set
// from the page are kept in stateFile (if set) and re-applied after a node
// or this service restarts.
func NewPoller(nodes []Node, interval time.Duration, stateFile string) *Poller {
	p := &Poller{nodes: nodes, interval: interval, stateFile: stateFile, last: make([]Status, len(nodes)), paused: map[string]bool{}}
	if stateFile != "" {
		var saved struct {
			Paused map[string]bool `json:"paused"`
		}
		if b, err := os.ReadFile(stateFile); err == nil && json.Unmarshal(b, &saved) == nil {
			for _, n := range nodes {
				if n.Pausable && saved.Paused[n.Key] {
					p.paused[n.Key] = true
				}
			}
		}
	}
	for i, n := range nodes {
		p.last[i] = Status{Key: n.Key, Name: n.Name, Pausable: n.Pausable, Paused: p.paused[n.Key], State: "starting", Error: "not checked yet"}
	}
	return p
}

// Errors from SetPaused.
var (
	ErrUnknownNode = errors.New("unknown node")
	ErrNotPausable = errors.New("this node's sync cannot be paused here")
)

// SetPaused pauses (or resumes) a node's sync: its p2p network is switched
// off (on) at once, and the choice is saved and re-applied on every check,
// so it survives node restarts. Resuming always switches the network on.
func (p *Poller) SetPaused(ctx context.Context, key string, paused bool) error {
	var n *Node
	for i := range p.nodes {
		if p.nodes[i].Key == key {
			n = &p.nodes[i]
		}
	}
	if n == nil {
		return ErrUnknownNode
	}
	if !n.Pausable {
		return ErrNotPausable
	}
	p.mu.Lock()
	prev := p.paused[key]
	p.paused[key] = paused
	err := p.saveLocked()
	if err != nil {
		p.paused[key] = prev
	}
	p.mu.Unlock()
	if err != nil {
		return fmt.Errorf("cannot save the pause: %w", err)
	}
	if err := n.RPC.Call(ctx, "setnetworkactive", []any{!paused}, nil); err != nil {
		// Saved anyway: the next check re-applies it once the node answers.
		return fmt.Errorf("saved; the node did not answer yet (applied when it does): %w", err)
	}
	p.CheckAll(ctx)
	return nil
}

func (p *Poller) saveLocked() error {
	if p.stateFile == "" {
		return nil
	}
	b, _ := json.MarshalIndent(map[string]any{"paused": p.paused}, "", "  ")
	tmp := p.stateFile + ".tmp"
	if err := os.MkdirAll(filepath.Dir(p.stateFile), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p.stateFile)
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
			defer cancel()
			s := Check(cctx, n)
			p.mu.Lock()
			paused := p.paused[n.Key]
			p.mu.Unlock()
			// Keep a pause in force: a restarted node comes back with its
			// network on.
			if paused && s.NetworkActive != nil && *s.NetworkActive {
				if n.RPC.Call(cctx, "setnetworkactive", []any{false}, nil) == nil {
					off := false
					s.NetworkActive = &off
				}
			}
			s.Paused = paused
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
	s := Status{Key: n.Key, Name: n.Name, Pausable: n.Pausable, Checked: time.Now().Unix()}
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
		Subversion    string `json:"subversion"`
		Connections   int    `json:"connections"`
		NetworkActive *bool  `json:"networkactive"`
	}
	if n.RPC.Call(ctx, "getnetworkinfo", nil, &ni) == nil {
		s.Version, s.Peers, s.NetworkActive = ni.Subversion, &ni.Connections, ni.NetworkActive
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
