package work

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/slippybogle/wizard-blocks/internal/address"
	"github.com/slippybogle/wizard-blocks/internal/node"
	"github.com/slippybogle/wizard-blocks/internal/stats"
)

// fakeNode answers submitblock and getblockheader like bitcoind would for an
// accepted block.
func fakeNode(t *testing.T, submits *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &req)
		switch req.Method {
		case "submitblock":
			submits.Add(1)
			io.WriteString(w, `{"result":null,"error":null}`)
		case "getblockheader":
			io.WriteString(w, `{"result":{"confirmations":1,"height":1},"error":null}`)
		default:
			io.WriteString(w, `{"result":null,"error":{"code":-32601,"message":"not found"}}`)
		}
	}))
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func TestBlockAnnouncedOnce(t *testing.T) {
	var submits atomic.Int32
	srv := fakeNode(t, &submits)
	defer srv.Close()
	var logs syncBuf
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	st := stats.New("bch", "t", "")
	p, _ := ParamsFor(address.BCH)
	m := NewManager(Config{Params: p, Chain: "regtest", Extranonce2Size: 8, PollInterval: time.Second, RefreshInterval: time.Second},
		node.NewClient(srv.URL, "u", "p", "", 5*time.Second), st, log)

	raw := &node.BlockTemplate{Version: 536870912, PreviousBlockHash: strings.Repeat("0", 63) + "1",
		CoinbaseValue: 5000000000, MinTime: 1000, CurTime: 2000, Bits: "207fffff", Height: 1}
	tmpl, err := NewTemplate(raw, p, "regtest")
	if err != nil {
		t.Fatal(err)
	}
	cb, _ := BuildCoinbase(CoinbaseParams{Height: 1, Extranonce2Size: 8, PayoutScript: []byte{0x51}, Value: tmpl.CoinbaseValue, MinTxSize: 100})
	job := newJob("1", 1, tmpl, []byte{0x51}, "x", cb)
	en1, en2 := []byte{1, 2, 3, 4}, make([]byte, 8)
	c := Candidate{Job: job, Header: job.Header(en1, en2, 2000, 7, tmpl.Version), En1: en1, En2: en2, Worker: "w"}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ { // the same solution reported concurrently
		wg.Add(1)
		go func() { defer wg.Done(); m.SubmitBlock(c) }()
	}
	wg.Wait()
	m.Wait(t.Context())
	m.SubmitBlock(c) // and again later
	m.Wait(t.Context())

	out := logs.String()
	if n := strings.Count(out, "BLOCK ACCEPTED"); n != 1 {
		t.Fatalf("BLOCK ACCEPTED logged %d times:\n%s", n, out)
	}
	if n := strings.Count(out, "BLOCK FOUND"); n != 1 {
		t.Fatalf("BLOCK FOUND logged %d times", n)
	}
	if submits.Load() != 1 {
		t.Fatalf("submitblock called %d times", submits.Load())
	}
	if s := st.Snapshot(); s.Pool.BlocksFound != 1 || len(s.Blocks) != 1 {
		t.Fatalf("blocks_found %d records %d", s.Pool.BlocksFound, len(s.Blocks))
	}
}
