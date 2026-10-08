//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/slippybogle/wizard-blocks/internal/address"
	"github.com/slippybogle/wizard-blocks/internal/bitcoin"
	"github.com/slippybogle/wizard-blocks/internal/engine"
	"github.com/slippybogle/wizard-blocks/internal/logging"
	"github.com/slippybogle/wizard-blocks/internal/stats"
	"github.com/slippybogle/wizard-blocks/internal/stratum"
	"github.com/slippybogle/wizard-blocks/internal/testminer"
)

const minZeroBits = 16 // every submitted share/block hash must start with 16 zero bits

// TestRegtestBTC runs with Bitcoin Core's default regtest deployments: the
// "testdummy" BIP9 deployment signals on version bit 28 — inside the BIP320
// rolling mask — from height 144, so the version-rolling interpretations
// diverge (as mainnet BIP9 signalling inside the mask would).
func TestRegtestBTC(t *testing.T) { runCoin(t, suite{coin: "btc", expectSignal: true}) }

// TestRegtestBTCNoSignal disables testdummy so every template version is
// 0x20000000 and all interpretations coincide (the normal mainnet path).
func TestRegtestBTCNoSignal(t *testing.T) {
	runCoin(t, suite{coin: "btc", nodeArgs: []string{"-vbparams=testdummy:-2:0"}})
}

// TestRegtestBCH is the extended BCH suite: 500+ blocks across four
// halvings, a 1000-tx template, a node restart, every CashAddr/legacy
// payout form, and an empty coinbase tag so the 100-byte minimum
// transaction size padding is exercised.
func TestRegtestBCH(t *testing.T) {
	runCoin(t, suite{coin: "bch", phaseA: 350, perVariant: 22, nTx: 1000, nodeRestart: true, emptyTagPhaseB: true,
		concurrent: true, minBlocks: 500})
}

// suite parameterises runCoin.
type suite struct {
	coin           string
	expectSignal   bool
	nodeArgs       []string
	phaseA         int // accepted blocks to reach in phase A (default 150)
	perVariant     int // blocks per payout variant in phase B (default 12)
	nTx            int // wallet transactions to put in one template (default 300)
	nodeRestart    bool
	concurrent     bool
	emptyTagPhaseB bool
	minBlocks      int // default 200
}

const rollMask = 0x1fffe000

// submission is what our miner (or a hand-built share) actually hashed.
type submission struct {
	worker, mode string
	version      uint32
}

// recorder maps block-hash hex -> submission for every accepted share.
type recorder struct {
	mu sync.Mutex
	m  map[string]submission
}

func (r *recorder) add(hash string, s submission) {
	r.mu.Lock()
	r.m[hash] = s
	r.mu.Unlock()
}

func (r *recorder) get(hash string) (submission, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.m[hash]
	return s, ok
}

// miner wraps a running test miner.
type miner struct {
	c        *testminer.Client
	cancel   context.CancelFunc
	done     chan struct{}
	accepted atomic.Int64
	mu       sync.Mutex
	rejects  map[int]int
}

func startMiner(t *testing.T, en *Engine, rec *recorder, user, mode string) *miner {
	t.Helper()
	ctx := context.Background()
	c, err := testminer.Dial(en.E.StratumAddr())
	if err != nil {
		t.Fatal(err)
	}
	mask, err := c.Configure(ctx, 0xffffffff)
	if err != nil {
		t.Fatal(err)
	}
	if mask != 0x1fffe000 {
		t.Fatalf("negotiated mask %08x, want 1fffe000", mask)
	}
	if err := c.Subscribe(ctx); err != nil {
		t.Fatal(err)
	}
	r, err := c.Authorize(ctx, user, "x")
	if err != nil || !r.OK() {
		t.Fatalf("authorize %s: %v %s", user, err, r.Error)
	}
	m := &miner{c: c, done: make(chan struct{}), rejects: map[int]int{}}
	mctx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	go func() {
		defer close(m.done)
		_ = c.Mine(mctx, testminer.MineOptions{
			Worker: user, Threads: 2, MinZeroBits: minZeroBits, RollVersion: true, VersionMode: mode,
			OneBlockPerPrevHash: true,
			OnSubmit: func(j *testminer.Job, hash string, version uint32) {
				rec.add(hash, submission{worker: user, mode: mode, version: version})
			},
			OnResult: func(j *testminer.Job, r *testminer.Response, hash string, version uint32) {
				if r.OK() {
					m.accepted.Add(1)
					return
				}
				m.mu.Lock()
				m.rejects[r.ErrCode()]++
				m.mu.Unlock()
			},
		})
	}()
	return m
}

func (m *miner) stop(t *testing.T) {
	m.cancel()
	m.c.Close()
	select {
	case <-m.done:
	case <-time.After(30 * time.Second):
		t.Fatal("miner did not stop")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Stale (21) is expected on regtest (a block every share); nothing else is.
	for code, n := range m.rejects {
		if code != 21 {
			t.Errorf("miner got %d rejections with code %d", n, code)
		}
	}
}

func mineUntil(t *testing.T, en *Engine, target int, timeout time.Duration) {
	t.Helper()
	waitFor(t, fmt.Sprintf("%d accepted blocks", target), timeout, func() bool { return en.acceptedBlocks() >= target })
}

func runCoin(t *testing.T, cfg suite) {
	coin, expectSignal, nodeArgs := cfg.coin, cfg.expectSignal, cfg.nodeArgs
	if cfg.phaseA == 0 {
		cfg.phaseA = 150
	}
	if cfg.perVariant == 0 {
		cfg.perVariant = 12
	}
	if cfg.nTx == 0 {
		cfg.nTx = 300
	}
	if cfg.minBlocks == 0 {
		cfg.minBlocks = 200
	}
	suffix := randHex(3)
	rec := &recorder{m: map[string]submission{}}
	netName := "wbit-" + coin + "-" + suffix
	docker(t, "network", "create", netName)
	t.Cleanup(func() { docker(t, "network", "rm", netName) })

	a := startNode(t, coin, netName, "wbit-"+coin+"-a-"+suffix, nodeArgs...)
	var none any
	a.call(t, a.RPC, "createwallet", &none, "w")
	external := 0
	if coin == "bch" {
		// BCHN serves getblocktemplate only with a peer and outside IBD.
		startNode(t, coin, netName, "wbit-"+coin+"-b-"+suffix, "-connect="+a.Name)
		waitFor(t, "BCHN peer", 30*time.Second, func() bool {
			var n int
			a.call(t, a.RPC, "getconnectioncount", &n)
			return n >= 1
		})
		var hs []string
		a.call(t, a.RPC, "generatetoaddress", &hs, 1, a.newAddress(t, ""))
	}
	payout := a.newAddress(t, "")
	startHeight := a.height(t)
	t.Logf("%s: start height %d payout %s", coin, startHeight, payout)

	t.Run("BadPayoutRefused", func(t *testing.T) { testBadPayout(t, a, coin, payout) })

	// ---- Phase A: fixed payout, ZMQ + polling, two miners, reconnect, many txs ----
	cfgA := engineConfig(a, coin)
	cfgA.Payout.Mode, cfgA.Payout.Address = "fixed", payout
	cfgA.UI.AdminPassword = "it-admin-pass"
	cfgA.DataDir = t.TempDir()
	enA := startEngine(t, cfgA, coin+"-fixed")

	m1 := startMiner(t, enA, rec, "rig1", "bip310")
	mineUntil(t, enA, 60, 5*time.Minute)

	// Disconnect, verify the server noticed, reconnect with ESP-Miner-style
	// (XOR) version-bits encoding.
	m1.stop(t)
	waitFor(t, "connection count 0", 10*time.Second, func() bool { return enA.E.Stats().Snapshot().Pool.Connections == 0 })
	m2 := startMiner(t, enA, rec, "rig1", "xor")
	mineUntil(t, enA, 110, 5*time.Minute)
	m2.stop(t)

	// Fill the mempool with many wallet transactions (coinbases are mature now).
	dests := make([]string, 8)
	for i := range dests {
		dests[i] = a.newAddress(t, "")
	}
	nTx := cfg.nTx
	txStart := time.Now()
	for i := 0; i < nTx; i++ {
		var txid string
		a.call(t, a.Wallet, "sendtoaddress", &txid, dests[i%len(dests)], "0.001")
	}
	var mp struct {
		Size int `json:"size"`
	}
	a.call(t, a.RPC, "getmempoolinfo", &mp)
	if mp.Size < nTx {
		t.Fatalf("mempool has %d txs, want >= %d", mp.Size, nTx)
	}
	t.Logf("created %d wallet txs in %v", nTx, time.Since(txStart))
	before := enA.acceptedBlocks()
	m3 := startMiner(t, enA, rec, "rig2", "or")
	waitFor(t, "mempool drained into our blocks", 3*time.Minute, func() bool {
		a.call(t, a.RPC, "getmempoolinfo", &mp)
		return mp.Size == 0 && enA.acceptedBlocks() > before
	})
	mineUntil(t, enA, cfg.phaseA, 10*time.Minute)
	m3.stop(t)

	if cfg.nodeRestart {
		t.Run("NodeRestart", func(t *testing.T) { testNodeRestart(t, enA, a, rec) })
	}

	var expectNonAccepted map[string][]string // hash -> allowed statuses
	t.Run("ProtocolAndInvalidShares", func(t *testing.T) { expectNonAccepted = testProtocol(t, enA, a, rec, &external) })
	if cfg.concurrent {
		t.Run("ConcurrentMiners", func(t *testing.T) { testConcurrent(t, enA, a, rec, expectNonAccepted) })
		t.Run("LiveDifficultySettings", func(t *testing.T) { testLiveDifficulty(t, enA, cfgA.DataDir) })
		t.Run("CreaturesAndMana", func(t *testing.T) { testCreaturesAndMana(t, enA) })
	}

	// ZMQ must have been the primary new-block signal in phase A.
	if s := enA.E.Stats().Snapshot(); !s.Node.ZMQConnected || s.Node.ZMQMessages < 100 {
		t.Errorf("zmq not used: connected=%v messages=%d", s.Node.ZMQConnected, s.Node.ZMQMessages)
	}
	checkAPI(t, enA)
	enA.Stop(t)

	// ---- Phase B: miner-address mode, ZMQ disabled (polling fallback only) ----
	cfgB := engineConfig(a, coin)
	cfgB.Payout.Mode, cfgB.Payout.Address = "miner", ""
	cfgB.Node.ZMQHashBlock = ""
	if cfg.emptyTagPhaseB {
		cfgB.Payout.CoinbaseTag = ""
	}
	enB := startEngine(t, cfgB, coin+"-miner")
	t.Run("MinerModeAuthorize", func(t *testing.T) { testMinerModeAuth(t, enB, a, coin, &external) })

	for i, addr := range payoutVariants(t, a, coin) {
		before := enB.acceptedBlocks()
		mode := []string{"bip310", "xor", "or"}[i%3]
		m := startMiner(t, enB, rec, fmt.Sprintf("%s.worker%d", addr, i), mode)
		mineUntil(t, enB, before+cfg.perVariant, 5*time.Minute)
		m.stop(t)
	}
	checkAPI(t, enB)
	enB.Stop(t)

	tagged := map[string]bool{}
	for _, b := range enA.E.Stats().Blocks() {
		tagged[b.Hash] = true
	}
	if !cfg.emptyTagPhaseB {
		for _, b := range enB.E.Stats().Blocks() {
			tagged[b.Hash] = true
		}
	}
	verifyChain(t, a, coin, startHeight, external, append(enA.E.Stats().Blocks(), enB.E.Stats().Blocks()...),
		rec, expectNonAccepted, expectSignal, tagged, cfg.minBlocks)
}

func testBadPayout(t *testing.T, a *Node, coin, good string) {
	cases := map[string]string{
		"garbage":            "notanaddress",
		"corrupted checksum": good[:len(good)-1] + flipChar(good[len(good)-1]),
	}
	if coin == "btc" {
		cases["mainnet bech32"] = "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4"
		cases["mainnet legacy"] = "1AGNa15ZQXAZUgFiqJ2i7Z2DPU2J6hW62i"
		cases["bch address"] = "bchreg:qq4m39v6cpv5pwfvpu0eqnqcprfkws3n3yxe3sflln"
		v2, _ := address.SegwitEncode("bcrt", 2, bytes.Repeat([]byte{7}, 32))
		cases["witness v2 (undefined)"] = v2
	} else {
		cases["mainnet cashaddr"] = "bitcoincash:qr6m7j9njldwwzlg9v7v53unlr4jkmx6eylep8ekg2"
		cases["btc address"] = "bcrt1qe3m97shecx7q6uvmrpsvk7q9cduy9xdhh6qp59"
		_, typ, h, err := address.CashAddrDecode(good, "bchreg")
		if err != nil {
			t.Fatal(err)
		}
		wrongNet, _ := address.CashAddrEncode("bchtest", typ, h)
		cases["testnet prefix"] = wrongNet
	}
	for name, addr := range cases {
		cfg := engineConfig(a, coin)
		cfg.Payout.Address = addr
		cfg.Stratum.Listen = "127.0.0.1:0"
		log, _ := logging.New(&bytes.Buffer{}, "error", "text")
		e := engine.New(cfg, "it", log)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := e.Start(ctx)
		cancel()
		if err == nil {
			t.Errorf("%s: engine started with bad payout %q", name, addr)
			continue
		}
		t.Logf("%s (%s): refused: %v", name, addr, err)
	}
	// An empty address is rejected by configuration validation.
	cfg := engineConfig(a, coin)
	cfg.Payout.Address = ""
	if err := cfg.Validate(); err == nil {
		t.Error("empty fixed payout address passed validation")
	}
}

func flipChar(c byte) string {
	if c == 'q' {
		return "p"
	}
	if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
		return "q"
	}
	return "Q"
}

func expectCode(t *testing.T, what string, r *testminer.Response, err error, code int) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	if code == 0 {
		if !r.OK() {
			t.Fatalf("%s: expected acceptance, got %s %s", what, r.Result, r.Error)
		}
		return
	}
	if r.ErrCode() != code || string(r.Result) == "true" {
		t.Fatalf("%s: expected error %d, got result=%s error=%s", what, code, r.Result, r.Error)
	}
	t.Logf("%s: rejected as expected: %s", what, r.Error)
}

func testProtocol(t *testing.T, en *Engine, a *Node, rec *recorder, external *int) map[string][]string {
	nonAccepted := map[string][]string{}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	addr := en.E.StratumAddr()

	// Submitting before subscribing.
	c0, err := testminer.Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c0.Close()
	r, err := c0.Authorize(ctx, "rig-ns", "x")
	expectCode(t, "authorize without subscribe", r, err, 0)
	r, err = c0.Submit(ctx, "rig-ns", "1", "0000000000000000", "00000000", "00000000", "")
	expectCode(t, "submit without subscribe", r, err, 25)

	c, err := testminer.Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	mask, err := c.Configure(ctx, 0x1fffe000)
	if err != nil || mask != 0x1fffe000 {
		t.Fatalf("configure: %08x %v", mask, err)
	}
	if err := c.Subscribe(ctx); err != nil {
		t.Fatal(err)
	}
	r, err = c.Call(ctx, "mining.extranonce.subscribe")
	expectCode(t, "extranonce.subscribe", r, err, 0)
	r, err = c.Authorize(ctx, "rig-x", "d=0.5")
	expectCode(t, "authorize", r, err, 0)
	if _, err := c.WaitJob(ctx); err != nil {
		t.Fatal(err)
	}
	en1, en2Size, _, diff, job := c.State()
	if diff <= 0 || diff > 4.7e-10 {
		t.Fatalf("share difficulty %v not capped at network difficulty", diff)
	}
	en2 := bytes.Repeat([]byte{0x42}, en2Size)
	en2Hex := hex.EncodeToString(en2)
	nt := fmt.Sprintf("%08x", job.NTime)
	jv := job.Version
	rolled := jv&^mask | 0x0aa00000&mask
	bits := fmt.Sprintf("%08x", rolled&mask)

	r, err = c.Submit(ctx, "nobody", job.ID, en2Hex, nt, "00000000", bits)
	expectCode(t, "unauthorized worker", r, err, 24)
	r, err = c.Submit(ctx, "rig-x", "ffffffffff", en2Hex, nt, "00000000", bits)
	expectCode(t, "unknown job", r, err, 21)
	r, err = c.Submit(ctx, "rig-x", job.ID, en2Hex[2:], nt, "00000000", bits)
	expectCode(t, "short extranonce2", r, err, 20)
	r, err = c.Submit(ctx, "rig-x", job.ID, en2Hex, nt, "zz000000", bits)
	expectCode(t, "bad nonce hex", r, err, 20)
	r, err = c.Submit(ctx, "rig-x", job.ID, en2Hex, fmt.Sprintf("%08x", job.NTime-3*86400), "00000000", bits)
	expectCode(t, "ntime too old", r, err, 20)
	r, err = c.Submit(ctx, "rig-x", job.ID, en2Hex, fmt.Sprintf("%08x", time.Now().Unix()+3*3600), "00000000", bits)
	expectCode(t, "ntime too far in future", r, err, 20)
	r, err = c.Submit(ctx, "rig-x", job.ID, en2Hex, nt, "00000000", "00000001")
	expectCode(t, "version bits outside mask", r, err, 20)
	r, err = c.CallRawParams(ctx, "mining.submit", json.RawMessage(`"not-an-array"`))
	expectCode(t, "params not an array", r, err, 20)
	r, err = c.CallRawParams(ctx, "mining.submit", json.RawMessage(`["rig-x",1,2,3,4]`))
	expectCode(t, "non-string params", r, err, 20)
	r, err = c.Call(ctx, "mining.get_transactions", job.ID)
	expectCode(t, "unsupported method", r, err, 20)

	// Low difficulty: a hash above the (capped) share target = network target.
	netTarget := testminer.CompactTarget(job.Bits)
	// When the job version has bits inside the mask (regtest's testdummy
	// deployment signals bit 28), the server also evaluates the XOR
	// interpretation of the version bits, so both headers must miss.
	altVersion := jv ^ (rolled & mask)
	nonce, _, ok := testminer.Grind(job, en1, en2, rolled, job.NTime, func(be [32]byte) bool {
		if new(big.Int).SetBytes(be[:]).Cmp(netTarget) <= 0 {
			return false
		}
		return true
	})
	for ok && jv&mask != 0 {
		alt := testminer.HashBE(testminer.Header(job, en1, en2, altVersion, job.NTime, nonce))
		if new(big.Int).SetBytes(alt[:]).Cmp(netTarget) > 0 {
			break
		}
		start := nonce + 1
		nonce, _, ok = testminer.GrindFrom(job, en1, en2, rolled, job.NTime, start, func(be [32]byte) bool {
			return new(big.Int).SetBytes(be[:]).Cmp(netTarget) > 0
		})
	}
	if !ok {
		t.Fatal("grind failed")
	}
	r, err = c.Submit(ctx, "rig-x", job.ID, en2Hex, nt, fmt.Sprintf("%08x", nonce), bits)
	expectCode(t, "low difficulty share", r, err, 23)

	// Valid share (solves the regtest block) sent twice in one write: the
	// first is accepted, the second is a duplicate.
	nonce, be, ok := testminer.Grind(job, en1, en2, rolled, job.NTime, func(be [32]byte) bool {
		return testminer.LeadingZeroBits(be) >= minZeroBits
	})
	if !ok {
		t.Fatal("grind failed")
	}
	h0 := a.height(t)
	p := []any{"rig-x", job.ID, en2Hex, nt, fmt.Sprintf("%08x", nonce), bits}
	rs, err := c.CallMany(ctx, "mining.submit", p, p)
	if err != nil {
		t.Fatal(err)
	}
	expectCode(t, "valid share", rs[0], nil, 0)
	expectCode(t, "duplicate share", rs[1], nil, 22)
	rec.add(hex.EncodeToString(be[:]), submission{worker: "rig-x", mode: "bip310", version: rolled})
	waitFor(t, "block from protocol test on chain", 30*time.Second, func() bool { return a.height(t) == h0+1 })
	var best string
	a.call(t, a.RPC, "getbestblockhash", &best)
	if best != hex.EncodeToString(be[:]) {
		t.Fatalf("best block %s, expected our share %x", best, be)
	}

	// Same-height race: two different block solutions for one job, pipelined.
	// Both are valid shares; exactly one becomes the block at that height, the
	// other must be recorded as orphaned/stale and not counted as found.
	waitFor(t, "fresh job for race", 10*time.Second, func() bool {
		_, _, _, _, j := c.State()
		return j != nil && !bytes.Equal(j.PrevHash, job.PrevHash)
	})
	raceJob := c.DrainJobs()
	rjv := raceJob.Version
	rrolled := rjv&^mask | 0x05500000&mask
	rbits := fmt.Sprintf("%08x", rrolled&mask)
	waitFor(t, "earlier block verifications to finish", 30*time.Second, func() bool {
		for _, b := range en.E.Stats().Blocks() {
			if b.Status == "pending" {
				return false
			}
		}
		return true
	})
	foundBefore := en.E.Stats().Snapshot().Pool.BlocksFound
	n1, be1, ok1 := testminer.Grind(raceJob, en1, en2, rrolled, raceJob.NTime, func(be [32]byte) bool {
		return testminer.LeadingZeroBits(be) >= minZeroBits
	})
	n2, be2, ok2 := testminer.GrindFrom(raceJob, en1, en2, rrolled, raceJob.NTime, n1+1, func(be [32]byte) bool {
		return testminer.LeadingZeroBits(be) >= minZeroBits
	})
	if !ok1 || !ok2 {
		t.Fatal("grind failed")
	}
	hr := a.height(t)
	rnt := fmt.Sprintf("%08x", raceJob.NTime)
	rs, err = c.CallMany(ctx, "mining.submit",
		[]any{"rig-x", raceJob.ID, en2Hex, rnt, fmt.Sprintf("%08x", n1), rbits},
		[]any{"rig-x", raceJob.ID, en2Hex, rnt, fmt.Sprintf("%08x", n2), rbits})
	if err != nil {
		t.Fatal(err)
	}
	expectCode(t, "race share 1", rs[0], nil, 0)
	expectCode(t, "race share 2", rs[1], nil, 0)
	h1, h2 := hex.EncodeToString(be1[:]), hex.EncodeToString(be2[:])
	rec.add(h1, submission{worker: "rig-x", mode: "bip310", version: rrolled})
	rec.add(h2, submission{worker: "rig-x", mode: "bip310", version: rrolled})
	final := func(h string) string {
		for _, b := range en.E.Stats().Blocks() {
			if b.Hash == h && b.Status != "pending" {
				return b.Status
			}
		}
		return ""
	}
	waitFor(t, "both race blocks resolved", 30*time.Second, func() bool { return final(h1) != "" && final(h2) != "" })
	s1, s2 := final(h1), final(h2)
	winner, loser, loserStatus := h1, h2, s2
	if s2 == "accepted" {
		winner, loser, loserStatus = h2, h1, s1
	}
	if final(winner) != "accepted" || (loserStatus != "orphaned" && loserStatus != "stale") {
		t.Fatalf("race: statuses %s=%s %s=%s; want exactly one accepted, other orphaned/stale", h1, s1, h2, s2)
	}
	var atHeight string
	a.call(t, a.RPC, "getblockhash", &atHeight, hr+1)
	if atHeight != winner || a.height(t) != hr+1 {
		t.Fatalf("race: chain has %s at %d, want winner %s", atHeight, hr+1, winner)
	}
	if got := en.E.Stats().Snapshot().Pool.BlocksFound; got != foundBefore+1 {
		t.Fatalf("race: blocks_found went %d -> %d, want +1 (loser must not count)", foundBefore, got)
	}
	nonAccepted[loser] = []string{"orphaned", "stale"}
	t.Logf("same-height race: winner %s accepted, loser %s %s", winner, loser, loserStatus)
	job = raceJob

	// Stale share: a new block arrives (mined by someone else) mid-job.
	waitFor(t, "fresh job", 10*time.Second, func() bool {
		_, _, _, _, j := c.State()
		return j != nil && !bytes.Equal(j.PrevHash, job.PrevHash)
	})
	oldJob := c.DrainJobs()
	other := a.newAddress(t, "")
	var hs []string
	detectStart := time.Now()
	a.call(t, a.RPC, "generatetoaddress", &hs, 1, other)
	*external++
	nj, err := c.WaitJob(ctx)
	for err == nil && bytes.Equal(nj.PrevHash, oldJob.PrevHash) {
		nj, err = c.WaitJob(ctx)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !nj.Clean {
		t.Fatal("new block job without clean_jobs=true")
	}
	t.Logf("external block detected and clean job sent in %v (zmq)", time.Since(detectStart))
	nonce, staleBE, ok := testminer.Grind(oldJob, en1, en2, oldJob.Version, oldJob.NTime, func(be [32]byte) bool {
		return testminer.LeadingZeroBits(be) >= minZeroBits
	})
	nonAccepted[hex.EncodeToString(staleBE[:])] = []string{"stale"}
	if !ok {
		t.Fatal("grind failed")
	}
	r, err = c.Submit(ctx, "rig-x", oldJob.ID, en2Hex, fmt.Sprintf("%08x", oldJob.NTime), fmt.Sprintf("%08x", nonce), fmt.Sprintf("%08x", oldJob.Version&mask))
	expectCode(t, "stale share", r, err, 21)
	waitFor(t, "stale block candidate recorded", 30*time.Second, func() bool {
		for _, b := range en.E.Stats().Blocks() {
			if b.Status == "stale" {
				return true
			}
		}
		return false
	})

	// Malformed JSON does not kill the connection; an oversized line does.
	if err := c.SendRaw([]byte("{this is not json\n")); err != nil {
		t.Fatal(err)
	}
	r, err = c.Call(ctx, "mining.extranonce.subscribe")
	expectCode(t, "connection alive after malformed json", r, err, 0)
	_ = c.SendRaw(append(bytes.Repeat([]byte("a"), 20000), '\n'))
	select {
	case <-c.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("oversized line did not disconnect")
	}

	return nonAccepted
}

func testMinerModeAuth(t *testing.T, en *Engine, a *Node, coin string, external *int) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c, err := testminer.Dial(en.E.StratumAddr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Subscribe(ctx); err != nil {
		t.Fatal(err)
	}
	bad := []string{"rig1", "garbage.worker", "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4.w"}
	if coin == "btc" {
		bad = append(bad, "bchreg:qq4m39v6cpv5pwfvpu0eqnqcprfkws3n3yxe3sflln.w")
	} else {
		bad = append(bad, "bcrt1qe3m97shecx7q6uvmrpsvk7q9cduy9xdhh6qp59.w", "bitcoincash:qr6m7j9njldwwzlg9v7v53unlr4jkmx6eylep8ekg2")
	}
	for _, u := range bad {
		r, err := c.Authorize(ctx, u, "x")
		expectCode(t, "authorize "+u, r, err, 24)
	}
	good := a.newAddress(t, "")
	r, err := c.Authorize(ctx, good+".probe", "x")
	expectCode(t, "authorize valid address", r, err, 0)
	j, err := c.WaitJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Polling-only new block detection (ZMQ disabled in this engine).
	var hs []string
	start := time.Now()
	a.call(t, a.RPC, "generatetoaddress", &hs, 1, a.newAddress(t, ""))
	*external++
	for {
		nj, err := c.WaitJob(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(nj.PrevHash, j.PrevHash) {
			if !nj.Clean {
				t.Fatal("clean_jobs not set on new block")
			}
			break
		}
	}
	el := time.Since(start)
	t.Logf("polling detected external block in %v", el)
	if el > 3*time.Second {
		t.Errorf("polling fallback too slow: %v", el)
	}
}

// payoutVariants returns addresses of every supported output type.
func payoutVariants(t *testing.T, a *Node, coin string) []string {
	if coin == "btc" {
		return []string{
			a.newAddress(t, "legacy"),
			a.newAddress(t, "p2sh-segwit"),
			a.newAddress(t, "bech32"),
			a.newAddress(t, "bech32m"),
			// External P2WSH (not in wallet) — validity is all that matters.
			mustSegwit(t, "bcrt", 0, bytes.Repeat([]byte{0x5a}, 32)),
		}
	}
	cash := a.newAddress(t, "")
	_, _, h, err := address.CashAddrDecode(cash, "bchreg")
	if err != nil {
		t.Fatal(err)
	}
	h32 := bytes.Repeat([]byte{0x44}, 32)
	legacy := address.Base58CheckEncode(append([]byte{0x6f}, h...))
	legacyP2SH32 := address.Base58CheckEncode(append([]byte{0xc4}, h32...))
	token, _ := address.CashAddrEncode("bchreg", 2, h)
	p2sh20, _ := address.CashAddrEncode("bchreg", 1, bytes.Repeat([]byte{0x33}, 20))
	p2sh32, _ := address.CashAddrEncode("bchreg", 1, h32)
	tokenP2SH32, _ := address.CashAddrEncode("bchreg", 3, bytes.Repeat([]byte{0x55}, 32))
	prefixless := strings.TrimPrefix(a.newAddress(t, ""), "bchreg:")
	upper := strings.ToUpper(a.newAddress(t, ""))
	return []string{cash, prefixless, upper, legacy, token, p2sh20, p2sh32, tokenP2SH32, legacyP2SH32}
}

func mustSegwit(t *testing.T, hrp string, v byte, prog []byte) string {
	s, err := address.SegwitEncode(hrp, v, prog)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func checkAPI(t *testing.T, en *Engine) {
	var snap stats.Snapshot
	waitFor(t, "no pending blocks", 30*time.Second, func() bool {
		snap = stats.Snapshot{}
		if err := json.Unmarshal(apiGet(t, en, "/stats"), &snap); err != nil {
			t.Fatal(err)
		}
		return snap.Pool.BlocksPending == 0
	})
	if snap.Pool.BlocksFound != en.acceptedBlocks() || !snap.Node.Synced || snap.Pool.Accepted == 0 || len(snap.Workers) == 0 {
		t.Errorf("unexpected /stats: blocks=%d accepted=%d synced=%v workers=%d", snap.Pool.BlocksFound, snap.Pool.Accepted, snap.Node.Synced, len(snap.Workers))
	}
	if snap.Pool.BestDiff <= 0 || snap.Pool.Hashrate5m <= 0 {
		t.Errorf("best diff %v / hashrate %v not populated", snap.Pool.BestDiff, snap.Pool.Hashrate5m)
	}
	metrics := string(apiGet(t, en, "/metrics"))
	want := fmt.Sprintf(`wb_blocks_total{coin="%s",status="accepted"} %d`, snap.Coin, en.acceptedBlocks())
	if !strings.Contains(metrics, want) {
		t.Errorf("/metrics missing %q", want)
	}
	if !strings.Contains(string(apiGet(t, en, "/healthz")), `"ok":true`) {
		t.Error("/healthz not ok")
	}
}

// verifyChain checks every block the engine submitted against the node.
func verifyChain(t *testing.T, a *Node, coin string, startHeight int64, external int, recs []stats.BlockRecord,
	rec *recorder, expectNonAccepted map[string][]string, expectSignal bool, tagged map[string]bool, minBlocks int) {
	ctx := context.Background()
	var accepted []stats.BlockRecord
	seenNon := map[string]bool{}
	for _, r := range recs {
		if r.Status == "accepted" {
			accepted = append(accepted, r)
			continue
		}
		allowed, ok := expectNonAccepted[r.Hash]
		if !ok || !contains(allowed, r.Status) {
			t.Errorf("unexpected non-accepted block %s at %d: status %s (%s)", r.Hash, r.Height, r.Status, r.Reason)
		}
		seenNon[r.Hash] = true
	}
	for h := range expectNonAccepted {
		if !seenNon[h] {
			t.Errorf("expected non-accepted block %s was not recorded", h)
		}
	}
	if len(accepted) < minBlocks {
		t.Fatalf("only %d accepted blocks, want >= %d", len(accepted), minBlocks)
	}
	final := a.height(t)
	if want := startHeight + int64(len(accepted)) + int64(external); final != want {
		t.Fatalf("chain height %d, want start %d + ours %d + external %d = %d", final, startHeight, len(accepted), external, want)
	}
	byHeight := map[int64]stats.BlockRecord{}
	maxTxs := 0
	var totalFees int64
	scriptCache := map[string]string{}
	signalled := map[string]int{}
	padded := 0
	halvings := map[int64]bool{}
	for _, r := range accepted {
		halvings[r.Height/150] = true
		if _, dup := byHeight[r.Height]; dup {
			t.Fatalf("two accepted blocks at height %d", r.Height)
		}
		byHeight[r.Height] = r
		// The server must have built exactly the header our miner hashed,
		// through the miner's own version-rolling interpretation.
		sub, ok := rec.get(r.Hash)
		if !ok {
			t.Fatalf("block %s at %d is not a header any of our miners hashed", r.Hash, r.Height)
		}
		if !stratum.HasInterp(r.VersionInterp, sub.mode) {
			t.Fatalf("block %d: server used interpretation %q, miner %s uses %q", r.Height, r.VersionInterp, sub.worker, sub.mode)
		}
		if r.BlockVersion != fmt.Sprintf("%08x", sub.version) {
			t.Fatalf("block %d: version %s, miner hashed %08x", r.Height, r.BlockVersion, sub.version)
		}
		tv, _ := strconv.ParseUint(r.TemplateVersion, 16, 32)
		if uint32(tv)&rollMask != 0 {
			signalled[sub.mode]++
			// bip310 and xor can never coincide when the template has mask bits.
			if stratum.HasInterp(r.VersionInterp, "bip310") && stratum.HasInterp(r.VersionInterp, "xor") {
				t.Fatalf("block %d: interpretation %q impossible with in-mask template %s", r.Height, r.VersionInterp, r.TemplateVersion)
			}
		} else if r.VersionInterp != "bip310+xor+or" {
			t.Fatalf("block %d: template %s has no in-mask bits but interpretation is %q", r.Height, r.TemplateVersion, r.VersionInterp)
		}
		var hdr struct {
			Confirmations int64 `json:"confirmations"`
			Height        int64 `json:"height"`
		}
		if err := a.RPC.Call(ctx, "getblockheader", []any{r.Hash, true}, &hdr); err != nil {
			t.Fatal(err)
		}
		if hdr.Confirmations < 1 || hdr.Height != r.Height {
			t.Fatalf("block %s: confirmations %d height %d (record %d)", r.Hash, hdr.Confirmations, hdr.Height, r.Height)
		}
		var rawHex string
		if err := a.RPC.Call(ctx, "getblock", []any{r.Hash, 0}, &rawHex); err != nil {
			t.Fatal(err)
		}
		raw, _ := hex.DecodeString(rawHex)
		blk, err := bitcoin.ParseBlock(raw, coin == "btc")
		if err != nil {
			t.Fatal(err)
		}
		cb := blk.Txs[0]
		if h, err := bitcoin.DecodeBIP34Height(cb.Inputs[0].Script); err != nil || h != r.Height {
			t.Fatalf("block %d: BIP34 height %d %v", r.Height, h, err)
		}
		if tagged[r.Hash] != bytes.Contains(cb.Inputs[0].Script, []byte("/wizard-blocks-it/")) {
			t.Fatalf("block %d: coinbase tag presence %v, want %v", r.Height, !tagged[r.Hash], tagged[r.Hash])
		}
		if coin == "bch" {
			// BCH (2018-11): every transaction, the coinbase included, >= 100 bytes.
			if len(cb.Raw) < 100 {
				t.Fatalf("block %d: coinbase is %d bytes (< 100)", r.Height, len(cb.Raw))
			}
			// Without a tag the scriptSig is <height><push 12 extranonce>;
			// anything longer is the zero padding added to reach 100 bytes.
			if !tagged[r.Hash] && len(cb.Inputs[0].Script) > len(bitcoin.BIP34HeightScript(r.Height))+1+12 {
				if len(cb.Raw) > 101 {
					t.Fatalf("block %d: padded coinbase is %d bytes (padding beyond minimum)", r.Height, len(cb.Raw))
				}
				padded++
			}
			for i := 2; i < len(blk.Txs); i++ {
				if !bitcoin.CTORLess(blk.Txs[i-1].TxID, blk.Txs[i].TxID) {
					t.Fatalf("block %d: CTOR violated", r.Height)
				}
			}
		}
		var bs struct {
			Subsidy  json.RawMessage `json:"subsidy"`
			TotalFee json.RawMessage `json:"totalfee"`
		}
		if err := a.RPC.Call(ctx, "getblockstats", []any{r.Height, []string{"subsidy", "totalfee"}}, &bs); err != nil {
			t.Fatal(err)
		}
		subsidy, fees := parseSats(t, bs.Subsidy), parseSats(t, bs.TotalFee)
		if want := int64(50*100_000_000) >> (r.Height / 150); subsidy != want {
			t.Fatalf("height %d: subsidy %d want %d", r.Height, subsidy, want)
		}
		script, ok := scriptCache[r.Address]
		if !ok {
			script = a.scriptOf(t, r.Address)
			scriptCache[r.Address] = script
		}
		var sum int64
		for _, o := range cb.Outputs {
			sum += o.Value
		}
		if hex.EncodeToString(cb.Outputs[0].Script) != script {
			t.Fatalf("height %d: coinbase pays %x, want %s (%s)", r.Height, cb.Outputs[0].Script, script, r.Address)
		}
		if cb.Outputs[0].Value != subsidy+fees || sum != subsidy+fees || r.Reward != subsidy+fees {
			t.Fatalf("height %d: payout %d total %d record %d, want subsidy %d + fees %d", r.Height, cb.Outputs[0].Value, sum, r.Reward, subsidy, fees)
		}
		if coin == "btc" {
			if len(cb.Outputs) != 2 || cb.Outputs[1].Value != 0 || !bytes.HasPrefix(cb.Outputs[1].Script, bitcoin.WitnessCommitmentHeader) {
				t.Fatalf("height %d: expected payout + witness commitment outputs", r.Height)
			}
		} else if len(cb.Outputs) != 1 {
			t.Fatalf("height %d: expected exactly one coinbase output", r.Height)
		}
		if len(blk.Txs) > maxTxs {
			maxTxs = len(blk.Txs)
		}
		totalFees += fees
	}
	// Every height is either ours or one of the external blocks we generated.
	ext := 0
	for h := startHeight + 1; h <= final; h++ {
		var hash string
		if err := a.RPC.Call(ctx, "getblockhash", []any{h}, &hash); err != nil {
			t.Fatal(err)
		}
		if r, ok := byHeight[h]; ok {
			if r.Hash != hash {
				t.Fatalf("height %d: chain has %s, engine recorded %s", h, hash, r.Hash)
			}
		} else {
			ext++
		}
	}
	if ext != external {
		t.Fatalf("%d heights not ours, expected %d external blocks", ext, external)
	}
	if maxTxs < 200 {
		t.Fatalf("largest engine block had %d txs, want >= 200", maxTxs)
	}
	if expectSignal {
		for _, mode := range []string{"bip310", "xor", "or"} {
			if signalled[mode] < 5 {
				t.Errorf("only %d blocks with in-mask template version for %s miners (want >= 5): %v", signalled[mode], mode, signalled)
			}
		}
	} else if len(signalled) != 0 {
		t.Errorf("unexpected in-mask template versions: %v", signalled)
	}
	t.Logf("blocks on templates signalling inside the rolling mask, by miner mode: %v", signalled)
	if coin == "bch" && len(tagged) < len(accepted) && padded == 0 {
		t.Error("no tagless coinbase hit the 100-byte padding path")
	}
	t.Logf("subsidy eras covered: %d; coinbases padded to the 100-byte minimum: %d", len(halvings), padded)
	types := map[string]bool{}
	for _, r := range accepted {
		types[r.Address] = true
	}
	t.Logf("%s: VERIFIED %d accepted blocks (heights %d..%d), %d external, largest block %d txs, total fees %d sats, %d distinct payout addresses",
		coin, len(accepted), startHeight+1, final, external, maxTxs, totalFees, len(types))
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// testNodeRestart restarts the full node under a running engine: the engine
// must survive the outage, keep miner sessions, reconnect RPC and ZMQ, and
// resume issuing work that becomes accepted blocks.
func testNodeRestart(t *testing.T, en *Engine, a *Node, rec *recorder) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	idle, err := testminer.Dial(en.E.StratumAddr())
	if err != nil {
		t.Fatal(err)
	}
	defer idle.Close()
	if err := idle.Subscribe(ctx); err != nil {
		t.Fatal(err)
	}
	if r, err := idle.Authorize(ctx, "idle", "x"); err != nil || !r.OK() {
		t.Fatal("authorize idle")
	}
	j0, err := idle.WaitJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	docker(t, "restart", "-t", "5", a.Name)
	waitFor(t, "engine notices node outage", 60*time.Second, func() bool { return !en.E.Stats().Snapshot().Node.Connected })
	waitFor(t, "node RPC back", 60*time.Second, func() bool { _, err := a.RPC.GetBlockchainInfo(ctx); return err == nil })
	var none any
	_ = a.Wallet.Call(ctx, "loadwallet", []any{"w"}, &none)
	waitFor(t, "engine reconnected", 60*time.Second, func() bool {
		s := en.E.Stats().Snapshot()
		return s.Node.Connected && s.Node.ZMQConnected
	})
	before := en.acceptedBlocks()
	m := startMiner(t, en, rec, "rig-restart", "bip310")
	mineUntil(t, en, before+10, 3*time.Minute)
	m.stop(t)
	select {
	case <-idle.Done():
		t.Fatal("idle miner session dropped during node restart")
	default:
	}
	_, _, _, _, j := idle.State()
	if j == nil || bytes.Equal(j.PrevHash, j0.PrevHash) {
		t.Fatal("idle session did not receive new work after restart")
	}
	t.Logf("node restart survived; %d blocks mined afterwards", en.acceptedBlocks()-before)
}

// testConcurrent opens 100 idle sessions and runs 4 miners at once. Every
// idle session must receive each new block's clean job; competing miners
// may race at the same height (losers end orphaned/stale, never counted).
func testConcurrent(t *testing.T, en *Engine, a *Node, rec *recorder, nonAccepted map[string][]string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var idle []*testminer.Client
	for i := 0; i < 100; i++ {
		c, err := testminer.Dial(en.E.StratumAddr())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if err := c.Subscribe(ctx); err != nil {
			t.Fatal(err)
		}
		if r, err := c.Authorize(ctx, fmt.Sprintf("idle%d", i), "x"); err != nil || !r.OK() {
			t.Fatalf("authorize idle%d", i)
		}
		idle = append(idle, c)
	}
	before := en.acceptedBlocks()
	var miners []*miner
	for i := 0; i < 4; i++ {
		miners = append(miners, startMiner(t, en, rec, fmt.Sprintf("conc%d", i), []string{"bip310", "xor", "or", "bip310"}[i]))
	}
	mineUntil(t, en, before+40, 3*time.Minute)
	for _, m := range miners {
		m.stop(t)
	}
	waitFor(t, "verifications finished", 30*time.Second, func() bool {
		for _, b := range en.E.Stats().Blocks() {
			if b.Status == "pending" {
				return false
			}
		}
		return true
	})
	var best string
	a.call(t, a.RPC, "getbestblockhash", &best)
	bestH, _ := bitcoin.HashFromDisplay(best)
	waitFor(t, "all idle sessions on the current tip", 10*time.Second, func() bool {
		for _, c := range idle {
			_, _, _, _, j := c.State()
			if j == nil || !bytes.Equal(j.PrevHash, prevHashHeaderBytes(bestH)) {
				return false
			}
		}
		return true
	})
	if n := en.E.Stats().Snapshot().Pool.Connections; n < 100 {
		t.Fatalf("only %d connections open", n)
	}
	lost := 0
	for _, b := range en.E.Stats().Blocks() {
		if strings.HasPrefix(b.Worker, "conc") && b.Status != "accepted" {
			if b.Status != "orphaned" && b.Status != "stale" {
				t.Errorf("concurrent block %s status %s", b.Hash, b.Status)
			}
			nonAccepted[b.Hash] = []string{"orphaned", "stale"}
			lost++
		}
	}
	t.Logf("100 idle sessions tracked every tip; 4 miners found %d blocks (%d lost same-height races)", en.acceptedBlocks()-before, lost)
}

// prevHashHeaderBytes is the header-order prevhash the miner reconstructs.
func prevHashHeaderBytes(h bitcoin.Hash) []byte { return h[:] }

// testLiveDifficulty drives the authenticated settings API on the UI port and
// checks that connected miners receive the new difficulty immediately.
func testLiveDifficulty(t *testing.T, en *Engine, dataDir string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	uiBase := "http://" + en.E.UIAddr()
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	call := func(method, path string, body any) int {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, uiBase+path, rd)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-WB-Admin", "1")
		res, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		return res.StatusCode
	}
	// Never exposed on the stats API port.
	if res, err := http.Get("http://" + en.E.APIAddr() + "/api/admin/settings"); err != nil || res.StatusCode != 404 {
		t.Fatalf("settings reachable on the API port: %v %v", res.StatusCode, err)
	}
	if code := call("GET", "/api/admin/settings", nil); code != 401 {
		t.Fatalf("unauthenticated settings: %d", code)
	}
	if code := call("POST", "/api/admin/login", map[string]string{"password": "it-admin-pass"}); code != 200 {
		t.Fatalf("login: %d", code)
	}

	dial := func(user, pass string) *testminer.Client {
		c, err := testminer.Dial(en.E.StratumAddr())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(c.Close)
		if err := c.Subscribe(ctx); err != nil {
			t.Fatal(err)
		}
		if r, err := c.Authorize(ctx, user, pass); err != nil || !r.OK() {
			t.Fatalf("authorize %s", user)
		}
		if _, err := c.WaitJob(ctx); err != nil {
			t.Fatal(err)
		}
		return c
	}
	diffIs := func(c *testminer.Client, want float64, what string) {
		waitFor(t, what, 5*time.Second, func() bool {
			_, _, _, d, _ := c.State()
			return math.Abs(d/want-1) < 1e-9
		})
	}
	a := dial("live1", "x")
	b := dial("live2", "d=2e-10") // miner pins its own difficulty
	// Values below regtest network difficulty (4.66e-10) so the cap does not hide them.
	settings := map[string]any{"vardiff_min": 1e-11, "vardiff_max": 1000, "vardiff_target_seconds": 10, "fixed_diff": 1e-10, "worker_overrides": map[string]float64{}}
	if code := call("PUT", "/api/admin/settings", settings); code != 200 {
		t.Fatalf("PUT fixed: %d", code)
	}
	diffIs(a, 1e-10, "FIXED_DIFF pushed to connected miner")
	diffIs(b, 2e-10, "password d= kept over FIXED_DIFF")
	jobBefore := a.DrainJobs()
	settings["worker_overrides"] = map[string]float64{"live1": 3e-10}
	if code := call("PUT", "/api/admin/settings", settings); code != 200 {
		t.Fatalf("PUT override: %d", code)
	}
	diffIs(a, 3e-10, "per-worker override pushed")
	if j := a.DrainJobs(); j == nil || j.ID == jobBefore.ID {
		t.Fatal("difficulty change was not followed by a fresh job")
	}
	// Overrides are clamped to min/max.
	settings["vardiff_max"] = 2.5e-10
	settings["fixed_diff"] = 2e-10
	if code := call("PUT", "/api/admin/settings", settings); code != 200 {
		t.Fatalf("PUT clamp: %d", code)
	}
	diffIs(a, 2.5e-10, "override clamped to VARDIFF_MAX")
	// Invalid settings are refused and nothing changes.
	if code := call("PUT", "/api/admin/settings", map[string]any{"vardiff_min": 10, "vardiff_max": 1, "vardiff_target_seconds": 10}); code != 400 {
		t.Fatalf("invalid PUT: %d", code)
	}
	diffIs(a, 2.5e-10, "unchanged after invalid PUT")
	saved, err := os.ReadFile(filepath.Join(dataDir, "settings-bch.json"))
	if err != nil || !strings.Contains(string(saved), `"live1": 3e-10`) {
		t.Fatalf("settings not saved: %v %s", err, saved)
	}
	if code := call("POST", "/api/admin/settings/reset", nil); code != 200 {
		t.Fatalf("reset: %d", code)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "settings-bch.json")); !os.IsNotExist(err) {
		t.Fatal("reset did not delete the saved settings")
	}
	t.Log("live difficulty: fixed, password pin, per-worker override, clamping, validation, persistence and reset verified")
}

// testCreaturesAndMana mines shares that do not solve blocks (possible on
// regtest only with a share difficulty below network difficulty) and checks
// the UI state: creature rarity from the job's best share as a % of network
// difficulty, and the MANA luck percentile filling since the last block.
func testCreaturesAndMana(t *testing.T, en *Engine) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	uiBase := "http://" + en.E.UIAddr()
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	send := func(method, path string, body any) int {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, uiBase+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-WB-Admin", "1")
		res, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if send("POST", "/api/admin/login", map[string]string{"password": "it-admin-pass"}) != 200 {
		t.Fatal("login")
	}
	if send("PUT", "/api/admin/settings", map[string]any{"vardiff_min": 1e-12, "vardiff_max": 1000,
		"vardiff_target_seconds": 10, "fixed_diff": 1e-11, "worker_overrides": map[string]float64{}}) != 200 {
		t.Fatal("PUT fixed_diff")
	}
	defer send("POST", "/api/admin/settings/reset", nil)

	c, err := testminer.Dial(en.E.StratumAddr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Configure(ctx, 0x1fffe000); err != nil {
		t.Fatal(err)
	}
	if err := c.Subscribe(ctx); err != nil {
		t.Fatal(err)
	}
	if r, err := c.Authorize(ctx, "beastmaster", "x"); err != nil || !r.OK() {
		t.Fatal("authorize")
	}
	waitFor(t, "fixed difficulty", 5*time.Second, func() bool { _, _, _, d, _ := c.State(); return d == 1e-11 })
	var accepted, rejected atomic.Int64
	mctx, mcancel := context.WithTimeout(ctx, 4*time.Second)
	_ = c.Mine(mctx, testminer.MineOptions{Worker: "beastmaster", Threads: 1, RollVersion: true, NonBlockShares: true,
		ShareInterval: 50 * time.Millisecond, OnResult: func(_ *testminer.Job, r *testminer.Response, _ string, _ uint32) {
			if r.OK() {
				accepted.Add(1)
			} else {
				rejected.Add(1)
			}
		}})
	mcancel()
	if accepted.Load() < 10 || rejected.Load() != 0 {
		t.Fatalf("non-block shares: accepted %d rejected %d", accepted.Load(), rejected.Load())
	}
	res, err := http.Get(uiBase + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Derived struct {
			Luck *float64 `json:"luck_since_last_block_pct"`
		} `json:"derived"`
		Pool struct {
			Luck struct {
				SumDiff  float64 `json:"sum_difficulty"`
				BestDiff float64 `json:"best_difficulty"`
				Shares   int     `json:"shares"`
			} `json:"luck_since_last_block"`
		} `json:"pool"`
		Rounds []struct {
			Current  bool    `json:"current"`
			Shares   int     `json:"shares"`
			Pct      float64 `json:"pct_of_network"`
			Tier     int     `json:"tier"`
			Rarity   string  `json:"rarity"`
			Creature string  `json:"creature"`
		} `json:"rounds"`
	}
	json.NewDecoder(res.Body).Decode(&st)
	res.Body.Close()
	var cur *struct {
		Current  bool    `json:"current"`
		Shares   int     `json:"shares"`
		Pct      float64 `json:"pct_of_network"`
		Tier     int     `json:"tier"`
		Rarity   string  `json:"rarity"`
		Creature string  `json:"creature"`
	}
	for i := range st.Rounds {
		if st.Rounds[i].Current {
			cur = &st.Rounds[i]
		}
	}
	if cur == nil || cur.Shares < 1 {
		t.Fatalf("no current job with shares: %+v", st.Rounds)
	}
	// Non-block shares have hashes above the network target: 0 < pct < 100.
	want := "Common"
	switch {
	case cur.Pct >= 90:
		want = "Legendary"
	case cur.Pct >= 76.7:
		want = "Epic"
	case cur.Pct >= 63.3:
		want = "Rare"
	case cur.Pct >= 50:
		want = "Uncommon"
	}
	if cur.Pct <= 0 || cur.Pct >= 100 || cur.Rarity != want || cur.Tier > 4 {
		t.Fatalf("creature %s/%s at %.3f%% of network (want %s)", cur.Rarity, cur.Creature, cur.Pct, want)
	}
	l := st.Pool.Luck
	if st.Derived.Luck == nil || l.Shares < int(accepted.Load()) || l.BestDiff <= 0 {
		t.Fatalf("mana luck not filled: %+v %+v", st.Derived.Luck, l)
	}
	if exp := math.Exp(-l.SumDiff/l.BestDiff) * 100; math.Abs(exp-*st.Derived.Luck) > 1e-6 {
		t.Fatalf("luck %v, want exp(-S/D) = %v", *st.Derived.Luck, exp)
	}
	t.Logf("%d non-block shares: job creature %s (%s) at %.2f%% of network difficulty; MANA luck %.2f%%",
		accepted.Load(), cur.Creature, cur.Rarity, cur.Pct, *st.Derived.Luck)
}
