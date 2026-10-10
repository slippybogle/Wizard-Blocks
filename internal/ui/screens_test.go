//go:build screens

package ui

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/fladnagmai/wizard-blocks/internal/node"
	"github.com/fladnagmai/wizard-blocks/internal/stats"
	"github.com/fladnagmai/wizard-blocks/internal/stratum"
)

// TestScreens serves the simple page in every Phase 4 state and has
// Chromium (Playwright) screenshot each at desktop and phone width.
// Run: WB_SCREENS_OUT=<dir> go test -tags screens -run TestScreens ./internal/ui
func TestScreens(t *testing.T) {
	out := os.Getenv("WB_SCREENS_OUT")
	if out == "" {
		out = t.TempDir()
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	type state struct {
		name, coin, reason string
		aux                bool
	}
	states := []state{
		{"ltc-doge-merged", "ltc", "", true},
		{"ltc-no-doge-address", "ltc", "no address", true},
		{"ltc-doge-node-down", "ltc", "node down", true},
		{"ltc-doge-node-syncing", "ltc", "node syncing", true},
		{"btc-unchanged", "btc", "", false},
	}
	urls := map[string]string{}
	now := time.Now()
	for _, s := range states {
		st := stats.New(s.coin, "0.2.0-dev", "")
		chain := "main"
		st.SetNode(func(n *stats.NodeStatus) {
			n.Connected, n.Synced, n.Height, n.Headers, n.Chain, n.ZMQEnabled, n.ZMQConnected = true, true, 2961234, 2961234, chain, true, true
		})
		nd := 52e6 * 65536 // LTC node difficulty in share units
		if s.coin == "btc" {
			nd = 1.26e14
		}
		st.SetTemplate(func(ti *stats.TemplateInfo) { ti.PrevHash = "p"; ti.Height = 2961235; ti.NetworkDiff = nd })
		st.Connected("dghome1")
		st.BlockSubmitted(stats.BlockRecord{Height: 2961100, Hash: "a1b2c3d4e5f60718293a4b5c6d7e8f90112233445566778899aabbccddeeff00", Worker: "dghome1", Status: "accepted", Time: now.Add(-3 * time.Hour)})
		if s.aux {
			st.SetAux("doge", func(a *stats.AuxStatus) {
				a.Merged, a.Reason, a.Connected, a.ZMQ, a.Height, a.NetworkDiff = s.reason == "", s.reason, s.reason != "node down", s.reason == "", 5891234, 21e6*65536
				if s.reason != "no address" {
					a.Address = "DQkwDpRYUyNNnoBjfERSzPWbKdtr2JUSyL"
				}
			})
			st.AuxBlockSubmitted(stats.BlockRecord{Chain: "doge", Height: 5891100, Hash: "d0d0e0e0f0f0a1a1b2b2c3c3d4d4e5e5f6f6a7a7b8b8c9c9dadaebebfcfc0d0d", Worker: "dghome1", Status: "accepted", Time: now.Add(-40 * time.Minute)})
		}
		// Shares after the blocks, so effort shows.
		for i := 0; i < 120; i++ {
			st.ShareAccepted("dghome1", 262144, 262144*float64(1+i%7), "bip310+xor+or")
		}
		srv := New(Config{Coin: s.coin, StratumPort: map[string]int{"ltc": 50333, "btc": 51492}[s.coin], PayoutMode: "fixed", SettingsOpen: true, Style: "simple"},
			st, node.NewClient("http://127.0.0.1:1", "u", "p", "", time.Second), log)
		d := stratum.DiffSettings{Min: 1024, Max: 1e12, TargetSeconds: 10, Start: 262144, Overrides: map[string]float64{"rental.*": 2e6}}
		if s.coin == "btc" {
			d = stratum.DiffSettings{Min: 1, Max: 1e15, TargetSeconds: 10, Start: 1024, Overrides: map[string]float64{}}
		}
		fb := &screenBackend{fakeBackend: fakeBackend{d: d, payout: map[string]string{"ltc": "ltc1qg82e7d7ky5uc8x0xmdmfdzt6yp7eqlzg7gj9pr", "btc": "bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq"}[s.coin]}, aux: s.aux}
		srv.SetSettingsBackend(fb)
		if err := srv.Listen("127.0.0.1:0"); err != nil {
			t.Fatal(err)
		}
		go srv.Serve()
		t.Cleanup(func() { srv.Shutdown(context.Background()) })
		urls[s.name] = "http://" + srv.Addr() + "/"
	}
	cfg, _ := json.Marshal(map[string]any{"out": out, "urls": urls})
	cmd := exec.Command("node", filepath.Join("testdata", "screens.js"), string(cfg))
	cmd.Env = append(os.Environ(), "NODE_PATH="+os.Getenv("NODE_PATH"))
	b, err := cmd.CombinedOutput()
	t.Logf("%s", b)
	if err != nil {
		t.Fatal(err)
	}
}

type screenBackend struct {
	fakeBackend
	aux bool
}

func (f *screenBackend) DogePayout() (string, bool)                            { return "", f.aux }
func (f *screenBackend) SetDogePayout(context.Context, string) (string, error) { return "", nil }
