package stratum

import (
	"math"
	"time"
)

// VardiffConfig configures per-connection difficulty adjustment. Min, Max,
// TargetShare and FixedDiff seed the live DiffSettings; the rest is static.
type VardiffConfig struct {
	Initial     float64
	Min         float64
	Max         float64
	TargetShare time.Duration // desired time between shares
	FixedDiff   float64       // > 0 disables vardiff
	Retarget    time.Duration // evaluation window
	VariancePct float64       // only retarget when off by more than this
}

// vardiff estimates a miner's hashrate from accepted share difficulty and
// picks a difficulty that yields one share per TargetShare.
type vardiff struct {
	cfg         VardiffConfig
	windowStart time.Time
	shares      int
	sumDiff     float64
}

func newVardiff(cfg VardiffConfig, now time.Time) *vardiff {
	return &vardiff{cfg: cfg, windowStart: now}
}

func (v *vardiff) reset(now time.Time) {
	v.windowStart, v.shares, v.sumDiff = now, 0, 0
}

// fastRampShares triggers an early retarget when a miner is far too fast for
// its difficulty (e.g. a 100 TH/s machine starting at the initial difficulty).
const fastRampShares = 20

// onShare records an accepted share credited at diff while the connection's
// difficulty is cur. It returns a new difficulty and true when it should change.
func (v *vardiff) onShare(now time.Time, diff, cur float64, ds DiffSettings) (float64, bool) {
	v.shares++
	v.sumDiff += diff
	elapsed := now.Sub(v.windowStart)
	if elapsed < v.cfg.Retarget && v.shares < fastRampShares {
		return cur, false
	}
	secs := math.Max(elapsed.Seconds(), 1)
	// hashrate ≈ sumDiff·2^32/secs; difficulty for one share per target:
	// hashrate·target/2^32 = sumDiff·target/secs.
	want := v.sumDiff * ds.TargetSeconds / secs
	v.reset(now)
	return v.decide(want, cur, ds)
}

// onTick lowers the difficulty of a connection that has gone quiet: no share
// in two retarget windows means the hashrate is far below what cur assumes.
func (v *vardiff) onTick(now time.Time, cur float64, ds DiffSettings) (float64, bool) {
	elapsed := now.Sub(v.windowStart)
	if v.shares > 0 || elapsed < 2*v.cfg.Retarget {
		return cur, false
	}
	factor := math.Max(ds.TargetSeconds/elapsed.Seconds(), 0.25)
	v.reset(now)
	return v.decide(cur*factor, cur, ds)
}

func (v *vardiff) decide(want, cur float64, ds DiffSettings) (float64, bool) {
	want = ds.Clamp(want)
	if cur > 0 && math.Abs(want/cur-1) <= v.cfg.VariancePct/100 {
		return cur, false
	}
	return want, want != cur
}
