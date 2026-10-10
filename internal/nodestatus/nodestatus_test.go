package nodestatus

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fladnagmai/wizard-blocks/internal/node"
)

// fakeNode answers JSON-RPC like litecoind/dogecoind with canned results;
// errs maps a method to an RPC error code.
func fakeNode(t *testing.T, user, pass string, results map[string]any, errs map[string]int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != user || p != pass {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &req)
		resp := map[string]any{"id": req.ID, "result": nil, "error": nil}
		if code, ok := errs[req.Method]; ok {
			resp["error"] = map[string]any{"code": code, "message": "Loading block index..."}
			w.WriteHeader(http.StatusInternalServerError)
		} else if res, ok := results[req.Method]; ok {
			resp["result"] = res
		} else {
			resp["error"] = map[string]any{"code": -32601, "message": "Method not found"}
			w.WriteHeader(http.StatusNotFound)
		}
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func ltcResults(blocks, headers int64, progress float64) map[string]any {
	return map[string]any{
		"getblockchaininfo": map[string]any{"chain": "main", "blocks": blocks, "headers": headers, "bestblockhash": "ab12",
			"difficulty": 5.2e7, "verificationprogress": progress, "pruned": true, "size_on_disk": int64(4_200_000_000), "initialblockdownload": progress < 0.9999},
		"getnetworkinfo": map[string]any{"subversion": "/LitecoinCore:0.21.5.8/", "connections": 12},
		"getmempoolinfo": map[string]any{"size": 1234, "bytes": 2_100_000},
		"getblockheader": map[string]any{"time": 1_791_620_000},
	}
}

func TestCheckSyncedLitecoin(t *testing.T) {
	srv := fakeNode(t, "umbrel", "pw", ltcResults(2961234, 2961234, 0.99999931), nil)
	s := Check(context.Background(), Node{Name: "Litecoin Node", RPC: node.NewClient(srv.URL, "umbrel", "pw", "", time.Second)})
	if s.State != "synced" || s.Blocks != 2961234 || s.Peers == nil || *s.Peers != 12 || s.MempoolTx == nil || *s.MempoolTx != 1234 || s.MempoolBytes != 2_100_000 ||
		s.TipTime != 1_791_620_000 || !s.Pruned || s.SizeOnDisk != 4_200_000_000 || s.Version != "/LitecoinCore:0.21.5.8/" || s.Chain != "main" || s.Error != "" {
		t.Fatalf("%+v", s)
	}
}

func TestCheckSyncing(t *testing.T) {
	srv := fakeNode(t, "u", "p", ltcResults(1500000, 2961234, 0.4231), nil)
	s := Check(context.Background(), Node{Name: "Litecoin Node", RPC: node.NewClient(srv.URL, "u", "p", "", time.Second)})
	if s.State != "syncing" || s.Progress != 0.4231 {
		t.Fatalf("%+v", s)
	}
	// All blocks of the known headers but still verifying old ones: syncing.
	srv2 := fakeNode(t, "u", "p", ltcResults(100, 100, 0.2), nil)
	if s := Check(context.Background(), Node{Name: "x", RPC: node.NewClient(srv2.URL, "u", "p", "", time.Second)}); s.State != "syncing" {
		t.Fatalf("blocks == headers at 20%% verified: %q", s.State)
	}
	// No headers yet (finding peers): syncing, not synced.
	srv3 := fakeNode(t, "u", "p", ltcResults(0, 0, 1), nil)
	if s := Check(context.Background(), Node{Name: "x", RPC: node.NewClient(srv3.URL, "u", "p", "", time.Second)}); s.State != "syncing" {
		t.Fatalf("no headers: %q", s.State)
	}
}

// A node that leaves out optional fields (size_on_disk) still reports.
func TestCheckDogecoinOldFields(t *testing.T) {
	res := map[string]any{
		"getblockchaininfo": map[string]any{"chain": "main", "blocks": 5891234, "headers": 5891234, "bestblockhash": "cd34",
			"difficulty": 2.1e7, "verificationprogress": 0.9999995, "pruned": true},
		"getnetworkinfo": map[string]any{"subversion": "/Shibetoshi:1.14.9/", "connections": 8},
		"getmempoolinfo": map[string]any{"size": 50, "bytes": 30_000},
		"getblockheader": map[string]any{"time": 1_791_620_500},
	}
	srv := fakeNode(t, "u", "p", res, nil)
	s := Check(context.Background(), Node{Name: "Dogecoin Node", RPC: node.NewClient(srv.URL, "u", "p", "", time.Second)})
	if s.State != "synced" || s.SizeOnDisk != 0 || s.Version != "/Shibetoshi:1.14.9/" || s.Peers == nil || *s.Peers != 8 {
		t.Fatalf("%+v", s)
	}
}

// A failed peers or mempool read shows as unknown, not as 0.
func TestCheckUnknownFields(t *testing.T) {
	res := ltcResults(10, 10, 1)
	delete(res, "getnetworkinfo")
	delete(res, "getmempoolinfo")
	srv := fakeNode(t, "u", "p", res, nil)
	s := Check(context.Background(), Node{Name: "x", RPC: node.NewClient(srv.URL, "u", "p", "", time.Second)})
	if s.State != "synced" || s.Peers != nil || s.MempoolTx != nil {
		t.Fatalf("%+v", s)
	}
	b, _ := json.Marshal(s)
	if strings.Contains(string(b), `"peers"`) || strings.Contains(string(b), `"mempool_tx"`) {
		t.Fatalf("unknown fields serialised: %s", b)
	}
}

func TestCheckStartingAndDown(t *testing.T) {
	srv := fakeNode(t, "u", "p", nil, map[string]int{"getblockchaininfo": -28})
	s := Check(context.Background(), Node{Name: "x", RPC: node.NewClient(srv.URL, "u", "p", "", time.Second)})
	if s.State != "starting" || s.Error != "Loading block index..." {
		t.Fatalf("warmup: %+v", s)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close() // nothing listens here
	s = Check(context.Background(), Node{Name: "x", RPC: node.NewClient("http://"+addr, "u", "p", "", time.Second)})
	if s.State != "down" || s.Error == "" {
		t.Fatalf("down: %+v", s)
	}
	// Wrong password: down, with the reason, and the password not in it.
	srv2 := fakeNode(t, "u", "right", ltcResults(1, 1, 1), nil)
	s = Check(context.Background(), Node{Name: "x", RPC: node.NewClient(srv2.URL, "u", "wrong", "", time.Second)})
	if s.State != "down" || !strings.Contains(s.Error, "unauthorized") || strings.Contains(s.Error, "wrong") {
		t.Fatalf("bad credentials: %+v", s)
	}
}

// A down node does not hold up the other one: both are checked at once.
func TestPollerChecksNodesConcurrently(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer slow.Close()
	fast := fakeNode(t, "u", "p", ltcResults(10, 10, 1), nil)
	p := NewPoller([]Node{
		{Name: "Litecoin Node", RPC: node.NewClient(fast.URL, "u", "p", "", time.Second)},
		{Name: "Dogecoin Node", RPC: node.NewClient(slow.URL, "u", "p", "", 500*time.Millisecond)},
	}, time.Hour, "")
	if s := p.Snapshot(); s[0].State != "starting" || s[1].Name != "Dogecoin Node" {
		t.Fatalf("before the first check: %+v", s)
	}
	start := time.Now()
	p.CheckAll(context.Background())
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("checks took %v", el)
	}
	s := p.Snapshot()
	if s[0].State != "synced" || s[1].State != "down" {
		t.Fatalf("%+v", s)
	}
}

func TestHandler(t *testing.T) {
	fast := fakeNode(t, "u", "secretpw", ltcResults(10, 10, 1), nil)
	p := NewPoller([]Node{{Name: "Litecoin Node", RPC: node.NewClient(fast.URL, "u", "secretpw", "", time.Second)}}, time.Hour, "")
	p.CheckAll(context.Background())
	srv := httptest.NewServer(Handler(p, "0.1.0"))
	defer srv.Close()
	get := func(path string) (*http.Response, string) {
		r, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r, string(b)
	}
	r, body := get("/api/status")
	if r.StatusCode != 200 || r.Header.Get("Content-Security-Policy") == "" || strings.Contains(body, "secretpw") || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("/api/status: %d %q", r.StatusCode, body)
	}
	var st struct {
		Version string   `json:"version"`
		Nodes   []Status `json:"nodes"`
	}
	if json.Unmarshal([]byte(body), &st) != nil || st.Version != "0.1.0" || len(st.Nodes) != 1 || st.Nodes[0].State != "synced" {
		t.Fatalf("status json: %s", body)
	}
	if r, _ := http.Post(srv.URL+"/api/status", "text/plain", nil); r.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/status: %d", r.StatusCode)
	}
	pr, page := get("/")
	if pr.Header.Get("Cache-Control") != "no-store" || pr.Header.Get("ETag") != "" {
		t.Fatalf("page caching: %v", pr.Header)
	}
	if !strings.Contains(page, `<pre id="out">`) || strings.Contains(page, "<style") || strings.Contains(page, "<script>") {
		t.Fatalf("page: %s", page)
	}
	_, js := get("/app.js")
	if !strings.Contains(js, "textContent") || strings.Contains(js, "innerHTML") {
		t.Fatal("app.js must write text, not HTML")
	}
	if _, css := get("/style.css"); !strings.Contains(css, "background: #000") || !strings.Contains(css, "color: #ff69b4") {
		t.Fatal("not hot pink on black")
	}
}
