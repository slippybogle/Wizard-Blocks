// Package auxpow implements merged mining (AuxPoW) as Dogecoin and Namecoin
// define it (dogecoin src/auxpow.{h,cpp}, v1.14.9).
//
// A parent-chain block (Litecoin) commits to one or more aux-chain block
// hashes through a tag in its coinbase scriptSig:
//
//	fabe6d6d ‖ chain merkle root (reversed) ‖ size u32le ‖ nonce u32le
//
// An aux block is then proven by a CAuxPow: the parent coinbase
// (serialized without witness), its merkle branch in the parent block, the
// branch of the aux hash in the chain merkle tree, and the parent header,
// whose proof of work must meet the aux chain's target.
package auxpow

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"

	"github.com/fladnagmai/wizard-blocks/internal/bitcoin"
)

// MergedMiningHeader is the magic that precedes the chain merkle root.
var MergedMiningHeader = []byte{0xfa, 0xbe, 0x6d, 0x6d}

// TagSize is the size of the coinbase tag: magic, root, size, nonce.
const TagSize = 4 + 32 + 4 + 4

// VersionAuxPow is the block-version flag of a block carrying an AuxPoW.
const VersionAuxPow = 1 << 8

// ChainID returns the chain ID encoded in a block version (version >> 16).
func ChainID(version uint32) uint32 { return version >> 16 }

// AuxPow is a parsed CAuxPow.
type AuxPow struct {
	// Coinbase is the parent coinbase transaction, serialized without
	// witness (Raw == Stripped).
	Coinbase    *bitcoin.Tx
	HashBlock   bitcoin.Hash // unused by consensus; zero when we build one
	Branch      []bitcoin.Hash
	Index       int32
	ChainBranch []bitcoin.Hash
	ChainIndex  int32
	Parent      [bitcoin.HeaderSize]byte
}

// Read parses a CAuxPow from r.
func Read(r *bitcoin.Reader) (*AuxPow, error) {
	a := &AuxPow{}
	var err error
	// Dogecoin parses the parent coinbase in its own (pre-segwit) format.
	if a.Coinbase, err = bitcoin.ReadTx(r, false); err != nil {
		return nil, fmt.Errorf("auxpow coinbase: %w", err)
	}
	hb, err := r.Bytes(32)
	if err != nil {
		return nil, err
	}
	copy(a.HashBlock[:], hb)
	if a.Branch, err = readHashes(r); err != nil {
		return nil, err
	}
	idx, err := r.Uint32()
	if err != nil {
		return nil, err
	}
	a.Index = int32(idx)
	if a.ChainBranch, err = readHashes(r); err != nil {
		return nil, err
	}
	if idx, err = r.Uint32(); err != nil {
		return nil, err
	}
	a.ChainIndex = int32(idx)
	ph, err := r.Bytes(bitcoin.HeaderSize)
	if err != nil {
		return nil, err
	}
	copy(a.Parent[:], ph)
	return a, nil
}

func readHashes(r *bitcoin.Reader) ([]bitcoin.Hash, error) {
	n, err := r.VarInt()
	if err != nil {
		return nil, err
	}
	if n > uint64(r.Len()/32) {
		return nil, bitcoin.ErrShort
	}
	out := make([]bitcoin.Hash, n)
	for i := range out {
		b, err := r.Bytes(32)
		if err != nil {
			return nil, err
		}
		copy(out[i][:], b)
	}
	return out, nil
}

// Serialize returns the CAuxPow serialization (submitauxblock's argument).
func (a *AuxPow) Serialize() []byte {
	out := make([]byte, 0, len(a.Coinbase.Stripped)+32*(len(a.Branch)+len(a.ChainBranch)+1)+100)
	out = append(out, a.Coinbase.Stripped...)
	out = append(out, a.HashBlock[:]...)
	out = appendHashes(out, a.Branch)
	out = binary.LittleEndian.AppendUint32(out, uint32(a.Index))
	out = appendHashes(out, a.ChainBranch)
	out = binary.LittleEndian.AppendUint32(out, uint32(a.ChainIndex))
	return append(out, a.Parent[:]...)
}

func appendHashes(out []byte, hs []bitcoin.Hash) []byte {
	out = bitcoin.AppendVarInt(out, uint64(len(hs)))
	for _, h := range hs {
		out = append(out, h[:]...)
	}
	return out
}

// ParentHeader returns the parsed parent header.
func (a *AuxPow) ParentHeader() (*bitcoin.Header, error) { return bitcoin.ParseHeader(a.Parent[:]) }

// branchRoot is CheckMerkleBranch: fold a leaf up a merkle branch.
func branchRoot(leaf bitcoin.Hash, branch []bitcoin.Hash, index int32) bitcoin.Hash {
	if index == -1 {
		return bitcoin.Hash{}
	}
	h := leaf
	for _, b := range branch {
		var buf [64]byte
		if index&1 != 0 {
			copy(buf[:32], b[:])
			copy(buf[32:], h[:])
		} else {
			copy(buf[:32], h[:])
			copy(buf[32:], b[:])
		}
		h = bitcoin.DoubleSHA256(buf[:])
		index >>= 1
	}
	return h
}

// ExpectedIndex is getExpectedIndex: the slot of a chain in a chain merkle
// tree of height h for a given nonce.
func ExpectedIndex(nonce, chainID uint32, h uint) uint32 {
	r := nonce
	r = r*1103515245 + 12345
	r += chainID
	r = r*1103515245 + 12345
	return r % (1 << h)
}

// Check is CAuxPow::check: does a prove the aux block auxHash of the chain
// chainID? It does not check proof of work (see CheckWork).
func (a *AuxPow) Check(auxHash bitcoin.Hash, chainID uint32, strictChainID bool) error {
	if a.Index != 0 {
		return errors.New("AuxPow is not a generate")
	}
	parent, err := a.ParentHeader()
	if err != nil {
		return err
	}
	if strictChainID && ChainID(parent.Version) == chainID {
		return errors.New("Aux POW parent has our chain ID")
	}
	if len(a.ChainBranch) > 30 {
		return errors.New("Aux POW chain merkle branch too long")
	}
	root := branchRoot(auxHash, a.ChainBranch, a.ChainIndex)
	rootRev := reversed(root)
	if branchRoot(a.Coinbase.TxID, a.Branch, a.Index) != parent.MerkleRoot {
		return errors.New("Aux POW merkle root incorrect")
	}
	if len(a.Coinbase.Inputs) == 0 {
		return errors.New("Aux POW coinbase has no inputs")
	}
	script := a.Coinbase.Inputs[0].Script
	head := bytes.Index(script, MergedMiningHeader)
	pc := bytes.Index(script, rootRev)
	if pc < 0 {
		return errors.New("Aux POW missing chain merkle root in parent coinbase")
	}
	if head >= 0 {
		if bytes.Contains(script[head+1:], MergedMiningHeader) {
			return errors.New("Multiple merged mining headers in coinbase")
		}
		if head+len(MergedMiningHeader) != pc {
			return errors.New("Merged mining header is not just before chain merkle root")
		}
	} else if pc > 20 {
		return errors.New("Aux POW chain merkle root must start in the first 20 bytes of the parent coinbase")
	}
	rest := script[pc+32:]
	if len(rest) < 8 {
		return errors.New("Aux POW missing chain merkle tree size and nonce in parent coinbase")
	}
	size := binary.LittleEndian.Uint32(rest[0:4])
	h := uint(len(a.ChainBranch))
	if size != 1<<h {
		return errors.New("Aux POW merkle branch size does not match parent coinbase")
	}
	nonce := binary.LittleEndian.Uint32(rest[4:8])
	if uint32(a.ChainIndex) != ExpectedIndex(nonce, chainID, h) {
		return errors.New("Aux POW wrong index")
	}
	return nil
}

func reversed(h bitcoin.Hash) []byte {
	out := make([]byte, 32)
	for i := 0; i < 32; i++ {
		out[i] = h[31-i]
	}
	return out
}

// Chain is one aux chain to merge-mine: its chain ID and current block hash.
type Chain struct {
	ID   uint32
	Hash bitcoin.Hash
}

// Commitment is a chain merkle tree over the aux chains being mined.
type Commitment struct {
	Size  uint32
	Nonce uint32
	slots map[uint32]int // chain ID -> slot
	tree  []bitcoin.Hash // leaves, in slot order (zero hash for empty slots)
}

// NewCommitment places each chain in its expected slot of the smallest tree
// (size a power of two, nonce searched from 0) where no two chains clash.
// With a single chain the tree has size 1, nonce 0, and the root is the
// chain's block hash.
func NewCommitment(chains []Chain) (*Commitment, error) {
	if len(chains) == 0 {
		return nil, errors.New("no aux chains")
	}
	ids := map[uint32]bool{}
	for _, c := range chains {
		if ids[c.ID] {
			return nil, fmt.Errorf("duplicate aux chain ID %d", c.ID)
		}
		ids[c.ID] = true
	}
	for h := uint(0); h <= 30; h++ {
		if 1<<h < len(chains) {
			continue
		}
		for nonce := uint32(0); nonce < 1024; nonce++ {
			slots := map[uint32]int{}
			used := map[uint32]bool{}
			ok := true
			for _, c := range chains {
				s := ExpectedIndex(nonce, c.ID, h)
				if used[s] {
					ok = false
					break
				}
				used[s] = true
				slots[c.ID] = int(s)
			}
			if !ok {
				continue
			}
			tree := make([]bitcoin.Hash, 1<<h)
			for _, c := range chains {
				tree[slots[c.ID]] = c.Hash
			}
			return &Commitment{Size: 1 << h, Nonce: nonce, slots: slots, tree: tree}, nil
		}
	}
	return nil, errors.New("no chain merkle tree layout found")
}

// Root is the chain merkle root.
func (c *Commitment) Root() bitcoin.Hash { return bitcoin.MerkleRoot(c.tree) }

// Tag is the coinbase scriptSig tag: magic ‖ root reversed ‖ size ‖ nonce.
func (c *Commitment) Tag() []byte {
	out := make([]byte, 0, TagSize)
	out = append(out, MergedMiningHeader...)
	out = append(out, reversed(c.Root())...)
	out = binary.LittleEndian.AppendUint32(out, c.Size)
	return binary.LittleEndian.AppendUint32(out, c.Nonce)
}

// Branch returns the chain merkle branch and index for chain id.
func (c *Commitment) Branch(id uint32) ([]bitcoin.Hash, int32, bool) {
	s, ok := c.slots[id]
	if !ok {
		return nil, 0, false
	}
	return merkleBranchAt(c.tree, s), int32(s), true
}

// merkleBranchAt returns the merkle branch of leaf i (Bitcoin-style tree,
// odd levels duplicate their last element).
func merkleBranchAt(leaves []bitcoin.Hash, i int) []bitcoin.Hash {
	var branch []bitcoin.Hash
	level := append([]bitcoin.Hash{}, leaves...)
	for len(level) > 1 {
		if len(level)%2 == 1 {
			level = append(level, level[len(level)-1])
		}
		branch = append(branch, level[i^1])
		next := make([]bitcoin.Hash, len(level)/2)
		for j := range next {
			var buf [64]byte
			copy(buf[:32], level[2*j][:])
			copy(buf[32:], level[2*j+1][:])
			next[j] = bitcoin.DoubleSHA256(buf[:])
		}
		level, i = next, i/2
	}
	return branch
}

// Build assembles the AuxPow for chain id from a solved parent block: its
// coinbase (any serialization; the witness is dropped), the coinbase's
// merkle branch in the parent block, and the 80-byte parent header.
func Build(c *Commitment, id uint32, coinbase []byte, branch []bitcoin.Hash, parent [bitcoin.HeaderSize]byte) (*AuxPow, error) {
	cb, err := bitcoin.ParseTx(coinbase, true)
	if err != nil {
		return nil, fmt.Errorf("parent coinbase: %w", err)
	}
	stripped, err := bitcoin.ParseTx(cb.Stripped, false)
	if err != nil {
		return nil, err
	}
	cbranch, cidx, ok := c.Branch(id)
	if !ok {
		return nil, fmt.Errorf("chain %d not in the commitment", id)
	}
	return &AuxPow{Coinbase: stripped, Branch: append([]bitcoin.Hash{}, branch...), Index: 0,
		ChainBranch: cbranch, ChainIndex: cidx, Parent: parent}, nil
}

// SortChains orders chains by ID (stable layouts across refreshes).
func SortChains(cs []Chain) { sort.Slice(cs, func(i, j int) bool { return cs[i].ID < cs[j].ID }) }

// Block is a parsed aux-chain (Dogecoin) block.
type Block struct {
	Header *bitcoin.Header
	AuxPow *AuxPow // nil for a block without the AuxPoW flag
	Txs    []*bitcoin.Tx
}

// ParseBlock parses a full Dogecoin block (header, optional AuxPoW, txs).
func ParseBlock(b []byte) (*Block, error) {
	if len(b) < bitcoin.HeaderSize {
		return nil, bitcoin.ErrShort
	}
	h, err := bitcoin.ParseHeader(b[:bitcoin.HeaderSize])
	if err != nil {
		return nil, err
	}
	r := bitcoin.NewReader(b[bitcoin.HeaderSize:])
	blk := &Block{Header: h}
	if h.Version&VersionAuxPow != 0 {
		if blk.AuxPow, err = Read(r); err != nil {
			return nil, err
		}
	}
	n, err := r.VarInt()
	if err != nil {
		return nil, err
	}
	if n == 0 || n > uint64(r.Len()/60+1) {
		return nil, fmt.Errorf("implausible tx count %d", n)
	}
	for i := uint64(0); i < n; i++ {
		tx, err := bitcoin.ReadTx(r, false)
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

// Serialize re-serializes a parsed block.
func (b *Block) Serialize() []byte {
	hb := b.Header.Serialize()
	out := append([]byte{}, hb[:]...)
	if b.AuxPow != nil {
		out = append(out, b.AuxPow.Serialize()...)
	}
	out = bitcoin.AppendVarInt(out, uint64(len(b.Txs)))
	for _, tx := range b.Txs {
		out = append(out, tx.Raw...)
	}
	return out
}
