//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestRegtestBTCUmbrelApp runs the engine the way the BTC Umbrel app does
// (fladnagmai-wizard-blocks-btc): Bitcoin Core pruned with txindex off,
// fixed payout with no address configured, settings open behind app_proxy,
// and the simple one-page UI. The payout address is set from that UI's API
// (checked by the node), then blocks are mined and checked on chain.
func TestRegtestBTCUmbrelApp(t *testing.T) {
	suffix := randHex(3)
	netName := "wbit-btcapp-" + suffix
	docker(t, "network", "create", netName)
	t.Cleanup(func() { docker(t, "network", "rm", netName) })
	a := startNode(t, "btc", netName, "wbit-btcapp-"+suffix, "-prune=550", "-txindex=0")
	var none any
	a.call(t, a.RPC, "createwallet", &none, "w")
	var ci struct {
		Pruned bool `json:"pruned"`
	}
	a.call(t, a.RPC, "getblockchaininfo", &ci)
	if !ci.Pruned {
		t.Fatal("node is not pruned")
	}
	var idx map[string]any
	a.call(t, a.RPC, "getindexinfo", &idx)
	if _, on := idx["txindex"]; on {
		t.Fatal("txindex is on")
	}

	cfg := engineConfig(a, "btc")
	cfg.Payout.Mode, cfg.Payout.Address = "fixed", ""
	cfg.UI.SettingsOpen = true
	cfg.UI.Style = "simple"
	cfg.DataDir = t.TempDir()
	en := startEngine(t, cfg, "btc-umbrel-app")
	base := "http://" + en.E.UIAddr()

	// The simple page and its files are served, self-contained.
	res, err := http.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(page), "WIZARD-BLOCKS") || !strings.Contains(string(page), `src="app.js"`) || strings.Contains(string(page), "The Ledger") {
		t.Fatalf("simple page not served:\n%.300s", page)
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Fatalf("CSP %q", csp)
	}
	for _, f := range []string{"/app.js", "/style.css"} {
		r, err := http.Get(base + f)
		if err != nil || r.StatusCode != 200 {
			t.Fatalf("GET %s: %v %v", f, err, r)
		}
		r.Body.Close()
	}

	state := func() map[string]any {
		r, err := http.Get(base + "/api/state")
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var m map[string]any
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if s := state()["stratum"].(map[string]any); s["payout_set"] != false {
		t.Fatalf("payout set before configuring: %v", s)
	}

	put := func(addr string) (int, string) {
		body, _ := json.Marshal(map[string]string{"address": addr})
		req, _ := http.NewRequest(http.MethodPut, base+"/api/admin/payout", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", base)
		req.Header.Set("X-WB-Admin", "1")
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, string(b)
	}
	if code, body := put("bc1qnotanaddressxxxxxxxxxxxxxxxxxxxxxxxxx"); code != http.StatusBadRequest {
		t.Fatalf("bad address accepted: %d %s", code, body)
	}
	payout := a.newAddress(t, "bech32")
	if code, body := put(payout); code != http.StatusOK {
		t.Fatalf("set payout: %d %s", code, body)
	}
	if s := state()["stratum"].(map[string]any); s["payout_set"] != true || s["payout_address"] != payout {
		t.Fatalf("payout not set: %v", s)
	}

	// Mine on the pruned node and check the blocks pay the address.
	rec := &recorder{m: map[string]submission{}}
	start := a.height(t)
	m := startMiner(t, en, rec, "bitaxe", "xor")
	mineUntil(t, en, 3, 3*time.Minute)
	m.stop(t)
	want := a.scriptOf(t, payout)
	for _, b := range en.E.Stats().Blocks() {
		if b.Status != "accepted" {
			continue
		}
		var blk struct {
			Height int64 `json:"height"`
			Tx     []struct {
				Vout []struct {
					ScriptPubKey struct {
						Hex string `json:"hex"`
					} `json:"scriptPubKey"`
				} `json:"vout"`
			} `json:"tx"`
		}
		a.call(t, a.RPC, "getblock", &blk, b.Hash, 2)
		if blk.Height <= start || blk.Tx[0].Vout[0].ScriptPubKey.Hex != want {
			t.Fatalf("block %s: height %d, coinbase pays %s, want %s", b.Hash, blk.Height, blk.Tx[0].Vout[0].ScriptPubKey.Hex, want)
		}
	}
	st := state()
	pool := st["pool"].(map[string]any)
	if pool["blocks_found"].(float64) < 3 || pool["shares_rejected"].(float64) != 0 {
		t.Fatalf("pool after mining: %v", pool)
	}
	// Node status (synced, height, ZMQ) is refreshed on a timer.
	waitFor(t, "node status in the state", 30*time.Second, func() bool {
		n := state()["node"].(map[string]any)
		return n["synced"] == true && n["zmq_connected"] == true && n["chain"] == "regtest" && n["height"].(float64) >= float64(start+3)
	})
	if ws := st["workers"].([]any); len(ws) != 1 || ws[0].(map[string]any)["name"] != "bitaxe" {
		t.Fatalf("workers: %v", ws)
	}
}
