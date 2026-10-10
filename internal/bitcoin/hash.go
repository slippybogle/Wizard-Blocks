// Package bitcoin implements the consensus-level primitives the engine needs:
// double SHA-256, transaction/block (de)serialization, merkle trees, BIP141
// witness commitments, block headers and compact-target arithmetic. Everything
// here is shared by BTC and BCH (BCH simply never sees witness data).
package bitcoin

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// Hash is a 32-byte hash in *internal* byte order (the order it is
// serialized in blocks and hashed in merkle trees). RPC interfaces and block
// explorers display hashes byte-reversed; use String / HashFromDisplay.
type Hash [32]byte

// DoubleSHA256 returns SHA256(SHA256(b)).
func DoubleSHA256(b []byte) Hash {
	first := sha256.Sum256(b)
	return Hash(sha256.Sum256(first[:]))
}

// String returns the conventional (byte-reversed) hex representation.
func (h Hash) String() string {
	var r [32]byte
	for i := 0; i < 32; i++ {
		r[i] = h[31-i]
	}
	return hex.EncodeToString(r[:])
}

// InternalHex returns the hex of the hash in internal byte order.
func (h Hash) InternalHex() string { return hex.EncodeToString(h[:]) }

// HashFromDisplay parses a 64-char byte-reversed (RPC style) hex hash.
func HashFromDisplay(s string) (Hash, error) {
	var h Hash
	if len(s) != 64 {
		return h, fmt.Errorf("hash %q: want 64 hex chars, got %d", s, len(s))
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return h, fmt.Errorf("hash %q: %w", s, err)
	}
	for i := 0; i < 32; i++ {
		h[i] = b[31-i]
	}
	return h, nil
}

// HashFromInternal parses a 64-char hex string in internal byte order.
func HashFromInternal(s string) (Hash, error) {
	var h Hash
	if len(s) != 64 {
		return h, fmt.Errorf("hash %q: want 64 hex chars, got %d", s, len(s))
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return h, fmt.Errorf("hash %q: %w", s, err)
	}
	copy(h[:], b)
	return h, nil
}

// CTORLess reports whether h sorts strictly before o in Bitcoin Cash
// canonical transaction order. BCHN's uint256 comparison treats the hash as a
// 256-bit little-endian number, i.e. it compares from the last internal byte
// (most significant) down, which equals ordering by the displayed hex string.
// Verified against real mainnet block 877227 in TestBCHBlockCTOR.
func CTORLess(h, o Hash) bool {
	for i := 31; i >= 0; i-- {
		if h[i] != o[i] {
			return h[i] < o[i]
		}
	}
	return false
}
