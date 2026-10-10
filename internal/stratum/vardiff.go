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
	// Until its first full retarget a connection is probed downward (see
	// onTick); probeFrom/probeDiff are where its silence started.
	retargeted bool
	probeFrom  time.Time
	probeDiff  float64
}

func newVardiff(cfg VardiffConfig, now time.Time) *vardiff {
	return &vardiff{cfg: cfg, windowStart: now}
}

func (v *vardiff) reset(now time.Time) {
	v.windowStart, v.shares, v.sumDiff = now, 0, 0
	v.probeFrom = time.Time{}
}

const (
	// fastRampShares triggers an early retarget when a miner is far too
	// fast for its difficulty (e.g. a rented 1 EH/s arriving at the start
	// difficulty).
	fastRampShares = 20
	// minRampSecs bounds the hashrate estimate of such a flood: 20 shares
	// in under 1 ms raise the difficulty up to 200000x in one step.
	minRampSecs = 0.001
	// probeQuietTargets: a connection with no share yet whose miner is too
	// slow for the start difficulty gets it halved every target interval
	// once it has been silent for this many target intervals.
	probeQuietTargets = 3
)

// onShare records an accepted share credited at diff while the connection's
// difficulty is cur. It returns a new difficulty and true when it should change.
func (v *vardiff) onShare(now time.Time, diff, cur float64, ds DiffSettings) (float64, bool) {
	v.shares++
	v.sumDiff += diff
	elapsed := now.Sub(v.windowStart)
	if elapsed < v.cfg.Retarget && v.shares < fastRampShares {
		return cur, false
	}
	secs := math.Max(elapsed.Seconds(), minRampSecs)
	// hashrate ≈ sumDiff·2^32/secs; difficulty for one share per target:
	// hashrate·target/2^32 = sumDiff·target/secs.
	want := v.sumDiff * ds.TargetSeconds / secs
	v.reset(now)
	v.retargeted = true
	return v.decide(want, cur, ds)
}

// onTick lowers the difficulty of a connection that has gone quiet: no share
// in two retarget windows means the hashrate is far below what cur assumes.
func (v *vardiff) onTick(now time.Time, cur float64, ds DiffSettings) (float64, bool) {
	if !v.retargeted {
		// A new connection, before its first full retarget: nothing says
		// the start difficulty fits, so a miner far below it is not left
		// waiting minutes per share.
		quietFor := probeQuietTargets * ds.TargetSeconds
		if v.shares == 0 {
			// Silent so far: halve every target interval.
			if v.probeFrom.IsZero() {
				v.probeFrom, v.probeDiff = v.windowStart, cur
			}
			quiet := now.Sub(v.probeFrom).Seconds() - quietFor
			if quiet < 0 {
				return cur, false
			}
			want := ds.Clamp(v.probeDiff * math.Pow(0.5, math.Floor(quiet/ds.TargetSeconds)+1))
			return want, want < cur
		}
		// A few shares: step down once they show at least 2x too slow.
		if el := now.Sub(v.windowStart).Seconds(); el >= quietFor {
			if want := ds.Clamp(v.sumDiff * ds.TargetSeconds / el); want < cur/2 {
				v.reset(now)
				return want, true
			}
		}
		return cur, false
	}
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
