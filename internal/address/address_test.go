package address

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type keyIOVector struct {
	addr   string
	script string
	chain  string
	priv   bool
}

func loadKeyIO(t *testing.T, file string) []keyIOVector {
	t.Helper()
	b, err := os.ReadFile("testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	var raw [][]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	var out []keyIOVector
	for _, r := range raw {
		var v keyIOVector
		_ = json.Unmarshal(r[0], &v.addr)
		_ = json.Unmarshal(r[1], &v.script)
		var meta struct {
			IsPrivkey bool   `json:"isPrivkey"`
			Chain     string `json:"chain"`
		}
		_ = json.Unmarshal(r[2], &meta)
		v.chain, v.priv = meta.Chain, meta.IsPrivkey
		out = append(out, v)
	}
	return out
}

// Bitcoin Core's src/test/data/key_io_valid.json.
func TestCoreKeyIOValid(t *testing.T) {
	ok, refused := 0, 0
	for _, v := range loadKeyIO(t, "core_key_io_valid.json") {
		if v.priv {
			continue
		}
		n, err := NetworkFor(BTC, v.chain)
		if err != nil {
			t.Fatal(err)
		}
		a, err := Decode(n, v.addr)
		// Witness v1 with non-32-byte programs and v2+ are refused on purpose.
		definedType := strings.HasPrefix(v.script, "0014") && len(v.script) == 44 ||
			strings.HasPrefix(v.script, "0020") && len(v.script) == 68 ||
			strings.HasPrefix(v.script, "5120") && len(v.script) == 68 ||
			strings.HasPrefix(v.script, "76a914") || strings.HasPrefix(v.script, "a914")
		if !definedType {
			if err == nil {
				t.Errorf("%s: undefined witness output accepted", v.addr)
			}
			refused++
			continue
		}
		if err != nil {
			t.Errorf("%s (%s): %v", v.addr, v.chain, err)
			continue
		}
		if hex.EncodeToString(a.Script) != v.script {
			t.Errorf("%s: script %x want %s", v.addr, a.Script, v.script)
		}
		// The same address must be rejected by every other network family.
		for _, other := range []string{"main", "regtest", "test"} {
			o, _ := NetworkFor(BTC, other)
			isSegwit := strings.HasPrefix(v.script, "00") || strings.HasPrefix(v.script, "51")
			if (isSegwit && o.Bech32HRP == n.Bech32HRP) || (!isSegwit && o.PubKeyHash == n.PubKeyHash) {
				continue // testnet and regtest share base58 version bytes
			}
			if _, err := Decode(o, v.addr); err == nil {
				t.Errorf("%s accepted on %s", v.addr, other)
			}
		}
		ok++
	}
	if ok < 30 || refused == 0 {
		t.Fatalf("too few vectors checked: ok=%d refused=%d", ok, refused)
	}
}

func TestCoreKeyIOInvalid(t *testing.T) {
	b, _ := os.ReadFile("testdata/core_key_io_invalid.json")
	var raw [][]string
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, r := range raw {
		for _, chain := range []string{"main", "test", "regtest", "signet"} {
			n, _ := NetworkFor(BTC, chain)
			if a, err := Decode(n, r[0]); err == nil {
				t.Errorf("invalid %q accepted on %s as %x", r[0], chain, a.Script)
			}
		}
	}
}

// Bitcoin Cash Node's src/test/data/key_io_valid.json (legacy base58 incl. P2SH32).
func TestBCHNKeyIOValid(t *testing.T) {
	ok := 0
	for _, v := range loadKeyIO(t, "bchn_key_io_valid.json") {
		if v.priv {
			continue
		}
		chains := []string{v.chain}
		if v.chain == "test" {
			chains = append(chains, "regtest")
		}
		for _, chain := range chains {
			n, err := NetworkFor(BCH, chain)
			if err != nil {
				t.Fatal(err)
			}
			a, err := Decode(n, v.addr)
			if err != nil {
				t.Errorf("%s: %v", v.addr, err)
				continue
			}
			if hex.EncodeToString(a.Script) != v.script {
				t.Errorf("%s: script %x want %s", v.addr, a.Script, v.script)
			}
			ok++
		}
	}
	if ok < 20 {
		t.Fatalf("only %d vectors", ok)
	}
}

func TestBCHNKeyIOInvalid(t *testing.T) {
	b, _ := os.ReadFile("testdata/bchn_key_io_invalid.json")
	var raw [][]string
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, r := range raw {
		for _, chain := range []string{"main", "test", "regtest"} {
			n, _ := NetworkFor(BCH, chain)
			if _, err := Decode(n, r[0]); err == nil {
				t.Errorf("invalid %q accepted on %s", r[0], chain)
			}
		}
	}
}

// BCHN src/test/data/cashaddr_token_types.json: checksum, type and payload
// decoding for every type/size combination, plus round-trip encoding.
func TestCashAddrTokenTypes(t *testing.T) {
	b, _ := os.ReadFile("testdata/bchn_cashaddr_token_types.json")
	var vs []struct {
		PayloadSize int    `json:"payloadSize"`
		Type        byte   `json:"type"`
		CashAddr    string `json:"cashaddr"`
		Payload     string `json:"payload"`
	}
	if err := json.Unmarshal(b, &vs); err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		prefix, typ, hash, err := CashAddrDecode(v.CashAddr, "")
		if err != nil {
			t.Errorf("%s: %v", v.CashAddr, err)
			continue
		}
		if typ != v.Type || !strings.EqualFold(hex.EncodeToString(hash), v.Payload) || len(hash) != v.PayloadSize {
			t.Errorf("%s: type %d payload %x", v.CashAddr, typ, hash)
		}
		enc, err := CashAddrEncode(prefix, typ, hash)
		if err != nil || enc != strings.ToLower(v.CashAddr) {
			t.Errorf("re-encode %s -> %s %v", v.CashAddr, enc, err)
		}
		// Payout decoding only accepts the standard script forms.
		n := &Network{Coin: BCH, Chain: "x", CashPrefix: prefix, PubKeyHash: 0, ScriptHash: 5}
		a, err := Decode(n, v.CashAddr)
		std := (v.Type == 0 || v.Type == 2) && v.PayloadSize == 20 ||
			(v.Type == 1 || v.Type == 3) && (v.PayloadSize == 20 || v.PayloadSize == 32)
		if std != (err == nil) {
			t.Errorf("%s: standard=%v err=%v", v.CashAddr, std, err)
		}
		if err == nil {
			h := strings.ToLower(v.Payload)
			var want string
			switch {
			case v.Type == 0 || v.Type == 2:
				want = "76a914" + h + "88ac"
			case v.PayloadSize == 20:
				want = "a914" + h + "87"
			default:
				want = "aa20" + h + "87"
			}
			if hex.EncodeToString(a.Script) != want {
				t.Errorf("%s: script %x want %s", v.CashAddr, a.Script, want)
			}
		}
	}
}

// Spec examples from the CashAddr specification
// (github.com/bitcoincashorg/bitcoincash.org spec/cashaddr.md).
func TestCashAddrSpec(t *testing.T) {
	n, _ := NetworkFor(BCH, "main")
	cases := map[string]string{
		"bitcoincash:qr6m7j9njldwwzlg9v7v53unlr4jkmx6eylep8ekg2": "76a914f5bf48b397dae70be82b3cca4793f8eb2b6cdac988ac",
		"bitcoincash:pr6m7j9njldwwzlg9v7v53unlr4jkmx6eyguug74nh": "a914f5bf48b397dae70be82b3cca4793f8eb2b6cdac987",
		"qr6m7j9njldwwzlg9v7v53unlr4jkmx6eylep8ekg2":             "76a914f5bf48b397dae70be82b3cca4793f8eb2b6cdac988ac",
		"BITCOINCASH:QR6M7J9NJLDWWZLG9V7V53UNLR4JKMX6EYLEP8EKG2": "76a914f5bf48b397dae70be82b3cca4793f8eb2b6cdac988ac",
	}
	for s, want := range cases {
		a, err := Decode(n, s)
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if hex.EncodeToString(a.Script) != want {
			t.Fatalf("%s: %x", s, a.Script)
		}
	}
	bad := []string{
		"bitcoincash:qr6m7j9njldwwzlg9v7v53unlr4jkmx6eylep8ekg3", // checksum
		"bitcoincash:Qr6m7j9njldwwzlg9v7v53unlr4jkmx6eylep8ekg2", // mixed case
		"bchtest:qr6m7j9njldwwzlg9v7v53unlr4jkmx6eylep8ekg2",     // wrong prefix
		"bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4",             // BTC address
	}
	for _, s := range bad {
		if _, err := Decode(n, s); err == nil {
			t.Fatalf("%s accepted", s)
		}
	}
}

// BIP173 / BIP350 test vectors.
func TestBech32Vectors(t *testing.T) {
	valid := map[string]string{
		"BC1QW508D6QEJXTDG4Y5R3ZARVARY0C5XW7KV8F3T4":                     "0014751e76e8199196d454941c45d1b3a323f1433bd6",
		"bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4":                     "0014751e76e8199196d454941c45d1b3a323f1433bd6",
		"bc1p0xlxvlhemja6c4dqv22uapctqupfhlxm9h8z3k2e72q4k9hcz7vqzk5jj0": "512079be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798",
	}
	n, _ := NetworkFor(BTC, "main")
	for s, want := range valid {
		a, err := Decode(n, s)
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if hex.EncodeToString(a.Script) != want {
			t.Fatalf("%s: %x", s, a.Script)
		}
	}
	tn, _ := NetworkFor(BTC, "test")
	if a, err := Decode(tn, "tb1qrp33g0q5c5txsp9arysrx4k6zdkfs4nce4xj0gdcccefvpysxf3q0sl5k7"); err != nil ||
		hex.EncodeToString(a.Script) != "00201863143c14c5166804bd19203356da136c985678cd4d27a1b8c6329604903262" {
		t.Fatalf("p2wsh testnet: %v", err)
	}
	invalid := []string{
		"bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t5",                     // bad checksum
		"bc1p0xlxvlhemja6c4dqv22uapctqupfhlxm9h8z3k2e72q4k9hcz7vqh2y7hd", // v1 with bech32 checksum (BIP350)
		"BC1S0XLXVLHEMJA6C4DQV22UAPCTQUPFHLXM9H8Z3K2E72Q4K9HCZ7VQ54WELL", // v16, refused
		"bc1zw508d6qejxtdg4y5r3zarvaryvaxxpcs",                           // v2, refused
		"bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4\x00",
		"tb1qrp33g0q5c5txsp9arysrx4k6zdkfs4nce4xj0gdcccefvpysxf3q0sl5k7", // wrong network
		"bc1qr508d6qejxtdg4y5r3zarvaryvq37j7u2",                          // bad v0 length
		"bc1zw508d6qejxtdg4y5r3zarvaryvqyzf3du",                          // invalid padding
		"BC1QW508d6QEJXTDG4Y5R3ZARVARY0C5XW7KV8F3T4",                     // mixed case
	}
	for _, s := range invalid {
		if _, err := Decode(n, s); err == nil {
			t.Errorf("%q accepted", s)
		}
	}
}

func TestRoundTrips(t *testing.T) {
	prog := make([]byte, 32)
	for i := range prog {
		prog[i] = byte(i * 7)
	}
	s, err := SegwitEncode("bcrt", 1, prog)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := NetworkFor(BTC, "regtest")
	a, err := Decode(n, s)
	if err != nil || a.Type != "p2tr" {
		t.Fatalf("%s %v", s, err)
	}
	b58 := Base58CheckEncode(append([]byte{0x6f}, prog[:20]...))
	a, err = Decode(n, b58)
	if err != nil || a.Type != "p2pkh" {
		t.Fatalf("%s %v", b58, err)
	}
	bn, _ := NetworkFor(BCH, "regtest")
	ca, _ := CashAddrEncode("bchreg", 1, prog)
	a, err = Decode(bn, ca)
	if err != nil || a.Type != "p2sh32" {
		t.Fatalf("%s %v", ca, err)
	}
}

func FuzzDecode(f *testing.F) {
	f.Add("bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4")
	f.Add("bitcoincash:qr6m7j9njldwwzlg9v7v53unlr4jkmx6eylep8ekg2")
	f.Add("1AGNa15ZQXAZUgFiqJ2i7Z2DPU2J6hW62i")
	nets := []*Network{}
	for _, c := range []Coin{BTC, BCH} {
		n, _ := NetworkFor(c, "main")
		nets = append(nets, n)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for _, n := range nets {
			_, _ = Decode(n, s)
		}
	})
}

func TestBCHNChainNames(t *testing.T) {
	// Values of getblockchaininfo.chain reported by BCHN 29 (verified live).
	for chain, prefix := range map[string]string{"main": "bitcoincash", "test": "bchtest", "test4": "bchtest",
		"chip": "bchtest", "scale": "bchtest", "regtest": "bchreg"} {
		n, err := NetworkFor(BCH, chain)
		if err != nil || n.CashPrefix != prefix {
			t.Errorf("%s: %v %v", chain, n, err)
		}
	}
}
