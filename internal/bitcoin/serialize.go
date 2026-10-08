package bitcoin

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// ErrShort is returned when a buffer ends before a structure is complete.
var ErrShort = errors.New("unexpected end of data")

// AppendVarInt appends a Bitcoin CompactSize integer.
func AppendVarInt(b []byte, v uint64) []byte {
	switch {
	case v < 0xfd:
		return append(b, byte(v))
	case v <= 0xffff:
		return append(b, 0xfd, byte(v), byte(v>>8))
	case v <= 0xffffffff:
		b = append(b, 0xfe)
		return binary.LittleEndian.AppendUint32(b, uint32(v))
	default:
		b = append(b, 0xff)
		return binary.LittleEndian.AppendUint64(b, v)
	}
}

// Reader is a bounds-checked cursor over a byte slice. All methods return
// ErrShort instead of panicking on truncated input.
type Reader struct {
	b   []byte
	pos int
}

// NewReader wraps b.
func NewReader(b []byte) *Reader { return &Reader{b: b} }

// Pos returns the current offset.
func (r *Reader) Pos() int { return r.pos }

// Len returns the number of unread bytes.
func (r *Reader) Len() int { return len(r.b) - r.pos }

// Bytes returns the next n bytes (aliasing the underlying slice).
func (r *Reader) Bytes(n int) ([]byte, error) {
	if n < 0 || n > r.Len() {
		return nil, ErrShort
	}
	out := r.b[r.pos : r.pos+n]
	r.pos += n
	return out, nil
}

// Byte reads one byte.
func (r *Reader) Byte() (byte, error) {
	if r.Len() < 1 {
		return 0, ErrShort
	}
	v := r.b[r.pos]
	r.pos++
	return v, nil
}

// Uint32 reads a little-endian uint32.
func (r *Reader) Uint32() (uint32, error) {
	b, err := r.Bytes(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}

// Uint64 reads a little-endian uint64.
func (r *Reader) Uint64() (uint64, error) {
	b, err := r.Bytes(8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(b), nil
}

// VarInt reads a CompactSize integer and rejects non-canonical encodings
// (as Bitcoin Core does since 0.10 for "non-canonical ReadCompactSize()").
func (r *Reader) VarInt() (uint64, error) {
	p, err := r.Byte()
	if err != nil {
		return 0, err
	}
	switch p {
	case 0xfd:
		b, err := r.Bytes(2)
		if err != nil {
			return 0, err
		}
		v := uint64(binary.LittleEndian.Uint16(b))
		if v < 0xfd {
			return 0, fmt.Errorf("non-canonical varint")
		}
		return v, nil
	case 0xfe:
		v, err := r.Uint32()
		if err != nil {
			return 0, err
		}
		if v <= 0xffff {
			return 0, fmt.Errorf("non-canonical varint")
		}
		return uint64(v), nil
	case 0xff:
		v, err := r.Uint64()
		if err != nil {
			return 0, err
		}
		if v <= 0xffffffff {
			return 0, fmt.Errorf("non-canonical varint")
		}
		return v, nil
	default:
		return uint64(p), nil
	}
}

// VarBytes reads a CompactSize length followed by that many bytes.
func (r *Reader) VarBytes() ([]byte, error) {
	n, err := r.VarInt()
	if err != nil {
		return nil, err
	}
	if n > uint64(r.Len()) {
		return nil, ErrShort
	}
	return r.Bytes(int(n))
}
