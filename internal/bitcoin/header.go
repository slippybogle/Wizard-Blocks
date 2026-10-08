package bitcoin

import (
	"encoding/binary"
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
}

// ParseBlock parses a full serialized block.
func ParseBlock(b []byte, allowWitness bool) (*Block, error) {
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
		tx, err := ReadTx(r, allowWitness)
		if err != nil {
			return nil, fmt.Errorf("tx %d: %w", i, err)
		}
		blk.Txs = append(blk.Txs, tx)
	}
	if r.Len() != 0 {
		return nil, fmt.Errorf("%d trailing bytes after block", r.Len())
	}
	return blk, nil
}
