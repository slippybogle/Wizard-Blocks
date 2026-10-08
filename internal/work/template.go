package work

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"time"

	"github.com/slippybogle/wizard-blocks/internal/address"
	"github.com/slippybogle/wizard-blocks/internal/bitcoin"
	"github.com/slippybogle/wizard-blocks/internal/node"
)

// CoinParams are the consensus differences between BTC and BCH that matter
// for block construction.
type CoinParams struct {
	Coin      address.Coin
	Segwit    bool     // BIP141 witness commitment + witness tx serialization
	CTOR      bool     // BCH canonical transaction order (2018-11)
	MinTxSize int      // BCH: 100 bytes (2018-11)
	GBTRules  []string // getblocktemplate "rules"
}

// ParamsFor returns the parameters for coin.
func ParamsFor(c address.Coin) (CoinParams, error) {
	switch c {
	case address.BTC:
		return CoinParams{Coin: c, Segwit: true, GBTRules: []string{"segwit"}}, nil
	case address.BCH:
		return CoinParams{Coin: c, CTOR: true, MinTxSize: 100}, nil
	}
	return CoinParams{}, fmt.Errorf("unknown coin %q", c)
}

// Template is a verified, preprocessed getblocktemplate result.
type Template struct {
	Height        int64
	PrevHash      bitcoin.Hash // internal byte order
	Version       uint32
	Bits          uint32
	Target        *big.Int // network target decoded from Bits
	NetworkDiff   float64
	CurTime       uint32
	MinTime       uint32
	CoinbaseValue int64
	TotalFees     int64
	TxIDs         []bitcoin.Hash // non-coinbase txids in block order
	TxData        []byte         // concatenated serialized non-coinbase txs
	Branch        []bitcoin.Hash // Stratum merkle branch for the coinbase
	BranchHex     []string       // Branch in internal byte order, as sent in mining.notify
	// WitnessCommitment is the BIP141 commitment output script (nil if none).
	WitnessCommitment []byte
	FetchedAt         time.Time
	ExpectedSubsidy   int64
}

// TxCount returns the number of transactions including the coinbase.
func (t *Template) TxCount() int { return len(t.TxIDs) + 1 }

// Subsidy returns the block subsidy at height for a halving interval.
func Subsidy(height, halvingInterval int64) int64 {
	halvings := height / halvingInterval
	if halvings >= 64 {
		return 0
	}
	return (50 * 100_000_000) >> halvings
}

// NewTemplate verifies raw and precomputes everything jobs need. Every
// transaction's txid (and wtxid on segwit chains) is recomputed from its
// data, and on BTC the witness commitment is recomputed and compared with
// the node's default_witness_commitment, so a malformed template can never
// turn into a block we would submit.
func NewTemplate(raw *node.BlockTemplate, p CoinParams, chain string) (*Template, error) {
	t := &Template{Height: raw.Height, CoinbaseValue: raw.CoinbaseValue, FetchedAt: time.Now()}
	if raw.Height <= 0 {
		return nil, fmt.Errorf("template height %d invalid", raw.Height)
	}
	if raw.CoinbaseValue <= 0 || raw.CoinbaseValue > 21_000_000*100_000_000 {
		return nil, fmt.Errorf("template coinbasevalue %d invalid", raw.CoinbaseValue)
	}
	if raw.Version <= 0 || raw.Version > 0xffffffff {
		return nil, fmt.Errorf("template version %d invalid", raw.Version)
	}
	t.Version = uint32(raw.Version)
	var err error
	if t.PrevHash, err = bitcoin.HashFromDisplay(raw.PreviousBlockHash); err != nil {
		return nil, fmt.Errorf("previousblockhash: %w", err)
	}
	bits, err := strconv.ParseUint(raw.Bits, 16, 32)
	if err != nil || len(raw.Bits) != 8 {
		return nil, fmt.Errorf("bits %q invalid", raw.Bits)
	}
	t.Bits = uint32(bits)
	if t.Target, err = bitcoin.CompactToTarget(t.Bits); err != nil {
		return nil, fmt.Errorf("bits %q: %w", raw.Bits, err)
	}
	if raw.Target != "" {
		tb, err := hex.DecodeString(raw.Target)
		if err != nil || new(big.Int).SetBytes(tb).Cmp(t.Target) != 0 {
			return nil, fmt.Errorf("template target %s disagrees with bits %s", raw.Target, raw.Bits)
		}
	}
	t.NetworkDiff = bitcoin.DifficultyFromTarget(t.Target)
	if raw.CurTime <= 0 || raw.CurTime > 0xffffffff || raw.MinTime < 0 || raw.MinTime > raw.CurTime {
		return nil, fmt.Errorf("template times cur=%d min=%d invalid", raw.CurTime, raw.MinTime)
	}
	t.CurTime, t.MinTime = uint32(raw.CurTime), uint32(raw.MinTime)

	halving := int64(210000)
	if chain == "regtest" {
		halving = 150
	}
	t.ExpectedSubsidy = Subsidy(raw.Height, halving)

	type entry struct {
		txid, wtxid bitcoin.Hash
		data        []byte
	}
	entries := make([]entry, 0, len(raw.Transactions))
	for i, tt := range raw.Transactions {
		data, err := hex.DecodeString(tt.Data)
		if err != nil {
			return nil, fmt.Errorf("tx %d: bad hex: %w", i, err)
		}
		tx, err := bitcoin.ParseTx(data, p.Segwit)
		if err != nil {
			return nil, fmt.Errorf("tx %d: %w", i, err)
		}
		if tx.IsCoinbase() {
			return nil, fmt.Errorf("tx %d: template contains a coinbase", i)
		}
		if tx.TxID.String() != tt.TxID {
			return nil, fmt.Errorf("tx %d: txid %s does not match data (%s)", i, tt.TxID, tx.TxID)
		}
		if p.Segwit && tt.Hash != "" && tx.WTxID.String() != tt.Hash {
			return nil, fmt.Errorf("tx %d: wtxid %s does not match data (%s)", i, tt.Hash, tx.WTxID)
		}
		if tt.Fee < 0 {
			return nil, fmt.Errorf("tx %d: negative fee", i)
		}
		t.TotalFees += tt.Fee
		entries = append(entries, entry{tx.TxID, tx.WTxID, data})
	}

	if p.CTOR {
		// BCH consensus: non-coinbase txs sorted ascending by txid. Nodes
		// already return CTOR order; if one did not, sorting is always valid
		// under CTOR (in-block dependency order is not required).
		sorted := sort.SliceIsSorted(entries, func(i, j int) bool { return bitcoin.CTORLess(entries[i].txid, entries[j].txid) })
		if !sorted {
			sort.Slice(entries, func(i, j int) bool { return bitcoin.CTORLess(entries[i].txid, entries[j].txid) })
		}
		for i := 1; i < len(entries); i++ {
			if !bitcoin.CTORLess(entries[i-1].txid, entries[i].txid) {
				return nil, fmt.Errorf("duplicate txid %s in template", entries[i].txid)
			}
		}
	}

	t.TxIDs = make([]bitcoin.Hash, len(entries))
	wtxids := make([]bitcoin.Hash, len(entries))
	size := 0
	for i, e := range entries {
		t.TxIDs[i], wtxids[i] = e.txid, e.wtxid
		size += len(e.data)
	}
	t.TxData = make([]byte, 0, size)
	for _, e := range entries {
		t.TxData = append(t.TxData, e.data...)
	}
	t.Branch = bitcoin.MerkleBranch(t.TxIDs)
	t.BranchHex = make([]string, len(t.Branch))
	for i, b := range t.Branch {
		t.BranchHex[i] = b.InternalHex() // no byte reversal (cgminer convention)
	}

	if p.Segwit {
		if raw.DefaultWitnessCommitment != "" {
			want, err := hex.DecodeString(raw.DefaultWitnessCommitment)
			if err != nil {
				return nil, fmt.Errorf("default_witness_commitment: %w", err)
			}
			got := bitcoin.WitnessCommitmentScript(bitcoin.WitnessCommitment(wtxids, bitcoin.WitnessReservedValue))
			if !bytes.Equal(got, want) {
				return nil, fmt.Errorf("witness commitment mismatch: node %x computed %x", want, got)
			}
			t.WitnessCommitment = got
		} else if hasRule(raw.Rules, "segwit") || hasRule(raw.Rules, "!segwit") {
			return nil, errors.New("segwit active but template has no default_witness_commitment")
		}
	} else if raw.DefaultWitnessCommitment != "" {
		return nil, errors.New("unexpected witness commitment in non-segwit template")
	}
	return t, nil
}

func hasRule(rules []string, r string) bool {
	for _, x := range rules {
		if x == r {
			return true
		}
	}
	return false
}

// StratumPrevHash encodes a previous-block hash for mining.notify: the
// internal byte order with each 32-bit word byte-swapped (the historical
// convention that cgminer, bfgminer, ckpool and all ASIC firmware share).
func StratumPrevHash(h bitcoin.Hash) string {
	var b [32]byte
	for i := 0; i < 8; i++ {
		w := binary.LittleEndian.Uint32(h[i*4:])
		binary.BigEndian.PutUint32(b[i*4:], w)
	}
	// BigEndian.Put of a LittleEndian-read word reverses the 4 bytes.
	return hex.EncodeToString(b[:])
}
