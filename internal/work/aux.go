package work

import (
	"context"
	"math/big"

	"github.com/fladnagmai/wizard-blocks/internal/auxpow"
	"github.com/fladnagmai/wizard-blocks/internal/bitcoin"
	"github.com/fladnagmai/wizard-blocks/internal/pow"
)

// AuxBlock is one aux-chain block offered for merged mining (from
// Dogecoin's createauxblock).
type AuxBlock struct {
	Chain      string // "doge"
	ChainID    uint32
	Hash       bitcoin.Hash // internal byte order
	Target     *big.Int
	Height     int64
	Reward     int64
	PayoutAddr string
}

// AuxSource supplies the aux chains merged-mined on the parent's work and
// takes their solutions.
type AuxSource interface {
	// Current returns the aux blocks to commit to now; none when the aux
	// node is unavailable (the parent is then mined alone).
	Current() []AuxBlock
	// Changed is signalled when Current changes.
	Changed() <-chan struct{}
	// Submit hands over a solved aux block; it returns at once and reports
	// the outcome in stats and logs.
	Submit(b AuxBlock, ap *auxpow.AuxPow, worker string, shareDiff float64, parentHash bitcoin.Hash)
}

// AuxWork is the merged-mining state of a Work: the aux blocks and the
// chain merkle commitment whose tag goes into every coinbase.
type AuxWork struct {
	Blocks     []AuxBlock
	Commitment *auxpow.Commitment
	// MinDiff is the lowest aux network difficulty in share units: share
	// difficulty is capped there too, so no aux-block solution is withheld.
	MinDiff float64
}

// newAuxWork builds the commitment for blocks (nil when there are none).
func newAuxWork(blocks []AuxBlock, algo pow.Algo) (*AuxWork, error) {
	if len(blocks) == 0 {
		return nil, nil
	}
	chains := make([]auxpow.Chain, len(blocks))
	for i, b := range blocks {
		chains[i] = auxpow.Chain{ID: b.ChainID, Hash: b.Hash}
	}
	auxpow.SortChains(chains)
	c, err := auxpow.NewCommitment(chains)
	if err != nil {
		return nil, err
	}
	aw := &AuxWork{Blocks: append([]AuxBlock(nil), blocks...), Commitment: c}
	for _, b := range blocks {
		if d := algo.Difficulty(b.Target); aw.MinDiff == 0 || d < aw.MinDiff {
			aw.MinDiff = d
		}
	}
	return aw, nil
}

// tag returns the coinbase tag of a (nil) AuxWork.
func (a *AuxWork) tag() []byte {
	if a == nil {
		return nil
	}
	return a.Commitment.Tag()
}

// sameAux reports whether two AuxWorks commit to the same blocks.
func sameAux(a, b *AuxWork) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Commitment.Root() == b.Commitment.Root()
}

// updateAux publishes the current template again with fresh aux work. It
// is not a new parent block: jobs stay in the same generation, so shares
// on the previous jobs remain valid parent shares, but the notification
// asks miners to switch at once (clean_jobs), since work on an outdated
// aux block can no longer win it.
func (m *Manager) updateAux(_ context.Context) {
	if m.cfg.Aux == nil {
		return
	}
	aw, err := newAuxWork(m.cfg.Aux.Current(), m.PoW())
	if err != nil {
		m.log.Error("aux commitment", "err", err)
		aw = nil
	}
	m.mu.Lock()
	prev := m.cur
	if prev == nil || sameAux(prev.Aux, aw) {
		m.mu.Unlock()
		return
	}
	w := &Work{Tmpl: prev.Tmpl, Gen: prev.Gen, Clean: true, Aux: aw, m: m, byScript: map[string]*Job{}}
	m.cur = w
	listeners := append([]func(*Work){}, m.listeners...)
	m.mu.Unlock()
	if aw != nil {
		for _, b := range aw.Blocks {
			m.log.Info("new aux work", "chain", b.Chain, "height", b.Height, "hash", b.Hash.String(), "parent_height", w.Tmpl.Height)
		}
	} else {
		m.log.Warn("no aux work: mining the parent chain alone")
	}
	for _, f := range listeners {
		f(w)
	}
}

// SubmitAux checks a share against each aux chain of its job and submits
// the aux blocks it solves. powHash is the share's proof-of-work hash. It
// works for any share of a job whose aux block is still current at the aux
// node, whether or not the share is a parent block or even a current
// parent share.
func (m *Manager) SubmitAux(c Candidate, powHash bitcoin.Hash) {
	aw := c.Job.Aux
	if aw == nil || m.cfg.Aux == nil {
		return
	}
	for _, b := range aw.Blocks {
		if !bitcoin.HashMeetsTarget(powHash, b.Target) {
			continue
		}
		key := b.Chain + ":" + b.Hash.String()
		if _, dup := m.auxSubmitted.LoadOrStore(key, struct{}{}); dup {
			continue // one solution per aux block is enough
		}
		hdr := c.Header.Serialize()
		ap, err := auxpow.Build(aw.Commitment, b.ChainID, c.Job.CB.Tx(c.En1, c.En2), c.Job.Tmpl.Branch, hdr)
		if err == nil {
			// Never submit a proof the aux node would refuse.
			err = ap.Check(b.Hash, b.ChainID, true)
		}
		if err != nil {
			m.log.Error("cannot build aux proof", "chain", b.Chain, "hash", b.Hash.String(), "err", err)
			continue
		}
		m.cfg.Aux.Submit(b, ap, c.Worker, c.ShareDiff, c.Header.Hash())
	}
}
