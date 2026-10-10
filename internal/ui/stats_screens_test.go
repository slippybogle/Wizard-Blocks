//go:build screens

package ui

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/fladnagmai/wizard-blocks/internal/node"
	"github.com/fladnagmai/wizard-blocks/internal/stats"
	"github.com/fladnagmai/wizard-blocks/internal/stratum"
)

// fakeLTCNode answers the RPCs the page's node panel polls.
func fakeLTCNode(t *testing.T) *httptest.Server {
	res := map[string]any{
		"getblockchaininfo": map[string]any{"chain": "main", "blocks": 2961234, "headers": 2961234, "difficulty": 48213345.2,
			"verificationprogress": 0.9999995, "pruned": true, "size_on_disk": 4_200_000_000},
		"getnetworkinfo":   map[string]any{"version": 210508, "subversion": "/LitecoinCore:0.21.5.8/", "connections": 11},
		"getmempoolinfo":   map[string]any{"size": 143, "bytes": 90_000},
		"uptime":           93780,
		"getnetworkhashps": 2.1e15,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		out := map[string]any{"id": req.ID, "error": nil, "result": res[req.Method]}
		if res[req.Method] == nil {
			out["error"] = map[string]any{"code": -32601, "message": "Method not found"}
		}
		json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestStatsScreens screenshots the LTC + DOGE stats page at desktop and
// phone width: merged mining with miners hashing in real time long enough
// for their live (3.5 min) hashrates, blocks on both chains, and a fresh
// engine with no miners.
// Run: WB_SCREENS_OUT=<dir> go test -tags screens -run TestStatsScreens -timeout 15m ./internal/ui
func TestStatsScreens(t *testing.T) {
	out := os.Getenv("WB_SCREENS_OUT")
	if out == "" {
		out = t.TempDir()
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rpc := fakeLTCNode(t)
	serve := func(st *stats.Collector, seed24h bool) string {
		srv := New(Config{Coin: "ltc", Version: "0.2.0-dev", Style: "stats", StratumPort: 50333, PayoutMode: "fixed", SettingsOpen: true}, st,
			node.NewClient(rpc.URL, "u", "p", "", time.Second), log)
		srv.SetSettingsBackend(&screenBackend{fakeBackend: fakeBackend{d: stratum.DiffSettings{Min: 1024, Max: 1e12, TargetSeconds: 10, Start: 262144},
			payout: "ltc1qg82e7d7ky5uc8x0xmdmfdzt6yp7eqlzg7gj9pr"}, aux: true})
		if seed24h {
			now := time.Now()
			for t := now.Add(-24 * time.Hour); t.Before(now); t = t.Add(30 * time.Second) {
				srv.hist.add(t, 2.08e9*(0.9+0.2*rand.Float64()))
			}
		}
		if err := srv.Listen("127.0.0.1:0"); err != nil {
			t.Fatal(err)
		}
		go srv.Serve()
		go srv.Run(ctx) // node panel polling
		t.Cleanup(func() { srv.Shutdown(context.Background()) })
		return "http://" + srv.Addr() + "/"
	}
	setNode := func(st *stats.Collector) {
		st.SetNode(func(n *stats.NodeStatus) {
			n.Connected, n.Synced, n.Height, n.Headers, n.Chain = true, true, 2961234, 2961234, "main"
			n.Subversion, n.ZMQEnabled, n.ZMQConnected, n.ZMQMessages = "/LitecoinCore:0.21.5.8/", true, true, 412
		})
		st.SetTemplate(func(ti *stats.TemplateInfo) {
			ti.PrevHash, ti.Height, ti.NetworkDiff, ti.Transactions = "p", 2961235, 48213345.2*65536, 143
			ti.NewBlocks, ti.Refreshes, ti.UpdatedAt = 412, 2210, time.Now().Add(-5*time.Minute)
		})
	}
	now := time.Now()
	mining := stats.New("ltc", "0.2.0-dev", "")
	setNode(mining)
	mining.SetAux("doge", func(a *stats.AuxStatus) {
		a.Merged, a.Connected, a.ZMQ, a.Height, a.NetworkDiff = true, true, true, 5412345, 29e6*65536
		a.Address = "DQkwDpRYUyNNnoBjfERSzPWbKdtr2JUSyL"
	})
	mining.AuxBlockSubmitted(stats.BlockRecord{Chain: "doge", Height: 5412345, Hash: "d0d0e0e0f0f0a1a1b2b2c3c3d4d4e5e5f6f6a7a7b8b8c9c9dadaebebfcfc0d0d",
		Worker: "dg1", Status: "accepted", ShareDiff: 30e6 * 65536, NetworkDiff: 29e6 * 65536, Time: now.Add(-2 * time.Hour)})
	for _, m := range []struct {
		name string
		hps  float64
	}{{"dg1", 2.1e9}, {"rentx.mrr-7781", 4.0e10}} {
		mining.Connected(m.name)
		mining.SetDifficulty(m.name, 262144)
		go func(name string, hps float64) {
			const diff = 16384.0
			for ctx.Err() == nil {
				time.Sleep(time.Duration(rand.ExpFloat64() * diff * 65536 / hps * float64(time.Second)))
				mining.ShareAccepted(name, diff, diff*(1+rand.ExpFloat64()*20), "")
			}
		}(m.name, m.hps)
	}
	for i := 0; i < 3; i++ {
		mining.ShareRejected("dg1", "stale")
	}
	mining.SetConnections(2)
	empty := stats.New("ltc", "0.2.0-dev", "")
	setNode(empty)
	empty.SetAux("doge", func(a *stats.AuxStatus) {
		a.Merged, a.Reason, a.Connected, a.Height, a.NetworkDiff = false, "no address", true, 5412345, 29e6*65536
	})
	urls := map[string]string{"stats-mining": serve(mining, true), "stats-no-miners": serve(empty, false)}
	start := time.Now()
	for os.Getenv("WB_SCREENS_FAST") == "" && (time.Since(start) < 210*time.Second || time.Now().Second() > 5) {
		time.Sleep(time.Second)
	}
	cfg, _ := json.Marshal(map[string]any{"out": out, "urls": urls})
	b, err := exec.Command("node", filepath.Join("testdata", "stats_screens.js"), string(cfg)).CombinedOutput()
	t.Logf("%s", b)
	if err != nil {
		t.Fatal(err)
	}
}
