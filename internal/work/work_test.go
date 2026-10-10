package work

import (
	"bytes"
	"compress/bzip2"
	"encoding/hex"
	"io"
	"math/rand"
	"os"
	"testing"

	"github.com/fladnagmai/wizard-blocks/internal/address"
	"github.com/fladnagmai/wizard-blocks/internal/bitcoin"
	"github.com/fladnagmai/wizard-blocks/internal/node"
)

func loadBlock(t *testing.T, name string, witness bool) *bitcoin.Block {
	t.Helper()
	f, err := os.Open("../bitcoin/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	raw, err := io.ReadAll(bzip2.NewReader(f))
	if err != nil {
		t.Fatal(err)
	}
	b, err := bitcoin.ParseBlock(raw, witness)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// templateFromBlock turns a real mainnet block into the getblocktemplate
// response a node would have produced for it.
func templateFromBlock(t *testing.T, b *bitcoin.Block, height int64, segwit bool) *node.BlockTemplate {
	var cbValue int64
	for _, o := range b.Txs[0].Outputs {
		cbValue += o.Value
	}
	tg, _ := bitcoin.CompactToTarget(b.Header.Bits)
	raw := &node.BlockTemplate{
		Version:           int64(b.Header.Version),
		PreviousBlockHash: b.Header.PrevBlock.String(),
		CoinbaseValue:     cbValue,
		Target:            hex.EncodeToString(leftPad(tg.Bytes())),
		CurTime:           int64(b.Header.Time),
		MinTime:           int64(b.Header.Time) - 600,
		Bits:              hexU32(b.Header.Bits),
		Height:            height,
	}
	var wtxids []bitcoin.Hash
	for _, tx := range b.Txs[1:] {
		raw.Transactions = append(raw.Transactions, node.TemplateTx{
			Data: hex.EncodeToString(tx.Raw), TxID: tx.TxID.String(), Hash: tx.WTxID.String(),
		})
		wtxids = append(wtxids, tx.WTxID)
	}
	if segwit {
		raw.Rules = []string{"csv", "!segwit", "taproot"}
		raw.DefaultWitnessCommitment = hex.EncodeToString(bitcoin.WitnessCommitmentScript(bitcoin.WitnessCommitment(wtxids, bitcoin.WitnessReservedValue)))
	}
	return raw
}

func leftPad(b []byte) []byte { return append(make([]byte, 32-len(b)), b...) }
func hexU32(v uint32) string {
	return hex.EncodeToString([]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

// checkAssembledBlock builds a job from tmpl, "solves" it with arbitrary
// extranonces and verifies the assembled block bytes are self-consistent:
// parseable, coinbase as specified, merkle root and witness commitment correct.
func checkAssembledBlock(t *testing.T, tmpl *Template, p CoinParams, script []byte) *bitcoin.Block {
	t.Helper()
	cb, err := BuildCoinbase(CoinbaseParams{
		Height: tmpl.Height, Tag: []byte("/wizard-blocks test/"), Extranonce2Size: 8,
		PayoutScript: script, Value: tmpl.CoinbaseValue, WitnessCommitment: tmpl.WitnessCommitment,
		MinTxSize: p.MinTxSize,
	})
	if err != nil {
		t.Fatal(err)
	}
	j := newJob("1", 1, tmpl, script, "x", cb)
	en1 := []byte{1, 2, 3, 4}
	en2 := []byte{9, 8, 7, 6, 5, 4, 3, 2}
	h := j.Header(en1, en2, tmpl.CurTime+1, 0xdeadbeef, tmpl.Version|0x00002000)
	parse := func(b []byte) (*bitcoin.Block, error) { return bitcoin.ParseBlock(b, p.Segwit) }
	if p.MWEB {
		parse = bitcoin.ParseBlockMWEB
	}
	blk, err := parse(j.Block(&h, en1, en2))
	if err != nil {
		t.Fatal(err)
	}
	if blk.Header.Hash() != h.Hash() {
		t.Fatal("header round trip")
	}
	txids := make([]bitcoin.Hash, len(blk.Txs))
	wtxids := make([]bitcoin.Hash, 0, len(blk.Txs))
	for i, tx := range blk.Txs {
		txids[i] = tx.TxID
		if i > 0 {
			wtxids = append(wtxids, tx.WTxID)
		}
	}
	if bitcoin.MerkleRoot(txids) != blk.Header.MerkleRoot {
		t.Fatal("assembled block merkle root mismatch")
	}
	cbtx := blk.Txs[0]
	if !cbtx.IsCoinbase() || cbtx.TxID != cb.TxID(en1, en2) {
		t.Fatal("coinbase identity")
	}
	if height, err := bitcoin.DecodeBIP34Height(cbtx.Inputs[0].Script); err != nil || height != tmpl.Height {
		t.Fatalf("BIP34 height %d %v", height, err)
	}
	if n := len(cbtx.Inputs[0].Script); n < 2 || n > 100 {
		t.Fatalf("scriptSig length %d", n)
	}
	if !bytes.Contains(cbtx.Inputs[0].Script, append(en1, en2...)) {
		t.Fatal("extranonce not in scriptSig")
	}
	if cbtx.Outputs[0].Value != tmpl.CoinbaseValue || !bytes.Equal(cbtx.Outputs[0].Script, script) {
		t.Fatal("payout output")
	}
	if tmpl.WitnessCommitment != nil {
		want, ok := bitcoin.FindWitnessCommitment(cbtx)
		if !ok || len(cbtx.Outputs) != 2 || cbtx.Outputs[1].Value != 0 {
			t.Fatal("witness commitment output missing")
		}
		if len(cbtx.Inputs[0].Witness) != 1 || !bytes.Equal(cbtx.Inputs[0].Witness[0], make([]byte, 32)) {
			t.Fatal("coinbase witness reserved value")
		}
		if bitcoin.WitnessCommitment(wtxids, bitcoin.WitnessReservedValue) != want {
			t.Fatal("assembled witness commitment does not commit to block wtxids")
		}
	} else {
		if len(cbtx.Outputs) != 1 || cbtx.HasWitness {
			t.Fatal("unexpected outputs/witness")
		}
	}
	if len(cbtx.Raw) < p.MinTxSize || len(cbtx.Stripped) < p.MinTxSize {
		t.Fatalf("coinbase %d bytes below minimum %d", len(cbtx.Raw), p.MinTxSize)
	}
	return blk
}

func TestTemplateFromRealSegwitBlock(t *testing.T) {
	b := loadBlock(t, "block_000000000000000000000c835b2adcaedc20fdf6ee440009c249452c726dafae.raw.bz2", true)
	p, _ := ParamsFor(address.BTC)
	raw := templateFromBlock(t, b, 702861, true)
	tmpl, err := NewTemplate(raw, p, "main")
	if err != nil {
		t.Fatal(err)
	}
	if tmpl.TxCount() != len(b.Txs) || tmpl.ExpectedSubsidy != 625000000 {
		t.Fatalf("txcount %d subsidy %d", tmpl.TxCount(), tmpl.ExpectedSubsidy)
	}
	// The branch must rebuild the real block's merkle root with the real coinbase.
	if bitcoin.RootFromBranch(b.Txs[0].TxID, tmpl.Branch) != b.Header.MerkleRoot {
		t.Fatal("branch does not rebuild real merkle root")
	}
	// Rebuild the real header from its parts the way a share is validated.
	h := bitcoin.Header{Version: b.Header.Version, PrevBlock: tmpl.PrevHash, MerkleRoot: bitcoin.RootFromBranch(b.Txs[0].TxID, tmpl.Branch),
		Time: b.Header.Time, Bits: tmpl.Bits, Nonce: b.Header.Nonce}
	if h.Hash().String() != "000000000000000000000c835b2adcaedc20fdf6ee440009c249452c726dafae" {
		t.Fatal("rebuilt header hash mismatch")
	}
	if !bitcoin.HashMeetsTarget(h.Hash(), tmpl.Target) {
		t.Fatal("real block does not meet template target")
	}
	checkAssembledBlock(t, tmpl, p, mustHex("0014751e76e8199196d454941c45d1b3a323f1433bd6"))

	// Tampering is detected.
	bad := *raw
	bad.Transactions = append([]node.TemplateTx(nil), raw.Transactions...)
	bad.Transactions[5].TxID = raw.Transactions[6].TxID
	if _, err := NewTemplate(&bad, p, "main"); err == nil {
		t.Fatal("wrong txid accepted")
	}
	bad = *raw
	bad.DefaultWitnessCommitment = raw.DefaultWitnessCommitment[:len(raw.DefaultWitnessCommitment)-2] + "00"
	if _, err := NewTemplate(&bad, p, "main"); err == nil {
		t.Fatal("wrong witness commitment accepted")
	}
	bad = *raw
	bad.Target = "00000000ffff0000000000000000000000000000000000000000000000000000"
	if _, err := NewTemplate(&bad, p, "main"); err == nil {
		t.Fatal("target/bits mismatch accepted")
	}
}

func TestTemplateFromRealBCHBlock(t *testing.T) {
	b := loadBlock(t, "bch_block877227.raw.bz2", false)
	p, _ := ParamsFor(address.BCH)
	raw := templateFromBlock(t, b, 877227, false)
	// Shuffle: the engine must restore canonical order (CTOR).
	rng := rand.New(rand.NewSource(1))
	rng.Shuffle(len(raw.Transactions), func(i, j int) {
		raw.Transactions[i], raw.Transactions[j] = raw.Transactions[j], raw.Transactions[i]
	})
	tmpl, err := NewTemplate(raw, p, "main")
	if err != nil {
		t.Fatal(err)
	}
	for i, tx := range b.Txs[1:] {
		if tmpl.TxIDs[i] != tx.TxID {
			t.Fatalf("CTOR order differs from real block at %d", i)
		}
	}
	if bitcoin.RootFromBranch(b.Txs[0].TxID, tmpl.Branch) != b.Header.MerkleRoot {
		t.Fatal("branch does not rebuild real merkle root")
	}
	blk := checkAssembledBlock(t, tmpl, p, mustHex("76a91465a16059864a2fdbc7c99a4723a8395bc6f188eb88ac"))
	for i := 2; i < len(blk.Txs); i++ {
		if !bitcoin.CTORLess(blk.Txs[i-1].TxID, blk.Txs[i].TxID) {
			t.Fatal("assembled block violates CTOR")
		}
	}
	// A segwit-style commitment in a BCH template is refused.
	raw.DefaultWitnessCommitment = "6a24aa21a9ed" + hex.EncodeToString(make([]byte, 32))
	if _, err := NewTemplate(raw, p, "main"); err == nil {
		t.Fatal("BCH template with witness commitment accepted")
	}
}

func TestPreSegwitTemplate(t *testing.T) {
	b := loadBlock(t, "block413567.raw.bz2", true)
	p, _ := ParamsFor(address.BTC)
	raw := templateFromBlock(t, b, 413567, false)
	tmpl, err := NewTemplate(raw, p, "main")
	if err != nil {
		t.Fatal(err)
	}
	if bitcoin.RootFromBranch(b.Txs[0].TxID, tmpl.Branch) != b.Header.MerkleRoot {
		t.Fatal("branch")
	}
	checkAssembledBlock(t, tmpl, p, mustHex("76a91465a16059864a2fdbc7c99a4723a8395bc6f188eb88ac"))
}

func TestEmptyTemplateRegtest(t *testing.T) {
	// Exactly what Bitcoin Core 28 returns on a fresh regtest chain.
	raw := &node.BlockTemplate{
		Version: 536870912, Rules: []string{"csv", "!segwit", "taproot"},
		PreviousBlockHash: "0f9188f13cb7b2c71f2a335e3a4fc328bf5beb436012afca590b1a11466e2206",
		CoinbaseValue:     5000000000, Target: "7fffff0000000000000000000000000000000000000000000000000000000000",
		MinTime: 1296688603, CurTime: 1791430774, Bits: "207fffff", Height: 1,
		DefaultWitnessCommitment: "6a24aa21a9ede2f61c3f71d1defd3fa999dfa36953755c690689799962b48bebd836974e8cf9",
	}
	for _, c := range []address.Coin{address.BTC, address.BCH} {
		p, _ := ParamsFor(c)
		r := *raw
		if c == address.BCH {
			r.DefaultWitnessCommitment, r.Rules = "", nil
		}
		tmpl, err := NewTemplate(&r, p, "regtest")
		if err != nil {
			t.Fatal(err)
		}
		if len(tmpl.Branch) != 0 || tmpl.ExpectedSubsidy != 5000000000 {
			t.Fatal("empty template branch/subsidy")
		}
		blk := checkAssembledBlock(t, tmpl, p, mustHex("0014cc765f42f9c1bc0d719b1860cb7805c3784299b7"))
		// BIP34 at height 1 is OP_1 (CScript() << 1), not a data push.
		if blk.Txs[0].Inputs[0].Script[0] != bitcoin.Op1 {
			t.Fatalf("height 1 encoding %x", blk.Txs[0].Inputs[0].Script[0])
		}
	}
}

func TestCoinbaseLimits(t *testing.T) {
	base := CoinbaseParams{Height: 800000, Extranonce2Size: 8, PayoutScript: []byte{0x51}, Value: 1}
	// Maximum tag that still fits in 100 bytes: 100 - 4 (height) - 2 (push hdr) - 1 - 12.
	base.Tag = bytes.Repeat([]byte{'a'}, 100-4-2-1-12)
	if _, err := BuildCoinbase(base); err != nil {
		t.Fatal(err)
	}
	base.Tag = append(base.Tag, 'b')
	if _, err := BuildCoinbase(base); err == nil {
		t.Fatal("oversized scriptSig accepted")
	}
	// BCH minimum size padding with an empty tag and a tiny output script.
	cb, err := BuildCoinbase(CoinbaseParams{Height: 2, Extranonce2Size: 4, PayoutScript: []byte{0x51}, Value: 1, MinTxSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	// The first padding byte also adds a push opcode, so 100 or 101.
	if n := len(cb.Tx(make([]byte, 4), make([]byte, 4))); n < 100 || n > 101 {
		t.Fatalf("padded coinbase is %d bytes, want 100..101", n)
	}
}

func TestStratumPrevHash(t *testing.T) {
	// Display hash -> notify encoding as used by slush's Stratum docs
	// (prevhash 4d16b6f85af6e2198f44ae2a6de67f78487ae5611b77c6c0440b921e00000000).
	h, _ := bitcoin.HashFromDisplay("00000000440b921e1b77c6c0487ae5616de67f788f44ae2a5af6e2194d16b6f8")
	if got := StratumPrevHash(h); got != "4d16b6f85af6e2198f44ae2a6de67f78487ae5611b77c6c0440b921e00000000" {
		t.Fatal(got)
	}
}

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func TestMinShareTime(t *testing.T) {
	raw := &node.BlockTemplate{
		Version: 536870912, PreviousBlockHash: "0f9188f13cb7b2c71f2a335e3a4fc328bf5beb436012afca590b1a11466e2206",
		CoinbaseValue: 5000000000, MinTime: 1000, CurTime: 5000, Bits: "1d00ffff", Height: 10,
	}
	p, _ := ParamsFor(address.BCH)
	for chain, want := range map[string]uint32{"main": 1000, "regtest": 1000, "chip": 5000, "test4": 5000, "test": 5000} {
		tmpl, err := NewTemplate(raw, p, chain)
		if err != nil {
			t.Fatal(err)
		}
		if tmpl.MinShareTime != want {
			t.Errorf("%s: MinShareTime %d want %d", chain, tmpl.MinShareTime, want)
		}
	}
}
