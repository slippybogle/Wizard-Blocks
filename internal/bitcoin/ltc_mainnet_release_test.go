//go:build release

package bitcoin

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMainnetMWEBBlock is a release blocker for the Litecoin app (see
// docs/RELEASE-BLOCKERS.md): a real mainnet block taken from the user's own
// Litecoin node must pass every MWEB check byte for byte. It fails until the
// block file is present. Run: go test -tags release ./internal/bitcoin
func TestMainnetMWEBBlock(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("testdata", "ltc", "mainnet-*.hex"))
	if len(files) == 0 {
		t.Fatal("RELEASE BLOCKER: no testdata/ltc/mainnet-*.hex (a block from the user's Litecoin node)")
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := hex.DecodeString(strings.TrimSpace(string(b)))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		blk, err := ParseBlockMWEB(raw)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if blk.MWEB == nil || !blk.Txs[len(blk.Txs)-1].HogEx {
			t.Fatalf("%s: no MWEB block / HogEx (every mainnet block since 2022 has one)", f)
		}
		txids := make([]Hash, len(blk.Txs))
		wtxids := make([]Hash, len(blk.Txs))
		for i, tx := range blk.Txs {
			txids[i], wtxids[i] = tx.TxID, tx.WTxID
		}
		if MerkleRoot(txids) != blk.Header.MerkleRoot {
			t.Fatalf("%s: merkle root mismatch", f)
		}
		var reserved [32]byte
		copy(reserved[:], blk.Txs[0].Inputs[0].Witness[0])
		if got, ok := FindWitnessCommitment(blk.Txs[0]); !ok || got != WitnessCommitment(wtxids[1:], reserved) {
			t.Fatalf("%s: witness commitment mismatch", f)
		}
		hb := blk.Header.Serialize()
		out := append([]byte{}, hb[:]...)
		out = AppendVarInt(out, uint64(len(blk.Txs)))
		for _, tx := range blk.Txs {
			out = append(out, tx.Raw...)
		}
		out = append(append(out, 0x01), blk.MWEB...)
		if !bytes.Equal(out, raw) {
			t.Fatalf("%s: does not re-serialize byte for byte", f)
		}
		t.Logf("%s: block %s, %d txs, MWEB %d bytes: OK", f, blk.Header.Hash(), len(blk.Txs), len(blk.MWEB))
	}
}
