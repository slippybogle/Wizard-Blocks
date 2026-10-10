package stratum

import (
	"fmt"
	"math"
	"math/rand/v2"
	"testing"
	"time"
)

// rampResult is how one simulated rental connection fared while vardiff
// raised its difficulty from the start difficulty.
type rampResult struct {
	settle       time.Duration // until difficulty is within 30% of ideal
	shares       int           // shares sent before settling
	disconnected bool          // the message rate limiter would have closed it
	final        float64
}

// simulateRamp runs vardiff for one connection of hashrate hps whose shares
// arrive as a Poisson process at hps/(diff·diff1Hashes); a new difficulty
// reaches the miner rtt later (shares meanwhile are at the old one). The
// token bucket is the session's (rate msgRate/s, burst msgBurst).
func simulateRamp(hps, diff1Hashes, start float64, rtt time.Duration, msgRate, msgBurst float64, seed uint64) rampResult {
	rng := rand.New(rand.NewPCG(seed, 1))
	cfg := VardiffConfig{Retarget: 60 * time.Second, VariancePct: 30}
	ds := DiffSettings{Min: 1, Max: DiffCeiling, TargetSeconds: 10}
	ideal := ds.Clamp(hps * ds.TargetSeconds / diff1Hashes)
	t0 := time.Unix(0, 0)
	now := t0
	v := newVardiff(cfg, now)
	cur, minerDiff := start, start
	var pendingAt time.Time
	pending := 0.0
	tokens, tokensAt := msgBurst, now
	res := rampResult{}
	for now.Sub(t0) < 10*time.Minute {
		now = now.Add(time.Duration(rng.ExpFloat64() / (hps / (minerDiff * diff1Hashes)) * float64(time.Second)))
		if pending > 0 && !now.Before(pendingAt) {
			minerDiff, pending = pending, 0
			// The share just found was at the old difficulty; close enough.
		}
		tokens = math.Min(msgBurst, tokens+now.Sub(tokensAt).Seconds()*msgRate)
		tokensAt = now
		if tokens < 1 {
			res.disconnected = true
			res.final = cur
			return res
		}
		tokens--
		res.shares++
		if nd, ok := v.onShare(now, minerDiff, cur, ds); ok {
			cur = nd
			pending, pendingAt = nd, now.Add(rtt)
		}
		if math.Abs(minerDiff/ideal-1) <= 0.3 {
			res.settle = now.Sub(t0)
			res.final = minerDiff
			return res
		}
	}
	res.settle, res.final = now.Sub(t0), cur
	return res
}

// TestRentalVardiffRamp reports how a rental of each target size, split
// over N connections, ramps from the start difficulty with the shipped
// message limits (100/s, burst 500) and a 100 ms round trip. It fails only
// when a configuration that is meant to work (start difficulty raised for
// rentals, or a suggest_difficulty near the ideal) would be disconnected.
func TestRentalVardiffRamp(t *testing.T) {
	const sha, scrypt = 1 << 32, 65536
	targets := []struct {
		name  string
		hps   float64
		diff1 float64
		start float64 // app default start difficulty
	}{
		{"BCH 50 PH/s", 50e15, sha, 1024},
		{"BTC 1 EH/s", 1e18, sha, 1024},
		{"LTC 50 TH/s", 50e12, scrypt, 262144},
	}
	for _, tg := range targets {
		for _, n := range []int{1, 10, 100, 1000} {
			per := tg.hps / float64(n)
			ideal := per * 10 / tg.diff1
			def := simulateRamp(per, tg.diff1, tg.start, 100*time.Millisecond, 100, 500, 1)
			// What a rental gets with a suggest_difficulty (or start
			// difficulty / rentx.* override) within 10x below the ideal.
			sug := simulateRamp(per, tg.diff1, math.Max(1, ideal/10), 100*time.Millisecond, 100, 500, 2)
			t.Logf("RAMP %-12s %4d conns: %.3g H/s each, ideal diff %.3g (%.2g shares/s at start diff) | start %g: %s | start ideal/10: %s",
				tg.name, n, per, ideal, per/(tg.start*tg.diff1), tg.start, fmtRamp(def), fmtRamp(sug))
			if sug.disconnected {
				t.Errorf("%s over %d connections: disconnected even with a start difficulty near the ideal", tg.name, n)
			}
			if ideal > DiffCeiling {
				t.Errorf("%s over %d connections: ideal difficulty %.3g above the ceiling %g", tg.name, n, ideal, DiffCeiling)
			}
		}
	}
}

func fmtRamp(r rampResult) string {
	if r.disconnected {
		return fmt.Sprintf("DISCONNECTED by rate limit after %d shares (diff reached %.3g)", r.shares, r.final)
	}
	return fmt.Sprintf("settled in %v, %d shares", r.settle.Round(time.Millisecond), r.shares)
}
