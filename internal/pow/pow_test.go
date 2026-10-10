package pow

import (
	"encoding/binary"
	"encoding/hex"
	"math"
	"math/big"
	"testing"

	"github.com/fladnagmai/wizard-blocks/internal/bitcoin"
)

// ltcGenesisHeader is Litecoin's genesis block header (80 bytes).
func ltcGenesisHeader(t *testing.T) []byte {
	t.Helper()
	h := make([]byte, 80)
	binary.LittleEndian.PutUint32(h[0:], 1)
	merkle, _ := hex.DecodeString("97ddfbbae6be97fd6cdf3e7ca13232a3afff2353e29badfab7f73011edd4ced9")
	for i := 0; i < 32; i++ { // display order -> internal order
		h[36+i] = merkle[31-i]
	}
	binary.LittleEndian.PutUint32(h[68:], 1317972665)
	binary.LittleEndian.PutUint32(h[72:], 0x1e0ffff0)
	binary.LittleEndian.PutUint32(h[76:], 2084524493)
	return h
}

func TestScryptLitecoinGenesis(t *testing.T) {
	h := ltcGenesisHeader(t)
	if got := bitcoin.DoubleSHA256(h).String(); got != "12a765e31ffd4059bada1e25190f6e98c99d9714d334efa41a195a7e7e04bfe2" {
		t.Fatalf("genesis identity hash %s", got)
	}
	target, err := bitcoin.CompactToTarget(0x1e0ffff0)
	if err != nil {
		t.Fatal(err)
	}
	pw := Scrypt.PoWHash(h)
	if !bitcoin.HashMeetsTarget(pw, target) {
		t.Fatalf("scrypt PoW %s does not meet the genesis target", pw)
	}
	// The same header hashed with SHA-256d does not meet it.
	if bitcoin.HashMeetsTarget(SHA256d.PoWHash(h), target) {
		t.Fatal("sha256d unexpectedly meets the scrypt target")
	}
	// A different nonce must (overwhelmingly) not meet it.
	binary.LittleEndian.PutUint32(h[76:], 2084524494)
	if bitcoin.HashMeetsTarget(Scrypt.PoWHash(h), target) {
		t.Fatal("wrong nonce meets target")
	}
}

func TestShareDifficultyScale(t *testing.T) {
	// Scrypt share difficulty 1 = 0x0000ffff00…: 65536× Bitcoin's diff-1 target.
	if new(big.Int).Div(Scrypt.ShareDiff1, bitcoin.Diff1Target).Int64() != 65536 {
		t.Fatal("scrypt diff1 is not 65536 x bitcoin diff1")
	}
	// The DG Home 1 default difficulty 262144: share target = diff1 / 262144.
	st := Scrypt.ShareTarget(262144)
	if d := Scrypt.Difficulty(st); math.Abs(d-262144)/262144 > 1e-9 {
		t.Fatalf("round trip %v", d)
	}
	// A node difficulty D is D*65536 in scrypt share units.
	nodeTarget, _ := bitcoin.CompactToTarget(0x1a0ffff0)
	nodeDiff := bitcoin.DifficultyFromTarget(nodeTarget)
	if r := Scrypt.Difficulty(nodeTarget) / nodeDiff; math.Abs(r-65536) > 1e-6 {
		t.Fatalf("node->share scale %v", r)
	}
	// Hashes per share: d * Diff1Hashes; 2^256 / ShareDiff1 ≈ Diff1Hashes.
	two256 := new(big.Float).SetInt(new(big.Int).Lsh(big.NewInt(1), 256))
	for _, a := range []Algo{SHA256d, Scrypt} {
		q, _ := new(big.Float).Quo(two256, new(big.Float).SetInt(a.ShareDiff1)).Float64()
		if math.Abs(q-a.Diff1Hashes)/a.Diff1Hashes > 1e-4 {
			t.Fatalf("%s: 2^256/diff1 = %v, Diff1Hashes %v", a.Name, q, a.Diff1Hashes)
		}
	}
	// SHA-256d keeps the existing meaning exactly.
	if SHA256d.ShareTarget(1024).Cmp(bitcoin.TargetFromDifficulty(1024)) != 0 {
		t.Fatal("sha256d share target changed")
	}
}

func TestScryptRFC7914(t *testing.T) {
	// The library itself: RFC 7914 §12 vector (N=16, r=1, p=1, empty input).
	// PoWHash fixes N=1024, so call through scryptHash's dependency directly.
	got := hex.EncodeToString(rfcScrypt(t))
	if got != "77d6576238657b203b19ca42c18a0497f16b4844e3074ae8dfdffa3fede21442fcd0069ded0948f8326a753a0fc81f17e8d3e0fb2e0d3628cf35e20c38d18906" {
		t.Fatalf("scrypt RFC vector: %s", got)
	}
}

func rfcScrypt(t *testing.T) []byte {
	k, err := scryptKey(nil, nil, 16, 1, 1, 64)
	if err != nil {
		t.Fatal(err)
	}
	return k
}
