package nodestatus

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fladnagmai/wizard-blocks/internal/node"
)

func dogePrune(conf string) *PruneConfig {
	return &PruneConfig{ConfFile: conf, Default: 2200, Min: 2200, Options: []int{2200, 5000, 10000, 20000}}
}

func TestPruneConfReadWrite(t *testing.T) {
	f := filepath.Join(t.TempDir(), "dogecoin.conf")
	if _, err := readPrune(f); err == nil {
		t.Fatal("missing file read without error")
	}
	os.WriteFile(f, []byte("# comment\nprune = 3000\nfoo=1\n"), 0o644)
	if v, err := readPrune(f); err != nil || v != 3000 {
		t.Fatalf("read: %d %v", v, err)
	}
	if err := writePrune(f, 5000); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(f)
	if v, _ := readPrune(f); v != 5000 || !strings.Contains(string(b), "prune=5000\n") {
		t.Fatalf("after write: %q", b)
	}
}

func TestSetPrune(t *testing.T) {
	sn, srv := newSwitchNode(t)
	sn.prune = 2200 << 20
	dir := t.TempDir()
	conf, state := filepath.Join(dir, "dogecoin.conf"), filepath.Join(dir, "state.json")
	writePrune(conf, 2200)
	nodes := []Node{{Key: "doge", Name: "Dogecoin Node", RPC: node.NewClient(srv.URL, "", "", "", time.Second), Prune: dogePrune(conf)}}
	p := NewPoller(nodes, time.Hour, state)
	ctx := context.Background()
	if err := p.SetPrune(ctx, "doge", 550); !errors.Is(err, ErrBadPrune) {
		t.Fatalf("below Dogecoin's minimum: %v", err)
	}
	if err := p.SetPrune(ctx, "doge", 7777); !errors.Is(err, ErrBadPrune) {
		t.Fatalf("not an option: %v", err)
	}
	if v, _ := readPrune(conf); v != 2200 || sn.stopCount() != 0 {
		t.Fatal("a refused target changed the file or restarted the node")
	}
	if err := p.SetPrune(ctx, "ltc", 5000); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("unknown node: %v", err)
	}
	if err := p.SetPrune(ctx, "doge", 10000); err != nil {
		t.Fatal(err)
	}
	if v, _ := readPrune(conf); v != 10000 || sn.stopCount() != 1 {
		t.Fatalf("conf %d, stops %d", v, sn.stopCount())
	}
	p.CheckAll(ctx)
	s := p.Snapshot()[0]
	if s.PruneSet != 10000 || s.PruneRunning != 2200 || len(s.PruneOptions) != 4 {
		t.Fatalf("before the node restarts: %+v", s)
	}
	sn.prune = 10000 << 20 // the node came back with the new target
	p.CheckAll(ctx)
	if s := p.Snapshot()[0]; s.PruneRunning != 10000 {
		t.Fatalf("after restart: %+v", s)
	}
	// The choice is remembered: a file reset to the default (and a node that
	// came back with it) is undone at startup, and the node restarted once
	// it answers.
	writePrune(conf, 2200)
	sn.prune = 2200 << 20
	p2 := NewPoller(nodes, time.Hour, state)
	fixed, err := p2.EnsurePruneConf()
	if err != nil || len(fixed) != 1 {
		t.Fatalf("reconcile: %v %v", fixed, err)
	}
	if v, _ := readPrune(conf); v != 10000 {
		t.Fatalf("conf after reconcile: %d", v)
	}
	p2.CheckAll(ctx)
	p2.CheckAll(ctx)
	if sn.stopCount() != 2 {
		t.Fatalf("stops %d, want exactly one more", sn.stopCount())
	}
	// Nothing to do when the file already matches.
	if fixed, _ := NewPoller(nodes, time.Hour, state).EnsurePruneConf(); len(fixed) != 0 {
		t.Fatalf("rewrote a matching file: %v", fixed)
	}
}

// A prune change made while the node cannot be stopped (warming up) is
// applied once it answers; it used to wait for a manual restart.
func TestSetPruneDuringWarmup(t *testing.T) {
	sn, srv := newSwitchNode(t)
	sn.prune, sn.warmup = 2200<<20, true
	conf := filepath.Join(t.TempDir(), "dogecoin.conf")
	writePrune(conf, 2200)
	p := NewPoller([]Node{{Key: "doge", Name: "Dogecoin Node", RPC: node.NewClient(srv.URL, "", "", "", time.Second), Prune: dogePrune(conf)}}, time.Hour, "")
	ctx := context.Background()
	if err := p.SetPrune(ctx, "doge", 5000); err != nil {
		t.Fatal(err)
	}
	p.CheckAll(ctx)
	if sn.stopCount() != 0 {
		t.Fatal("stopped while warming up")
	}
	sn.mu.Lock()
	sn.warmup = false
	sn.mu.Unlock()
	p.CheckAll(ctx)
	if sn.stopCount() != 1 {
		t.Fatalf("new target never applied: stops %d", sn.stopCount())
	}
}

// First install: the app ships no conf files; the page writes the default
// before the nodes start, so a node already running that target is not
// restarted.
func TestEnsurePruneConfFirstStart(t *testing.T) {
	sn, srv := newSwitchNode(t)
	sn.prune = 2200 << 20
	conf := filepath.Join(t.TempDir(), "dogecoin.conf")
	if PruneConfReady(conf, 2200) {
		t.Fatal("missing file reported ready")
	}
	p := NewPoller([]Node{{Key: "doge", Name: "Dogecoin Node", RPC: node.NewClient(srv.URL, "", "", "", time.Second), Prune: dogePrune(conf)}}, time.Hour, "")
	if fixed, err := p.EnsurePruneConf(); err != nil || len(fixed) != 1 || !PruneConfReady(conf, 2200) {
		t.Fatalf("%v %v", fixed, err)
	}
	p.CheckAll(context.Background())
	p.CheckAll(context.Background())
	if sn.stopCount() != 0 {
		t.Fatalf("restarted a node already on its target: %d", sn.stopCount())
	}
}

// A missing conf file (the node would run unpruned) is recreated with the
// default target and a node running without it restarted.
func TestEnsurePruneConfMissing(t *testing.T) {
	sn, srv := newSwitchNode(t)
	conf := filepath.Join(t.TempDir(), "dogecoin.conf")
	p := NewPoller([]Node{{Key: "doge", Name: "Dogecoin Node", RPC: node.NewClient(srv.URL, "", "", "", time.Second), Prune: dogePrune(conf)}}, time.Hour, "")
	if fixed, err := p.EnsurePruneConf(); err != nil || len(fixed) != 1 {
		t.Fatalf("%v %v", fixed, err)
	}
	if v, _ := readPrune(conf); v != 2200 {
		t.Fatalf("default not written: %d", v)
	}
	p.CheckAll(context.Background())
	if sn.stopCount() != 1 {
		t.Fatalf("node not restarted: %d", sn.stopCount())
	}
	if s := p.Snapshot()[0]; s.State != "starting" {
		t.Fatalf("state %q", s.State)
	}
}

func TestPruneHTTP(t *testing.T) {
	sn, srv := newSwitchNode(t)
	conf := filepath.Join(t.TempDir(), "dogecoin.conf")
	writePrune(conf, 2200)
	p := NewPoller([]Node{{Key: "doge", Name: "Dogecoin Node", RPC: node.NewClient(srv.URL, "", "", "", time.Second), Prune: dogePrune(conf)}}, time.Hour, "")
	hs := httptest.NewServer(Handler(p, "t"))
	defer hs.Close()
	post := func(body string, hdr map[string]string) int {
		req, _ := http.NewRequest(http.MethodPost, hs.URL+"/api/nodes/doge/prune", strings.NewReader(body))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	if c := post(`{"mib":5000}`, nil); c != 403 {
		t.Fatalf("without the page's header: %d", c)
	}
	if c := post(`{"mib":5000}`, map[string]string{"X-NS-Action": "1", "Origin": "http://evil.example"}); c != 403 {
		t.Fatalf("cross-site: %d", c)
	}
	ok := map[string]string{"X-NS-Action": "1"}
	if c := post(`{"mib":123}`, ok); c != 400 {
		t.Fatalf("bad target: %d", c)
	}
	if c := post(`{}`, ok); c != 400 {
		t.Fatalf("no target: %d", c)
	}
	if v, _ := readPrune(conf); v != 2200 || sn.stopCount() != 0 {
		t.Fatal("refused requests changed something")
	}
	if c := post(`{"mib":5000}`, ok); c != 200 {
		t.Fatalf("set: %d", c)
	}
	if v, _ := readPrune(conf); v != 5000 || sn.stopCount() != 1 {
		t.Fatalf("conf %d stops %d", v, sn.stopCount())
	}
}
