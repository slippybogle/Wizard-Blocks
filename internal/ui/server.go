package ui

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/slippybogle/wizard-blocks/internal/node"
	"github.com/slippybogle/wizard-blocks/internal/stats"
)

//go:embed static
var staticFS embed.FS

// Config is what the UI needs to know about the engine's configuration.
type Config struct {
	Coin            string
	Version         string
	StratumPort     int
	PayoutMode      string
	PayoutAddress   string
	Extranonce2Size int
	VersionMask     string
	DataDir         string
}

// Server is the web UI server.
type Server struct {
	cfg Config
	st  *stats.Collector
	rpc *node.Client
	log *slog.Logger

	srv *http.Server
	ln  net.Listener

	mu    sync.Mutex
	node  NodeDetail
	confs map[string]int64 // block hash -> confirmations (-1 = not on active chain)
	hist  *history

	sseClients atomic.Int32
}

// NodeDetail is node information polled for the Ledger page.
type NodeDetail struct {
	Version       int64      `json:"version"`
	Subversion    string     `json:"subversion"`
	Chain         string     `json:"chain"`
	Blocks        int64      `json:"blocks"`
	Headers       int64      `json:"headers"`
	SyncPct       float64    `json:"sync_pct"`
	IBD           bool       `json:"initial_block_download"`
	Peers         int        `json:"peers"`
	Difficulty    float64    `json:"difficulty"`
	NetworkHashps float64    `json:"network_hashps"`
	MempoolTxs    int64      `json:"mempool_txs"`
	MempoolBytes  int64      `json:"mempool_bytes"`
	UptimeS       int64      `json:"uptime_s"`
	DiskBytes     int64      `json:"disk_bytes"`
	Pruned        bool       `json:"pruned"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
	Error         string     `json:"error,omitempty"`
}

// New creates the UI server.
func New(cfg Config, st *stats.Collector, rpc *node.Client, log *slog.Logger) *Server {
	s := &Server{cfg: cfg, st: st, rpc: rpc, log: log, confs: map[string]int64{}, hist: newHistory()}
	if cfg.DataDir != "" {
		s.hist.load(filepath.Join(cfg.DataDir, "history-"+cfg.Coin+".json"))
	}
	return s
}

// Listen binds addr.
func (s *Server) Listen(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.ln = ln
	sub, _ := fs.Sub(staticFS, "static")
	files := http.FileServer(http.FS(sub))
	mux := http.NewServeMux()
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/history", s.handleHistory)
	mux.HandleFunc("/api/blocks", s.handleBlocks)
	mux.Handle("/", files)
	s.srv = &http.Server{Handler: secure(mux), ReadHeaderTimeout: 5 * time.Second}
	return nil
}

// secure adds headers that keep the page self-contained: no third-party
// scripts, styles, fonts or connections are possible.
func secure(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; media-src 'none'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-cache")
		}
		h.ServeHTTP(w, r)
	})
}

// Addr returns the bound address.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Serve serves HTTP until Shutdown.
func (s *Server) Serve() error {
	if err := s.srv.Serve(s.ln); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Shutdown stops serving and persists history.
func (s *Server) Shutdown(ctx context.Context) error {
	s.SaveHistory()
	return s.srv.Shutdown(ctx)
}

// SaveHistory persists the hashrate history (no-op without a data dir).
func (s *Server) SaveHistory() {
	if s.cfg.DataDir == "" {
		return
	}
	if err := s.hist.save(filepath.Join(s.cfg.DataDir, "history-"+s.cfg.Coin+".json")); err != nil {
		s.log.Warn("saving ui history failed", "err", err)
	}
}

// Run polls the node for Ledger details and samples hashrate history.
func (s *Server) Run(ctx context.Context) {
	nodeTick := time.NewTicker(10 * time.Second)
	sample := time.NewTicker(historyStep)
	save := time.NewTicker(5 * time.Minute)
	defer nodeTick.Stop()
	defer sample.Stop()
	defer save.Stop()
	s.pollNode(ctx)
	s.pollConfirmations(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-nodeTick.C:
			s.pollNode(ctx)
			s.pollConfirmations(ctx)
		case now := <-sample.C:
			snap := s.st.Snapshot()
			s.hist.add(now, snap.Pool.Hashrate5m)
		case <-save.C:
			s.SaveHistory()
		}
	}
}

func (s *Server) pollNode(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var d NodeDetail
	var ci struct {
		Chain                string  `json:"chain"`
		Blocks               int64   `json:"blocks"`
		Headers              int64   `json:"headers"`
		Difficulty           float64 `json:"difficulty"`
		VerificationProgress float64 `json:"verificationprogress"`
		IBD                  bool    `json:"initialblockdownload"`
		SizeOnDisk           int64   `json:"size_on_disk"`
		Pruned               bool    `json:"pruned"`
	}
	var ni struct {
		Version     int64  `json:"version"`
		Subversion  string `json:"subversion"`
		Connections int    `json:"connections"`
	}
	var mp struct {
		Size  int64 `json:"size"`
		Bytes int64 `json:"bytes"`
	}
	var errs []string
	call := func(method string, out any, params ...any) {
		if err := s.rpc.Call(ctx, method, params, out); err != nil {
			errs = append(errs, method+": "+err.Error())
		}
	}
	call("getblockchaininfo", &ci)
	call("getnetworkinfo", &ni)
	call("getmempoolinfo", &mp)
	call("uptime", &d.UptimeS)
	call("getnetworkhashps", &d.NetworkHashps)
	d.Version, d.Subversion, d.Peers = ni.Version, ni.Subversion, ni.Connections
	d.Chain, d.Blocks, d.Headers, d.Difficulty = ci.Chain, ci.Blocks, ci.Headers, ci.Difficulty
	d.SyncPct = math.Min(100, ci.VerificationProgress*100)
	d.IBD, d.DiskBytes, d.Pruned = ci.IBD, ci.SizeOnDisk, ci.Pruned
	d.MempoolTxs, d.MempoolBytes = mp.Size, mp.Bytes
	now := time.Now()
	d.UpdatedAt = &now
	d.Error = strings.Join(errs, "; ")
	s.mu.Lock()
	if d.Error != "" && s.node.UpdatedAt != nil {
		// Keep the last good values but surface the error.
		prev := s.node
		prev.Error = d.Error
		d = prev
	}
	s.node = d
	s.mu.Unlock()
}

// pollConfirmations refreshes confirmations for blocks that are still
// pending or not yet matured (and re-checks matured ones occasionally is not
// needed: a reorg deeper than maturity is out of scope).
func (s *Server) pollConfirmations(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	maturity := int64(Coins[s.cfg.Coin].Maturity)
	for _, b := range s.st.Blocks() {
		if b.Status != "accepted" && b.Status != "pending" {
			continue
		}
		s.mu.Lock()
		c, known := s.confs[b.Hash]
		s.mu.Unlock()
		if known && c > maturity {
			continue
		}
		hdr, err := s.rpc.GetBlockHeader(ctx, b.Hash)
		if err != nil {
			continue
		}
		s.mu.Lock()
		s.confs[b.Hash] = hdr.Confirmations
		s.mu.Unlock()
	}
}

// BlockView is a block record with chain state for display.
type BlockView struct {
	stats.BlockRecord
	Confirmations int64  `json:"confirmations"`
	ChainStatus   string `json:"chain_status"` // pending | confirming | matured | orphaned | stale | rejected
	ExplorerURL   string `json:"explorer_url,omitempty"`
}

// RoundView is a completed or current job with its creature and luck.
type RoundView struct {
	stats.Round
	Current      bool    `json:"current"`
	PctOfNetwork float64 `json:"pct_of_network"`
	// LuckPct is the percentage of equally-sized rounds whose best share
	// would be lower: P(best < D) = exp(-S/D), S = total credited share
	// difficulty (each hash beats difficulty D with probability 1/(D*2^32)).
	LuckPct  *float64 `json:"luck_percentile"`
	Tier     int      `json:"tier"`
	Creature string   `json:"creature"`
}

// State is the document sent to the UI.
type State struct {
	Now      int64                  `json:"now"`
	Coin     CoinInfo               `json:"coin"`
	Chain    string                 `json:"chain"`
	Version  string                 `json:"version"`
	UptimeS  float64                `json:"uptime_s"`
	Node     stats.NodeStatus       `json:"node"`
	Detail   NodeDetail             `json:"node_detail"`
	Template stats.TemplateInfo     `json:"template"`
	Pool     stats.PoolSnapshot     `json:"pool"`
	Derived  Derived                `json:"derived"`
	Workers  []stats.WorkerSnapshot `json:"workers"`
	Blocks   []BlockView            `json:"blocks"`
	Rounds   []RoundView            `json:"rounds"`
	Stratum  StratumInfo            `json:"stratum"`
}

// Derived are values computed from the raw statistics.
type Derived struct {
	Hashrate24h     float64  `json:"hashrate_24h"`
	History24hSpanS float64  `json:"hashrate_24h_span_s"` // how much history the 24h average covers
	BestThisJob     float64  `json:"best_this_job"`
	BestThisJobPct  float64  `json:"best_this_job_pct"`
	ExpectedBlockS  *float64 `json:"expected_time_to_block_s"`
	OddsDay         float64  `json:"odds_day"`
	OddsWeek        float64  `json:"odds_week"`
	OddsYear        float64  `json:"odds_year"`
	StaleShares     uint64   `json:"stale_shares"`
	WorkersTotal    int      `json:"workers_total"`
}

// StratumInfo tells miners how to connect.
type StratumInfo struct {
	Port            int    `json:"port"`
	PayoutMode      string `json:"payout_mode"`
	PayoutAddress   string `json:"payout_address,omitempty"`
	UsernameFormat  string `json:"username_format"`
	Extranonce2Size int    `json:"extranonce2_size"`
	VersionMask     string `json:"version_rolling_mask"`
}

// odds returns the probability of at least one block in seconds at hashrate.
func odds(hashrate, netDiff, seconds float64) float64 {
	if hashrate <= 0 || netDiff <= 0 {
		return 0
	}
	return 1 - math.Exp(-hashrate*seconds/(netDiff*4294967296))
}

// BuildState assembles the UI document.
func (s *Server) BuildState(maxBlocks, maxRounds int) State {
	snap := s.st.Snapshot()
	coin := Coins[s.cfg.Coin]
	chain := snap.Node.Chain
	st := State{
		Now: time.Now().UnixMilli(), Coin: coin, Chain: chain, Version: s.cfg.Version, UptimeS: snap.Uptime,
		Node: snap.Node, Template: snap.Template, Pool: snap.Pool, Workers: snap.Workers,
		Stratum: StratumInfo{
			Port: s.cfg.StratumPort, PayoutMode: s.cfg.PayoutMode, PayoutAddress: s.cfg.PayoutAddress,
			Extranonce2Size: s.cfg.Extranonce2Size, VersionMask: s.cfg.VersionMask,
		},
	}
	if st.Workers == nil {
		st.Workers = []stats.WorkerSnapshot{}
	}
	if s.cfg.PayoutMode == "miner" {
		st.Stratum.UsernameFormat = "<" + strings.TrimSuffix(coin.AddressHint[chain], "…") + "…address>.<worker>"
	} else {
		st.Stratum.UsernameFormat = "any name, e.g. bitaxe1"
	}
	s.mu.Lock()
	st.Detail = s.node
	confs := make(map[string]int64, len(s.confs))
	for k, v := range s.confs {
		confs[k] = v
	}
	s.mu.Unlock()

	nd := snap.Template.NetworkDiff
	st.Derived.Hashrate24h, st.Derived.History24hSpanS = s.hist.average(24 * time.Hour)
	if st.Derived.History24hSpanS == 0 {
		st.Derived.Hashrate24h = snap.Pool.Hashrate1h
	}
	hr := snap.Pool.Hashrate1h
	if hr == 0 {
		hr = snap.Pool.Hashrate5m
	}
	if hr > 0 && nd > 0 {
		v := nd * 4294967296 / hr
		st.Derived.ExpectedBlockS = &v
	}
	st.Derived.OddsDay = odds(hr, nd, 86400)
	st.Derived.OddsWeek = odds(hr, nd, 7*86400)
	st.Derived.OddsYear = odds(hr, nd, 365*86400)
	st.Derived.StaleShares = snap.Pool.Rejects["stale"]
	st.Derived.WorkersTotal = len(snap.Workers)

	// Blocks, newest first.
	st.Blocks = []BlockView{}
	maturity := int64(coin.Maturity)
	for i := len(snap.Blocks) - 1; i >= 0 && len(st.Blocks) < maxBlocks; i-- {
		b := snap.Blocks[i]
		v := BlockView{BlockRecord: b}
		c, known := confs[b.Hash]
		v.Confirmations = c
		switch {
		case b.Status == "pending":
			v.ChainStatus = "pending"
		case b.Status == "accepted" && known && c < 0:
			v.ChainStatus = "orphaned" // reorganised out after acceptance
		case b.Status == "accepted" && known && c > maturity:
			v.ChainStatus = "matured"
		case b.Status == "accepted":
			v.ChainStatus = "confirming"
			if !known {
				v.Confirmations = 1
			}
		default:
			v.ChainStatus = b.Status
		}
		if u := coin.Explorer[chain]; u != "" {
			v.ExplorerURL = strings.ReplaceAll(u, "{hash}", b.Hash)
		}
		st.Blocks = append(st.Blocks, v)
	}

	st.Rounds = []RoundView{}
	for i, r := range s.st.Rounds(maxRounds) {
		v := RoundView{Round: r, Current: i == 0 && r.End.IsZero()}
		if r.NetworkDiff > 0 {
			v.PctOfNetwork = r.BestDiff / r.NetworkDiff * 100
		}
		if r.BestDiff > 0 && r.SumDiff > 0 {
			l := math.Exp(-r.SumDiff/r.BestDiff) * 100
			v.LuckPct = &l
		}
		v.Tier, v.Creature = creatureFor(v.PctOfNetwork)
		if r.BestDiff == 0 {
			v.Tier, v.Creature = -1, "nothing yet"
		}
		if v.Current {
			st.Derived.BestThisJob, st.Derived.BestThisJobPct = r.BestDiff, v.PctOfNetwork
		}
		st.Rounds = append(st.Rounds, v)
	}
	return st
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.BuildState(50, 30))
}

func (s *Server) handleBlocks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.BuildState(10000, 0).Blocks)
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	rng := r.URL.Query().Get("range")
	pts, step, ok := s.hist.series(rng)
	if !ok {
		http.Error(w, "range must be 1h, 24h or 7d", http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"range": rng, "step_s": step.Seconds(), "points": pts})
}

const maxSSEClients = 32

// handleEvents streams the state every second over Server-Sent Events.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	if s.sseClients.Add(1) > maxSSEClients {
		s.sseClients.Add(-1)
		http.Error(w, "too many clients", http.StatusServiceUnavailable)
		return
	}
	defer s.sseClients.Add(-1)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	fmt.Fprint(w, "retry: 3000\n\n")
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		b, err := json.Marshal(s.BuildState(50, 30))
		if err != nil {
			return
		}
		if _, err := fmt.Fprintf(w, "event: state\ndata: %s\n\n", b); err != nil {
			return
		}
		fl.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
		}
	}
}
