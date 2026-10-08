// Package stats collects engine statistics and serves them as JSON (for a
// future UI) and in the Prometheus text exposition format.
package stats

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const (
	bucketSecs = 10
	numBuckets = 360 // one hour of 10 s buckets
)

// rateWindow accumulates share difficulty in fixed time buckets so hashrate
// can be estimated over several trailing windows.
type rateWindow struct {
	buckets [numBuckets]float64
	stamps  [numBuckets]int64 // bucket epoch (unix/bucketSecs) each slot holds
	first   time.Time
}

func (r *rateWindow) add(now time.Time, diff float64) {
	if r.first.IsZero() {
		r.first = now
	}
	e := now.Unix() / bucketSecs
	i := e % numBuckets
	if r.stamps[i] != e {
		r.stamps[i], r.buckets[i] = e, 0
	}
	r.buckets[i] += diff
}

// hashrate returns H/s over the trailing window: Σdiff · 2^32 / seconds.
func (r *rateWindow) hashrate(now time.Time, window time.Duration) float64 {
	if r.first.IsZero() {
		return 0
	}
	e := now.Unix() / bucketSecs
	n := int64(window.Seconds()) / bucketSecs
	sum := 0.0
	for k := int64(0); k < n && k < numBuckets; k++ {
		i := (e - k) % numBuckets
		if i < 0 {
			i += numBuckets
		}
		if r.stamps[i] == e-k {
			sum += r.buckets[i]
		}
	}
	secs := window.Seconds()
	// For a worker younger than the window, divide by its age (≥ 30 s) so
	// early readings are not diluted.
	if age := now.Sub(r.first).Seconds(); age < secs {
		secs = math.Max(age, 30)
	}
	return sum * 4294967296 / secs
}

// Worker statistics, keyed by the full username.
type Worker struct {
	Name        string
	Connections int
	Accepted    uint64
	Rejected    uint64
	Rejects     map[string]uint64
	BestDiff    float64
	Difficulty  float64
	LastShare   time.Time
	FirstSeen   time.Time
	rate        rateWindow
}

// BlockRecord describes a block candidate the engine submitted.
type BlockRecord struct {
	Height      int64     `json:"height"`
	Hash        string    `json:"hash"`
	Worker      string    `json:"worker"`
	Address     string    `json:"address"`
	Reward      int64     `json:"reward_sats"`
	Time        time.Time `json:"time"`
	Status      string    `json:"status"` // submitted | accepted | rejected | orphaned | stale
	Reason      string    `json:"reason,omitempty"`
	ShareDiff   float64   `json:"share_difficulty"`
	NetworkDiff float64   `json:"network_difficulty"`
}

// NodeStatus describes the full node connection.
type NodeStatus struct {
	Connected    bool      `json:"connected"`
	Synced       bool      `json:"synced"`
	Chain        string    `json:"chain"`
	Height       int64     `json:"height"`
	Headers      int64     `json:"headers"`
	BestHash     string    `json:"best_hash"`
	Subversion   string    `json:"subversion"`
	ZMQEnabled   bool      `json:"zmq_enabled"`
	ZMQConnected bool      `json:"zmq_connected"`
	ZMQMessages  uint64    `json:"zmq_messages"`
	LastError    string    `json:"last_error,omitempty"`
	LastErrorAt  time.Time `json:"last_error_at,omitempty"`
}

// TemplateInfo describes the current block template.
type TemplateInfo struct {
	Height        int64     `json:"height"`
	PrevHash      string    `json:"prev_hash"`
	Transactions  int       `json:"transactions"`
	Fees          int64     `json:"fees_sats"`
	CoinbaseValue int64     `json:"coinbase_value_sats"`
	NetworkDiff   float64   `json:"network_difficulty"`
	Bits          string    `json:"bits"`
	UpdatedAt     time.Time `json:"updated_at"`
	NewBlocks     uint64    `json:"new_block_events"`
	Refreshes     uint64    `json:"template_refreshes"`
}

// Collector is safe for concurrent use.
type Collector struct {
	mu          sync.Mutex
	start       time.Time
	coin        string
	version     string
	node        NodeStatus
	tmpl        TemplateInfo
	workers     map[string]*Worker
	pool        rateWindow
	accepted    uint64
	rejected    uint64
	rejects     map[string]uint64
	bestDiff    float64
	bestWorker  string
	blocks      []BlockRecord
	connections int
	persistPath string
}

// New creates a collector. If dataDir is non-empty, found blocks and the
// best share are persisted there across restarts.
func New(coin, version, dataDir string) *Collector {
	c := &Collector{
		start: time.Now(), coin: coin, version: version,
		workers: map[string]*Worker{}, rejects: map[string]uint64{},
	}
	if dataDir != "" {
		c.persistPath = filepath.Join(dataDir, "state-"+coin+".json")
		c.load()
	}
	return c
}

type persisted struct {
	Blocks     []BlockRecord `json:"blocks"`
	BestDiff   float64       `json:"best_share_difficulty"`
	BestWorker string        `json:"best_share_worker"`
}

func (c *Collector) load() {
	b, err := os.ReadFile(c.persistPath)
	if err != nil {
		return
	}
	var p persisted
	if json.Unmarshal(b, &p) == nil {
		c.blocks, c.bestDiff, c.bestWorker = p.Blocks, p.BestDiff, p.BestWorker
	}
}

// Save writes persistent state atomically (no-op without a data dir).
func (c *Collector) Save() error {
	if c.persistPath == "" {
		return nil
	}
	c.mu.Lock()
	b, err := json.MarshalIndent(persisted{Blocks: c.blocks, BestDiff: c.bestDiff, BestWorker: c.bestWorker}, "", "  ")
	c.mu.Unlock()
	if err != nil {
		return err
	}
	tmp := c.persistPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.persistPath)
}

func (c *Collector) worker(name string) *Worker {
	w := c.workers[name]
	if w == nil {
		w = &Worker{Name: name, Rejects: map[string]uint64{}, FirstSeen: time.Now()}
		c.workers[name] = w
	}
	return w
}

// Connected records a new authorized connection for worker.
func (c *Collector) Connected(worker string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.worker(worker).Connections++
}

// Disconnected records a closed authorized connection for worker.
func (c *Collector) Disconnected(worker string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if w := c.workers[worker]; w != nil && w.Connections > 0 {
		w.Connections--
	}
}

// Connections sets the number of open TCP connections.
func (c *Collector) SetConnections(n int) {
	c.mu.Lock()
	c.connections = n
	c.mu.Unlock()
}

// SetDifficulty records the current share difficulty of worker.
func (c *Collector) SetDifficulty(worker string, d float64) {
	c.mu.Lock()
	c.worker(worker).Difficulty = d
	c.mu.Unlock()
}

// ShareAccepted records an accepted share credited at shareDiff whose hash
// achieved achieved difficulty.
func (c *Collector) ShareAccepted(worker string, shareDiff, achieved float64) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	w := c.worker(worker)
	w.Accepted++
	w.LastShare = now
	w.rate.add(now, shareDiff)
	c.pool.add(now, shareDiff)
	c.accepted++
	if achieved > w.BestDiff {
		w.BestDiff = achieved
	}
	if achieved > c.bestDiff {
		c.bestDiff, c.bestWorker = achieved, worker
	}
}

// ShareRejected records a rejected share.
func (c *Collector) ShareRejected(worker, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if worker != "" {
		w := c.worker(worker)
		w.Rejected++
		w.Rejects[reason]++
	}
	c.rejected++
	c.rejects[reason]++
}

// SetNode updates the node status (fields are replaced wholesale).
func (c *Collector) SetNode(f func(*NodeStatus)) {
	c.mu.Lock()
	f(&c.node)
	c.mu.Unlock()
}

// SetTemplate updates template information.
func (c *Collector) SetTemplate(f func(*TemplateInfo)) {
	c.mu.Lock()
	f(&c.tmpl)
	c.mu.Unlock()
}

// BlockSubmitted records (or updates, keyed by hash) a block record.
func (c *Collector) BlockSubmitted(r BlockRecord) {
	c.mu.Lock()
	for i := range c.blocks {
		if c.blocks[i].Hash == r.Hash {
			c.blocks[i] = r
			c.mu.Unlock()
			_ = c.Save()
			return
		}
	}
	c.blocks = append(c.blocks, r)
	if len(c.blocks) > 10000 {
		c.blocks = c.blocks[len(c.blocks)-10000:]
	}
	c.mu.Unlock()
	_ = c.Save()
}

// Blocks returns a copy of the block records.
func (c *Collector) Blocks() []BlockRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]BlockRecord(nil), c.blocks...)
}

// WorkerSnapshot is the JSON form of a worker.
type WorkerSnapshot struct {
	Name        string            `json:"name"`
	Connections int               `json:"connections"`
	Hashrate1m  float64           `json:"hashrate_1m"`
	Hashrate5m  float64           `json:"hashrate_5m"`
	Hashrate1h  float64           `json:"hashrate_1h"`
	Accepted    uint64            `json:"shares_accepted"`
	Rejected    uint64            `json:"shares_rejected"`
	Rejects     map[string]uint64 `json:"rejects"`
	BestDiff    float64           `json:"best_share_difficulty"`
	Difficulty  float64           `json:"difficulty"`
	LastShare   *time.Time        `json:"last_share_at"`
}

// PoolSnapshot aggregates all workers.
type PoolSnapshot struct {
	Hashrate1m  float64           `json:"hashrate_1m"`
	Hashrate5m  float64           `json:"hashrate_5m"`
	Hashrate1h  float64           `json:"hashrate_1h"`
	Workers     int               `json:"workers_online"`
	Connections int               `json:"connections"`
	Accepted    uint64            `json:"shares_accepted"`
	Rejected    uint64            `json:"shares_rejected"`
	Rejects     map[string]uint64 `json:"rejects"`
	BestDiff    float64           `json:"best_share_difficulty"`
	BestWorker  string            `json:"best_share_worker"`
	BlocksFound int               `json:"blocks_found"`
}

// Snapshot is the full JSON document served at /stats.
type Snapshot struct {
	Coin     string           `json:"coin"`
	Version  string           `json:"version"`
	Uptime   float64          `json:"uptime_s"`
	Node     NodeStatus       `json:"node"`
	Template TemplateInfo     `json:"template"`
	Pool     PoolSnapshot     `json:"pool"`
	Workers  []WorkerSnapshot `json:"workers"`
	Blocks   []BlockRecord    `json:"blocks"`
}

// Snapshot returns a consistent copy of all statistics.
func (c *Collector) Snapshot() Snapshot {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	s := Snapshot{
		Coin: c.coin, Version: c.version, Uptime: now.Sub(c.start).Seconds(),
		Node: c.node, Template: c.tmpl,
		Pool: PoolSnapshot{
			Hashrate1m:  c.pool.hashrate(now, time.Minute),
			Hashrate5m:  c.pool.hashrate(now, 5*time.Minute),
			Hashrate1h:  c.pool.hashrate(now, time.Hour),
			Connections: c.connections, Accepted: c.accepted, Rejected: c.rejected,
			Rejects: copyMap(c.rejects), BestDiff: c.bestDiff, BestWorker: c.bestWorker,
		},
		Blocks: append([]BlockRecord{}, c.blocks...),
	}
	for _, b := range c.blocks {
		if b.Status == "accepted" {
			s.Pool.BlocksFound++
		}
	}
	for _, w := range c.workers {
		ws := WorkerSnapshot{
			Name: w.Name, Connections: w.Connections,
			Hashrate1m: w.rate.hashrate(now, time.Minute),
			Hashrate5m: w.rate.hashrate(now, 5*time.Minute),
			Hashrate1h: w.rate.hashrate(now, time.Hour),
			Accepted:   w.Accepted, Rejected: w.Rejected, Rejects: copyMap(w.Rejects),
			BestDiff: w.BestDiff, Difficulty: w.Difficulty,
		}
		if !w.LastShare.IsZero() {
			t := w.LastShare
			ws.LastShare = &t
		}
		if w.Connections > 0 {
			s.Pool.Workers++
		}
		s.Workers = append(s.Workers, ws)
	}
	sort.Slice(s.Workers, func(i, j int) bool { return s.Workers[i].Name < s.Workers[j].Name })
	return s
}

func copyMap(m map[string]uint64) map[string]uint64 {
	out := make(map[string]uint64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
