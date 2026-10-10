package engine

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fladnagmai/wizard-blocks/internal/address"
	"github.com/fladnagmai/wizard-blocks/internal/config"
	"github.com/fladnagmai/wizard-blocks/internal/node"
)

const testBTCAddr = "1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2"

// payoutNode answers validateaddress: the first `down` calls fail (node not
// answering), later ones give the node's verdict.
func payoutNode(t *testing.T, down int32, valid bool) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	n, _ := address.NetworkFor(address.BTC, "main")
	a, err := address.Decode(n, testBTCAddr)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= down {
			http.Error(w, "warming up", http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"id": 1, "error": nil,
			"result": map[string]any{"isvalid": valid, "scriptPubKey": hex.EncodeToString(a.Script)}})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func payoutEngine(t *testing.T, url string) *Engine {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "payout-btc.json"), []byte(`{"address":"`+testBTCAddr+`"}`), 0o600)
	n, _ := address.NetworkFor(address.BTC, "main")
	cfg := config.Config{Coin: "btc", DataDir: dir}
	cfg.Payout.Mode = "fixed"
	return &Engine{cfg: cfg, net: n, log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		rpc: node.NewClient(url, "u", "p", "", time.Second)}
}

func shortPayoutRetry(t *testing.T, total time.Duration) {
	f, e := payoutRetryFor, payoutRetryEvery
	payoutRetryFor, payoutRetryEvery = total, 10*time.Millisecond
	t.Cleanup(func() { payoutRetryFor, payoutRetryEvery = f, e })
}

// A saved address is kept when the node briefly does not answer at start
// (it used to be dropped for the whole run, refusing every miner).
func TestSavedPayoutSurvivesNodeBlip(t *testing.T) {
	shortPayoutRetry(t, 5*time.Second)
	srv, calls := payoutNode(t, 3, true)
	e := payoutEngine(t, srv.URL)
	if err := e.initPayout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p := e.fixedPayout(); p == nil || p.Address != testBTCAddr {
		t.Fatalf("saved payout lost: %+v", p)
	}
	if calls.Load() != 4 {
		t.Fatalf("validateaddress calls %d, want 4", calls.Load())
	}
}

// If the node never answers, start fails (the restart policy retries)
// instead of running with no payout.
func TestSavedPayoutNodeDownFailsStart(t *testing.T) {
	shortPayoutRetry(t, 100*time.Millisecond)
	srv, _ := payoutNode(t, 1<<30, true)
	e := payoutEngine(t, srv.URL)
	if err := e.initPayout(context.Background()); err == nil {
		t.Fatal("started with an unchecked saved payout")
	}
	if e.fixedPayout() != nil {
		t.Fatal("unchecked payout used")
	}
}

// A node verdict against the saved address is not retried: it is ignored
// (logged) and miners wait for a new address, as before.
func TestSavedPayoutRejectedByNode(t *testing.T) {
	shortPayoutRetry(t, 5*time.Second)
	srv, calls := payoutNode(t, 0, false)
	e := payoutEngine(t, srv.URL)
	if err := e.initPayout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.fixedPayout() != nil || calls.Load() != 1 {
		t.Fatalf("fixed %+v after %d calls", e.fixedPayout(), calls.Load())
	}
}
