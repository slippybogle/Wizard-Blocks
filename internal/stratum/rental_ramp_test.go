package stratum

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"testing"
	"time"
)

// rampResult is how one simulated connection fared while vardiff moved its
// difficulty from the start difficulty to its hashrate.
type rampResult struct {
	settle       time.Duration // until the miner's difficulty is within 2x of ideal (5-20 s per share)
	shares       int           // shares sent until then
	firstSecond  int           // shares in the first second (the flood the engine absorbs)
	disconnected bool          // the message limiter would have closed it
	final        float64
}

// simulateRamp runs vardiff and the message limiter as the session does for
// one connection of hashrate hps: shares arrive as a Poisson process at
// hps/(diff·diff1Hashes); a new difficulty reaches the miner rtt later
// (shares meanwhile are at the old one and accepted at it); each message
// costs a token (rate msgRate/s, burst msgBurst) and, with refund, an
// accepted share gives it back; vardiff ticks every 5 s.
func simulateRamp(hps, diff1Hashes, start float64, rtt time.Duration, msgRate, msgBurst float64, refund bool, seed uint64) rampResult {
	rng := rand.New(rand.NewPCG(seed, 1))
	cfg := VardiffConfig{Retarget: 60 * time.Second, VariancePct: 30}
	ds := DiffSettings{Min: 1, Max: DiffCeiling, TargetSeconds: 10}
	ideal := ds.Clamp(hps * ds.TargetSeconds / diff1Hashes)
	t0 := time.Unix(0, 0)
	v := newVardiff(cfg, t0)
	cur, minerDiff := start, start
	type delivery struct {
		at   time.Time
		diff float64
	}
	var inFlight []delivery // set_difficulty messages on their way, in order
	send := func(now time.Time, d float64) { inFlight = append(inFlight, delivery{now.Add(rtt), d}) }
	tokens, tokensAt := msgBurst, t0
	nextTick := t0.Add(5 * time.Second)
	res := rampResult{}
	now := t0
	settled := func() bool { return minerDiff >= ideal/2 && minerDiff <= ideal*2 }
	for now.Sub(t0) < 30*time.Minute {
		if settled() {
			res.settle, res.final = now.Sub(t0), minerDiff
			return res
		}
		next := now.Add(time.Duration(rng.ExpFloat64() / (hps / (minerDiff * diff1Hashes)) * float64(time.Second)))
		// Whatever happens first: a difficulty reaching the miner, a vardiff
		// tick, or the next share. (A new difficulty restarts the share clock;
		// exponential arrivals are memoryless.)
		if len(inFlight) > 0 && inFlight[0].at.Before(next) && !inFlight[0].at.After(nextTick) {
			now, minerDiff = inFlight[0].at, inFlight[0].diff
			inFlight = inFlight[1:]
			continue
		}
		if nextTick.Before(next) {
			now = nextTick
			nextTick = nextTick.Add(5 * time.Second)
			if nd, ok := v.onTick(now, cur, ds); ok {
				cur = nd
				send(now, nd)
			}
			continue
		}
		now = next
		tokens = math.Min(msgBurst, tokens+now.Sub(tokensAt).Seconds()*msgRate)
		tokensAt = now
		if tokens < 1 {
			res.disconnected, res.final = true, cur
			return res
		}
		tokens--
		if refund {
			tokens = math.Min(msgBurst, tokens+1)
		}
		res.shares++
		if now.Sub(t0) < time.Second {
			res.firstSecond++
		}
		if nd, ok := v.onShare(now, minerDiff, cur, ds); ok {
			cur = nd
			send(now, nd)
		}
	}
	res.settle, res.final = now.Sub(t0), cur
	return res
}

const (
	sha256Diff1 = 1 << 32
	scryptDiff1 = 65536
)

// TestRentalVardiffRamp: with the shipped start difficulties and limits, a
// rental of each target size on ONE connection is never disconnected and
// settles quickly, and small home miners still settle at their own
// difficulty (they are not stuck at a high start difficulty).
func TestRentalVardiffRamp(t *testing.T) {
	type miner struct {
		name  string
		hps   float64
		diff1 float64
		start float64
		limit time.Duration // must settle within
	}
	shaStart, ltcStart := 65536.0, 262144.0
	miners := []miner{
		{"BCH rental 50 PH/s, 1 conn", 50e15, sha256Diff1, shaStart, 2 * time.Minute},
		{"BTC rental 1 EH/s, 1 conn", 1e18, sha256Diff1, shaStart, 2 * time.Minute},
		{"LTC rental 50 TH/s, 1 conn", 50e12, scryptDiff1, ltcStart, 2 * time.Minute},
		{"BTC Bitaxe 1.2 TH/s", 1.2e12, sha256Diff1, shaStart, 5 * time.Minute},
		{"BTC NerdQaxe 6 TH/s", 6e12, sha256Diff1, shaStart, 5 * time.Minute},
		{"BTC S21 200 TH/s", 200e12, sha256Diff1, shaStart, 5 * time.Minute},
		{"LTC DG Home 1 2.1 GH/s", 2.1e9, scryptDiff1, ltcStart, 5 * time.Minute},
		{"LTC L9 16 GH/s", 16e9, scryptDiff1, ltcStart, 5 * time.Minute},
	}
	for _, m := range miners {
		oldStart := map[bool]float64{true: 262144, false: 1024}[m.diff1 == scryptDiff1]
		var now, before []time.Duration
		flood, oldDisc := 0, 0
		for seed := uint64(1); seed <= 101; seed++ {
			r := simulateRamp(m.hps, m.diff1, m.start, 100*time.Millisecond, 100, 500, true, seed)
			if r.disconnected {
				t.Errorf("%s: disconnected (seed %d)", m.name, seed)
			}
			now = append(now, r.settle)
			flood = max(flood, r.firstSecond)
			o := simulateRamp(m.hps, m.diff1, oldStart, 100*time.Millisecond, 100, 500, false, seed)
			if o.disconnected {
				oldDisc++
			} else {
				before = append(before, o.settle)
			}
		}
		p50, p90 := pctDur(now, 0.5), pctDur(now, 0.9)
		old := fmt.Sprintf("median %v, p90 %v", pctDur(before, 0.5), pctDur(before, 0.9))
		if oldDisc > 0 {
			old = fmt.Sprintf("DISCONNECTED in %d of 101 runs", oldDisc)
		}
		t.Logf("RAMP %-26s start %-7g: median %-7v p90 %-7v (flood ≤%d shares in 1st second) | before (start %g, no refund): %s",
			m.name, m.start, p50, p90, flood, oldStart, old)
		if p90 > m.limit {
			t.Errorf("%s: p90 %v to settle, want ≤ %v", m.name, p90, m.limit)
		}
	}
}

func pctDur(d []time.Duration, p float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	return d[int(p*float64(len(d)-1))].Round(100 * time.Millisecond)
}

func fmtRamp(r rampResult) string {
	if r.disconnected {
		return fmt.Sprintf("DISCONNECTED by the rate limit after %d shares", r.shares)
	}
	return fmt.Sprintf("settled in %v", r.settle.Round(100*time.Millisecond))
}

// TestVardiffSettledMinerStays: a miner already at its right difficulty
// stays within 2.5x of it for hours (it was 1/8..2.5x before damping) (the new-connection probing and the faster
// flood ramp do not make a settled miner oscillate).
func TestVardiffSettledMinerStays(t *testing.T) {
	cfg := VardiffConfig{Retarget: 60 * time.Second, VariancePct: 30}
	ds := DiffSettings{Min: 1, Max: DiffCeiling, TargetSeconds: 10}
	for _, m := range []struct {
		name  string
		hps   float64
		diff1 float64
	}{{"Bitaxe", 1.2e12, sha256Diff1}, {"S21", 200e12, sha256Diff1}, {"DG Home 1", 2.1e9, scryptDiff1}} {
		ideal := m.hps * ds.TargetSeconds / m.diff1
		for seed := uint64(1); seed <= 20; seed++ {
			rng := rand.New(rand.NewPCG(seed, 7))
			t0 := time.Unix(0, 0)
			v := newVardiff(cfg, t0)
			cur, now, tick := ideal, t0, t0.Add(5*time.Second)
			// Past the first retarget, as a connected miner is.
			lo, hi, changes := cur, cur, 0
			for now.Sub(t0) < 6*time.Hour {
				next := now.Add(time.Duration(rng.ExpFloat64() / (m.hps / (cur * m.diff1)) * float64(time.Second)))
				for !tick.After(next) {
					if nd, ok := v.onTick(tick, cur, ds); ok {
						cur, changes = nd, changes+1
					}
					tick = tick.Add(5 * time.Second)
				}
				now = next
				if nd, ok := v.onShare(now, cur, cur, ds); ok {
					cur, changes = nd, changes+1
				}
				if now.Sub(t0) > 10*time.Minute {
					lo, hi = math.Min(lo, cur), math.Max(hi, cur)
				}
			}
			if lo < ideal/2.5 || hi > ideal*2.5 {
				t.Errorf("%s seed %d: difficulty ranged %.3g..%.3g, ideal %.3g (%d changes in 6 h)", m.name, seed, lo, hi, ideal, changes)
			}
			if seed == 1 {
				t.Logf("SETTLED %-9s ideal %.3g: ranged %.3g..%.3g over 6 h, %d changes", m.name, ideal, lo, hi, changes)
			}
		}
	}
}
