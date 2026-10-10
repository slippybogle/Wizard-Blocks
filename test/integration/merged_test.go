//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/fladnagmai/wizard-blocks/internal/auxpow"
	"github.com/fladnagmai/wizard-blocks/internal/bitcoin"
	"github.com/fladnagmai/wizard-blocks/internal/pow"
	"github.com/fladnagmai/wizard-blocks/internal/stats"
	"github.com/fladnagmai/wizard-blocks/internal/testminer"
)

// dogeAuxpowHeight is the first Dogecoin regtest height that must carry an
// AuxPoW (dogecoin chainparams.cpp: auxpowConsensus.nHeightEffective = 20).
const dogeAuxpowHeight = 20

func auxBlocks(en *Engine, status string) []stats.BlockRecord {
	var out []stats.BlockRecord
	for _, b := range en.E.Stats().AuxBlocks() {
		if status == "" || b.Status == status {
			out = append(out, b)
		}
	}
	return out
}

// TestRegtestMergedLTCDOGE mines Litecoin with Dogecoin merged on top: a
// share that is a block on both chains is accepted by both nodes; a new
// Dogecoin block sends miners fresh (clean) jobs; with the Dogecoin node
// down, Litecoin carries on alone and merged mining resumes when it is
// back; without a DOGE address the engine mines Litecoin only. Every
// Dogecoin block is read back, checked with Dogecoin's AuxPoW rules and
// byte for byte, and tied to the Litecoin block whose header proves it.
func TestRegtestMergedLTCDOGE(t *testing.T) {
	track(t)
	suffix := randHex(3)
	netName := "wbit-mm-" + suffix
	docker(t, "network", "create", netName)
	t.Cleanup(func() { docker(t, "network", "rm", netName) })
	l := startNode(t, "ltc", netName, "wbit-mm-ltc-"+suffix)
	d := startNode(t, "doge", netName, "wbit-mm-doge-"+suffix)
	var none any
	l.call(t, l.RPC, "createwallet", &none, "w")
	var hs []string
	l.call(t, l.RPC, "generatetoaddress", &hs, 101, l.newAddress(t, "bech32"))
	var dogeMine, dogePayout string
	d.call(t, d.RPC, "getnewaddress", &dogeMine)
	d.call(t, d.RPC, "getnewaddress", &dogePayout)
	d.call(t, d.RPC, "generatetoaddress", &hs, dogeAuxpowHeight+5, dogeMine)
	var dni struct {
		Subversion string `json:"subversion"`
	}
	d.call(t, d.RPC, "getnetworkinfo", &dni)
	t.Logf("nodes: litecoin + %s; DOGE payout %s", dni.Subversion, dogePayout)
	dogeScript := d.scriptOf(t, dogePayout)

	ltcPayout := l.newAddress(t, "bech32")
	cfg := engineConfig(l, "ltc")
	cfg.Payout.Mode, cfg.Payout.Address = "fixed", ltcPayout
	cfg.Doge.RPCURL, cfg.Doge.RPCUser, cfg.Doge.RPCPassword = d.RPCURL, rpcUser, rpcPass
	cfg.Doge.ZMQHashBlock = d.ZMQ // no DOGE address yet: it is set from Settings below
	cfg.Doge.PollIntervalMs, cfg.Doge.RefreshS = 500, 2
	cfg.UI.SettingsOpen = true
	cfg.DataDir = t.TempDir()
	en := startEngine(t, cfg, "merged")
	rec := &recorder{m: map[string]submission{}}
	auxOn := func() bool { w := en.E.Manager().Current(); return w != nil && w.Aux != nil }
	dogeView := func() map[string]any {
		r, err := http.Get("http://" + en.E.UIAddr() + "/api/state")
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		ch, _ := m["chains"].(map[string]any)
		dv, _ := ch["doge"].(map[string]any)
		return dv
	}
	reasonIs := func(want string) func() bool {
		return func() bool { v := dogeView(); return v != nil && v["merged"] == false && v["reason"] == want }
	}

	// 0. No DOGE address: Litecoin only, the page says why, no tag.
	waitFor(t, "banner reason: no address", 30*time.Second, reasonIs("no address"))
	if auxOn() {
		t.Fatal("aux work without a DOGE address")
	}
	noAddrFrom := len(en.E.Stats().Blocks())
	mineLTC(t, en, l, rec, "rig-ltc-only", 2)
	noAddrBlocks := en.E.Stats().Blocks()[noAddrFrom:]
	// Set the DOGE address from Settings: checked by the Dogecoin node,
	// refused if it is not a DOGE address, applied without a restart.
	putDoge := func(addr string) (int, string) {
		body, _ := json.Marshal(map[string]string{"address": addr})
		req, _ := http.NewRequest(http.MethodPut, "http://"+en.E.UIAddr()+"/api/admin/doge-payout", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://"+en.E.UIAddr())
		req.Header.Set("X-WB-Admin", "1")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	if code, body := putDoge(ltcPayout); code != http.StatusBadRequest {
		t.Fatalf("an LTC address accepted as DOGE payout: %d %s", code, body)
	}
	if code, body := putDoge(dogePayout); code != http.StatusOK {
		t.Fatalf("set DOGE payout: %d %s", code, body)
	}
	waitFor(t, "Dogecoin aux work", 30*time.Second, auxOn)
	waitFor(t, "banner gone", 30*time.Second, func() bool { v := dogeView(); return v != nil && v["merged"] == true })
	t.Log("DOGE payout set from Settings: merged mining on, banner gone")

	// 1. Merged mining: LTC regtest and DOGE regtest share the same easy
	// target, so each solving share is a block on both chains.
	mineLTC(t, en, l, rec, "rig-mm", 10)
	waitFor(t, "DOGE blocks resolved", 60*time.Second, func() bool {
		return len(auxBlocks(en, "pending")) == 0 && len(auxBlocks(en, "accepted")) >= 5
	})

	// 2. A new Dogecoin block from elsewhere: fresh clean jobs on the same
	// Litecoin tip, and merged mining continues on the new aux block.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c, err := testminer.Dial(en.E.StratumAddr())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Subscribe(ctx); err != nil {
		t.Fatal(err)
	}
	if r, err := c.Authorize(ctx, "rig-watch", "x"); err != nil || !r.OK() {
		t.Fatal("authorize")
	}
	j0, err := c.WaitJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	auxBefore := en.E.Manager().Current().Aux.Blocks[0].Hash
	d.call(t, d.RPC, "generatetoaddress", &hs, 1, dogeMine)
	j1, err := c.WaitJobOtherThan(ctx, j0.ID)
	for err == nil && en.E.Manager().Current().Aux.Blocks[0].Hash == auxBefore {
		j1, err = c.WaitJobOtherThan(ctx, j1.ID)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !j1.Clean || !bytes.Equal(j1.PrevHash, j0.PrevHash) {
		t.Fatalf("after a new DOGE block: clean=%v, same LTC tip=%v", j1.Clean, bytes.Equal(j1.PrevHash, j0.PrevHash))
	}
	t.Log("new Dogecoin block: clean job on the same Litecoin tip")
	c.Close()
	mineLTC(t, en, l, rec, "rig-mm2", 3)

	// 3. Dogecoin node down: Litecoin continues alone, untagged; merged
	// mining resumes when the node is back.
	docker(t, "stop", "-t", "5", d.Name)
	waitFor(t, "aux work dropped", 30*time.Second, func() bool { return !auxOn() })
	waitFor(t, "banner reason: node down", 30*time.Second, reasonIs("node down"))
	downFrom := len(en.E.Stats().Blocks())
	mineLTC(t, en, l, rec, "rig-solo", 3)
	soloBlocks := en.E.Stats().Blocks()[downFrom:]
	// An engine started while the Dogecoin node is down still starts, and
	// mines Litecoin alone.
	cfgDown := cfg
	cfgDown.Doge.PayoutAddress = dogePayout
	cfgDown.UI.SettingsOpen = false
	cfgDown.DataDir = t.TempDir()
	cfgDown.Stratum.Listen, cfgDown.API.Listen, cfgDown.UI.Listen = "127.0.0.1:0", "127.0.0.1:0", "127.0.0.1:0"
	enDown := startEngine(t, cfgDown, "merged-doge-down")
	if w := enDown.E.Manager().Current(); w.Aux != nil {
		t.Fatal("aux work while the Dogecoin node is down")
	}
	enDown.Stop(t)
	docker(t, "start", d.Name)
	waitFor(t, "Dogecoin RPC back", 60*time.Second, func() bool { _, err := d.RPC.GetBlockchainInfo(ctx); return err == nil })
	waitFor(t, "aux work back", 60*time.Second, auxOn)
	waitFor(t, "banner gone again", 30*time.Second, func() bool { v := dogeView(); return v != nil && v["merged"] == true })
	dogeBefore := len(auxBlocks(en, "accepted"))
	mineLTC(t, en, l, rec, "rig-mm3", 3)
	waitFor(t, "DOGE blocks after restart", 60*time.Second, func() bool {
		return len(auxBlocks(en, "pending")) == 0 && len(auxBlocks(en, "accepted")) > dogeBefore
	})
	en.Stop(t)

	// ---- verify ----
	ltcAccepted := map[string]bool{}
	for _, b := range en.E.Stats().Blocks() {
		if b.Status != "accepted" {
			t.Errorf("LTC block %s at %d: %s (%s)", b.Hash, b.Height, b.Status, b.Reason)
			continue
		}
		var best string
		l.call(t, l.RPC, "getblockhash", &best, b.Height)
		if best != b.Hash {
			t.Fatalf("LTC block %d not on the active chain", b.Height)
		}
		ltcAccepted[b.Hash] = true
	}
	for _, b := range append(append([]stats.BlockRecord{}, soloBlocks...), noAddrBlocks...) {
		var raw string
		l.call(t, l.RPC, "getblock", &raw, b.Hash, 0)
		rb, _ := hex.DecodeString(raw)
		blk, err := bitcoin.ParseBlockMWEB(rb)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(blk.Txs[0].Inputs[0].Script, auxpow.MergedMiningHeader) {
			t.Fatalf("LTC block %d mined while Dogecoin was down carries a merged-mining tag", b.Height)
		}
	}
	both := 0
	for _, r := range en.E.Stats().AuxBlocks() {
		if r.Status != "accepted" {
			t.Errorf("DOGE block %s at %d: %s (%s)", r.Hash, r.Height, r.Status, r.Reason)
			continue
		}
		var raw string
		d.call(t, d.RPC, "getblock", &raw, r.Hash, false)
		rb, _ := hex.DecodeString(raw)
		blk, err := auxpow.ParseBlock(rb)
		if err != nil {
			t.Fatalf("DOGE block %d: %v", r.Height, err)
		}
		if !bytes.Equal(blk.Serialize(), rb) {
			t.Fatalf("DOGE block %d does not re-serialize byte for byte", r.Height)
		}
		if blk.Header.Hash().String() != r.Hash || blk.AuxPow == nil {
			t.Fatalf("DOGE block %d: hash or AuxPoW missing", r.Height)
		}
		if err := blk.AuxPow.Check(blk.Header.Hash(), 0x62, true); err != nil {
			t.Fatalf("DOGE block %d: AuxPoW check: %v", r.Height, err)
		}
		tgt, _ := bitcoin.CompactToTarget(blk.Header.Bits)
		if !bitcoin.HashMeetsTarget(pow.Scrypt.PoWHash(blk.AuxPow.Parent[:]), tgt) {
			t.Fatalf("DOGE block %d: parent Scrypt work misses the DOGE target", r.Height)
		}
		if hex.EncodeToString(blk.Txs[0].Outputs[0].Script) != dogeScript {
			t.Fatalf("DOGE block %d does not pay the DOGE payout address", r.Height)
		}
		ph, _ := blk.AuxPow.ParentHeader()
		if ph.Hash().String() != r.ParentHash {
			t.Fatalf("DOGE block %d: parent %s, recorded %s", r.Height, ph.Hash(), r.ParentHash)
		}
		if ltcAccepted[r.ParentHash] {
			// One share, two blocks: the parent is an accepted LTC block
			// and the AuxPoW's coinbase is that block's coinbase.
			var raw string
			l.call(t, l.RPC, "getblock", &raw, r.ParentHash, 0)
			lb, _ := hex.DecodeString(raw)
			lblk, err := bitcoin.ParseBlockMWEB(lb)
			if err != nil {
				t.Fatal(err)
			}
			if lblk.Txs[0].TxID != blk.AuxPow.Coinbase.TxID {
				t.Fatalf("DOGE block %d: AuxPoW coinbase is not LTC block %s's coinbase", r.Height, r.ParentHash)
			}
			both++
		}
	}
	nd := len(auxBlocks(en, "accepted"))
	// The page's per-chain counts match the two chains.
	st := dogeView()
	if int(st["blocks_found"].(float64)) != nd {
		t.Fatalf("page DOGE blocks_found %v, nodes have %d", st["blocks_found"], nd)
	}
	blocksVerified.Add(int64(len(ltcAccepted) + nd))
	t.Logf("merged: VERIFIED %d LTC blocks and %d DOGE blocks; %d shares were blocks on both chains; %d LTC blocks mined while Dogecoin was down",
		len(ltcAccepted), nd, both, len(soloBlocks))
	if both < 5 || len(soloBlocks) < 3 {
		t.Fatal("not every case was covered")
	}

	// 4. No DOGE address: Litecoin only.
	cfg2 := cfg
	cfg2.Doge.PayoutAddress = ""
	cfg2.UI.SettingsOpen = false
	cfg2.DataDir = t.TempDir()
	en2 := startEngine(t, cfg2, "ltc-only")
	if w := en2.E.Manager().Current(); w.Aux != nil {
		t.Fatal("aux work without a DOGE payout address")
	}
	en2.Stop(t)
}
