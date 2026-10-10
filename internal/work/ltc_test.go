package work

import (
	"bytes"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/fladnagmai/wizard-blocks/internal/address"
	"github.com/fladnagmai/wizard-blocks/internal/bitcoin"
	"github.com/fladnagmai/wizard-blocks/internal/node"
)

func loadLTCBlock(t *testing.T, name string) ([]byte, *bitcoin.Block) {
	t.Helper()
	raw, err := os.ReadFile("../bitcoin/testdata/ltc/" + name)
	if err != nil {
		t.Fatal(err)
	}
	b, err := bitcoin.ParseBlockMWEB(raw)
	if err != nil {
		t.Fatal(err)
	}
	return raw, b
}

// A real testnet4 MWEB block, presented as the template a Litecoin node
// would have returned for it (its transactions, the HogEx last, and the
// "mweb" field), goes through the engine's template checks and block
// assembly. Everything after our own coinbase must be byte for byte the
// real block: the transactions, the 0x01 flag and the MWEB block.
func TestLTCTemplateFromRealMWEBBlocks(t *testing.T) {
	p, err := ParamsFor(address.LTC)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"testnet4Block2215584.dat", "testnet4Block2215586.dat", "testnet4Block2319633.dat", "testnet4Block2321749.dat"} {
		t.Run(name, func(t *testing.T) {
			raw, b := loadLTCBlock(t, name)
			height, err := bitcoin.DecodeBIP34Height(b.Txs[0].Inputs[0].Script)
			if err != nil {
				t.Fatal(err)
			}
			gbt := templateFromBlock(t, b, height, true)
			gbt.Rules = []string{"csv", "!segwit", "taproot", "!mweb"}
			gbt.MWEB = hex.EncodeToString(b.MWEB)
			tmpl, err := NewTemplate(gbt, p, "test")
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(tmpl.MWEB, b.MWEB) {
				t.Fatal("template MWEB differs from the block's")
			}
			got := checkAssembledBlock(t, tmpl, p, []byte{0x00, 0x14, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20})
			if !got.Txs[len(got.Txs)-1].HogEx || !bytes.Equal(got.MWEB, b.MWEB) {
				t.Fatal("assembled block lost the HogEx or the MWEB block")
			}
			// Byte for byte after the coinbase.
			realTail := raw[bitcoin.HeaderSize+len(bitcoin.AppendVarInt(nil, uint64(len(b.Txs))))+len(b.Txs[0].Raw):]
			var ours []byte
			for _, tx := range got.Txs[1:] {
				ours = append(ours, tx.Raw...)
			}
			ours = append(append(ours, 0x01), got.MWEB...)
			if !bytes.Equal(ours, realTail) {
				t.Fatalf("assembled block differs from the real one after the coinbase (%d vs %d bytes)", len(ours), len(realTail))
			}
		})
	}
}

// The HogEx and the "mweb" field must come together, with the HogEx last.
func TestLTCTemplateMWEBConsistency(t *testing.T) {
	p, _ := ParamsFor(address.LTC)
	_, b := loadLTCBlock(t, "testnet4Block2321749.dat")
	height, _ := bitcoin.DecodeBIP34Height(b.Txs[0].Inputs[0].Script)
	good := func() *node.BlockTemplate {
		g := templateFromBlock(t, b, height, true)
		g.MWEB = hex.EncodeToString(b.MWEB)
		return g
	}

	noMWEB := good()
	noMWEB.MWEB = ""
	if _, err := NewTemplate(noMWEB, p, "test"); err == nil || !strings.Contains(err.Error(), "no mweb block") {
		t.Fatalf("HogEx without mweb: %v", err)
	}
	noHogEx := good()
	noHogEx.Transactions = noHogEx.Transactions[:len(noHogEx.Transactions)-1]
	noHogEx.DefaultWitnessCommitment = ""
	noHogEx.Rules = nil
	if _, err := NewTemplate(noHogEx, p, "test"); err == nil || !strings.Contains(err.Error(), "not a HogEx") {
		t.Fatalf("mweb without HogEx: %v", err)
	}
	moved := good()
	n := len(moved.Transactions)
	moved.Transactions[0], moved.Transactions[n-1] = moved.Transactions[n-1], moved.Transactions[0]
	if _, err := NewTemplate(moved, p, "test"); err == nil || !strings.Contains(err.Error(), "not the last") {
		t.Fatalf("HogEx not last: %v", err)
	}
	badHex := good()
	badHex.MWEB = "zz"
	if _, err := NewTemplate(badHex, p, "test"); err == nil {
		t.Fatal("bad mweb hex accepted")
	}
	// BTC parameters never accept an mweb block.
	btc, _ := ParamsFor(address.BTC)
	if _, err := NewTemplate(good(), btc, "test"); err == nil {
		t.Fatal("BTC accepted an MWEB template")
	}
}
