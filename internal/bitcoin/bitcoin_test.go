package bitcoin

import (
	"bytes"
	"compress/bzip2"
	"encoding/hex"
	"io"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func mustHex(t testing.TB, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func loadBlock(t testing.TB, name string, allowWitness bool) (*Block, []byte) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	raw, err := io.ReadAll(bzip2.NewReader(f))
	if err != nil {
		t.Fatal(err)
	}
	blk, err := ParseBlock(raw, allowWitness)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return blk, raw
}

// Bitcoin genesis block, from Bitcoin Core chainparams.
const genesisHex = "0100000000000000000000000000000000000000000000000000000000000000000000003ba3edfd7a7b12b27ac72c3e67768f617fc81bc3888a51323a9fb8aa4b1e5e4a29ab5f49ffff001d1dac2b7c0101000000010000000000000000000000000000000000000000000000000000000000000000ffffffff4d04ffff001d0104455468652054696d65732030332f4a616e2f32303039204368616e63656c6c6f72206f6e206272696e6b206f66207365636f6e64206261696c6f757420666f722062616e6b73ffffffff0100f2052a01000000434104678afdb0fe5548271967f1a67130b7105cd6a828e03909a67962e0ea1f61deb649f6bc3f4cef38c4f35504e51ec112de5c384df7ba0b8d578a4c702b6bf11d5fac00000000"

func TestGenesis(t *testing.T) {
	blk, err := ParseBlock(mustHex(t, genesisHex), true)
	if err != nil {
		t.Fatal(err)
	}
	if got := blk.Header.Hash().String(); got != "000000000019d6689c085ae165831e934ff763ae46a2a6c172b3f1b60a8ce26f" {
		t.Fatalf("genesis hash %s", got)
	}
	if got := blk.Txs[0].TxID.String(); got != "4a5e1e4baab89f3a32518a88c31bc87f618f76673e2cc77ab2127b7afdeda33b" {
		t.Fatalf("genesis txid %s", got)
	}
	if MerkleRoot([]Hash{blk.Txs[0].TxID}) != blk.Header.MerkleRoot {
		t.Fatal("genesis merkle")
	}
}

type blockVector struct {
	file         string
	witness      bool
	height       int64
	hash         string // "" = only PoW-checked
	bch          bool
	expectSegwit bool
}

var blockVectors = []blockVector{
	{file: "block413567.raw.bz2", height: 413567},
	{file: "block_000000000000000000000c835b2adcaedc20fdf6ee440009c249452c726dafae.raw.bz2", witness: true,
		hash: "000000000000000000000c835b2adcaedc20fdf6ee440009c249452c726dafae", expectSegwit: true},
	{file: "bch_block877227.raw.bz2", height: 877227, bch: true},
}

func TestRealBlocks(t *testing.T) {
	for _, v := range blockVectors {
		t.Run(v.file, func(t *testing.T) {
			blk, raw := loadBlock(t, v.file, !v.bch)
			h := blk.Header
			hash := h.Hash()
			if v.hash != "" && hash.String() != v.hash {
				t.Fatalf("header hash %s want %s", hash, v.hash)
			}
			target, err := CompactToTarget(h.Bits)
			if err != nil {
				t.Fatal(err)
			}
			if !HashMeetsTarget(hash, target) {
				t.Fatalf("hash %s does not meet target of bits %08x", hash, h.Bits)
			}
			// Mainnet difficulty at these heights is enormous: a wrong hash
			// would essentially never pass the check above by accident.
			if DifficultyFromTarget(target) < 1e10 {
				t.Fatalf("unexpectedly low difficulty %v", DifficultyFromTarget(target))
			}
			cb := blk.Txs[0]
			if !cb.IsCoinbase() {
				t.Fatal("first tx is not coinbase")
			}
			height, err := DecodeBIP34Height(cb.Inputs[0].Script)
			if err != nil {
				t.Fatal(err)
			}
			if v.height != 0 && height != v.height {
				t.Fatalf("BIP34 height %d want %d", height, v.height)
			}
			if !bytes.HasPrefix(cb.Inputs[0].Script, BIP34HeightScript(height)) {
				t.Fatal("BIP34 prefix encoding mismatch")
			}
			t.Logf("height %d hash %s txs %d bytes %d", height, hash, len(blk.Txs), len(raw))

			txids := make([]Hash, len(blk.Txs))
			for i, tx := range blk.Txs {
				txids[i] = tx.TxID
				if !tx.HasWitness && !bytes.Equal(tx.Raw, tx.Stripped) {
					t.Fatal("stripped != raw for non-witness tx")
				}
			}
			if MerkleRoot(txids) != h.MerkleRoot {
				t.Fatal("merkle root mismatch")
			}
			// Stratum path: the branch for the coinbase must rebuild the root.
			branch := MerkleBranch(txids[1:])
			if RootFromBranch(cb.TxID, branch) != h.MerkleRoot {
				t.Fatal("merkle branch does not rebuild the root")
			}

			if v.expectSegwit {
				nw := 0
				wtxids := make([]Hash, 0, len(blk.Txs)-1)
				for _, tx := range blk.Txs[1:] {
					if tx.HasWitness {
						nw++
					}
					wtxids = append(wtxids, tx.WTxID)
				}
				if nw == 0 {
					t.Fatal("expected witness transactions")
				}
				want, ok := FindWitnessCommitment(cb)
				if !ok {
					t.Fatal("no witness commitment in coinbase")
				}
				if len(cb.Inputs[0].Witness) != 1 || len(cb.Inputs[0].Witness[0]) != 32 {
					t.Fatal("coinbase witness reserved value missing")
				}
				var reserved [32]byte
				copy(reserved[:], cb.Inputs[0].Witness[0])
				if got := WitnessCommitment(wtxids, reserved); got != want {
					t.Fatalf("witness commitment %x want %x", got, want)
				}
				t.Logf("segwit txs: %d/%d", nw, len(blk.Txs)-1)
			}

			if v.bch {
				for i := 2; i < len(blk.Txs); i++ {
					if !CTORLess(blk.Txs[i-1].TxID, blk.Txs[i].TxID) {
						t.Fatalf("CTOR violated at %d", i)
					}
				}
				// Sanity: the opposite (raw internal byte) ordering must NOT hold,
				// proving CTORLess compares in the right direction.
				internalSorted := sort.SliceIsSorted(blk.Txs[1:], func(i, j int) bool {
					a, b := blk.Txs[1+i].TxID, blk.Txs[1+j].TxID
					return bytes.Compare(a[:], b[:]) < 0
				})
				if internalSorted && len(blk.Txs) > 10 {
					t.Fatal("block is also sorted in internal byte order; CTOR direction unproven")
				}
			}
		})
	}
}

func TestCompact(t *testing.T) {
	cases := []struct {
		bits uint32
		hex  string
	}{
		{0x1d00ffff, "00000000ffff0000000000000000000000000000000000000000000000000000"},
		{0x207fffff, "7fffff0000000000000000000000000000000000000000000000000000000000"},
		{0x1715a35c, "00000000000000000015a35c0000000000000000000000000000000000000000"},
		{0x03123456, "0000000000000000000000000000000000000000000000000000000000123456"},
		{0x02123456, "0000000000000000000000000000000000000000000000000000000000001234"},
	}
	for _, c := range cases {
		got, err := CompactToTarget(c.bits)
		if err != nil {
			t.Fatal(err)
		}
		want := new(big.Int).SetBytes(mustHex(t, c.hex))
		if got.Cmp(want) != 0 {
			t.Fatalf("%08x: %x want %x", c.bits, got, want)
		}
	}
	for _, bad := range []uint32{0x04923456, 0x01003456, 0x23000001, 0xff123456, 0x00000000} {
		if _, err := CompactToTarget(bad); err == nil {
			t.Fatalf("%08x accepted", bad)
		}
	}
	if Diff1Target.Cmp(mustTarget(t, 0x1d00ffff)) != 0 {
		t.Fatal("diff1")
	}
}

func mustTarget(t *testing.T, bits uint32) *big.Int {
	v, err := CompactToTarget(bits)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDifficulty(t *testing.T) {
	if d := DifficultyFromTarget(Diff1Target); d != 1 {
		t.Fatal(d)
	}
	// Regtest powLimit difficulty as reported by getblockchaininfo.
	if d := DifficultyFromTarget(mustTarget(t, 0x207fffff)); math.Abs(d/4.656542373906925e-10-1) > 1e-12 {
		t.Fatalf("regtest difficulty %v", d)
	}
	if TargetFromDifficulty(1).Cmp(Diff1Target) != 0 {
		t.Fatal("diff 1 target")
	}
	tg := TargetFromDifficulty(1024)
	want := new(big.Int).Rsh(Diff1Target, 10)
	if tg.Cmp(want) != 0 {
		t.Fatalf("diff 1024 %x want %x", tg, want)
	}
	// Difficulty below 1 gives a target above diff1.
	if TargetFromDifficulty(0.5).Cmp(new(big.Int).Lsh(Diff1Target, 1)) != 0 {
		t.Fatal("diff 0.5")
	}
	// Extremely low difficulty clamps to 2^256-1.
	if TargetFromDifficulty(1e-80).BitLen() != 256 {
		t.Fatal("clamp")
	}
}

func TestBIP34(t *testing.T) {
	cases := map[int64]string{
		1:       "51",
		16:      "60",
		17:      "0111",
		127:     "017f",
		128:     "028000",
		255:     "02ff00",
		256:     "020001",
		32767:   "02ff7f",
		32768:   "03008000",
		413567:  "037f4f06",
		877227:  "03ab620d",
		8388607: "03ffff7f",
		8388608: "0400008000",
	}
	for h, want := range cases {
		got := hex.EncodeToString(BIP34HeightScript(h))
		if got != want {
			t.Errorf("height %d: %s want %s", h, got, want)
		}
		back, err := DecodeBIP34Height(BIP34HeightScript(h))
		if err != nil || back != h {
			t.Errorf("decode %d: %d %v", h, back, err)
		}
	}
}

func TestMerkleBranchSmall(t *testing.T) {
	// Cross-check branch folding against full root for every tree size 1..40.
	for n := 1; n <= 40; n++ {
		leaves := make([]Hash, n)
		for i := range leaves {
			leaves[i] = DoubleSHA256([]byte{byte(i), byte(n)})
		}
		br := MerkleBranch(leaves[1:])
		if RootFromBranch(leaves[0], br) != MerkleRoot(leaves) {
			t.Fatalf("n=%d branch mismatch", n)
		}
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	// Must never panic, whatever the input.
	inputs := [][]byte{nil, {0}, {1, 0, 0, 0}, {1, 0, 0, 0, 0}, {1, 0, 0, 0, 0, 1}, {1, 0, 0, 0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}}
	blk, raw := loadBlock(t, "block_000000000000000000000c835b2adcaedc20fdf6ee440009c249452c726dafae.raw.bz2", true)
	tx := blk.Txs[1].Raw
	for i := 0; i < len(tx); i++ {
		inputs = append(inputs, tx[:i])
	}
	for _, in := range inputs {
		if _, err := ParseTx(in, true); err == nil {
			t.Fatalf("accepted garbage %x", in)
		}
	}
	for i := 0; i < 200; i++ {
		_, _ = ParseBlock(raw[:i*37%len(raw)], true)
	}
}
