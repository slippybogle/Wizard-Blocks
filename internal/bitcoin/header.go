package bitcoin

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// HeaderSize is the size of a serialized block header.
const HeaderSize = 80

// Header is a block header. Hashes are in internal byte order.
type Header struct {
	Version    uint32
	PrevBlock  Hash
	MerkleRoot Hash
	Time       uint32
	Bits       uint32
	Nonce      uint32
}

// Serialize returns the 80-byte consensus serialization.
func (h *Header) Serialize() [HeaderSize]byte {
	var b [HeaderSize]byte
	binary.LittleEndian.PutUint32(b[0:], h.Version)
	copy(b[4:36], h.PrevBlock[:])
	copy(b[36:68], h.MerkleRoot[:])
	binary.LittleEndian.PutUint32(b[68:], h.Time)
	binary.LittleEndian.PutUint32(b[72:], h.Bits)
	binary.LittleEndian.PutUint32(b[76:], h.Nonce)
	return b
}

// Hash returns the block hash (SHA256d of the serialized header).
func (h *Header) Hash() Hash {
	b := h.Serialize()
	return DoubleSHA256(b[:])
}

// ParseHeader parses an 80-byte header.
func ParseHeader(b []byte) (*Header, error) {
	if len(b) != HeaderSize {
		return nil, fmt.Errorf("header must be 80 bytes, got %d", len(b))
	}
	h := &Header{
		Version: binary.LittleEndian.Uint32(b[0:]),
		Time:    binary.LittleEndian.Uint32(b[68:]),
		Bits:    binary.LittleEndian.Uint32(b[72:]),
		Nonce:   binary.LittleEndian.Uint32(b[76:]),
	}
	copy(h.PrevBlock[:], b[4:36])
	copy(h.MerkleRoot[:], b[36:68])
	return h, nil
}

// Block is a parsed block.
type Block struct {
	Header *Header
	Txs    []*Tx
	// MWEB is the serialized MWEB block of a Litecoin block (nil if none):
	// the bytes after the 0x01 optional-pointer flag, exactly as
	// getblocktemplate returns them in its "mweb" field.
	MWEB []byte
}

// ParseBlock parses a full serialized block.
func ParseBlock(b []byte, allowWitness bool) (*Block, error) {
	return parseBlock(b, allowWitness, false)
}

// ParseBlockMWEB parses a Litecoin block: transactions may use the MWEB flag
// (HogEx), and when the last transaction is a HogEx the block ends with the
// MWEB block as an optional pointer (one 0x01 byte, then the MWEB block;
// litecoin primitives/block.h CBlock::SerializationOp).
func ParseBlockMWEB(b []byte) (*Block, error) { return parseBlock(b, true, true) }

func parseBlock(b []byte, allowWitness, allowMWEB bool) (*Block, error) {
	if len(b) < HeaderSize {
		return nil, ErrShort
	}
	h, err := ParseHeader(b[:HeaderSize])
	if err != nil {
		return nil, err
	}
	r := NewReader(b[HeaderSize:])
	n, err := r.VarInt()
	if err != nil {
		return nil, err
	}
	if n == 0 || n > uint64(r.Len()/60+1) {
		return nil, fmt.Errorf("implausible tx count %d", n)
	}
	blk := &Block{Header: h, Txs: make([]*Tx, 0, n)}
	for i := uint64(0); i < n; i++ {
		tx, err := readTx(r, allowWitness, allowMWEB)
		if err != nil {
			return nil, fmt.Errorf("tx %d: %w", i, err)
		}
		if tx.HogEx && i != n-1 {
			return nil, fmt.Errorf("tx %d: HogEx is not the last transaction", i)
		}
		blk.Txs = append(blk.Txs, tx)
	}
	if last := blk.Txs[len(blk.Txs)-1]; last.HogEx && len(blk.Txs) >= 2 {
		set, err := r.Byte()
		if err != nil {
			return nil, err
		}
		if set != 1 {
			return nil, errors.New("block with a HogEx but no MWEB block")
		}
		blk.MWEB = r.b[r.Pos():]
		if len(blk.MWEB) == 0 {
			return nil, errors.New("empty MWEB block")
		}
		r.pos = len(r.b)
	}
	if r.Len() != 0 {
		return nil, fmt.Errorf("%d trailing bytes after block", r.Len())
	}
	return blk, nil
}
