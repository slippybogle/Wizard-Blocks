//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
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
	"github.com/slippybogle/wizard-blocks/internal/testminer"
)

const minZeroBits = 16 // every submitted share/block hash must start with 16 zero bits

func TestRegtestBTC(t *testing.T) { runCoin(t, "btc") }
func TestRegtestBCH(t *testing.T) { runCoin(t, "bch") }

// miner wraps a running test miner.
type miner struct {
	c        *testminer.Client
	cancel   context.CancelFunc
	done     chan struct{}
	accepted atomic.Int64
	mu       sync.Mutex
	rejects  map[int]int
}

func startMiner(t *testing.T, en *Engine, user string, xor bool) *miner {
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
			Worker: user, Threads: 2, MinZeroBits: minZeroBits, RollVersion: true, XORVersion: xor,
			OneBlockPerPrevHash: true,
			OnResult: func(j *testminer.Job, r *testminer.Response, hash string) {
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

func runCoin(t *testing.T, coin string) {
	suffix := randHex(3)
	netName := "wbit-" + coin + "-" + suffix
	docker(t, "network", "create", netName)
	t.Cleanup(func() { docker(t, "network", "rm", netName) })

	a := startNode(t, coin, netName, "wbit-"+coin+"-a-"+suffix)
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
	enA := startEngine(t, cfgA, coin+"-fixed")

	m1 := startMiner(t, enA, "rig1", false)
	mineUntil(t, enA, 60, 5*time.Minute)

	// Disconnect, verify the server noticed, reconnect with a firmware-style
	// (XOR) version-bits encoding.
	m1.stop(t)
	waitFor(t, "connection count 0", 10*time.Second, func() bool { return enA.E.Stats().Snapshot().Pool.Connections == 0 })
	m2 := startMiner(t, enA, "rig1", true)
	mineUntil(t, enA, 110, 5*time.Minute)
	m2.stop(t)

	// Fill the mempool with many wallet transactions (coinbases are mature now).
	dests := make([]string, 8)
	for i := range dests {
		dests[i] = a.newAddress(t, "")
	}
	const nTx = 300
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
	before := enA.acceptedBlocks()
	m3 := startMiner(t, enA, "rig2", false)
	waitFor(t, "mempool drained into our blocks", 3*time.Minute, func() bool {
		a.call(t, a.RPC, "getmempoolinfo", &mp)
		return mp.Size == 0 && enA.acceptedBlocks() > before
	})
	mineUntil(t, enA, 150, 5*time.Minute)
	m3.stop(t)

	t.Run("ProtocolAndInvalidShares", func(t *testing.T) { testProtocol(t, enA, a, &external) })

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
	enB := startEngine(t, cfgB, coin+"-miner")
	t.Run("MinerModeAuthorize", func(t *testing.T) { testMinerModeAuth(t, enB, a, coin, &external) })

	for i, addr := range payoutVariants(t, a, coin) {
		before := enB.acceptedBlocks()
		m := startMiner(t, enB, fmt.Sprintf("%s.worker%d", addr, i), i%2 == 1)
		mineUntil(t, enB, before+12, 5*time.Minute)
		m.stop(t)
	}
	checkAPI(t, enB)
	enB.Stop(t)

	verifyChain(t, a, coin, startHeight, external, append(enA.E.Stats().Blocks(), enB.E.Stats().Blocks()...))
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

func testProtocol(t *testing.T, en *Engine, a *Node, external *int) {
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
	waitFor(t, "block from protocol test on chain", 30*time.Second, func() bool { return a.height(t) == h0+1 })
	var best string
	a.call(t, a.RPC, "getbestblockhash", &best)
	if best != hex.EncodeToString(be[:]) {
		t.Fatalf("best block %s, expected our share %x", best, be)
	}

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
	nonce, _, ok = testminer.Grind(oldJob, en1, en2, oldJob.Version, oldJob.NTime, func(be [32]byte) bool {
		return testminer.LeadingZeroBits(be) >= minZeroBits
	})
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

	// Silent unauthenticated clients are dropped by the auth timeout (60 s is
	// too long for the test; the slow-client path is covered by line limits).
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
	legacy := address.Base58CheckEncode(append([]byte{0x6f}, h...))
	token, _ := address.CashAddrEncode("bchreg", 2, h)
	p2sh20, _ := address.CashAddrEncode("bchreg", 1, bytes.Repeat([]byte{0x33}, 20))
	p2sh32, _ := address.CashAddrEncode("bchreg", 1, bytes.Repeat([]byte{0x44}, 32))
	return []string{cash, legacy, token, p2sh20, p2sh32}
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
	if err := json.Unmarshal(apiGet(t, en, "/stats"), &snap); err != nil {
		t.Fatal(err)
	}
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
func verifyChain(t *testing.T, a *Node, coin string, startHeight int64, external int, recs []stats.BlockRecord) {
	ctx := context.Background()
	var accepted []stats.BlockRecord
	stale := 0
	for _, r := range recs {
		switch r.Status {
		case "accepted":
			accepted = append(accepted, r)
		case "stale":
			stale++
		default:
			t.Errorf("block %s at %d has status %s (%s)", r.Hash, r.Height, r.Status, r.Reason)
		}
	}
	if stale != 1 {
		t.Errorf("expected exactly 1 (deliberate) stale block candidate, got %d", stale)
	}
	if len(accepted) < 200 {
		t.Fatalf("only %d accepted blocks, want >= 200", len(accepted))
	}
	final := a.height(t)
	if want := startHeight + int64(len(accepted)) + int64(external); final != want {
		t.Fatalf("chain height %d, want start %d + ours %d + external %d = %d", final, startHeight, len(accepted), external, want)
	}
	byHeight := map[int64]stats.BlockRecord{}
	maxTxs := 0
	var totalFees int64
	scriptCache := map[string]string{}
	for _, r := range accepted {
		if _, dup := byHeight[r.Height]; dup {
			t.Fatalf("two accepted blocks at height %d", r.Height)
		}
		byHeight[r.Height] = r
		if !strings.HasPrefix(r.Hash, "0000") {
			t.Fatalf("block %s lacks the miner's %d leading zero bits: server and miner disagree on the header", r.Hash, minZeroBits)
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
		if !bytes.Contains(cb.Inputs[0].Script, []byte("/wizard-blocks-it/")) {
			t.Fatalf("block %d: coinbase tag missing", r.Height)
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
	types := map[string]bool{}
	for _, r := range accepted {
		types[r.Address] = true
	}
	t.Logf("%s: VERIFIED %d accepted blocks (heights %d..%d), %d external, largest block %d txs, total fees %d sats, %d distinct payout addresses",
		coin, len(accepted), startHeight+1, final, external, maxTxs, totalFees, len(types))
}
