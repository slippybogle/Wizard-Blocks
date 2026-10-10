package address

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

// Real mainnet LTC and DOGE addresses (valid checksums), and the scripts the
// nodes produce for them.
func TestLTCDOGEMainnet(t *testing.T) {
	for _, c := range []struct {
		coin         Coin
		addr, typ, s string
	}{
		{DOGE, "DH5yaieqoZN36fDVciNyRueRGvGLR3mr7L", "p2pkh", "76a914830a7420e63d76244ff7cbd1c248e94c1446325988ac"},
		{DOGE, "DBXu2kgc3xtvCUWFcxFE3r9hEYgmuaaCyD", "p2pkh", "76a9144620b70031f0e9437e374a2100934fba4911046088ac"},
		{LTC, "LTpYZG19YmfvY2bBDYtCKpunVRw7nVgRHW", "p2pkh", "76a9145e4bc6ad3e470db21143a6d71e83cbeb00ee722588ac"},
		{LTC, "LM2WMpR1Rp6j3Sa59cMXMs1SPzj9eXpGc1", "p2pkh", "76a91413c60d8e68d7349f5b4ca362c3954b15045061b188ac"},
		{LTC, "MQMcJhpWHYVeQArcZR3sBgyPZxxRtnH441", "p2sh", "a914b48297bff5dadecc5f36145cec6a5f20d57c8f9b87"},
	} {
		n, err := NetworkFor(c.coin, "main")
		if err != nil {
			t.Fatal(err)
		}
		a, err := Decode(n, c.addr)
		if err != nil {
			t.Fatalf("%s: %v", c.addr, err)
		}
		if a.Type != c.typ || hex.EncodeToString(a.Script) != c.s {
			t.Fatalf("%s: %s %x", c.addr, a.Type, a.Script)
		}
	}
}

func TestLTCAddressForms(t *testing.T) {
	n, _ := NetworkFor(LTC, "main")
	h := bytes.Repeat([]byte{0xab}, 20)
	// Both P2SH versions (M… 0x32 and legacy 3… 0x05) pay the same script.
	m, err := Decode(n, Base58CheckEncode(append([]byte{0x32}, h...)))
	if err != nil {
		t.Fatal(err)
	}
	three, err := Decode(n, Base58CheckEncode(append([]byte{0x05}, h...)))
	if err != nil || !bytes.Equal(m.Script, three.Script) || m.Type != "p2sh" {
		t.Fatalf("legacy p2sh: %v %x %x", err, m.Script, three.Script)
	}
	// Bech32 ltc1 (v0) and bech32m (taproot).
	for _, c := range []struct {
		ver  byte
		prog []byte
		typ  string
	}{{0, h, "p2wpkh"}, {0, bytes.Repeat([]byte{1}, 32), "p2wsh"}, {1, bytes.Repeat([]byte{2}, 32), "p2tr"}} {
		s, err := SegwitEncode("ltc", c.ver, c.prog)
		if err != nil {
			t.Fatal(err)
		}
		a, err := Decode(n, s)
		if err != nil || a.Type != c.typ {
			t.Fatalf("%s: %v %v", s, err, a)
		}
	}
	// Wrong network prefixes and BTC addresses are refused.
	for _, bad := range []string{
		mustSeg(t, "tltc", h), mustSeg(t, "bc", h),
		Base58CheckEncode(append([]byte{0x00}, h...)), // BTC P2PKH
		Base58CheckEncode(append([]byte{0x1e}, h...)), // DOGE P2PKH
	} {
		if _, err := Decode(n, bad); err == nil {
			t.Fatalf("%s accepted as LTC mainnet", bad)
		}
	}
	// MWEB addresses can't receive a coinbase: refused with a clear reason.
	_, err = Decode(n, "ltcmweb1qq0z8dzmxw0yz0q0fyruqa5mwm6sve2y44xn8sauxy2lzrgaxvf2z7qdcnnmqpd7ddga5jvlvqv0ay87e0pfstsqpj7tq4hq9h9awvgf8cwacq8r6aex2vxt9y4g3xxv3txn5fn6ssvl0yqt")
	if err == nil || !strings.Contains(err.Error(), "MWEB") {
		t.Fatalf("MWEB address: %v", err)
	}
	// Regtest uses rltc and 0x3a/0xc4.
	r, _ := NetworkFor(LTC, "regtest")
	if a, err := Decode(r, mustSeg(t, "rltc", h)); err != nil || a.Type != "p2wpkh" {
		t.Fatalf("rltc: %v", err)
	}
}

func TestDOGEAddressForms(t *testing.T) {
	h := bytes.Repeat([]byte{0xcd}, 20)
	for chain, vers := range map[string][2]byte{"main": {0x1e, 0x16}, "test": {0x71, 0xc4}, "regtest": {0x6f, 0xc4}} {
		n, err := NetworkFor(DOGE, chain)
		if err != nil {
			t.Fatal(err)
		}
		if a, err := Decode(n, Base58CheckEncode(append([]byte{vers[0]}, h...))); err != nil || a.Type != "p2pkh" {
			t.Fatalf("%s p2pkh: %v", chain, err)
		}
		if a, err := Decode(n, Base58CheckEncode(append([]byte{vers[1]}, h...))); err != nil || a.Type != "p2sh" {
			t.Fatalf("%s p2sh: %v", chain, err)
		}
		// No segwit on Dogecoin 1.14.
		if _, err := Decode(n, mustSeg(t, "doge", h)); err == nil {
			t.Fatalf("%s: bech32 accepted", chain)
		}
	}
}

func mustSeg(t *testing.T, hrp string, prog []byte) string {
	t.Helper()
	s, err := SegwitEncode(hrp, 0, prog)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
