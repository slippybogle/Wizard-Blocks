package bitcoin

import (
	"errors"
	"fmt"
)

// TxOut is a parsed transaction output.
type TxOut struct {
	Value  int64
	Script []byte
}

// TxIn is a parsed transaction input.
type TxIn struct {
	PrevHash  Hash
	PrevIndex uint32
	Script    []byte
	Sequence  uint32
	Witness   [][]byte
}

// Tx is a parsed transaction together with its identifiers.
type Tx struct {
	Version  uint32
	Inputs   []TxIn
	Outputs  []TxOut
	LockTime uint32

	// Raw is the full serialization as found (with witness if present).
	Raw []byte
	// Stripped is the serialization without witness data (BIP144), used for txid.
	Stripped []byte
	// TxID = SHA256d(Stripped); WTxID = SHA256d(Raw) (BIP141). On Litecoin
	// both exclude the MWEB part (litecoin primitives/transaction.cpp).
	TxID, WTxID Hash
	HasWitness  bool
	// HogEx is set for Litecoin's MWEB integrating transaction: extended
	// flag bit 8 with an empty MWEB part. It is always the last transaction
	// of a block that carries an MWEB block.
	HogEx bool
}

// Litecoin extended-serialization flag bits (primitives/transaction.h).
const (
	txFlagWitness = 1
	txFlagMWEB    = 8
)

// ParseTx parses exactly one transaction occupying all of b.
func ParseTx(b []byte, allowWitness bool) (*Tx, error) {
	return parseTx(b, allowWitness, false)
}

// ParseTxMWEB parses one Litecoin transaction occupying all of b, also
// accepting the MWEB flag (HogEx only).
func ParseTxMWEB(b []byte) (*Tx, error) { return parseTx(b, true, true) }

func parseTx(b []byte, allowWitness, allowMWEB bool) (*Tx, error) {
	r := NewReader(b)
	tx, err := readTx(r, allowWitness, allowMWEB)
	if err != nil {
		return nil, err
	}
	if r.Len() != 0 {
		return nil, fmt.Errorf("%d trailing bytes after transaction", r.Len())
	}
	return tx, nil
}

// ReadTx parses one transaction from r. If allowWitness is true, the BIP144
// extended serialization (marker 0x00, flag 0x01) is recognised. BCH never
// uses it, and a BCH transaction can never start with a zero input count, so
// callers pass false for BCH to keep parsing strict.
func ReadTx(r *Reader, allowWitness bool) (*Tx, error) { return readTx(r, allowWitness, false) }

// ReadTxMWEB reads one Litecoin transaction (witness and HogEx MWEB flag).
func ReadTxMWEB(r *Reader) (*Tx, error) { return readTx(r, true, true) }

func readTx(r *Reader, allowWitness, allowMWEB bool) (*Tx, error) {
	start := r.Pos()
	tx := &Tx{}
	var err error
	if tx.Version, err = r.Uint32(); err != nil {
		return nil, err
	}
	// Stripped serialization is assembled from slices of the raw buffer.
	stripped := make([]byte, 0, 256)
	stripped = append(stripped, r.b[start:start+4]...)

	afterVersion := r.Pos()
	mweb := false
	nIn, err := r.VarInt()
	if err != nil {
		return nil, err
	}
	if nIn == 0 {
		if !allowWitness {
			return nil, errors.New("transaction with zero inputs")
		}
		flag, err := r.Byte()
		if err != nil {
			return nil, err
		}
		allowed := byte(txFlagWitness)
		if allowMWEB {
			allowed |= txFlagMWEB
		}
		if flag == 0 || flag&^allowed != 0 {
			return nil, fmt.Errorf("unsupported extended tx flag %d", flag)
		}
		tx.HasWitness = flag&txFlagWitness != 0
		mweb = flag&txFlagMWEB != 0
		afterVersion = r.Pos()
		if nIn, err = r.VarInt(); err != nil {
			return nil, err
		}
		if nIn == 0 && !mweb {
			return nil, errors.New("witness transaction with zero inputs")
		}
	}
	// Each input is at least 41 bytes; bound the allocation by what remains.
	if nIn > uint64(r.Len()/41+1) {
		return nil, ErrShort
	}
	tx.Inputs = make([]TxIn, nIn)
	for i := range tx.Inputs {
		in := &tx.Inputs[i]
		ph, err := r.Bytes(32)
		if err != nil {
			return nil, err
		}
		copy(in.PrevHash[:], ph)
		if in.PrevIndex, err = r.Uint32(); err != nil {
			return nil, err
		}
		if in.Script, err = r.VarBytes(); err != nil {
			return nil, err
		}
		if in.Sequence, err = r.Uint32(); err != nil {
			return nil, err
		}
	}
	nOut, err := r.VarInt()
	if err != nil {
		return nil, err
	}
	if nOut > uint64(r.Len()/9+1) {
		return nil, ErrShort
	}
	tx.Outputs = make([]TxOut, nOut)
	for i := range tx.Outputs {
		v, err := r.Uint64()
		if err != nil {
			return nil, err
		}
		tx.Outputs[i].Value = int64(v)
		if tx.Outputs[i].Script, err = r.VarBytes(); err != nil {
			return nil, err
		}
	}
	stripped = append(stripped, r.b[afterVersion:r.Pos()]...)
	if tx.HasWitness {
		any := false
		for i := range tx.Inputs {
			n, err := r.VarInt()
			if err != nil {
				return nil, err
			}
			if n > uint64(r.Len()) {
				return nil, ErrShort
			}
			items := make([][]byte, n)
			for j := range items {
				if items[j], err = r.VarBytes(); err != nil {
					return nil, err
				}
			}
			if n > 0 {
				any = true
			}
			tx.Inputs[i].Witness = items
		}
		// BIP144: "If the witness is empty, the old serialization format must be used."
		if !any {
			return nil, errors.New("extended serialization with empty witness")
		}
	}
	witnessEnd := r.Pos()
	if mweb {
		// The MWEB part is an optional pointer: 0 = none (a HogEx), 1 =
		// an MWEB transaction. Blocks carry MWEB transactions in the MWEB
		// block, never inside a canonical transaction, so only the HogEx
		// form is accepted.
		set, err := r.Byte()
		if err != nil {
			return nil, err
		}
		if set != 0 {
			return nil, errors.New("canonical transaction carrying MWEB transaction data")
		}
		if len(tx.Outputs) == 0 {
			return nil, errors.New("HogEx without outputs")
		}
		tx.HogEx = true
	}
	lt, err := r.Bytes(4)
	if err != nil {
		return nil, err
	}
	tx.LockTime = uint32(lt[0]) | uint32(lt[1])<<8 | uint32(lt[2])<<16 | uint32(lt[3])<<24
	stripped = append(stripped, lt...)

	tx.Raw = r.b[start:r.Pos()]
	tx.Stripped = stripped
	tx.TxID = DoubleSHA256(tx.Stripped)
	if tx.HasWitness {
		if mweb {
			// wtxid: the witness serialization without the MWEB flag/part.
			w := make([]byte, 0, len(tx.Raw))
			w = append(w, tx.Raw[:4]...)
			w = append(w, 0, txFlagWitness)
			w = append(w, r.b[afterVersion:witnessEnd]...)
			w = append(w, lt...)
			tx.WTxID = DoubleSHA256(w)
		} else {
			tx.WTxID = DoubleSHA256(tx.Raw)
		}
	} else {
		tx.WTxID = tx.TxID
	}
	return tx, nil
}

// IsCoinbase reports whether tx has the coinbase input shape.
func (tx *Tx) IsCoinbase() bool {
	return len(tx.Inputs) == 1 && tx.Inputs[0].PrevHash == (Hash{}) && tx.Inputs[0].PrevIndex == 0xffffffff
}
