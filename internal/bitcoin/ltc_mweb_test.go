package bitcoin

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// Real Litecoin testnet4 blocks (testdata/ltc/README.md). Each must parse,
// hash to its published block hash, have a merkle root and witness
// commitment that match the txids/wtxids we compute (the HogEx included),
// and re-serialize byte for byte from its parts: header, tx count, the
// transactions exactly as found, then 0x01 and the MWEB block.
func TestLitecoinMWEBBlocks(t *testing.T) {
	cases := []struct {
		file    string
		hash    string // published block hash ("" if not given at the source)
		txs     int
		hasMWEB bool
	}{
		{"testnet4Block1821752.dat", "", 3, false},
		{"testnet4Block2215584.dat", "7e35fabe7b3c694ebeb0368a1a1c31e83962f3c5b4cc8dcede3ae94ed3deb306", 5, true},
		{"testnet4Block2215586.dat", "", 0, true},
		{"testnet4Block2319633.dat", "e9fe2c6496aedefa8bf6529bdc5c1f9fd4af565ca4c98cab73e3a1f616fb3502", 2, true},
		{"testnet4Block2321749.dat", "57929846db4a92d937eb596354d10949e33c815ee45df0c9b3bbdfb283e15bcd", 4, true},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", "ltc", c.file))
			if err != nil {
				t.Fatal(err)
			}
			blk, err := ParseBlockMWEB(raw)
			if err != nil {
				t.Fatal(err)
			}
			if c.hash != "" && blk.Header.Hash().String() != c.hash {
				t.Fatalf("block hash %s, published %s", blk.Header.Hash(), c.hash)
			}
			if c.txs != 0 && len(blk.Txs) != c.txs {
				t.Fatalf("%d txs, want %d", len(blk.Txs), c.txs)
			}
			last := blk.Txs[len(blk.Txs)-1]
			if c.hasMWEB != (blk.MWEB != nil) || c.hasMWEB != last.HogEx {
				t.Fatalf("MWEB %d bytes, last tx HogEx %v; want MWEB %v", len(blk.MWEB), last.HogEx, c.hasMWEB)
			}

			txids := make([]Hash, len(blk.Txs))
			wtxids := make([]Hash, len(blk.Txs))
			for i, tx := range blk.Txs {
				txids[i], wtxids[i] = tx.TxID, tx.WTxID
				// A transaction re-parsed on its own gives the same ids.
				again, err := ParseTxMWEB(tx.Raw)
				if err != nil || again.TxID != tx.TxID || again.WTxID != tx.WTxID {
					t.Fatalf("tx %d does not round-trip: %v", i, err)
				}
			}
			if MerkleRoot(txids) != blk.Header.MerkleRoot {
				t.Fatalf("merkle root mismatch: header %s computed %s", blk.Header.MerkleRoot, MerkleRoot(txids))
			}
			// BIP141 commitment in the coinbase covers every wtxid (coinbase
			// as zero), the HogEx included.
			cb := blk.Txs[0]
			if len(cb.Inputs[0].Witness) == 1 {
				var reserved [32]byte
				copy(reserved[:], cb.Inputs[0].Witness[0])
				got, ok := FindWitnessCommitment(cb)
				if want := WitnessCommitment(wtxids[1:], reserved); !ok || got != want {
					t.Fatalf("coinbase witness commitment %s, computed %s", got, want)
				}
			} else if c.hasMWEB {
				t.Fatal("MWEB block without a segwit coinbase")
			}

			// Byte for byte, the way the engine assembles a block.
			hb := blk.Header.Serialize()
			out := append([]byte{}, hb[:]...)
			out = AppendVarInt(out, uint64(len(blk.Txs)))
			for _, tx := range blk.Txs {
				out = append(out, tx.Raw...)
			}
			if blk.MWEB != nil {
				out = append(out, 0x01)
				out = append(out, blk.MWEB...)
			}
			if !bytes.Equal(out, raw) {
				t.Fatalf("re-serialized block differs (%d vs %d bytes)", len(out), len(raw))
			}
			t.Logf("%s: %d txs, MWEB %d bytes, hash %s", c.file, len(blk.Txs), len(blk.MWEB), blk.Header.Hash())
		})
	}
}

// The HogEx has no witness, so its wtxid equals its txid; both exclude the
// MWEB flag byte and the empty MWEB part (litecoin primitives/transaction.cpp).
func TestHogExIDsExcludeMWEB(t *testing.T) {
	raw, _ := os.ReadFile(filepath.Join("testdata", "ltc", "testnet4Block2319633.dat"))
	blk, err := ParseBlockMWEB(raw)
	if err != nil {
		t.Fatal(err)
	}
	h := blk.Txs[len(blk.Txs)-1]
	if !h.HogEx || h.HasWitness || h.WTxID != h.TxID {
		t.Fatalf("HogEx %v witness %v", h.HogEx, h.HasWitness)
	}
	if bytes.Equal(h.Stripped, h.Raw) || DoubleSHA256(h.Raw) == h.TxID {
		t.Fatal("txid must not cover the MWEB flag and part")
	}
	// Without MWEB support the same bytes are refused.
	if _, err := ParseTx(h.Raw, true); err == nil {
		t.Fatal("HogEx parsed without MWEB support")
	}
	if _, err := ParseBlock(raw, true); err == nil {
		t.Fatal("MWEB block parsed without MWEB support")
	}
}
