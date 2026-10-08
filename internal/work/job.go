package work

import (
	"encoding/hex"
	"fmt"
	"time"

	"github.com/slippybogle/wizard-blocks/internal/bitcoin"
)

// Job is one unit of Stratum work: a template plus a coinbase paying one
// payout script. Jobs are immutable after creation.
type Job struct {
	ID         string
	Gen        uint64 // prevhash generation
	Tmpl       *Template
	Payout     []byte
	PayoutAddr string
	CB         *Coinbase
	Created    time.Time

	// Precomputed mining.notify fields.
	PrevHashHex string
	Coinb1Hex   string
	Coinb2Hex   string
	BranchHex   []string
	VersionHex  string
	BitsHex     string
	TimeHex     string
}

func newJob(id string, gen uint64, t *Template, payout []byte, payoutAddr string, cb *Coinbase) *Job {
	j := &Job{
		ID: id, Gen: gen, Tmpl: t, Payout: payout, PayoutAddr: payoutAddr, CB: cb, Created: time.Now(),
		PrevHashHex: StratumPrevHash(t.PrevHash),
		Coinb1Hex:   hex.EncodeToString(cb.Coinb1),
		Coinb2Hex:   hex.EncodeToString(cb.Coinb2),
		VersionHex:  fmt.Sprintf("%08x", t.Version),
		BitsHex:     fmt.Sprintf("%08x", t.Bits),
		TimeHex:     fmt.Sprintf("%08x", t.CurTime),
	}
	j.BranchHex = t.BranchHex
	return j
}

// withID returns a copy of j under a new id (same work, e.g. re-sent after a
// difficulty change so the miner unambiguously applies the new target).
func (j *Job) withID(id string) *Job {
	c := *j
	c.ID = id
	c.Created = time.Now()
	return &c
}

// NotifyParams returns the mining.notify parameter array.
func (j *Job) NotifyParams(clean bool) []any {
	return []any{j.ID, j.PrevHashHex, j.Coinb1Hex, j.Coinb2Hex, j.BranchHex, j.VersionHex, j.BitsHex, j.TimeHex, clean}
}

// Header builds the block header for a submission.
func (j *Job) Header(en1, en2 []byte, ntime, nonce, version uint32) bitcoin.Header {
	cbid := j.CB.TxID(en1, en2)
	return bitcoin.Header{
		Version:    version,
		PrevBlock:  j.Tmpl.PrevHash,
		MerkleRoot: bitcoin.RootFromBranch(cbid, j.Tmpl.Branch),
		Time:       ntime,
		Bits:       j.Tmpl.Bits,
		Nonce:      nonce,
	}
}

// Block serializes the full block for submitblock.
func (j *Job) Block(h *bitcoin.Header, en1, en2 []byte) []byte {
	hb := h.Serialize()
	cb := j.CB.BlockTx(en1, en2)
	out := make([]byte, 0, len(hb)+9+len(cb)+len(j.Tmpl.TxData))
	out = append(out, hb[:]...)
	out = bitcoin.AppendVarInt(out, uint64(j.Tmpl.TxCount()))
	out = append(out, cb...)
	return append(out, j.Tmpl.TxData...)
}
