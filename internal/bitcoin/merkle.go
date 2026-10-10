package bitcoin

import "bytes"

func hashPair(a, b Hash) Hash {
	var buf [64]byte
	copy(buf[:32], a[:])
	copy(buf[32:], b[:])
	return DoubleSHA256(buf[:])
}

// MerkleRoot computes the Bitcoin merkle root of leaves (internal byte order).
// An odd node at any level is paired with itself, as in Bitcoin Core's
// ComputeMerkleRoot. Returns the zero hash for no leaves.
func MerkleRoot(leaves []Hash) Hash {
	if len(leaves) == 0 {
		return Hash{}
	}
	level := append([]Hash(nil), leaves...)
	for len(level) > 1 {
		if len(level)%2 == 1 {
			level = append(level, level[len(level)-1])
		}
		next := make([]Hash, 0, len(level)/2)
		for i := 0; i < len(level); i += 2 {
			next = append(next, hashPair(level[i], level[i+1]))
		}
		level = next
	}
	return level[0]
}

// MerkleBranch returns the Stratum merkle branch for the coinbase: the list of
// sibling hashes, bottom-up, that combine with the coinbase txid (leaf 0) to
// produce the merkle root. others are the txids of all non-coinbase
// transactions in block order.
func MerkleBranch(others []Hash) []Hash {
	// Work on a level that has a placeholder at index 0 (the coinbase); the
	// placeholder's value never influences the branch because at each level
	// we only take the sibling of index 0 and combine siblings of index >= 2.
	level := make([]Hash, 0, len(others)+1)
	level = append(level, Hash{})
	level = append(level, others...)
	var branch []Hash
	for len(level) > 1 {
		if len(level)%2 == 1 {
			level = append(level, level[len(level)-1])
		}
		branch = append(branch, level[1])
		next := make([]Hash, 0, len(level)/2)
		next = append(next, Hash{}) // path containing the coinbase
		for i := 2; i < len(level); i += 2 {
			next = append(next, hashPair(level[i], level[i+1]))
		}
		level = next
	}
	return branch
}

// RootFromBranch folds a Stratum merkle branch onto the coinbase txid.
func RootFromBranch(coinbaseTxID Hash, branch []Hash) Hash {
	h := coinbaseTxID
	for _, b := range branch {
		h = hashPair(h, b)
	}
	return h
}

// WitnessReservedValue is the 32-byte coinbase witness item used by every
// template-based miner (BIP141 allows any value; Core's GBT assumes zeros).
var WitnessReservedValue = [32]byte{}

// WitnessCommitmentHeader is OP_RETURN, push 36, and the BIP141 magic aa21a9ed.
var WitnessCommitmentHeader = []byte{0x6a, 0x24, 0xaa, 0x21, 0xa9, 0xed}

// WitnessCommitment computes the BIP141 commitment hash:
// SHA256d(witness_merkle_root || witness_reserved_value), where the witness
// merkle root uses 0x00..00 as the coinbase wtxid. wtxids excludes the coinbase.
func WitnessCommitment(wtxids []Hash, reserved [32]byte) Hash {
	leaves := make([]Hash, 0, len(wtxids)+1)
	leaves = append(leaves, Hash{})
	leaves = append(leaves, wtxids...)
	root := MerkleRoot(leaves)
	var buf [64]byte
	copy(buf[:32], root[:])
	copy(buf[32:], reserved[:])
	return DoubleSHA256(buf[:])
}

// WitnessCommitmentScript returns the scriptPubKey of the commitment output.
func WitnessCommitmentScript(commitment Hash) []byte {
	s := make([]byte, 0, 38)
	s = append(s, WitnessCommitmentHeader...)
	return append(s, commitment[:]...)
}

// FindWitnessCommitment returns the commitment carried by a coinbase, using
// the BIP141 rule that the output with the highest index matching the
// pattern wins.
func FindWitnessCommitment(coinbase *Tx) (Hash, bool) {
	for i := len(coinbase.Outputs) - 1; i >= 0; i-- {
		s := coinbase.Outputs[i].Script
		if len(s) >= 38 && bytes.Equal(s[:6], WitnessCommitmentHeader) {
			var h Hash
			copy(h[:], s[6:38])
			return h, true
		}
	}
	return Hash{}, false
}
