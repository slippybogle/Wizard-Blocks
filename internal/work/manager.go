package work

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/slippybogle/wizard-blocks/internal/bitcoin"
	"github.com/slippybogle/wizard-blocks/internal/logging"
	"github.com/slippybogle/wizard-blocks/internal/node"
	"github.com/slippybogle/wizard-blocks/internal/stats"
)

// Config configures a Manager.
type Config struct {
	Params          CoinParams
	Chain           string
	Tag             []byte
	Extranonce2Size int
	PollInterval    time.Duration
	RefreshInterval time.Duration
	ZMQEndpoint     string
}

// Work is the current unit of mining state: one verified template. Jobs for
// individual payout scripts are derived lazily from it.
type Work struct {
	Tmpl  *Template
	Gen   uint64
	Clean bool // the previous block changed: miners must drop old work

	m        *Manager
	mu       sync.Mutex
	byScript map[string]*Job
}

// Job returns (creating if needed) the job paying script.
func (w *Work) Job(script []byte, addr string) (*Job, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if j := w.byScript[string(script)]; j != nil {
		return j, nil
	}
	cb, err := BuildCoinbase(CoinbaseParams{
		Height:            w.Tmpl.Height,
		Tag:               w.m.cfg.Tag,
		Extranonce2Size:   w.m.cfg.Extranonce2Size,
		PayoutScript:      script,
		Value:             w.Tmpl.CoinbaseValue,
		WitnessCommitment: w.Tmpl.WitnessCommitment,
		MinTxSize:         w.m.cfg.Params.MinTxSize,
	})
	if err != nil {
		return nil, err
	}
	j := newJob(w.m.nextJobID(), w.Gen, w.Tmpl, append([]byte(nil), script...), addr, cb)
	w.m.register(j)
	w.byScript[string(script)] = j
	return j, nil
}

// Manager owns templates, jobs and block submission.
type Manager struct {
	cfg Config
	rpc *node.Client
	log *slog.Logger
	st  *stats.Collector
	zmq *node.ZMQSubscriber

	mu        sync.RWMutex
	cur       *Work
	gen       uint64
	jobs      map[string]*Job
	listeners []func(*Work)

	jobSeq    atomic.Uint64
	kick      chan struct{}
	submitWG  sync.WaitGroup
	submitted sync.Map // block hash -> struct{}
	ready     chan struct{}
	readyOnce sync.Once
}

// NewManager creates a manager.
func NewManager(cfg Config, rpc *node.Client, st *stats.Collector, log *slog.Logger) *Manager {
	m := &Manager{
		cfg: cfg, rpc: rpc, st: st, log: log,
		jobs: map[string]*Job{}, kick: make(chan struct{}, 1), ready: make(chan struct{}),
	}
	m.jobSeq.Store(uint64(time.Now().Unix()) << 20) // ids unique across restarts
	if cfg.ZMQEndpoint != "" {
		m.zmq = &node.ZMQSubscriber{Endpoint: cfg.ZMQEndpoint, Topics: []string{"hashblock"}, Log: log}
	}
	st.SetNode(func(n *stats.NodeStatus) { n.ZMQEnabled = m.zmq != nil; n.Chain = cfg.Chain })
	return m
}

// OnWork registers a callback invoked (synchronously, in order) for every new Work.
func (m *Manager) OnWork(f func(*Work)) {
	m.mu.Lock()
	m.listeners = append(m.listeners, f)
	m.mu.Unlock()
}

// Ready is closed once the first template is available.
func (m *Manager) Ready() <-chan struct{} { return m.ready }

// Current returns the current work (nil before the first template).
func (m *Manager) Current() *Work {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cur
}

// CurrentGen returns the current prevhash generation.
func (m *Manager) CurrentGen() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.gen
}

// LookupJob finds a job by id. Jobs from the current and the previous
// generation are retained (the latter to recognise stale shares and to still
// submit a stale share that happens to solve its block).
func (m *Manager) LookupJob(id string) *Job {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.jobs[id]
}

// Resend registers a copy of j under a fresh id.
func (m *Manager) Resend(j *Job) *Job {
	c := j.withID(m.nextJobID())
	m.register(c)
	return c
}

func (m *Manager) nextJobID() string { return strconv.FormatUint(m.jobSeq.Add(1), 16) }

func (m *Manager) register(j *Job) {
	m.mu.Lock()
	m.jobs[j.ID] = j
	m.mu.Unlock()
}

// Kick requests an immediate template refresh.
func (m *Manager) Kick() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// Wait blocks until in-flight block submissions finish or ctx expires.
func (m *Manager) Wait(ctx context.Context) {
	done := make(chan struct{})
	go func() { m.submitWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		m.log.Error("shutdown while block submission still in flight")
	}
}

func (m *Manager) nodeError(err error) {
	m.st.SetNode(func(n *stats.NodeStatus) {
		n.Connected = false
		now := time.Now()
		n.LastError, n.LastErrorAt = err.Error(), &now
	})
}

// WaitSynced blocks until the node reports it is fully synced.
func (m *Manager) WaitSynced(ctx context.Context) error {
	last := time.Time{}
	for {
		ci, err := m.rpc.GetBlockchainInfo(ctx)
		if err == nil {
			m.st.SetNode(func(n *stats.NodeStatus) {
				n.Connected, n.Synced, n.Height, n.Headers, n.BestHash = true, ci.Synced(), ci.Blocks, ci.Headers, ci.BestBlockHash
			})
			if ci.Synced() {
				m.log.Info("node synced", "chain", ci.Chain, "height", ci.Blocks)
				return nil
			}
			if time.Since(last) > 30*time.Second {
				m.log.Info("waiting for node to sync before issuing work",
					"blocks", ci.Blocks, "headers", ci.Headers, "ibd", ci.InitialBlockDownload,
					"progress", ci.VerificationProgress)
				last = time.Now()
			}
		} else {
			m.nodeError(err)
			if time.Since(last) > 10*time.Second {
				m.log.Warn("node not reachable", "err", err)
				last = time.Now()
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// Run drives template updates until ctx is cancelled. It waits for the node
// to be synced first.
func (m *Manager) Run(ctx context.Context) error {
	if err := m.WaitSynced(ctx); err != nil {
		return err
	}
	zmqCh := make(chan node.ZMQMessage, 8)
	if m.zmq != nil {
		go m.zmq.Run(ctx, zmqCh)
	}
	poll := time.NewTicker(m.cfg.PollInterval)
	defer poll.Stop()
	refresh := time.NewTicker(m.cfg.RefreshInterval)
	defer refresh.Stop()
	status := time.NewTicker(5 * time.Second)
	defer status.Stop()

	m.update(ctx, "startup")
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg := <-zmqCh:
			m.log.Debug("zmq hashblock", "hash", hex.EncodeToString(msg.Body), "seq", msg.Seq)
			m.update(ctx, "zmq")
		case <-m.kick:
			m.update(ctx, "kick")
		case <-poll.C:
			cur := m.Current()
			best, err := m.rpc.GetBestBlockHash(ctx)
			if err != nil {
				m.nodeError(err)
				continue
			}
			if cur == nil || best != cur.Tmpl.PrevHash.String() {
				m.update(ctx, "poll")
			}
		case <-refresh.C:
			m.update(ctx, "refresh")
		case <-status.C:
			m.refreshNodeStatus(ctx)
		}
	}
}

func (m *Manager) refreshNodeStatus(ctx context.Context) {
	ci, err := m.rpc.GetBlockchainInfo(ctx)
	if err != nil {
		m.nodeError(err)
		return
	}
	m.st.SetNode(func(n *stats.NodeStatus) {
		n.Connected, n.Synced, n.Height, n.Headers, n.BestHash = true, ci.Synced(), ci.Blocks, ci.Headers, ci.BestBlockHash
		if m.zmq != nil {
			n.ZMQConnected, n.ZMQMessages = m.zmq.Connected(), m.zmq.Received()
		}
	})
}

// update fetches a template and publishes it if anything changed.
func (m *Manager) update(ctx context.Context, reason string) {
	defer func() {
		if r := recover(); r != nil {
			// Keep serving the previous work rather than dying on bad node data.
			m.log.Error("panic while processing block template (recovered)", "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
		}
	}()
	raw, err := m.rpc.GetBlockTemplate(ctx, m.cfg.Params.GBTRules)
	if err != nil {
		m.nodeError(err)
		m.log.Warn("getblocktemplate failed", "reason", reason, "err", err)
		return
	}
	t, err := NewTemplate(raw, m.cfg.Params, m.cfg.Chain)
	if err != nil {
		// A template we cannot verify must never be mined on.
		m.log.Error("rejecting invalid block template", "height", raw.Height, "err", err)
		m.nodeError(err)
		return
	}
	if want := t.ExpectedSubsidy + t.TotalFees; t.CoinbaseValue != want {
		m.log.Warn("coinbasevalue differs from subsidy+fees; using node value",
			"coinbasevalue", t.CoinbaseValue, "subsidy", t.ExpectedSubsidy, "fees", t.TotalFees)
	}

	m.mu.Lock()
	prev := m.cur
	clean := prev == nil || prev.Tmpl.PrevHash != t.PrevHash
	if !clean && reason != "refresh" {
		// A new-tip trigger (zmq/poll/kick) raced with another one that
		// already produced work for this tip; mempool refreshes are periodic.
		m.mu.Unlock()
		return
	}
	if clean {
		m.gen++
		// Retain only the current and previous generation.
		for id, j := range m.jobs {
			if j.Gen+1 < m.gen {
				delete(m.jobs, id)
			}
		}
	}
	w := &Work{Tmpl: t, Gen: m.gen, Clean: clean, m: m, byScript: map[string]*Job{}}
	m.cur = w
	listeners := append([]func(*Work){}, m.listeners...)
	m.mu.Unlock()

	m.st.SetNode(func(n *stats.NodeStatus) { n.Connected, n.LastError, n.LastErrorAt = true, "", nil })
	m.st.SetTemplate(func(ti *stats.TemplateInfo) {
		ti.Height, ti.PrevHash, ti.Transactions = t.Height, t.PrevHash.String(), t.TxCount()
		ti.Fees, ti.CoinbaseValue, ti.NetworkDiff = t.TotalFees, t.CoinbaseValue, t.NetworkDiff
		ti.Bits, ti.UpdatedAt = fmt.Sprintf("%08x", t.Bits), time.Now()
		if clean {
			ti.NewBlocks++
		} else {
			ti.Refreshes++
		}
	})
	if clean {
		m.log.Info("new block template", "height", t.Height, "prev", t.PrevHash.String(), "txs", t.TxCount(),
			"coinbase_value", t.CoinbaseValue, "network_diff", t.NetworkDiff, "trigger", reason)
	} else {
		m.log.Debug("template refreshed", "height", t.Height, "txs", t.TxCount(), "trigger", reason)
	}
	for _, f := range listeners {
		f(w)
	}
	m.readyOnce.Do(func() { close(m.ready) })
}

// Candidate is a solved block reported by a session.
type Candidate struct {
	Job       *Job
	Header    bitcoin.Header
	En1, En2  []byte
	Worker    string
	ShareDiff float64
	Stale     bool // job belongs to an outdated prevhash
	// VersionInterp names the version-rolling interpretation(s) that produced
	// Header.Version (see stratum/version.go).
	VersionInterp string
}

// SubmitBlock submits a solved block in the background. It returns
// immediately so the share response is not delayed; submission and
// verification are logged and recorded in stats.
func (m *Manager) SubmitBlock(c Candidate) {
	hash := c.Header.Hash()
	if _, dup := m.submitted.LoadOrStore(hash, struct{}{}); dup {
		return
	}
	// Assemble before spawning so the caller's buffers can be reused.
	block := m.assemble(c)
	m.submitWG.Add(1)
	go func() {
		defer m.submitWG.Done()
		defer func() {
			if r := recover(); r != nil {
				m.log.Error("panic during block submission (recovered)", "hash", hash.String(), "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
			}
		}()
		m.submit(c, hash, block)
	}()
}

func (m *Manager) assemble(c Candidate) []byte {
	return c.Job.Block(&c.Header, c.En1, c.En2)
}

func (m *Manager) submit(c Candidate, hash bitcoin.Hash, block []byte) {
	t := c.Job.Tmpl
	rec := stats.BlockRecord{
		Height: t.Height, Hash: hash.String(), Worker: c.Worker, Address: c.Job.PayoutAddr,
		Reward: t.CoinbaseValue, Time: time.Now(), Status: "pending",
		ShareDiff: c.ShareDiff, NetworkDiff: t.NetworkDiff,
		TemplateVersion: fmt.Sprintf("%08x", t.Version), BlockVersion: fmt.Sprintf("%08x", c.Header.Version),
		VersionInterp: c.VersionInterp,
	}
	m.log.Log(context.Background(), logging.LevelBlock, "*** BLOCK FOUND — submitting ***",
		"height", t.Height, "hash", rec.Hash, "worker", c.Worker, "payout", c.Job.PayoutAddr,
		"reward_sats", t.CoinbaseValue, "txs", t.TxCount(), "stale_job", c.Stale,
		"template_version", rec.TemplateVersion, "block_version", rec.BlockVersion, "version_interp", c.VersionInterp)
	m.st.BlockSubmitted(rec)

	// Use a context independent of shutdown: a found block must be delivered.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	blockHex := hex.EncodeToString(block)
	var result string
	var err error
	for attempt := 1; attempt <= 6; attempt++ {
		result, err = m.rpc.SubmitBlock(ctx, blockHex)
		var rpcErr *node.RPCError
		if err == nil || errors.As(err, &rpcErr) {
			break // node answered; retrying cannot change its verdict
		}
		m.log.Error("submitblock transport error; retrying", "attempt", attempt, "hash", rec.Hash, "err", err)
		time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
	}
	m.Kick() // move miners to the next block right away
	switch {
	case err != nil:
		rec.Status, rec.Reason = "rejected", err.Error()
	case result == "" || result == "duplicate" || result == "inconclusive":
		rec.Reason = result
	default:
		rec.Status, rec.Reason = "rejected", result
	}
	if rec.Status == "rejected" {
		m.log.Error("BLOCK REJECTED by node", "height", t.Height, "hash", rec.Hash, "reason", rec.Reason)
		// Even so, check whether the node knows the block (e.g. "duplicate" race).
	}

	// Verify acceptance: the block must be on the active chain.
	status := rec.Status // "pending" until the node confirms the block on the active chain
	for i := 0; i < 20; i++ {
		hdr, herr := m.rpc.GetBlockHeader(ctx, rec.Hash)
		if herr == nil {
			switch {
			case hdr.Confirmations >= 1:
				status = "accepted"
			case hdr.Confirmations == -1:
				status = "orphaned"
			}
			if status == "accepted" || (status == "orphaned" && i >= 5) {
				break
			}
		}
		if status == "rejected" && i >= 2 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if status == "pending" {
		// submitblock did not complain, yet the node does not know the header.
		status, rec.Reason = "rejected", strings.TrimSpace(rec.Reason+" block unknown to node after submit")
	}
	if c.Stale && status != "accepted" {
		status = "stale"
	}
	rec.Status = status
	m.st.BlockSubmitted(rec)
	if status == "accepted" {
		m.log.Log(context.Background(), logging.LevelBlock, "*** BLOCK ACCEPTED — on active chain ***",
			"height", t.Height, "hash", rec.Hash, "worker", c.Worker, "payout", c.Job.PayoutAddr, "reward_sats", t.CoinbaseValue)
	} else {
		m.log.Error("block not on active chain", "height", t.Height, "hash", rec.Hash, "status", status,
			"reason", strings.TrimSpace(rec.Reason))
	}
}
