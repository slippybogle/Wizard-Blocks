package bitcoin

import (
	"errors"
	"math"
	"math/big"
)

var (
	// Diff1Target is the difficulty-1 target used by Stratum ("bdiff"):
	// 0x00000000FFFF0000000000000000000000000000000000000000000000000000,
	// i.e. the target encoded by compact bits 0x1d00ffff.
	Diff1Target = new(big.Int).Lsh(big.NewInt(0xffff), 208)

	maxTarget = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
)

// CompactToTarget decodes nBits (arith_uint256::SetCompact). Negative or
// overflowing encodings are rejected, as consensus does in CheckProofOfWork.
func CompactToTarget(bits uint32) (*big.Int, error) {
	exp := uint(bits >> 24)
	mant := bits & 0x007fffff
	var t *big.Int
	if exp <= 3 {
		t = big.NewInt(int64(mant >> (8 * (3 - exp))))
	} else {
		t = new(big.Int).Lsh(big.NewInt(int64(mant)), 8*(exp-3))
	}
	if mant != 0 && bits&0x00800000 != 0 {
		return nil, errors.New("negative compact target")
	}
	if mant != 0 && (exp > 34 || (mant > 0xff && exp > 33) || (mant > 0xffff && exp > 32)) {
		return nil, errors.New("overflowing compact target")
	}
	if t.Sign() == 0 {
		return nil, errors.New("zero compact target")
	}
	return t, nil
}

// HashToBig interprets an internal-order hash as the little-endian 256-bit
// integer that consensus compares against the target.
func HashToBig(h Hash) *big.Int {
	var be [32]byte
	for i := 0; i < 32; i++ {
		be[i] = h[31-i]
	}
	return new(big.Int).SetBytes(be[:])
}

// HashMeetsTarget reports whether hash <= target (CheckProofOfWork).
func HashMeetsTarget(h Hash, target *big.Int) bool {
	return HashToBig(h).Cmp(target) <= 0
}

// TargetFromDifficulty returns floor(Diff1Target / diff), clamped to
// [1, 2^256-1]. diff must be positive and finite.
func TargetFromDifficulty(diff float64) *big.Int {
	if !(diff > 0) || math.IsInf(diff, 0) || math.IsNaN(diff) {
		return new(big.Int).Set(maxTarget)
	}
	num := new(big.Float).SetPrec(512).SetInt(Diff1Target)
	den := new(big.Float).SetPrec(512).SetFloat64(diff)
	q, _ := new(big.Float).SetPrec(512).Quo(num, den).Int(nil)
	if q.Cmp(maxTarget) > 0 {
		return new(big.Int).Set(maxTarget)
	}
	if q.Sign() <= 0 {
		return big.NewInt(1)
	}
	return q
}

// DifficultyFromTarget returns Diff1Target / target as a float64.
func DifficultyFromTarget(target *big.Int) float64 {
	if target.Sign() <= 0 {
		return math.Inf(1)
	}
	num := new(big.Float).SetPrec(256).SetInt(Diff1Target)
	den := new(big.Float).SetPrec(256).SetInt(target)
	f, _ := new(big.Float).Quo(num, den).Float64()
	return f
}

// HashDifficulty returns the difficulty a hash achieves (Diff1Target / hash).
func HashDifficulty(h Hash) float64 {
	v := HashToBig(h)
	if v.Sign() == 0 {
		return math.Inf(1)
	}
	return DifficultyFromTarget(v)
}
