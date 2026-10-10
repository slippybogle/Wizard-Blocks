package stats

import (
	"bytes"
	"math"
	"strings"
	"testing"
	"time"
)

func TestHashrateWindow(t *testing.T) {
	var r rateWindow
	start := time.Unix(1_700_000_000, 0)
	// 1 TH/s: one difficulty-1000 share every 1000*2^32/1e12 ≈ 4.29 s.
	interval := 1000 * 4294967296 / 1e12
	now := start
	for now.Sub(start) < 2*time.Hour {
		r.add(now, 1000)
		now = now.Add(time.Duration(interval * float64(time.Second)))
	}
	for _, w := range []time.Duration{5 * time.Minute, time.Hour} {
		if hr := r.hashrate(now, w, 4294967296); math.Abs(hr/1e12-1) > 0.05 {
			t.Errorf("window %v: %g H/s", w, hr)
		}
	}
	// Nothing in the last hour after a long pause.
	if hr := r.hashrate(now.Add(3*time.Hour), time.Hour, 4294967296); hr != 0 {
		t.Errorf("stale buckets counted: %g", hr)
	}
}

func TestCollectorAndPersistence(t *testing.T) {
	dir := t.TempDir()
	c := New("btc", "test", dir)
	c.Connected("w1")
	c.ShareAccepted("w1", 100, 5000, "bip310+xor+or")
	c.ShareRejected("w1", "stale")
	c.BlockSubmitted(BlockRecord{Height: 10, Hash: "aa", Status: "pending"})
	if p := c.Snapshot().Pool; p.BlocksFound != 0 || p.BlocksPending != 1 {
		t.Fatalf("pending block counted as found: %+v", p)
	}
	c.BlockSubmitted(BlockRecord{Height: 10, Hash: "aa", Status: "accepted"})
	s := c.Snapshot()
	if s.Pool.Accepted != 1 || s.Pool.Rejected != 1 || s.Pool.BestDiff != 5000 || s.Pool.BlocksFound != 1 || s.Pool.BlocksPending != 0 || s.Pool.Workers != 1 {
		t.Fatalf("snapshot %+v", s.Pool)
	}
	if len(s.Blocks) != 1 {
		t.Fatal("block record not updated in place")
	}
	var buf bytes.Buffer
	WritePrometheus(&buf, s)
	for _, want := range []string{`wb_blocks_total{coin="btc",status="accepted"} 1`, `wb_shares_total{coin="btc",result="rejected",reason="stale"} 1`, `wb_worker_best_share_difficulty{coin="btc",worker="w1"} 5000`} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("metrics missing %q", want)
		}
	}
	c2 := New("btc", "test", dir)
	if s2 := c2.Snapshot(); s2.Pool.BlocksFound != 1 || s2.Pool.BestDiff != 5000 {
		t.Fatalf("persisted state not restored: %+v", s2.Pool)
	}
	c.Disconnected("w1")
	c.Prune(0)
	if len(c.Snapshot().Workers) != 0 {
		t.Fatal("idle worker not pruned")
	}
}

func TestRounds(t *testing.T) {
	c := New("bch", "test", "")
	c.SetTemplate(func(ti *TemplateInfo) { ti.Height, ti.PrevHash, ti.NetworkDiff = 10, "aa", 1000 })
	c.ShareAccepted("w", 2, 50, "x")
	c.ShareAccepted("w", 2, 7, "x")
	c.SetTemplate(func(ti *TemplateInfo) { ti.Transactions = 5 }) // refresh, same prevhash
	c.SetTemplate(func(ti *TemplateInfo) { ti.Height, ti.PrevHash = 11, "bb" })
	c.ShareAccepted("v", 2, 9, "x")
	r := c.Rounds(10)
	if len(r) != 2 || r[0].Height != 11 || r[0].BestDiff != 9 || r[1].Height != 10 ||
		r[1].BestDiff != 50 || r[1].Shares != 2 || r[1].SumDiff != 4 || r[1].NetworkDiff != 1000 || r[1].End.IsZero() {
		t.Fatalf("%+v", r)
	}
}

func TestLuckResetsOnlyOnFoundBlock(t *testing.T) {
	dir := t.TempDir()
	c := New("bch", "t", dir)
	c.SetTemplate(func(ti *TemplateInfo) { ti.PrevHash = "a" })
	c.ShareAccepted("w", 10, 500, "x")
	c.SetTemplate(func(ti *TemplateInfo) { ti.PrevHash = "b" }) // new job: no reset
	c.ShareAccepted("w", 10, 20, "x")
	l := c.Snapshot().Pool.Luck
	if l.SumDiff != 20 || l.BestDiff != 500 || l.Shares != 2 {
		t.Fatalf("luck after new job: %+v", l)
	}
	c.BlockSubmitted(BlockRecord{Hash: "h", Status: "pending"}) // not yet found
	if c.Snapshot().Pool.Luck.Shares != 2 {
		t.Fatal("pending block reset luck")
	}
	if New("bch", "t", dir).Snapshot().Pool.Luck.Shares != 2 {
		t.Fatal("luck not persisted")
	}
	c.BlockSubmitted(BlockRecord{Hash: "h", Status: "accepted"})
	if l := c.Snapshot().Pool.Luck; l.Shares != 0 || l.SumDiff != 0 || l.Since.IsZero() {
		t.Fatalf("luck after found block: %+v", l)
	}
	c.BlockSubmitted(BlockRecord{Hash: "o", Status: "orphaned"})
	c.ShareAccepted("w", 10, 30, "x")
	c.BlockSubmitted(BlockRecord{Hash: "o", Status: "orphaned"}) // not a found block
	if c.Snapshot().Pool.Luck.Shares != 1 {
		t.Fatal("orphaned block reset luck")
	}
}

func TestBestDropRecordedOnFoundBlock(t *testing.T) {
	c := New("bch", "t", "")
	c.SetTemplate(func(ti *TemplateInfo) { ti.PrevHash = "a"; ti.Height = 10; ti.NetworkDiff = 1000 })
	c.ShareAccepted("w", 1, 400, "x") // 40%
	c.SetTemplate(func(ti *TemplateInfo) { ti.PrevHash = "b"; ti.Height = 11; ti.NetworkDiff = 1000 })
	c.ShareAccepted("w", 1, 920, "x")  // 92%: the best drop
	c.ShareAccepted("w", 1, 1500, "x") // the block share itself never counts
	c.BlockSubmitted(BlockRecord{Hash: "h", Height: 11, Status: "pending"})
	c.BlockSubmitted(BlockRecord{Hash: "h", Height: 11, Status: "accepted"})
	b := c.Snapshot().Blocks[0]
	if b.BestDropPct != 92 || b.BestDropHeight != 11 {
		t.Fatalf("best drop on the block: %+v", b)
	}
	c.BlockSubmitted(BlockRecord{Hash: "h", Height: 11, Status: "accepted"}) // later update keeps it
	if c.Snapshot().Blocks[0].BestDropPct != 92 {
		t.Fatal("best drop lost on update")
	}
	if l := c.Snapshot().Pool.Luck; l.BestDropPct != 0 {
		t.Fatalf("best drop not reset after the block: %+v", l)
	}
}

func TestRecentShares(t *testing.T) {
	c := New("bch", "t", "")
	for i := 1; i <= 20; i++ {
		c.ShareAccepted("w", 1, float64(i), "x")
	}
	r := c.Snapshot().Pool.RecentShares
	if len(r) != 16 || r[0].Difficulty != 5 || r[15].Difficulty != 20 || r[15].Worker != "w" {
		t.Fatalf("recent shares %v", r)
	}
}

// A block-solving share on height 406 whose bookkeeping runs after the
// height-407 template is out (run 18 of the regtest loop: the share was
// checked, the block submitted, another miner's block took the height, and
// only then was the share recorded). It must land in round 406, not make
// round 407 look like it holds an unaccepted block.
func TestShareCreditedToItsOwnRound(t *testing.T) {
	c := New("bch", "t", "")
	c.SetTemplate(func(ti *TemplateInfo) { ti.PrevHash = "p405"; ti.Height = 406; ti.NetworkDiff = 1000 })
	c.SetTemplate(func(ti *TemplateInfo) { ti.PrevHash = "p406"; ti.Height = 407; ti.NetworkDiff = 1000 })
	c.ShareAcceptedOn("p405", "conc2", 1, 52000, "x") // late: mined on height 406
	c.ShareAcceptedOn("p406", "beast", 1, 300, "x")   // on the current round
	rs := c.Rounds(5)
	if len(rs) != 2 {
		t.Fatalf("rounds %+v", rs)
	}
	cur, prev := rs[0], rs[1]
	if cur.Height != 407 || cur.Shares != 1 || cur.BestDiff != 300 || cur.BestWorker != "beast" {
		t.Fatalf("current round got the late share: %+v", cur)
	}
	if prev.Height != 406 || prev.Shares != 1 || prev.BestDiff != 52000 || prev.BestWorker != "conc2" {
		t.Fatalf("late share not in its own round: %+v", prev)
	}
	// A share for a round no longer kept is still counted, just not in a round.
	c.ShareAcceptedOn("long-gone", "w", 1, 5, "x")
	if s := c.Snapshot(); s.Pool.Accepted != 3 {
		t.Fatalf("accepted %d", s.Pool.Accepted)
	}
}

// An aux (DOGE) block resets only DOGE effort; an LTC block only LTC
// effort. Both survive a restart; a state file without aux luck loads.
func TestAuxEffortIsPerChain(t *testing.T) {
	dir := t.TempDir()
	c := New("ltc", "t", dir)
	c.SetTemplate(func(ti *TemplateInfo) { ti.PrevHash = "a"; ti.Height = 10; ti.NetworkDiff = 1000 })
	c.SetAux("doge", func(a *AuxStatus) { a.Merged = true; a.NetworkDiff = 100 })
	c.ShareAccepted("w", 10, 20, "x")
	s := c.Snapshot()
	if s.Pool.Luck.Effort != 0.01 || s.Aux["doge"].Luck.Effort != 0.1 {
		t.Fatalf("effort ltc %v doge %v", s.Pool.Luck.Effort, s.Aux["doge"].Luck.Effort)
	}
	c.AuxBlockSubmitted(BlockRecord{Chain: "doge", Hash: "d1", Status: "pending"})
	c.AuxBlockSubmitted(BlockRecord{Chain: "doge", Hash: "d1", Status: "accepted"})
	s = c.Snapshot()
	if s.Aux["doge"].Luck.Effort != 0 || s.Pool.Luck.Effort != 0.01 {
		t.Fatalf("DOGE block reset: ltc %v doge %v", s.Pool.Luck.Effort, s.Aux["doge"].Luck.Effort)
	}
	c.ShareAccepted("w", 10, 20, "x")
	c.BlockSubmitted(BlockRecord{Hash: "l1", Status: "accepted"})
	s = c.Snapshot()
	if s.Pool.Luck.Effort != 0 || s.Aux["doge"].Luck.Effort != 0.1 {
		t.Fatalf("LTC block reset: ltc %v doge %v", s.Pool.Luck.Effort, s.Aux["doge"].Luck.Effort)
	}
	// Not merged: no DOGE effort accrues.
	c.SetAux("doge", func(a *AuxStatus) { a.Merged = false; a.Reason = "node down" })
	c.ShareAccepted("w", 10, 20, "x")
	if e := c.Snapshot().Aux["doge"].Luck.Effort; e != 0.1 {
		t.Fatalf("effort accrued while not merged: %v", e)
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	c2 := New("ltc", "t", dir)
	if e := c2.Snapshot().Aux["doge"].Luck.Effort; e != 0.1 || len(c2.AuxBlocks()) != 1 {
		t.Fatalf("after restart: doge effort %v, aux blocks %d", e, len(c2.AuxBlocks()))
	}
	// Metrics carry the DOGE series beside the LTC ones.
	var b bytes.Buffer
	WritePrometheus(&b, c2.Snapshot())
	for _, want := range []string{`wb_blocks_total{coin="doge",status="accepted"} 1`, `wb_merged{coin="doge"} 0`, `wb_effort_ratio{coin="doge"} 0.1`} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("metrics missing %q", want)
		}
	}
}

// The live figure is the average of the 3.5 minutes before the last minute
// boundary: it stays the same throughout a minute and changes only at the
// next boundary.
func TestHashrateLiveChangesOncePerMinute(t *testing.T) {
	var r rateWindow
	t0 := time.Unix(1_800_000_000, 0) // a minute boundary
	// One diff-1 share per second for 4 minutes, then two per second.
	for s := 0; s < 480; s++ {
		n := 1
		if s >= 240 {
			n = 2
		}
		for k := 0; k < n; k++ {
			r.add(t0.Add(time.Duration(s)*time.Second), 1)
		}
	}
	h1 := 4294967296.0 // 1 share/s at difficulty 1
	if got := r.live(t0.Add(30*time.Second), 4294967296); got != 0 {
		t.Fatalf("during the first minute: %g, want 0", got)
	}
	for s := 240; s < 300; s++ { // window [30s, 240s): all at 1/s
		if got := r.live(t0.Add(time.Duration(s)*time.Second), 4294967296); got != h1 {
			t.Fatalf("at %ds: %g, want %g (unchanged all minute)", s, got, h1)
		}
	}
	// At 300 s the window is [90s, 300s): 150 s at 1/s + 60 s at 2/s.
	if got, want := r.live(t0.Add(300*time.Second), 4294967296), h1*(150+120)/210; got != want {
		t.Fatalf("at 300s: %g, want %g", got, want)
	}
	// A worker that started 30 s into a minute: averaged over its 30 s.
	var y rateWindow
	for s := 30; s < 60; s++ {
		y.add(t0.Add(time.Duration(s)*time.Second), 1)
	}
	if got := y.live(t0.Add(75*time.Second), 4294967296); got != h1 {
		t.Fatalf("young worker: %g, want %g", got, h1)
	}
}

// The pool's live hashrate is the sum of its workers' live hashrates.
func TestPoolLiveIsSumOfWorkers(t *testing.T) {
	c := New("btc", "t", "")
	for i := 0; i < 300; i++ {
		c.ShareAccepted("a", 1000, 1000, "")
		if i%3 == 0 {
			c.ShareAccepted("b", 1000, 1000, "")
		}
	}
	s := c.Snapshot()
	sum := 0.0
	for _, w := range s.Workers {
		sum += w.HashrateLive
	}
	if s.Pool.HashrateLive != sum {
		t.Fatalf("pool live %g, sum of workers %g", s.Pool.HashrateLive, sum)
	}
}
