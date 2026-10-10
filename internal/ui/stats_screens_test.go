//go:build screens

package ui

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/fladnagmai/wizard-blocks/internal/node"
	"github.com/fladnagmai/wizard-blocks/internal/stats"
)

// TestStatsScreens screenshots the plain stats page (style "stats") at
// desktop and phone width: with three miners hashing in real time long
// enough for their live (3.5 min) hashrates, and with no miners yet.
// Run: WB_SCREENS_OUT=<dir> go test -tags screens -run TestStatsScreens -timeout 15m ./internal/ui
func TestStatsScreens(t *testing.T) {
	out := os.Getenv("WB_SCREENS_OUT")
	if out == "" {
		out = t.TempDir()
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serve := func(st *stats.Collector, seed24h bool) string {
		srv := New(Config{Coin: "ltc", Style: "stats"}, st, node.NewClient("http://127.0.0.1:1", "u", "p", "", time.Second), log)
		if seed24h {
			// A day of pool history at ~6.3 GH/s for the 24h figure.
			now := time.Now()
			for t := now.Add(-24 * time.Hour); t.Before(now); t = t.Add(30 * time.Second) {
				srv.hist.add(t, 6.3e9*(0.9+0.2*rand.Float64()))
			}
		}
		if err := srv.Listen("127.0.0.1:0"); err != nil {
			t.Fatal(err)
		}
		go srv.Serve()
		t.Cleanup(func() { srv.Shutdown(context.Background()) })
		return "http://" + srv.Addr() + "/"
	}
	mining := stats.New("ltc", "0.2.0-dev", "")
	empty := stats.New("ltc", "0.2.0-dev", "")
	// Real-time shares at 16384 (Scrypt share units): DG Home 1 2.1 GH/s,
	// an L9 16 GH/s... scaled to keep the share rate modest, plus a rented rig.
	for _, m := range []struct {
		name string
		hps  float64
	}{{"dghome1", 2.1e9}, {"l9-garage", 1.6e10}, {"rentx.mrr-7781", 4.0e10}} {
		mining.Connected(m.name)
		go func(name string, hps float64) {
			const diff = 16384.0
			for ctx.Err() == nil {
				time.Sleep(time.Duration(rand.ExpFloat64() * diff * 65536 / hps * float64(time.Second)))
				mining.ShareAccepted(name, diff, diff*(1+rand.ExpFloat64()*20), "")
			}
		}(m.name, m.hps)
	}
	urls := map[string]string{"stats-mining": serve(mining, true), "stats-no-miners": serve(empty, false)}
	// Wait for a minute boundary at least 3.5 min after the first shares, so
	// the live column is a full 3.5-minute average.
	start := time.Now()
	for time.Since(start) < 210*time.Second || time.Now().Second() > 5 {
		time.Sleep(time.Second)
	}
	cfg, _ := json.Marshal(map[string]any{"out": out, "urls": urls})
	b, err := exec.Command("node", filepath.Join("testdata", "stats_screens.js"), string(cfg)).CombinedOutput()
	t.Logf("%s", b)
	if err != nil {
		t.Fatal(err)
	}
}
