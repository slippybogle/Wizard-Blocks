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

	"github.com/fladnagmai/wizard-blocks/internal/pow"
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

// hashrate returns H/s over the trailing window: Σdiff · diff1Hashes / seconds
// (diff1Hashes: 2^32 for SHA-256d, 2^16 for Scrypt shares).
func (r *rateWindow) hashrate(now time.Time, window time.Duration, diff1Hashes float64) float64 {
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
	return sum * diff1Hashes / secs
}

// lastMinute returns H/s averaged over the last completed clock minute, so
// the figure changes only once a minute. A worker that started during that
// minute is divided by the part of it since its first share (≥ 10 s).
func (r *rateWindow) lastMinute(now time.Time, diff1Hashes float64) float64 {
	if r.first.IsZero() {
		return 0
	}
	end := now.Unix() / 60 * 60
	start := end - 60
	sum := 0.0
	for e := start / bucketSecs; e < end/bucketSecs; e++ {
		if i := e % numBuckets; r.stamps[i] == e {
			sum += r.buckets[i]
		}
	}
	secs := 60.0
	if f := r.first.Unix(); f > start {
		if f >= end {
			return 0
		}
		secs = math.Max(float64(end-f), bucketSecs)
	}
	return sum * diff1Hashes / secs
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
	Interps     map[string]uint64 // accepted shares by version-rolling interpretation
	Race        string            // character race chosen in the stratum password ("race=elf"), or ""
	LastShare   time.Time
	FirstSeen   time.Time
	rate        rateWindow
}

// BlockRecord describes a block candidate the engine submitted.
type BlockRecord struct {
	Height  int64     `json:"height"`
	Hash    string    `json:"hash"`
	Worker  string    `json:"worker"`
	Address string    `json:"address"`
	Reward  int64     `json:"reward_sats"`
	Time    time.Time `json:"time"`
	// Status: pending (submitted, not yet confirmed on the active chain) |
	// accepted (confirmed on the active chain) | rejected | orphaned | stale.
	// Only "accepted" counts as a found block.
	Status      string  `json:"status"`
	Reason      string  `json:"reason,omitempty"`
	ShareDiff   float64 `json:"share_difficulty"`
	NetworkDiff float64 `json:"network_difficulty"`
	// Version rolling: template version, final header version and the
	// interpretation(s) of the submitted version bits that produced it.
	TemplateVersion string `json:"template_version"`
	BlockVersion    string `json:"block_version"`
	VersionInterp   string `json:"version_interpretation"`
	// Best drop: the best non-block share (as % of network difficulty) and
	// its job height since the previous found block; set when the block is
	// accepted. The UI offers to mount that creature's head.
	BestDropPct    float64 `json:"best_drop_pct,omitempty"`
	BestDropHeight int64   `json:"best_drop_height,omitempty"`
	// Chain is set on merged-mined aux blocks ("doge"); empty for the
	// parent chain. ParentHash is the parent block whose header carried the
	// proof of work (a parent share, not necessarily a parent block).
	Chain      string `json:"chain,omitempty"`
	ParentHash string `json:"parent_hash,omitempty"`
}

// NodeStatus describes the full node connection.
type NodeStatus struct {
	Connected    bool       `json:"connected"`
	Synced       bool       `json:"synced"`
	Chain        string     `json:"chain"`
	Height       int64      `json:"height"`
	Headers      int64      `json:"headers"`
	BestHash     string     `json:"best_hash"`
	Subversion   string     `json:"subversion"`
	ZMQEnabled   bool       `json:"zmq_enabled"`
	ZMQConnected bool       `json:"zmq_connected"`
	ZMQMessages  uint64     `json:"zmq_messages"`
	LastError    string     `json:"last_error,omitempty"`
	LastErrorAt  *time.Time `json:"last_error_at,omitempty"`
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
	badMessages uint64  // non-JSON lines on the Stratum port
	diff1Hashes float64 // hashes per share at difficulty 1 (pow.Algo.Diff1Hashes)
	bestDiff    float64
	bestWorker  string
	blocks      []BlockRecord
	auxBlocks   []BlockRecord // merged-mined aux chain blocks (Dogecoin)
	aux         map[string]*AuxStatus
	connections int
	persistPath string
	luck        Luck
	recent      []RecentShare // latest accepted shares, oldest first
	round       Round         // shares on the current block template (prevhash)
	rounds      []Round       // completed rounds, oldest first
}

// Round aggregates shares mined on one previous-block hash ("job" in the
// UI): the best share relative to network difficulty is the basis of the
// UI's creature log and luck percentile.
type Round struct {
	Height      int64     `json:"height"`
	PrevHash    string    `json:"prev_hash"`
	NetworkDiff float64   `json:"network_difficulty"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end,omitempty"`
	Shares      uint64    `json:"shares"`
	SumDiff     float64   `json:"sum_difficulty"` // total credited share difficulty
	BestDiff    float64   `json:"best_difficulty"`
	BestWorker  string    `json:"best_worker"`
}

const maxRounds = 500

// New creates a collector. If dataDir is non-empty, found blocks and the
// best share are persisted there across restarts.
func New(coin, version, dataDir string) *Collector {
	c := &Collector{
		start: time.Now(), coin: coin, version: version, diff1Hashes: pow.For(coin).Diff1Hashes,
		workers: map[string]*Worker{}, rejects: map[string]uint64{},
	}
	if dataDir != "" {
		c.persistPath = filepath.Join(dataDir, "state-"+coin+".json")
		c.load()
	}
	return c
}

// AuxStatus is the state of a merged-mined aux chain (Dogecoin).
type AuxStatus struct {
	// Merged is true while its blocks are being mined; Reason says why not
	// otherwise: "no address", "node down", "node syncing".
	Merged      bool    `json:"merged"`
	Reason      string  `json:"reason,omitempty"`
	Connected   bool    `json:"connected"`
	ZMQ         bool    `json:"zmq_connected"`
	Height      int64   `json:"height"`
	NetworkDiff float64 `json:"network_difficulty"` // share units
	Address     string  `json:"payout_address,omitempty"`
	// Luck since this chain's last found block (independent of the parent).
	Luck Luck `json:"luck"`
}

type persisted struct {
	Blocks     []BlockRecord   `json:"blocks"`
	AuxBlocks  []BlockRecord   `json:"aux_blocks,omitempty"`
	AuxLuck    map[string]Luck `json:"aux_luck,omitempty"`
	BestDiff   float64         `json:"best_share_difficulty"`
	BestWorker string          `json:"best_share_worker"`
	Luck       Luck            `json:"luck"`
}

// Luck accumulates shares since the last block we found (confirmed on the
// active chain); it resets only then, not on new jobs. The UI turns it into
// a luck percentile: P(best < BestDiff) = exp(-SumDiff / BestDiff).
type Luck struct {
	SumDiff  float64   `json:"sum_difficulty"`
	BestDiff float64   `json:"best_difficulty"`
	Shares   uint64    `json:"shares"`
	Since    time.Time `json:"since"`
	// Effort: work done since the last found block, as a fraction of the
	// network difficulty (sum of share difficulty / network difficulty at
	// the time). 1.0 = a block's worth of expected work.
	Effort float64 `json:"effort"`
	// Best share below network difficulty, as % of it, and its job height.
	BestDropPct    float64 `json:"best_drop_pct"`
	BestDropHeight int64   `json:"best_drop_height"`
}

func (c *Collector) load() {
	b, err := os.ReadFile(c.persistPath)
	if err != nil {
		return
	}
	var p persisted
	if json.Unmarshal(b, &p) == nil {
		c.blocks, c.auxBlocks, c.bestDiff, c.bestWorker, c.luck = p.Blocks, p.AuxBlocks, p.BestDiff, p.BestWorker, p.Luck
		for chain, l := range p.AuxLuck {
			c.auxLocked(chain).Luck = l
		}
	}
}

// Save writes persistent state atomically (no-op without a data dir).
func (c *Collector) Save() error {
	if c.persistPath == "" {
		return nil
	}
	c.mu.Lock()
	b, err := json.MarshalIndent(persisted{Blocks: c.blocks, AuxBlocks: c.auxBlocks, AuxLuck: c.auxLuckLocked(), BestDiff: c.bestDiff, BestWorker: c.bestWorker, Luck: c.luck}, "", "  ")
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
		w = &Worker{Name: name, Rejects: map[string]uint64{}, Interps: map[string]uint64{}, FirstSeen: time.Now()}
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

// SetRace records the character race a worker chose in its password.
func (c *Collector) SetRace(worker, race string) {
	c.mu.Lock()
	c.worker(worker).Race = race
	c.mu.Unlock()
}

// SetDifficulty records the current share difficulty of worker.
func (c *Collector) SetDifficulty(worker string, d float64) {
	c.mu.Lock()
	c.worker(worker).Difficulty = d
	c.mu.Unlock()
}

// ShareAccepted records an accepted share credited at shareDiff whose hash
// achieved achieved difficulty; interp is the version-rolling interpretation.
func (c *Collector) ShareAccepted(worker string, shareDiff, achieved float64, interp string) {
	c.ShareAcceptedOn("", worker, shareDiff, achieved, interp)
}

// ShareAcceptedOn records an accepted share mined on the template whose
// previous-block hash is prevHash ("" means the current round). A new block
// can arrive between the share check and this call; the share is then
// credited to the round it was mined in, not to the new one.
func (c *Collector) ShareAcceptedOn(prevHash, worker string, shareDiff, achieved float64, interp string) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	w := c.worker(worker)
	w.Accepted++
	w.Interps[interp]++
	w.LastShare = now
	w.rate.add(now, shareDiff)
	c.pool.add(now, shareDiff)
	c.accepted++
	c.recent = append(c.recent, RecentShare{Worker: worker, Difficulty: achieved})
	if len(c.recent) > maxRecentShares {
		c.recent = c.recent[len(c.recent)-maxRecentShares:]
	}
	if achieved > w.BestDiff {
		w.BestDiff = achieved
	}
	if achieved > c.bestDiff {
		c.bestDiff, c.bestWorker = achieved, worker
	}
	if c.luck.Since.IsZero() {
		c.luck.Since = now
	}
	c.luck.Shares++
	c.luck.SumDiff += shareDiff
	if achieved > c.luck.BestDiff {
		c.luck.BestDiff = achieved
	}
	for _, a := range c.aux {
		if a.Merged && a.NetworkDiff > 0 {
			if a.Luck.Since.IsZero() {
				a.Luck.Since = now
			}
			a.Luck.Shares++
			a.Luck.SumDiff += shareDiff
			a.Luck.Effort += shareDiff / a.NetworkDiff
			if achieved > a.Luck.BestDiff {
				a.Luck.BestDiff = achieved
			}
		}
	}
	r := c.roundFor(prevHash)
	if r == nil {
		return // a round too old to be kept
	}
	if nd := r.NetworkDiff; nd > 0 {
		c.luck.Effort += shareDiff / nd
		if pct := achieved / nd * 100; pct < 100 && pct > c.luck.BestDropPct {
			c.luck.BestDropPct, c.luck.BestDropHeight = pct, r.Height
		}
	}
	r.Shares++
	r.SumDiff += shareDiff
	if achieved > r.BestDiff {
		r.BestDiff, r.BestWorker = achieved, worker
	}
}

// roundFor returns the round with this previous-block hash: the current one,
// or a recently completed one. Callers hold c.mu.
func (c *Collector) roundFor(prevHash string) *Round {
	if prevHash == "" || prevHash == c.round.PrevHash {
		return &c.round
	}
	for i := len(c.rounds) - 1; i >= 0 && i >= len(c.rounds)-8; i-- {
		if c.rounds[i].PrevHash == prevHash {
			return &c.rounds[i]
		}
	}
	return nil
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

// BadMessage records a line on the Stratum port that is not JSON-RPC at all
// (an HTTP request, a port scan, a misconfigured client). It is not a share,
// so it is counted apart from the share rejects.
func (c *Collector) BadMessage() {
	c.mu.Lock()
	c.badMessages++
	c.mu.Unlock()
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
	if c.tmpl.PrevHash != c.round.PrevHash {
		now := time.Now()
		if c.round.PrevHash != "" {
			c.round.End = now
			c.rounds = append(c.rounds, c.round)
			if len(c.rounds) > maxRounds {
				c.rounds = c.rounds[len(c.rounds)-maxRounds:]
			}
		}
		c.round = Round{Height: c.tmpl.Height, PrevHash: c.tmpl.PrevHash, NetworkDiff: c.tmpl.NetworkDiff, Start: now}
	}
	c.mu.Unlock()
}

// Rounds returns the current round followed by up to n completed rounds,
// newest first.
func (c *Collector) Rounds(n int) []Round {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := []Round{}
	if c.round.PrevHash != "" {
		out = append(out, c.round)
	}
	for i := len(c.rounds) - 1; i >= 0 && len(out) < n+1; i-- {
		out = append(out, c.rounds[i])
	}
	return out
}

// BlockSubmitted records (or updates, keyed by hash) a block record.
func (c *Collector) BlockSubmitted(r BlockRecord) {
	c.mu.Lock()
	for i := range c.blocks {
		if c.blocks[i].Hash == r.Hash {
			if r.BestDropPct == 0 {
				r.BestDropPct, r.BestDropHeight = c.blocks[i].BestDropPct, c.blocks[i].BestDropHeight
			}
			if r.Status == "accepted" && c.blocks[i].Status != "accepted" {
				r.BestDropPct, r.BestDropHeight = c.luck.BestDropPct, c.luck.BestDropHeight
				c.luck = Luck{Since: time.Now()} // we found a block: luck starts over
			}
			c.blocks[i] = r
			c.mu.Unlock()
			_ = c.Save()
			return
		}
	}
	if r.Status == "accepted" {
		r.BestDropPct, r.BestDropHeight = c.luck.BestDropPct, c.luck.BestDropHeight
		c.luck = Luck{Since: time.Now()}
	}
	c.blocks = append(c.blocks, r)
	if len(c.blocks) > 10000 {
		c.blocks = c.blocks[len(c.blocks)-10000:]
	}
	c.mu.Unlock()
	_ = c.Save()
}

// Prune forgets disconnected workers idle for longer than maxIdle.
func (c *Collector) Prune(maxIdle time.Duration) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, w := range c.workers {
		last := w.LastShare
		if last.IsZero() {
			last = w.FirstSeen
		}
		if w.Connections == 0 && now.Sub(last) > maxIdle {
			delete(c.workers, k)
		}
	}
}

// auxLocked returns (creating) a chain's status. c.mu must be held.
func (c *Collector) auxLocked(chain string) *AuxStatus {
	if c.aux == nil {
		c.aux = map[string]*AuxStatus{}
	}
	a := c.aux[chain]
	if a == nil {
		a = &AuxStatus{}
		c.aux[chain] = a
	}
	return a
}

func (c *Collector) auxLuckLocked() map[string]Luck {
	if len(c.aux) == 0 {
		return nil
	}
	out := map[string]Luck{}
	for k, a := range c.aux {
		out[k] = a.Luck
	}
	return out
}

// SetAux updates an aux chain's status (its luck is kept).
func (c *Collector) SetAux(chain string, f func(*AuxStatus)) {
	c.mu.Lock()
	a := c.auxLocked(chain)
	luck := a.Luck
	f(a)
	a.Luck = luck
	c.mu.Unlock()
}

// AuxBlockSubmitted records or updates (by chain and hash) a merged-mined
// aux block. Aux blocks never touch the parent chain's luck or counts; a
// newly accepted one resets that chain's own luck.
func (c *Collector) AuxBlockSubmitted(r BlockRecord) {
	c.mu.Lock()
	found := false
	for i := range c.auxBlocks {
		if c.auxBlocks[i].Hash == r.Hash && c.auxBlocks[i].Chain == r.Chain {
			if r.Status == "accepted" && c.auxBlocks[i].Status != "accepted" {
				c.auxLocked(r.Chain).Luck = Luck{Since: time.Now()}
			}
			c.auxBlocks[i] = r
			found = true
			break
		}
	}
	if !found && r.Status == "accepted" {
		c.auxLocked(r.Chain).Luck = Luck{Since: time.Now()}
	}
	if !found {
		c.auxBlocks = append(c.auxBlocks, r)
		if len(c.auxBlocks) > 10000 {
			c.auxBlocks = c.auxBlocks[len(c.auxBlocks)-10000:]
		}
	}
	c.mu.Unlock()
	_ = c.Save()
}

// AuxBlocks returns a copy of the aux block records.
func (c *Collector) AuxBlocks() []BlockRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]BlockRecord(nil), c.auxBlocks...)
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
	Hashrate60s float64           `json:"hashrate_60s"` // last completed minute; changes once a minute
	Hashrate1m  float64           `json:"hashrate_1m"`
	Hashrate5m  float64           `json:"hashrate_5m"`
	Hashrate1h  float64           `json:"hashrate_1h"`
	Accepted    uint64            `json:"shares_accepted"`
	Rejected    uint64            `json:"shares_rejected"`
	Rejects     map[string]uint64 `json:"rejects"`
	Interps     map[string]uint64 `json:"version_interpretations"`
	BestDiff    float64           `json:"best_share_difficulty"`
	Difficulty  float64           `json:"difficulty"`
	LastShare   *time.Time        `json:"last_share_at"`
	Race        string            `json:"race,omitempty"`
}

// PoolSnapshot aggregates all workers.
type PoolSnapshot struct {
	Hashrate60s   float64           `json:"hashrate_60s"` // last completed minute; changes once a minute
	Hashrate1m    float64           `json:"hashrate_1m"`
	Hashrate5m    float64           `json:"hashrate_5m"`
	Hashrate1h    float64           `json:"hashrate_1h"`
	Workers       int               `json:"workers_online"`
	Connections   int               `json:"connections"`
	Accepted      uint64            `json:"shares_accepted"`
	Rejected      uint64            `json:"shares_rejected"`
	Rejects       map[string]uint64 `json:"rejects"`
	BadMessages   uint64            `json:"bad_messages"` // non-JSON lines on the Stratum port (not shares)
	BestDiff      float64           `json:"best_share_difficulty"`
	BestWorker    string            `json:"best_share_worker"`
	BlocksFound   int               `json:"blocks_found"`   // confirmed on the active chain only
	BlocksPending int               `json:"blocks_pending"` // submitted, awaiting confirmation
	Luck          Luck              `json:"luck_since_last_block"`
	// The latest accepted shares (up to 16, oldest first): who found each
	// and its achieved difficulty. The UI turns each into an attack by the
	// party member of that miner's group, sized by the difficulty.
	RecentShares []RecentShare `json:"recent_shares"`
}

// RecentShare is one accepted share for the UI.
type RecentShare struct {
	Worker     string  `json:"worker"`
	Difficulty float64 `json:"difficulty"`
}

const maxRecentShares = 16

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
	// AuxBlocks are merged-mined aux chain blocks (Dogecoin), newest last.
	AuxBlocks []BlockRecord `json:"aux_blocks,omitempty"`
	// Aux is the status of each merged-mined aux chain, by name ("doge").
	Aux map[string]AuxStatus `json:"aux,omitempty"`
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
			Hashrate60s: c.pool.lastMinute(now, c.diff1Hashes),
			Hashrate1m:  c.pool.hashrate(now, time.Minute, c.diff1Hashes),
			Hashrate5m:  c.pool.hashrate(now, 5*time.Minute, c.diff1Hashes),
			Hashrate1h:  c.pool.hashrate(now, time.Hour, c.diff1Hashes),
			Connections: c.connections, Accepted: c.accepted, Rejected: c.rejected,
			Rejects: copyMap(c.rejects), BadMessages: c.badMessages, BestDiff: c.bestDiff, BestWorker: c.bestWorker, Luck: c.luck,
			RecentShares: append([]RecentShare{}, c.recent...),
		},
		Blocks:    append([]BlockRecord{}, c.blocks...),
		AuxBlocks: append([]BlockRecord(nil), c.auxBlocks...),
	}
	if len(c.aux) > 0 {
		s.Aux = map[string]AuxStatus{}
		for k, a := range c.aux {
			s.Aux[k] = *a
		}
	}
	for _, b := range c.blocks {
		switch b.Status {
		case "accepted":
			s.Pool.BlocksFound++
		case "pending":
			s.Pool.BlocksPending++
		}
	}
	for _, w := range c.workers {
		ws := WorkerSnapshot{
			Name: w.Name, Connections: w.Connections,
			Hashrate60s: w.rate.lastMinute(now, c.diff1Hashes),
			Hashrate1m:  w.rate.hashrate(now, time.Minute, c.diff1Hashes),
			Hashrate5m:  w.rate.hashrate(now, 5*time.Minute, c.diff1Hashes),
			Hashrate1h:  w.rate.hashrate(now, time.Hour, c.diff1Hashes),
			Accepted:    w.Accepted, Rejected: w.Rejected, Rejects: copyMap(w.Rejects),
			BestDiff: w.BestDiff, Difficulty: w.Difficulty, Race: w.Race,
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
