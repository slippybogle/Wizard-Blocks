// Package work turns node block templates into Stratum jobs, reconstructs
// candidate blocks from miner submissions and submits solved blocks.
package work

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/fladnagmai/wizard-blocks/internal/bitcoin"
)

// Extranonce1Size is the per-connection extranonce assigned by the server.
const Extranonce1Size = 4

// MaxScriptSig is the consensus limit on coinbase scriptSig length (2..100).
const MaxScriptSig = 100

// CoinbaseParams describes one coinbase transaction.
type CoinbaseParams struct {
	Height            int64
	Tag               []byte
	Extranonce2Size   int
	PayoutScript      []byte
	Value             int64
	WitnessCommitment []byte // full output script; nil = no commitment (BCH, or pre-segwit)
	MinTxSize         int    // BCH: 100 (2018-11 upgrade); BTC: 0
}

// Coinbase is a coinbase transaction split for Stratum:
// tx (non-witness serialization) = Coinb1 || extranonce1 || extranonce2 || Coinb2.
type Coinbase struct {
	Coinb1, Coinb2  []byte
	Extranonce2Size int
	// Witness is true when the block carries a BIP141 commitment; the coinbase
	// must then be serialized with the 32-byte witness reserved value.
	Witness bool
}

// BuildCoinbase constructs the coinbase transaction:
//
//	version=1 | 1 input (null prevout, index 0xffffffff)
//	scriptSig = <BIP34 height> <push tag[+pad]> <push extranonce1||extranonce2>
//	sequence=0xffffffff | outputs | locktime=0
//
// The extranonce is a proper push so the scriptSig is a well-formed script.
func BuildCoinbase(p CoinbaseParams) (*Coinbase, error) {
	if p.Height < 0 {
		return nil, errors.New("negative height")
	}
	if p.Extranonce2Size < 2 || p.Extranonce2Size > 16 {
		return nil, fmt.Errorf("extranonce2 size %d out of range 2..16", p.Extranonce2Size)
	}
	if p.Value < 0 || p.Value > 21_000_000*100_000_000 {
		return nil, fmt.Errorf("coinbase value %d out of range", p.Value)
	}
	if len(p.PayoutScript) == 0 {
		return nil, errors.New("empty payout script")
	}
	enLen := Extranonce1Size + p.Extranonce2Size
	pad := 0
	for {
		tagData := append(append([]byte(nil), p.Tag...), make([]byte, pad)...)
		var sigPrefix []byte
		sigPrefix = append(sigPrefix, bitcoin.BIP34HeightScript(p.Height)...)
		if len(tagData) > 0 {
			sigPrefix = bitcoin.AppendPush(sigPrefix, tagData)
		}
		sigPrefix = append(sigPrefix, byte(enLen)) // direct push opcode (enLen < 76)
		sigLen := len(sigPrefix) + enLen
		if sigLen > MaxScriptSig {
			return nil, fmt.Errorf("coinbase scriptSig would be %d bytes (max %d); shorten the coinbase tag", sigLen, MaxScriptSig)
		}
		if sigLen < 2 {
			return nil, errors.New("coinbase scriptSig too short")
		}

		var c1 []byte
		c1 = binary.LittleEndian.AppendUint32(c1, 1) // version
		c1 = bitcoin.AppendVarInt(c1, 1)             // input count
		c1 = append(c1, make([]byte, 32)...)         // null prevout hash
		c1 = binary.LittleEndian.AppendUint32(c1, 0xffffffff)
		c1 = bitcoin.AppendVarInt(c1, uint64(sigLen))
		c1 = append(c1, sigPrefix...)

		var c2 []byte
		c2 = binary.LittleEndian.AppendUint32(c2, 0xffffffff) // sequence
		nOut := 1
		if p.WitnessCommitment != nil {
			nOut++
		}
		c2 = bitcoin.AppendVarInt(c2, uint64(nOut))
		c2 = binary.LittleEndian.AppendUint64(c2, uint64(p.Value))
		c2 = bitcoin.AppendVarInt(c2, uint64(len(p.PayoutScript)))
		c2 = append(c2, p.PayoutScript...)
		if p.WitnessCommitment != nil {
			// BIP141: commitment output, value 0. Placed last; consensus uses
			// the highest-index output matching the pattern.
			c2 = binary.LittleEndian.AppendUint64(c2, 0)
			c2 = bitcoin.AppendVarInt(c2, uint64(len(p.WitnessCommitment)))
			c2 = append(c2, p.WitnessCommitment...)
		}
		c2 = binary.LittleEndian.AppendUint32(c2, 0) // locktime

		size := len(c1) + enLen + len(c2)
		if size < p.MinTxSize {
			pad += p.MinTxSize - size
			continue
		}
		// 64-byte transactions can be confused with inner merkle nodes
		// (CVE-2017-12842) and are invalid under BIP54; a coinbase here is
		// always larger, but assert it.
		if size == 64 {
			return nil, errors.New("coinbase would be exactly 64 bytes")
		}
		return &Coinbase{Coinb1: c1, Coinb2: c2, Extranonce2Size: p.Extranonce2Size, Witness: p.WitnessCommitment != nil}, nil
	}
}

// Tx returns the non-witness serialization (the form hashed for the txid).
func (c *Coinbase) Tx(en1, en2 []byte) []byte {
	out := make([]byte, 0, len(c.Coinb1)+len(en1)+len(en2)+len(c.Coinb2))
	out = append(out, c.Coinb1...)
	out = append(out, en1...)
	out = append(out, en2...)
	return append(out, c.Coinb2...)
}

// TxID returns SHA256d of the non-witness serialization.
func (c *Coinbase) TxID(en1, en2 []byte) bitcoin.Hash {
	return bitcoin.DoubleSHA256(c.Tx(en1, en2))
}

// BlockTx returns the serialization used inside the block: the BIP144
// extended form with the witness reserved value when a commitment exists.
func (c *Coinbase) BlockTx(en1, en2 []byte) []byte {
	tx := c.Tx(en1, en2)
	if !c.Witness {
		return tx
	}
	out := make([]byte, 0, len(tx)+2+34)
	out = append(out, tx[:4]...)          // version
	out = append(out, 0x00, 0x01)         // marker, flag
	out = append(out, tx[4:len(tx)-4]...) // inputs, outputs
	out = append(out, 0x01, 0x20)         // 1 witness item of 32 bytes
	out = append(out, bitcoin.WitnessReservedValue[:]...)
	return append(out, tx[len(tx)-4:]...) // locktime
}
