package work

import (
	"bytes"
	"encoding/hex"
	"io"
	"log/slog"
	"math/big"
	"sync"
	"testing"

	"github.com/fladnagmai/wizard-blocks/internal/address"
	"github.com/fladnagmai/wizard-blocks/internal/auxpow"
	"github.com/fladnagmai/wizard-blocks/internal/bitcoin"
	"github.com/fladnagmai/wizard-blocks/internal/stats"
)

type fakeAux struct {
	mu     sync.Mutex
	blocks []AuxBlock
	got    []*auxpow.AuxPow
	ch     chan struct{}
}

func (f *fakeAux) Current() []AuxBlock      { f.mu.Lock(); defer f.mu.Unlock(); return f.blocks }
func (f *fakeAux) Changed() <-chan struct{} { return f.ch }
func (f *fakeAux) Submit(b AuxBlock, ap *auxpow.AuxPow, _ string, _ float64, _ bitcoin.Hash) {
	f.mu.Lock()
	f.got = append(f.got, ap)
	f.mu.Unlock()
}

// A job built on a real Litecoin MWEB template commits to the aux block in
// its coinbase; a share whose PoW hash meets the aux target becomes an
// AuxPoW that passes Dogecoin's checks, with our coinbase (without witness)
// and the share's header as parent. A share above the aux target, or the
// same solution again, submits nothing.
func TestMergedMiningJobAndProof(t *testing.T) {
	p, _ := ParamsFor(address.LTC)
	_, b := loadLTCBlock(t, "testnet4Block2319633.dat")
	height, _ := bitcoin.DecodeBIP34Height(b.Txs[0].Inputs[0].Script)
	gbt := templateFromBlock(t, b, height, true)
	gbt.MWEB = hex.EncodeToString(b.MWEB)
	tmpl, err := NewTemplate(gbt, p, "test")
	if err != nil {
		t.Fatal(err)
	}
	easy := new(big.Int).Lsh(big.NewInt(1), 255)
	aux := &fakeAux{blocks: []AuxBlock{{Chain: "doge", ChainID: 0x62, Hash: bitcoin.Hash{0xd0, 0x6e}, Target: easy, Height: 5}}, ch: make(chan struct{})}
	m := NewManager(Config{Params: p, Chain: "test", Tag: []byte("/t/"), Extranonce2Size: 8, Aux: aux}, nil,
		stats.New("ltc", "t", ""), slog.New(slog.NewTextHandler(io.Discard, nil)))
	aw, err := newAuxWork(aux.Current(), m.PoW())
	if err != nil {
		t.Fatal(err)
	}
	w := &Work{Tmpl: tmpl, Gen: 1, Aux: aw, m: m, byScript: map[string]*Job{}}
	job, err := w.Job([]byte{0x00, 0x14, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}, "x")
	if err != nil {
		t.Fatal(err)
	}
	en1, en2 := []byte{1, 2, 3, 4}, []byte{5, 6, 7, 8, 9, 10, 11, 12}
	cb, err := bitcoin.ParseTx(job.CB.Tx(en1, en2), false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(cb.Inputs[0].Script, aw.Commitment.Tag()) {
		t.Fatal("coinbase does not carry the merged-mining tag")
	}
	// The assembled parent block is still a valid MWEB block.
	hdr := job.Header(en1, en2, tmpl.CurTime, 7, tmpl.Version)
	if _, err := bitcoin.ParseBlockMWEB(job.Block(&hdr, en1, en2)); err != nil {
		t.Fatal(err)
	}
	c := Candidate{Job: job, Header: hdr, En1: en1, En2: en2, Worker: "w"}
	m.SubmitAux(c, bitcoin.Hash{}) // hash 0 meets any target
	m.SubmitAux(c, bitcoin.Hash{}) // the same aux block again: ignored
	if len(aux.got) != 1 {
		t.Fatalf("%d aux submissions, want 1", len(aux.got))
	}
	ap := aux.got[0]
	if err := ap.Check(aux.blocks[0].Hash, 0x62, true); err != nil {
		t.Fatalf("our AuxPoW fails Dogecoin's check: %v", err)
	}
	hb := hdr.Serialize()
	if ap.Parent != hb || ap.Coinbase.TxID != cb.TxID || !bytes.Equal(ap.Coinbase.Raw, job.CB.Tx(en1, en2)) {
		t.Fatal("AuxPoW parent header or coinbase is not the share's")
	}
	// A share that misses the aux target submits nothing.
	aux.blocks[0].Hash = bitcoin.Hash{0xd0, 0x6f}
	aw2, _ := newAuxWork(aux.Current(), m.PoW())
	aw2.Blocks[0].Target = big.NewInt(1)
	job.Aux = aw2
	m.SubmitAux(c, bitcoin.Hash{0xff})
	if len(aux.got) != 1 {
		t.Fatal("share above the aux target was submitted")
	}
	// Share difficulty is capped at the aux network difficulty.
	if aw.MinDiff <= 0 {
		t.Fatal("MinDiff not set")
	}
}
