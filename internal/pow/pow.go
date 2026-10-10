// Package pow describes a proof-of-work algorithm: how a block header is
// hashed for the work check, and what Stratum share difficulty 1 means.
//
// SHA-256d (BTC, BCH): share difficulty 1 is the Bitcoin difficulty-1 target
// 0x00000000ffff0000…, worth 2^32 hashes.
//
// Scrypt (LTC, DOGE): the PoW hash is scrypt(header, header, N=1024, r=1,
// p=1, 32 bytes). Scrypt Stratum uses a difficulty-1 target 2^16 times
// easier, 0x0000ffff0000…, so one share at difficulty d is worth d·2^16
// hashes. Scrypt ASICs expect exactly this meaning of mining.set_difficulty.
// Nodes still report difficulty against the Bitcoin difficulty-1 target, so a
// network difficulty in share units is the node's value × 65536.
//
// The block's identity hash (prev-hash links, explorers, submitblock) is
// always double SHA-256; only the work check uses the PoW hash.
package pow

import (
	"math"
	"math/big"

	"golang.org/x/crypto/scrypt"

	"github.com/fladnagmai/wizard-blocks/internal/bitcoin"
)

// Algo is one proof-of-work algorithm.
type Algo struct {
	Name string
	// ShareDiff1 is the target of Stratum share difficulty 1.
	ShareDiff1 *big.Int
	// Diff1Hashes is the expected number of hashes for one share at
	// difficulty 1 (2^256 / ShareDiff1, rounded): 2^32 or 2^16.
	Diff1Hashes float64
	// NodeDiffScale converts a node-reported difficulty (Bitcoin diff-1)
	// into share units: 1 for SHA-256d, 65536 for Scrypt.
	NodeDiffScale float64
	hash          func(header []byte) bitcoin.Hash
}

var (
	// SHA256d is Bitcoin's double SHA-256.
	SHA256d = Algo{
		Name: "sha256d", ShareDiff1: bitcoin.Diff1Target, Diff1Hashes: 4294967296, NodeDiffScale: 1,
		hash: bitcoin.DoubleSHA256,
	}
	// Scrypt is Litecoin's scrypt(N=1024, r=1, p=1).
	Scrypt = Algo{
		Name: "scrypt", ShareDiff1: new(big.Int).Lsh(big.NewInt(0xffff), 224), Diff1Hashes: 65536, NodeDiffScale: 65536,
		hash: scryptHash,
	}
)

func scryptHash(header []byte) bitcoin.Hash {
	// N=1024, r=1, p=1 never returns an error.
	k, _ := scrypt.Key(header, header, 1024, 1, 1, 32)
	var h bitcoin.Hash
	copy(h[:], k)
	return h
}

// PoWHash returns the hash checked against targets, in internal byte order
// (compared as a little-endian 256-bit number, like bitcoin.HashToBig).
func (a Algo) PoWHash(header []byte) bitcoin.Hash { return a.hash(header) }

var maxTarget = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))

// ShareTarget returns floor(ShareDiff1 / d), clamped to [1, 2^256-1].
func (a Algo) ShareTarget(d float64) *big.Int {
	if !(d > 0) || math.IsInf(d, 0) || math.IsNaN(d) {
		return new(big.Int).Set(maxTarget)
	}
	num := new(big.Float).SetPrec(512).SetInt(a.ShareDiff1)
	den := new(big.Float).SetPrec(512).SetFloat64(d)
	q, _ := new(big.Float).SetPrec(512).Quo(num, den).Int(nil)
	if q.Cmp(maxTarget) > 0 {
		return new(big.Int).Set(maxTarget)
	}
	if q.Sign() <= 0 {
		return big.NewInt(1)
	}
	return q
}

// Difficulty returns a target's difficulty in share units (ShareDiff1 / t).
func (a Algo) Difficulty(t *big.Int) float64 {
	if t.Sign() <= 0 {
		return math.Inf(1)
	}
	num := new(big.Float).SetPrec(256).SetInt(a.ShareDiff1)
	den := new(big.Float).SetPrec(256).SetInt(t)
	f, _ := new(big.Float).Quo(num, den).Float64()
	return f
}

// HashDifficulty returns the share difficulty a PoW hash achieves.
func (a Algo) HashDifficulty(h bitcoin.Hash) float64 {
	v := bitcoin.HashToBig(h)
	if v.Sign() == 0 {
		return math.Inf(1)
	}
	return a.Difficulty(v)
}

var scryptKey = scrypt.Key

// For returns the algorithm of a coin: Scrypt for "ltc" and "doge",
// SHA-256d otherwise.
func For(coin string) Algo {
	switch coin {
	case "ltc", "doge":
		return Scrypt
	}
	return SHA256d
}
