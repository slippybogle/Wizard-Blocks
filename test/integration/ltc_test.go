//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fladnagmai/wizard-blocks/internal/bitcoin"
	"github.com/fladnagmai/wizard-blocks/internal/node"
	"github.com/fladnagmai/wizard-blocks/internal/pow"
	"github.com/fladnagmai/wizard-blocks/internal/testminer"
	"github.com/fladnagmai/wizard-blocks/internal/work"
)

// firstMWEBHeight is the first regtest height that carries an MWEB block
// (litecoin test_framework/ltc_util.py FIRST_MWEB_HEIGHT).
const firstMWEBHeight = 432

// ltcMiner runs the Scrypt CPU miner against an engine.
func ltcMiner(t *testing.T, en *Engine, rec *recorder, user string) *miner {
	t.Helper()
	ctx := context.Background()
	c, err := testminer.Dial(en.E.StratumAddr())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Configure(ctx, 0xffffffff); err != nil {
		t.Fatal(err)
	}
	if err := c.Subscribe(ctx); err != nil {
		t.Fatal(err)
	}
	if r, err := c.Authorize(ctx, user, "x"); err != nil || !r.OK() {
		t.Fatalf("authorize %s: %v", user, err)
	}
	m := &miner{c: c, done: make(chan struct{}), rejects: map[int]int{}}
	mctx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	go func() {
		defer close(m.done)
		_ = c.Mine(mctx, testminer.MineOptions{
			Worker: user, Threads: 2, Scrypt: true, RollVersion: true, VersionMode: "bip310", OneBlockPerPrevHash: true,
			OnSubmit: func(j *testminer.Job, hash string, version uint32) {
				rec.add(hash, submission{worker: user, mode: "bip310", version: version})
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

// mineLTC mines n more accepted blocks with a fresh miner.
func mineLTC(t *testing.T, en *Engine, a *Node, rec *recorder, user string, n int) {
	t.Helper()
	before := en.acceptedBlocks()
	conns := en.E.Stats().Snapshot().Pool.Connections
	m := ltcMiner(t, en, rec, user)
	mineUntil(t, en, before+n, 3*time.Minute)
	m.stop(t)
	// The stopped miner's last block may still be in flight; the next miner
	// must not start on the work that block replaces. The engine reads a
	// connection's lines in order, so once it has closed the session every
	// share sent on it has been handled.
	waitFor(t, "miner session closed", 10*time.Second, func() bool { return en.E.Stats().Snapshot().Pool.Connections <= conns })
	settleLTC(t, en)
	waitEngineTip(t, en, a)
}

type ltcBlock struct {
	Height int64 `json:"height"`
	MWEB   *struct {
		Inputs  []string `json:"inputs"`
		Outputs []string `json:"outputs"`
		Kernels []string `json:"kernels"`
	} `json:"mweb"`
}

// TestRegtestLTC mines Litecoin through the engine: blocks with ordinary
// transactions before MWEB activates, the first MWEB block (with a peg-in),
// blocks with MWEB-to-MWEB spends and peg-outs, blocks after activation
// with no MWEB transactions, a template refresh where the pre-refresh job
// still builds a valid block, and a refused block whose exact reason and
// full hex are logged. Every engine block is read back from the node and
// checked byte by byte.
func TestRegtestLTC(t *testing.T) {
	track(t)
	suffix := randHex(3)
	netName := "wbit-ltc-" + suffix
	docker(t, "network", "create", netName)
	t.Cleanup(func() { docker(t, "network", "rm", netName) })
	a := startNode(t, "ltc", netName, "wbit-ltc-"+suffix)
	var none any
	a.call(t, a.RPC, "createwallet", &none, "w")
	a.call(t, a.RPC, "createwallet", &none, "m") // will hold only MWEB coins
	mw := node.NewClient(a.RPCURL+"/wallet/m", rpcUser, rpcPass, "", 120*time.Second)
	var subver struct {
		Subversion string `json:"subversion"`
	}
	a.call(t, a.RPC, "getnetworkinfo", &subver)
	t.Logf("node %s", subver.Subversion)

	var hs []string
	a.call(t, a.RPC, "generatetoaddress", &hs, 101, a.newAddress(t, "bech32"))
	payout := a.newAddress(t, "bech32")
	if !strings.HasPrefix(payout, "rltc1") {
		t.Fatalf("unexpected regtest address %s", payout)
	}
	startHeight := a.height(t)

	cfg := engineConfig(a, "ltc")
	cfg.Payout.Mode, cfg.Payout.Address = "fixed", payout
	cfg.DataDir = t.TempDir()
	en := startEngine(t, cfg, "ltc")
	rec := &recorder{m: map[string]submission{}}
	external := 0

	// 1. Before MWEB: blocks with ordinary wallet transactions.
	for i := 0; i < 20; i++ {
		var txid string
		a.call(t, a.Wallet, "sendtoaddress", &txid, a.newAddress(t, "bech32"), "0.01")
	}
	mineLTC(t, en, a, rec, "rig-pre", 10)
	if h := a.height(t); h >= firstMWEBHeight-1 {
		t.Fatalf("height %d already at MWEB activation", h)
	}

	// 2. Up to the block before activation (node-mined), then a peg-in, and
	// the engine mines the first MWEB block. Height 432 cannot be mined
	// without a peg-in, so wait for the engine's last submissions to land
	// before counting, and never overshoot.
	settleLTC(t, en)
	for h := a.height(t); h < firstMWEBHeight-1; h = a.height(t) {
		n := firstMWEBHeight - 1 - h
		if n > 50 {
			n = 50
		}
		a.call(t, a.RPC, "generatetoaddress", &hs, n, a.newAddress(t, "bech32"))
		external += len(hs)
	}
	var mwebAddr string
	a.call(t, mw, "getnewaddress", &mwebAddr, "", "mweb")
	var pegin string
	a.call(t, a.Wallet, "sendtoaddress", &pegin, mwebAddr, "5")
	// The node cannot build a height-432 template until a peg-in is in its
	// mempool; then the engine's work must move onto the node-mined tip.
	waitEngineTip(t, en, a)
	mineLTC(t, en, a, rec, "rig-mweb", 1)
	settleLTC(t, en)
	var firstHash string
	a.call(t, a.RPC, "getblockhash", &firstHash, firstMWEBHeight)
	if _, ours := rec.get(firstHash); !ours {
		t.Fatalf("block %d (the first MWEB block) was not mined by the engine", firstMWEBHeight)
	}

	// 3. MWEB-to-MWEB, peg-outs and more peg-ins, from the MWEB-only wallet.
	mineLTC(t, en, a, rec, "rig-mweb", 1) // the peg-in output becomes spendable
	for i := 0; i < 3; i++ {
		var a1, a2, txid string
		a.call(t, mw, "getnewaddress", &a1, "", "mweb")
		a.call(t, mw, "sendtoaddress", &txid, a1, "0.5") // MWEB -> MWEB
		a2 = a.newAddress(t, "bech32")
		a.call(t, mw, "sendtoaddress", &txid, a2, "0.3") // peg-out
		// Peg-in. The wallet keeps a peg-in's change inside MWEB, so a small
		// amount would be paid from MWEB coins; more than its MWEB balance
		// forces canonical inputs and a real peg-in output.
		a.call(t, a.Wallet, "sendtoaddress", &txid, mwebAddr, "80")
		a.call(t, a.Wallet, "sendtoaddress", &txid, a.newAddress(t, "bech32"), "0.01")
		mineLTC(t, en, a, rec, fmt.Sprintf("rig-mix%d", i), 1)
	}

	// 4. After activation, blocks with no MWEB transactions at all.
	mineLTC(t, en, a, rec, "rig-empty", 5)

	// 5. Template refresh: a job taken before new transactions arrive must
	// still build a valid block after the refresh replaced the template.
	testLTCTemplateRefresh(t, en, a, mwebAddr, rec)

	// 6. A block the node refuses (corrupted MWEB block): exact reason and
	// full hex in the log, hex saved under the data dir.
	rejectedHash := testLTCRejectedBlock(t, en, payout, cfg.DataDir)

	// ---- verify every engine block against the node ----
	waitFor(t, "no pending blocks", 30*time.Second, func() bool {
		for _, b := range en.E.Stats().Blocks() {
			if b.Status == "pending" {
				return false
			}
		}
		return true
	})
	en.Stop(t)
	script := a.scriptOf(t, payout)
	var accepted, withMWEB, withMWEBSpend, withPegout, withPegin, preMWEBWithTxs, postEmpty int
	for _, r := range en.E.Stats().Blocks() {
		if r.Hash == rejectedHash {
			if r.Status != "rejected" {
				t.Errorf("corrupted block status %s", r.Status)
			}
			continue
		}
		if r.Status != "accepted" {
			t.Errorf("block %s at %d: %s (%s)", r.Hash, r.Height, r.Status, r.Reason)
			continue
		}
		accepted++
		if _, ok := rec.get(r.Hash); !ok {
			t.Errorf("block %s is not a header our miner hashed", r.Hash)
		}
		var rawHex string
		a.call(t, a.RPC, "getblock", &rawHex, r.Hash, 0)
		raw, _ := hex.DecodeString(rawHex)
		blk, err := bitcoin.ParseBlockMWEB(raw)
		if err != nil {
			t.Fatalf("block %d: %v", r.Height, err)
		}
		// Proof of work: Scrypt, not SHA-256d; the id stays SHA-256d.
		hb := blk.Header.Serialize()
		tgt, _ := bitcoin.CompactToTarget(blk.Header.Bits)
		if blk.Header.Hash().String() != r.Hash || !bitcoin.HashMeetsTarget(pow.Scrypt.PoWHash(hb[:]), tgt) {
			t.Fatalf("block %d: identity or Scrypt work wrong", r.Height)
		}
		cb := blk.Txs[0]
		if h, err := bitcoin.DecodeBIP34Height(cb.Inputs[0].Script); err != nil || h != r.Height {
			t.Fatalf("block %d: BIP34 height %d %v", r.Height, h, err)
		}
		if hex.EncodeToString(cb.Outputs[0].Script) != script || !bytes.Contains(cb.Inputs[0].Script, []byte("/wizard-blocks-it/")) {
			t.Fatalf("block %d: coinbase payout or tag", r.Height)
		}
		if _, ok := bitcoin.FindWitnessCommitment(cb); !ok {
			t.Fatalf("block %d: no witness commitment", r.Height)
		}
		var info ltcBlock
		a.call(t, a.RPC, "getblock", &info, r.Hash, 1)
		last := blk.Txs[len(blk.Txs)-1]
		if r.Height < firstMWEBHeight {
			if blk.MWEB != nil || last.HogEx || info.MWEB != nil {
				t.Fatalf("block %d: MWEB before activation", r.Height)
			}
			if len(blk.Txs) > 1 {
				preMWEBWithTxs++
			}
			continue
		}
		if blk.MWEB == nil || !last.HogEx || info.MWEB == nil {
			t.Fatalf("block %d: no HogEx/MWEB block after activation", r.Height)
		}
		withMWEB++
		if len(info.MWEB.Inputs) > 0 {
			withMWEBSpend++
		}
		if len(last.Outputs) > 1 { // HogEx outputs after the first are peg-outs
			withPegout++
		}
		// A peg-in is a canonical transaction paying a witness v9 program
		// (OP_9 <32 bytes>, litecoin script/standard.h), swept by the HogEx.
		for _, tx := range blk.Txs[1 : len(blk.Txs)-1] {
			pegs := false
			for _, o := range tx.Outputs {
				if len(o.Script) == 34 && o.Script[0] == 0x59 && o.Script[1] == 0x20 {
					pegs = true
				}
			}
			if pegs {
				withPegin++
				break
			}
		}
		if len(info.MWEB.Kernels) == 0 {
			postEmpty++
		}
		t.Logf("block %d: %d txs, HogEx %d in / %d out, MWEB %d in / %d out / %d kernels",
			r.Height, len(blk.Txs), len(last.Inputs), len(last.Outputs), len(info.MWEB.Inputs), len(info.MWEB.Outputs), len(info.MWEB.Kernels))
	}
	final := a.height(t)
	if want := startHeight + int64(accepted+external); final != want {
		t.Fatalf("chain height %d, want %d + %d ours + %d external", final, startHeight, accepted, external)
	}
	blocksVerified.Add(int64(accepted))
	t.Logf("ltc: VERIFIED %d engine blocks: %d before MWEB with txs, %d with an MWEB block (%d spending MWEB outputs, %d with peg-outs, %d with peg-ins, %d with no MWEB txs)",
		accepted, preMWEBWithTxs, withMWEB, withMWEBSpend, withPegout, withPegin, postEmpty)
	if preMWEBWithTxs == 0 || withMWEB < 10 || withMWEBSpend == 0 || withPegout == 0 || withPegin == 0 || postEmpty == 0 {
		t.Fatal("not every MWEB case was covered")
	}
}

// settleLTC waits until the engine has no block submission in flight.
func settleLTC(t *testing.T, en *Engine) {
	t.Helper()
	waitFor(t, "engine block submissions settled", 30*time.Second, func() bool {
		for _, b := range en.E.Stats().Blocks() {
			if b.Status == "pending" {
				return false
			}
		}
		return true
	})
}

// waitEngineTip waits until the engine's current work builds on the node's
// best block (after blocks mined outside the engine).
func waitEngineTip(t *testing.T, en *Engine, a *Node) {
	t.Helper()
	waitFor(t, "engine work on the node tip", 20*time.Second, func() bool {
		var best string
		a.call(t, a.RPC, "getbestblockhash", &best)
		return en.E.Manager().Current().Tmpl.PrevHash.String() == best
	})
}

// grindScrypt finds a nonce whose Scrypt hash meets the job's network target.
func grindScrypt(t *testing.T, j *testminer.Job, en1, en2 []byte, version uint32) (uint32, [32]byte) {
	t.Helper()
	target := testminer.CompactTarget(j.Bits)
	for nonce := uint32(0); nonce < 1<<20; nonce++ {
		h := testminer.Header(j, en1, en2, version, j.NTime, nonce)
		pw := pow.Scrypt.PoWHash(h[:])
		if bitcoin.HashToBig(pw).Cmp(target) <= 0 {
			return nonce, testminer.HashBE(h)
		}
	}
	t.Fatal("grind failed")
	return 0, [32]byte{}
}

func testLTCTemplateRefresh(t *testing.T, en *Engine, a *Node, mwebAddr string, rec *recorder) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c, err := testminer.Dial(en.E.StratumAddr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Subscribe(ctx); err != nil {
		t.Fatal(err)
	}
	if r, err := c.Authorize(ctx, "rig-refresh", "x"); err != nil || !r.OK() {
		t.Fatal("authorize")
	}
	// Start from settled work on the node's current tip.
	settleLTC(t, en)
	var best0 string
	var old *testminer.Job
	waitFor(t, "job on the current tip", 20*time.Second, func() bool {
		a.call(t, a.RPC, "getbestblockhash", &best0)
		cur := en.E.Manager().Current()
		_, _, _, _, j := c.State()
		if j == nil || cur.Tmpl.PrevHash.String() != best0 || !bytes.Equal(j.PrevHash, cur.Tmpl.PrevHash[:]) {
			return false
		}
		old = j
		return true
	})
	oldTxs := en.E.Manager().Current().Tmpl.TxCount()
	var txid string
	a.call(t, a.Wallet, "sendtoaddress", &txid, mwebAddr, "0.1") // peg-in: changes the HogEx and MWEB block
	a.call(t, a.Wallet, "sendtoaddress", &txid, a.newAddress(t, "bech32"), "0.01")
	// Periodic refreshes re-send work even when nothing changed; wait for
	// the refresh that brings the new transactions (a longer merkle branch).
	waitFor(t, "template refresh with the new transactions", 20*time.Second, func() bool {
		return en.E.Manager().Current().Tmpl.TxCount() > oldTxs
	})
	var nj *testminer.Job
	waitFor(t, "job for the refreshed template", 10*time.Second, func() bool {
		_, _, _, _, j := c.State()
		if j != nil && len(j.Branch) != len(old.Branch) {
			nj = j
			return true
		}
		return false
	})
	cur := en.E.Manager().Current()
	if nj.Clean || !bytes.Equal(nj.PrevHash, old.PrevHash) || cur.Clean {
		t.Fatalf("refresh: clean=%v same prev=%v", nj.Clean, bytes.Equal(nj.PrevHash, old.PrevHash))
	}
	t.Logf("template refreshed without a new block: %d -> %d txs, clean_jobs=false", oldTxs, cur.Tmpl.TxCount())

	// Solve the old job: the block must use the old template's own
	// transactions and MWEB block, and be accepted.
	en1, en2Size, _, _, _ := c.State()
	en2 := make([]byte, en2Size)
	en2[0] = 0x77
	nonce, id := grindScrypt(t, old, en1, en2, old.Version)
	rec.add(hex.EncodeToString(id[:]), submission{worker: "rig-refresh", mode: "bip310", version: old.Version})
	h0 := a.height(t)
	r, err := c.Submit(ctx, "rig-refresh", old.ID, hex.EncodeToString(en2), fmt.Sprintf("%08x", old.NTime), fmt.Sprintf("%08x", nonce), "")
	if err != nil || !r.OK() {
		t.Fatalf("old-job share: %v %s", err, r.Error)
	}
	waitFor(t, "old-job block on chain", 30*time.Second, func() bool { return a.height(t) == h0+1 })
	var best string
	a.call(t, a.RPC, "getbestblockhash", &best)
	if best != hex.EncodeToString(id[:]) {
		t.Fatalf("best block %s, want the old-job block %x", best, id)
	}
	var blk struct {
		Tx []string `json:"tx"`
	}
	a.call(t, a.RPC, "getblock", &blk, best, 1)
	if len(blk.Tx) != oldTxs {
		t.Fatalf("old-job block has %d txs, its template had %d", len(blk.Tx), oldTxs)
	}
	// The new transactions are mined next, on a fresh job.
	mineLTC(t, en, a, rec, "rig-refresh2", 1)
	var mp struct {
		Size int `json:"size"`
	}
	a.call(t, a.RPC, "getmempoolinfo", &mp)
	if mp.Size != 0 {
		t.Fatalf("mempool still has %d txs", mp.Size)
	}
}

func testLTCRejectedBlock(t *testing.T, en *Engine, payout, dataDir string) string {
	w := en.E.Manager().Current()
	pay, err := en.E.ValidatePayout(context.Background(), payout)
	if err != nil {
		t.Fatal(err)
	}
	job, err := w.Job(pay.Script, payout)
	if err != nil {
		t.Fatal(err)
	}
	if len(job.Tmpl.MWEB) < 64 {
		t.Fatalf("template MWEB only %d bytes", len(job.Tmpl.MWEB))
	}
	// Same header (it commits to the HogEx, not to the MWEB bytes directly),
	// corrupted MWEB block body.
	bad := *job
	tm := *job.Tmpl
	tm.MWEB = append([]byte{}, job.Tmpl.MWEB...)
	tm.MWEB[len(tm.MWEB)/2] ^= 0xff
	bad.Tmpl = &tm
	en1, en2 := []byte{9, 9, 9, 9}, make([]byte, 8)
	var hdr bitcoin.Header
	var found atomic.Bool
	target := job.Tmpl.Target
	for nonce := uint32(0); nonce < 1<<20 && !found.Load(); nonce++ {
		hdr = bad.Header(en1, en2, job.Tmpl.CurTime, nonce, job.Tmpl.Version)
		b := hdr.Serialize()
		if bitcoin.HashToBig(pow.Scrypt.PoWHash(b[:])).Cmp(new(big.Int).Set(target)) <= 0 {
			found.Store(true)
		}
	}
	if !found.Load() {
		t.Fatal("grind failed")
	}
	hash := hdr.Hash().String()
	en.E.Manager().SubmitBlock(work.Candidate{Job: &bad, Header: hdr, En1: en1, En2: en2, Worker: "rig-bad", ShareDiff: 1})
	var rec string
	waitFor(t, "corrupted block resolved", 30*time.Second, func() bool {
		for _, b := range en.E.Stats().Blocks() {
			if b.Hash == hash && b.Status != "pending" {
				rec = b.Status + " " + b.Reason
				return true
			}
		}
		return false
	})
	if !strings.HasPrefix(rec, "rejected ") {
		t.Fatalf("corrupted block: %s", rec)
	}
	want := hex.EncodeToString(bad.Block(&hdr, en1, en2))
	logb, _ := os.ReadFile(en.log)
	if !strings.Contains(string(logb), "hex="+want) || !strings.Contains(string(logb), "rejected block hex") {
		t.Fatal("full block hex not in the engine log")
	}
	saved, err := os.ReadFile(filepath.Join(dataDir, "rejected-blocks", fmt.Sprintf("%d-%s.hex", job.Tmpl.Height, hash)))
	if err != nil || strings.TrimSpace(string(saved)) != want {
		t.Fatalf("saved rejected block: %v", err)
	}
	t.Logf("corrupted MWEB block refused by the node: %q; full hex logged and saved", strings.TrimPrefix(rec, "rejected "))
	return hash
}
