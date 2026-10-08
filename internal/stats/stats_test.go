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
		if hr := r.hashrate(now, w); math.Abs(hr/1e12-1) > 0.05 {
			t.Errorf("window %v: %g H/s", w, hr)
		}
	}
	// Nothing in the last hour after a long pause.
	if hr := r.hashrate(now.Add(3*time.Hour), time.Hour); hr != 0 {
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
