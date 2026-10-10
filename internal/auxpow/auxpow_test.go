package auxpow

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/fladnagmai/wizard-blocks/internal/bitcoin"
	"github.com/fladnagmai/wizard-blocks/internal/pow"
)

const dogeChainID = 0x62

// Real Dogecoin mainnet blocks (testdata/README.md): parse, match the
// published hashes, pass Dogecoin's AuxPoW checks and the parent's Scrypt
// proof of work against the block's target, and re-serialize byte for byte.
func TestRealDogecoinBlocks(t *testing.T) {
	cases := []struct {
		file, hash, parent, coinbase string
		auxpow                       bool
	}{
		{"dogecoin_block250000.bin", "", "", "", false},
		{"dogecoin_block371337.bin", "60323982f9c5ff1b5a954eac9dc1269352835f47c2c5222691d80f0d50dcf053",
			"45df41e40aba5b2a03d08bd1202a1c02ef3954d8aa22ea6c5ae62fd00f290ea9", "e5422732b20e9e7ecc243427abbe296e9528d308bb111aae8d30c3465e442de8", true},
		{"dogecoin_block748634.bin", "bd98a06391115285265c04984e8505229739f6ffa5d498929a91fbe7c281ea7b", "", "", true},
		{"dogecoin_block894863.bin", "93a207e6d227f4d60ee64fad584b47255f654b0b6378d78e774123dd66f4fef9", "", "c84431cf41f592373cc70db07f6804f945202f5f7baad31a8bbab89aaecb7b8b", true},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", c.file))
			if err != nil {
				t.Fatal(err)
			}
			blk, err := ParseBlock(raw)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(blk.Serialize(), raw) {
				t.Fatal("block does not re-serialize byte for byte")
			}
			txids := make([]bitcoin.Hash, len(blk.Txs))
			for i, tx := range blk.Txs {
				txids[i] = tx.TxID
			}
			if bitcoin.MerkleRoot(txids) != blk.Header.MerkleRoot {
				t.Fatal("merkle root mismatch")
			}
			tgt, _ := bitcoin.CompactToTarget(blk.Header.Bits)
			if !c.auxpow {
				hb := blk.Header.Serialize()
				if blk.AuxPow != nil || !bitcoin.HashMeetsTarget(pow.Scrypt.PoWHash(hb[:]), tgt) {
					t.Fatal("pre-AuxPoW block: unexpected AuxPoW or bad Scrypt work")
				}
				return
			}
			if c.hash != "" && blk.Header.Hash().String() != c.hash {
				t.Fatalf("block hash %s, published %s", blk.Header.Hash(), c.hash)
			}
			if ChainID(blk.Header.Version) != dogeChainID || blk.AuxPow == nil {
				t.Fatalf("chain ID %d, auxpow %v", ChainID(blk.Header.Version), blk.AuxPow != nil)
			}
			a := blk.AuxPow
			if err := a.Check(blk.Header.Hash(), dogeChainID, true); err != nil {
				t.Fatalf("Dogecoin AuxPoW check: %v", err)
			}
			if !bitcoin.HashMeetsTarget(pow.Scrypt.PoWHash(a.Parent[:]), tgt) {
				t.Fatal("parent Scrypt work does not meet the block's target")
			}
			parent, _ := a.ParentHeader()
			if c.parent != "" && parent.Hash().String() != c.parent {
				t.Fatalf("parent hash %s, published %s", parent.Hash(), c.parent)
			}
			if c.coinbase != "" && a.Coinbase.TxID.String() != c.coinbase {
				t.Fatalf("parent coinbase txid %s, published %s", a.Coinbase.TxID, c.coinbase)
			}
			// The AuxPoW alone re-serializes byte for byte.
			if !bytes.Contains(raw, a.Serialize()) {
				t.Fatal("AuxPoW does not re-serialize byte for byte")
			}
			// Any other aux hash, or another chain ID, must fail.
			if a.Check(bitcoin.Hash{1}, dogeChainID, true) == nil {
				t.Fatal("check passed for a different aux block hash")
			}
			t.Logf("%s: %d txs, chain branch %d, coinbase branch %d", c.file, len(blk.Txs), len(a.ChainBranch), len(a.Branch))
		})
	}
}

// A commitment we build, a parent coinbase carrying its tag and an AuxPoW we
// assemble pass Dogecoin's check, for one chain and for several.
func TestBuildPassesCheck(t *testing.T) {
	for _, chains := range [][]Chain{
		{{ID: dogeChainID, Hash: bitcoin.Hash{0xd0}}},
		{{ID: dogeChainID, Hash: bitcoin.Hash{0xd0}}, {ID: 1, Hash: bitcoin.Hash{0x01}}, {ID: 7, Hash: bitcoin.Hash{0x07}}},
	} {
		com, err := NewCommitment(chains)
		if err != nil {
			t.Fatal(err)
		}
		if len(chains) == 1 && (com.Size != 1 || com.Nonce != 0 || com.Root() != chains[0].Hash) {
			t.Fatalf("single chain: size %d nonce %d", com.Size, com.Nonce)
		}
		script := append([]byte{0x03, 0x01, 0x02, 0x03}, com.Tag()...)
		script = append(script, 1, 2, 3, 4, 5, 6, 7, 8)
		cb := []byte{1, 0, 0, 0, 1}
		cb = append(cb, make([]byte, 32)...)
		cb = append(cb, 0xff, 0xff, 0xff, 0xff)
		cb = bitcoin.AppendVarInt(cb, uint64(len(script)))
		cb = append(cb, script...)
		cb = append(cb, 0xff, 0xff, 0xff, 0xff, 1)
		cb = append(cb, make([]byte, 8)...)
		cb = append(cb, 1, 0x51, 0, 0, 0, 0)
		cbtx, err := bitcoin.ParseTx(cb, false)
		if err != nil {
			t.Fatal(err)
		}
		other := []bitcoin.Hash{{9}, {8}}
		root := branchRoot(cbtx.TxID, other, 0)
		h := bitcoin.Header{Version: 0x20000000, MerkleRoot: root, Bits: 0x207fffff}
		a, err := Build(com, dogeChainID, cb, other, h.Serialize())
		if err != nil {
			t.Fatal(err)
		}
		if err := a.Check(chains[0].Hash, dogeChainID, true); err != nil {
			t.Fatalf("%d chains: %v", len(chains), err)
		}
		for _, c := range chains[1:] {
			ca, _ := Build(com, c.ID, cb, other, h.Serialize())
			if err := ca.Check(c.Hash, c.ID, true); err != nil {
				t.Fatalf("chain %d: %v", c.ID, err)
			}
		}
		// Round trip through the wire format.
		back, err := Read(bitcoin.NewReader(a.Serialize()))
		if err != nil || !bytes.Equal(back.Serialize(), a.Serialize()) {
			t.Fatalf("round trip: %v", err)
		}
		// A parent with Dogecoin's own chain ID is refused.
		h.Version = dogeChainID << 16
		bad, _ := Build(com, dogeChainID, cb, other, h.Serialize())
		if bad.Check(chains[0].Hash, dogeChainID, true) == nil {
			t.Fatal("parent with our chain ID accepted")
		}
	}
}
