package nodestatus

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fladnagmai/wizard-blocks/internal/node"
)

// switchNode is a fake node with a p2p network switch (setnetworkactive).
type switchNode struct {
	mu     sync.Mutex
	active bool
	calls  []bool
}

func (sn *switchNode) restart() { sn.mu.Lock(); sn.active = true; sn.mu.Unlock() }
func (sn *switchNode) isActive() bool {
	sn.mu.Lock()
	defer sn.mu.Unlock()
	return sn.active
}

func newSwitchNode(t *testing.T) (*switchNode, *httptest.Server) {
	sn := &switchNode{active: true}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		sn.mu.Lock()
		defer sn.mu.Unlock()
		var res any
		switch req.Method {
		case "getblockchaininfo":
			res = map[string]any{"chain": "main", "blocks": 100, "headers": 5000, "bestblockhash": "ab", "verificationprogress": 0.02, "pruned": true}
		case "getnetworkinfo":
			peers := 8
			if !sn.active {
				peers = 0
			}
			res = map[string]any{"subversion": "/Shibetoshi:1.14.9/", "connections": peers, "networkactive": sn.active}
		case "getmempoolinfo":
			res = map[string]any{"size": 0, "bytes": 0}
		case "getblockheader":
			res = map[string]any{"time": 1}
		case "setnetworkactive":
			on, _ := req.Params[0].(bool)
			sn.active = on
			sn.calls = append(sn.calls, on)
			res = on
		}
		json.NewEncoder(w).Encode(map[string]any{"id": req.ID, "result": res, "error": nil})
	}))
	t.Cleanup(srv.Close)
	return sn, srv
}

func TestPauseResumeDoge(t *testing.T) {
	sn, srv := newSwitchNode(t)
	ltc := fakeNode(t, "u", "p", ltcResults(10, 10, 1), nil)
	state := filepath.Join(t.TempDir(), "state.json")
	nodes := []Node{
		{Key: "ltc", Name: "Litecoin Node", RPC: node.NewClient(ltc.URL, "u", "p", "", time.Second)},
		{Key: "doge", Name: "Dogecoin Node", RPC: node.NewClient(srv.URL, "", "", "", time.Second), Pausable: true},
	}
	p := NewPoller(nodes, time.Hour, state)
	ctx := context.Background()
	if err := p.SetPaused(ctx, "ltc", true); err != ErrNotPausable {
		t.Fatalf("pausing ltc: %v", err)
	}
	if err := p.SetPaused(ctx, "nope", true); err != ErrUnknownNode {
		t.Fatalf("unknown node: %v", err)
	}
	if err := p.SetPaused(ctx, "doge", true); err != nil {
		t.Fatal(err)
	}
	if sn.isActive() {
		t.Fatal("doge network still on after pause")
	}
	s := p.Snapshot()[1]
	if !s.Paused || s.NetworkActive == nil || *s.NetworkActive || !s.Pausable || s.State != "syncing" {
		t.Fatalf("after pause: %+v", s)
	}
	// The node restarts with its network on: the next check switches it off.
	sn.restart()
	p.CheckAll(ctx)
	if sn.isActive() || !p.Snapshot()[1].Paused {
		t.Fatal("pause not re-applied after a node restart")
	}
	// This service restarts: the pause is read back and kept.
	p2 := NewPoller(nodes, time.Hour, state)
	if !p2.Snapshot()[1].Paused {
		t.Fatal("pause lost across a service restart")
	}
	sn.restart()
	p2.CheckAll(ctx)
	if sn.isActive() {
		t.Fatal("pause not re-applied after a service restart")
	}
	// Resume: network back on, and it stays on.
	if err := p2.SetPaused(ctx, "doge", false); err != nil {
		t.Fatal(err)
	}
	p2.CheckAll(ctx)
	if !sn.isActive() || p2.Snapshot()[1].Paused {
		t.Fatal("resume did not switch the network back on")
	}
	if p3 := NewPoller(nodes, time.Hour, state); p3.Snapshot()[1].Paused {
		t.Fatal("resume not saved")
	}
	// Litecoin is never touched.
	if s := p2.Snapshot()[0]; s.Paused || s.Pausable {
		t.Fatalf("ltc: %+v", s)
	}
}

func TestPauseHTTP(t *testing.T) {
	sn, srv := newSwitchNode(t)
	p := NewPoller([]Node{{Key: "doge", Name: "Dogecoin Node", RPC: node.NewClient(srv.URL, "", "", "", time.Second), Pausable: true}}, time.Hour, "")
	hs := httptest.NewServer(Handler(p, "t"))
	defer hs.Close()
	post := func(path, body string, hdr map[string]string) int {
		req, _ := http.NewRequest(http.MethodPost, hs.URL+path, bytes.NewBufferString(body))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
		return r.StatusCode
	}
	ok := map[string]string{"X-NS-Action": "1", "Content-Type": "application/json"}
	if c := post("/api/nodes/doge/sync", `{"paused":true}`, map[string]string{"Content-Type": "application/json"}); c != 403 || !sn.isActive() {
		t.Fatalf("without the page's header: %d", c)
	}
	if c := post("/api/nodes/doge/sync", `{"paused":true}`, map[string]string{"X-NS-Action": "1", "Origin": "http://evil.example"}); c != 403 || !sn.isActive() {
		t.Fatalf("cross-site: %d", c)
	}
	if c := post("/api/nodes/doge/sync", `{}`, ok); c != 400 {
		t.Fatalf("no paused field: %d", c)
	}
	if c := post("/api/nodes/ltc/sync", `{"paused":true}`, ok); c != 404 {
		t.Fatalf("unknown node: %d", c)
	}
	if c := post("/api/nodes/doge/sync", `{"paused":true}`, ok); c != 200 || sn.isActive() {
		t.Fatalf("pause: %d active=%v", c, sn.isActive())
	}
	if r, _ := http.Get(hs.URL + "/api/nodes/doge/sync"); r.StatusCode == 200 {
		t.Fatal("GET must not change anything")
	}
	if c := post("/api/nodes/doge/sync", `{"paused":false}`, ok); c != 200 || !sn.isActive() {
		t.Fatalf("resume: %d", c)
	}
}
