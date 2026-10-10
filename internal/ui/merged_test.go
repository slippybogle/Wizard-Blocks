package ui

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/fladnagmai/wizard-blocks/internal/node"
	"github.com/fladnagmai/wizard-blocks/internal/stats"
	"github.com/fladnagmai/wizard-blocks/internal/stratum"
)

type dogeFake struct {
	fakeBackend
	doge string
}

func (f *dogeFake) DogePayout() (string, bool) { return f.doge, true }
func (f *dogeFake) SetDogePayout(_ context.Context, addr string) (string, error) {
	if addr != "" && !strings.HasPrefix(addr, "D") {
		return "", errors.New("Dogecoin node does not confirm DOGE address")
	}
	f.doge = addr
	return addr, nil
}

// The two-chain state: chains.ltc / chains.doge with each chain's own
// difficulty, odds and counts, and the DOGE status for the banner, with
// DOGE merged, without an address, and with its node down.
func TestTwoChainState(t *testing.T) {
	st := stats.New("ltc", "t", "")
	s := New(Config{Coin: "ltc"}, st, node.NewClient("http://127.0.0.1:1", "u", "p", "", time.Second), slog.New(slog.NewTextHandler(io.Discard, nil)))
	// No aux chain configured: no chains section (BTC/BCH pages unchanged).
	if got := s.BuildState(10, 10); got.Chains != nil {
		t.Fatalf("chains without an aux chain: %+v", got.Chains)
	}
	st.SetTemplate(func(ti *stats.TemplateInfo) { ti.PrevHash = "a"; ti.Height = 100; ti.NetworkDiff = 4e9 })
	st.SetAux("doge", func(a *stats.AuxStatus) {
		a.Merged, a.Connected, a.Height, a.NetworkDiff, a.Address = true, true, 50, 2e9, "Dabc"
	})
	for i := 0; i < 20; i++ {
		st.ShareAccepted("w", 262144, 300000, "x")
	}
	st.AuxBlockSubmitted(stats.BlockRecord{Chain: "doge", Height: 49, Hash: "dd", Status: "accepted", Time: time.Now()})
	got := s.BuildState(10, 10)
	ltc, doge := got.Chains["ltc"], got.Chains["doge"]
	if !doge.Merged || doge.Reason != "" || doge.BlocksFound != 1 || doge.PayoutAddress != "Dabc" || got.AuxCoins["doge"].Ticker != "DOGE" {
		t.Fatalf("doge view %+v", doge)
	}
	if ltc.NetworkDiff != 4e9 || doge.NetworkDiff != 2e9 {
		t.Fatalf("difficulties ltc %v doge %v", ltc.NetworkDiff, doge.NetworkDiff)
	}
	// Each chain's expected time uses its own difficulty: DOGE half of LTC.
	if ltc.ExpectedBlockS == nil || doge.ExpectedBlockS == nil || math.Abs(*ltc.ExpectedBlockS/(*doge.ExpectedBlockS)-2) > 1e-9 {
		t.Fatalf("expected times ltc %v doge %v", ltc.ExpectedBlockS, doge.ExpectedBlockS)
	}
	if doge.EffortPct != nil {
		t.Fatalf("DOGE effort should have reset with its block: %v", *doge.EffortPct)
	}
	if len(got.AuxBlocks) != 1 || got.AuxBlocks[0].Chain != "doge" {
		t.Fatalf("aux blocks %+v", got.AuxBlocks)
	}
	for _, c := range []struct{ reason, want string }{{"no address", "no address"}, {"node down", "node down"}} {
		st.SetAux("doge", func(a *stats.AuxStatus) { a.Merged, a.Reason = false, c.reason })
		if d := s.BuildState(10, 10).Chains["doge"]; d.Merged || d.Reason != c.want {
			t.Fatalf("doge %q: %+v", c.reason, d)
		}
	}
}

// The DOGE payout endpoint: login and CSRF rules as for the LTC address,
// node-refused addresses rejected with the reason, empty clears it.
func TestDogePayoutEndpoint(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(Config{Coin: "ltc", AdminPassword: "pw"}, stats.New("ltc", "t", ""), node.NewClient("http://127.0.0.1:1", "u", "p", "", time.Second), log)
	fb := &dogeFake{fakeBackend: fakeBackend{d: stratum.DiffSettings{Min: 1, Max: 1e12, TargetSeconds: 10, Overrides: map[string]float64{}}}}
	s.SetSettingsBackend(fb)
	if err := s.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	go s.Serve()
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	c := newClient(t, "http://"+s.Addr())
	if code, _ := c.do("PUT", "/api/admin/doge-payout", `{"address":"Dxyz"}`, true); code != 401 {
		t.Fatalf("unauthenticated: %d", code)
	}
	c.do("POST", "/api/admin/login", `{"password":"pw"}`, true)
	if code, _ := c.do("PUT", "/api/admin/doge-payout", `{"address":"Dxyz"}`, false); code != 403 {
		t.Fatalf("without the CSRF header: %d", code)
	}
	if code, m := c.do("PUT", "/api/admin/doge-payout", `{"address":"ltc1qnotdoge"}`, true); code != 400 || !strings.Contains(m["error"].(string), "does not confirm") {
		t.Fatalf("refused address: %d %v", code, m)
	}
	if code, m := c.do("PUT", "/api/admin/doge-payout", `{"address":"Dxyz"}`, true); code != 200 || fb.doge != "Dxyz" || m["doge_payout_address"] != "Dxyz" {
		t.Fatalf("set: %d %v", code, m)
	}
	if code, _ := c.do("PUT", "/api/admin/doge-payout", `{"address":""}`, true); code != 200 || fb.doge != "" {
		t.Fatalf("clear: %d %q", code, fb.doge)
	}
	// An engine without merged mining answers 404.
	_, _, base := startUI(t, "pw")
	c2 := newClient(t, base)
	c2.do("POST", "/api/admin/login", `{"password":"pw"}`, true)
	if code, _ := c2.do("PUT", "/api/admin/doge-payout", `{"address":"Dxyz"}`, true); code != 404 {
		t.Fatalf("without merged mining: %d", code)
	}
}
