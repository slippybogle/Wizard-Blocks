package ui

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fladnagmai/wizard-blocks/internal/node"
	"github.com/fladnagmai/wizard-blocks/internal/stats"
)

func get(t *testing.T, url string) string {
	t.Helper()
	r, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	if r.StatusCode != 200 {
		t.Fatalf("%s: %d", url, r.StatusCode)
	}
	return string(b)
}

// The plain stats page (style "stats") is served with its own files, and
// /api/state carries everything it shows: pool 1m/5m/1h/24h hashrate and
// best share, and each miner's live hashrate.
func TestStatsPage(t *testing.T) {
	st := stats.New("ltc", "t", "")
	st.Connected("dghome1")
	for i := 0; i < 50; i++ {
		st.ShareAccepted("dghome1", 262144, 262144*float64(1+i%5), "")
	}
	srv := New(Config{Coin: "ltc", Style: "stats"}, st, node.NewClient("http://127.0.0.1:1", "u", "p", "", time.Second), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := srv.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	go srv.Serve()
	defer srv.Shutdown(context.Background())
	base := "http://" + srv.Addr() + "/"

	page := get(t, base)
	if !strings.Contains(page, `<pre id="out">`) || !strings.Contains(page, `src="app.js"`) || strings.Contains(page, "<style") || strings.Contains(page, "<script>") {
		t.Fatalf("not the plain stats page (or inline code, which the CSP forbids):\n%s", page)
	}
	js := get(t, base+"app.js")
	for _, want := range []string{"hashrate_1m", "hashrate_5m", "hashrate_1h", "hashrate_24h", "best_share_difficulty", "hashrate_live", "textContent"} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js does not use %s", want)
		}
	}
	if strings.Contains(js, "innerHTML") {
		t.Error("app.js writes HTML (miner names must go in as text)")
	}
	if css := get(t, base+"style.css"); !strings.Contains(css, "background: #000") {
		t.Error("style.css: background is not black")
	}

	var s struct {
		Pool    map[string]any   `json:"pool"`
		Derived map[string]any   `json:"derived"`
		Workers []map[string]any `json:"workers"`
	}
	if err := json.Unmarshal([]byte(get(t, base+"api/state")), &s); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"hashrate_1m", "hashrate_5m", "hashrate_1h", "best_share_difficulty"} {
		if _, ok := s.Pool[k]; !ok {
			t.Errorf("pool.%s missing", k)
		}
	}
	if _, ok := s.Derived["hashrate_24h"]; !ok {
		t.Error("derived.hashrate_24h missing")
	}
	if len(s.Workers) != 1 || s.Workers[0]["name"] != "dghome1" {
		t.Fatalf("workers: %v", s.Workers)
	}
	if _, ok := s.Workers[0]["hashrate_live"]; !ok {
		t.Error("workers[].hashrate_live missing")
	}
	if s.Pool["best_share_difficulty"].(float64) != 262144*5 {
		t.Errorf("best share %v", s.Pool["best_share_difficulty"])
	}
}
