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
	// TxID = SHA256d(Stripped); WTxID = SHA256d(Raw) (BIP141).
	TxID, WTxID Hash
	HasWitness  bool
}

// ParseTx parses exactly one transaction occupying all of b.
func ParseTx(b []byte, allowWitness bool) (*Tx, error) {
	r := NewReader(b)
	tx, err := ReadTx(r, allowWitness)
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
func ReadTx(r *Reader, allowWitness bool) (*Tx, error) {
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
		if flag != 1 {
			return nil, fmt.Errorf("unsupported extended tx flag %d", flag)
		}
		tx.HasWitness = true
		afterVersion = r.Pos()
		if nIn, err = r.VarInt(); err != nil {
			return nil, err
		}
		if nIn == 0 {
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
		tx.WTxID = DoubleSHA256(tx.Raw)
	} else {
		tx.WTxID = tx.TxID
	}
	return tx, nil
}

// IsCoinbase reports whether tx has the coinbase input shape.
func (tx *Tx) IsCoinbase() bool {
	return len(tx.Inputs) == 1 && tx.Inputs[0].PrevHash == (Hash{}) && tx.Inputs[0].PrevIndex == 0xffffffff
}
