package stats

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Server serves /stats (JSON), /metrics (Prometheus) and /healthz.
type Server struct {
	C          *Collector
	Prometheus bool
	Log        *slog.Logger
	srv        *http.Server
	ln         net.Listener
}

// Listen binds addr. Call Serve afterwards.
func (s *Server) Listen(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.ln = ln
	mux := http.NewServeMux()
	mux.HandleFunc("/stats", s.handleStats)
	mux.HandleFunc("/healthz", s.handleHealth)
	if s.Prometheus {
		mux.HandleFunc("/metrics", s.handleMetrics)
	}
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 30 * time.Second}
	return nil
}

// Addr returns the bound address.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Serve serves until Shutdown.
func (s *Server) Serve() error {
	err := s.srv.Serve(s.ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// Shutdown stops the server.
func (s *Server) Shutdown(ctx context.Context) error { return s.srv.Shutdown(ctx) }

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(s.C.Snapshot())
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	snap := s.C.Snapshot()
	ok := snap.Node.Connected && snap.Node.Synced && !snap.Template.UpdatedAt.IsZero()
	w.Header().Set("Content-Type", "application/json")
	if !ok {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": ok, "node_connected": snap.Node.Connected, "synced": snap.Node.Synced})
}

func esc(v string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v)
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// WritePrometheus renders the snapshot in Prometheus text format 0.0.4.
func WritePrometheus(w io.Writer, s Snapshot) {
	g := func(name, help, typ string) { fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ) }
	coin := esc(s.Coin)

	g("wb_up_seconds", "Engine uptime in seconds.", "gauge")
	fmt.Fprintf(w, "wb_up_seconds{coin=\"%s\"} %g\n", coin, s.Uptime)
	g("wb_node_connected", "Whether the full node RPC is reachable.", "gauge")
	fmt.Fprintf(w, "wb_node_connected{coin=\"%s\"} %g\n", coin, b2f(s.Node.Connected))
	g("wb_node_synced", "Whether the full node is synced.", "gauge")
	fmt.Fprintf(w, "wb_node_synced{coin=\"%s\"} %g\n", coin, b2f(s.Node.Synced))
	g("wb_node_height", "Full node block height.", "gauge")
	fmt.Fprintf(w, "wb_node_height{coin=\"%s\"} %d\n", coin, s.Node.Height)
	g("wb_zmq_connected", "Whether the ZMQ hashblock subscription is connected.", "gauge")
	fmt.Fprintf(w, "wb_zmq_connected{coin=\"%s\"} %g\n", coin, b2f(s.Node.ZMQConnected))
	g("wb_network_difficulty", "Network difficulty of the current template.", "gauge")
	fmt.Fprintf(w, "wb_network_difficulty{coin=\"%s\"} %g\n", coin, s.Template.NetworkDiff)
	g("wb_template_height", "Height of the block being mined.", "gauge")
	fmt.Fprintf(w, "wb_template_height{coin=\"%s\"} %d\n", coin, s.Template.Height)
	g("wb_template_transactions", "Transactions in the current template (incl. coinbase).", "gauge")
	fmt.Fprintf(w, "wb_template_transactions{coin=\"%s\"} %d\n", coin, s.Template.Transactions)
	g("wb_template_coinbase_value_sats", "Block reward + fees of the current template.", "gauge")
	fmt.Fprintf(w, "wb_template_coinbase_value_sats{coin=\"%s\"} %d\n", coin, s.Template.CoinbaseValue)
	g("wb_connections", "Open Stratum connections.", "gauge")
	fmt.Fprintf(w, "wb_connections{coin=\"%s\"} %d\n", coin, s.Pool.Connections)
	g("wb_hashrate", "Estimated pool hashrate (H/s).", "gauge")
	for _, x := range []struct {
		w string
		v float64
	}{{"1m", s.Pool.Hashrate1m}, {"5m", s.Pool.Hashrate5m}, {"1h", s.Pool.Hashrate1h}} {
		fmt.Fprintf(w, "wb_hashrate{coin=\"%s\",window=\"%s\"} %g\n", coin, x.w, x.v)
	}
	g("wb_shares_total", "Shares by result.", "counter")
	fmt.Fprintf(w, "wb_shares_total{coin=\"%s\",result=\"accepted\",reason=\"\"} %d\n", coin, s.Pool.Accepted)
	reasons := make([]string, 0, len(s.Pool.Rejects))
	for r := range s.Pool.Rejects {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		fmt.Fprintf(w, "wb_shares_total{coin=\"%s\",result=\"rejected\",reason=\"%s\"} %d\n", coin, esc(r), s.Pool.Rejects[r])
	}
	g("wb_best_share_difficulty", "Best share difficulty ever achieved.", "gauge")
	fmt.Fprintf(w, "wb_best_share_difficulty{coin=\"%s\"} %g\n", coin, s.Pool.BestDiff)
	g("wb_blocks_total", "Block candidates by final status.", "counter")
	byStatus := map[string]int{"accepted": 0, "rejected": 0, "orphaned": 0, "pending": 0, "stale": 0}
	for _, b := range s.Blocks {
		byStatus[b.Status]++
	}
	statuses := make([]string, 0, len(byStatus))
	for k := range byStatus {
		statuses = append(statuses, k)
	}
	sort.Strings(statuses)
	for _, k := range statuses {
		fmt.Fprintf(w, "wb_blocks_total{coin=\"%s\",status=\"%s\"} %d\n", coin, esc(k), byStatus[k])
	}
	g("wb_worker_hashrate", "Estimated worker hashrate (H/s).", "gauge")
	for _, wk := range s.Workers {
		n := esc(wk.Name)
		fmt.Fprintf(w, "wb_worker_hashrate{coin=\"%s\",worker=\"%s\",window=\"1m\"} %g\n", coin, n, wk.Hashrate1m)
		fmt.Fprintf(w, "wb_worker_hashrate{coin=\"%s\",worker=\"%s\",window=\"5m\"} %g\n", coin, n, wk.Hashrate5m)
		fmt.Fprintf(w, "wb_worker_hashrate{coin=\"%s\",worker=\"%s\",window=\"1h\"} %g\n", coin, n, wk.Hashrate1h)
	}
	g("wb_worker_shares_total", "Worker shares by result.", "counter")
	for _, wk := range s.Workers {
		n := esc(wk.Name)
		fmt.Fprintf(w, "wb_worker_shares_total{coin=\"%s\",worker=\"%s\",result=\"accepted\"} %d\n", coin, n, wk.Accepted)
		fmt.Fprintf(w, "wb_worker_shares_total{coin=\"%s\",worker=\"%s\",result=\"rejected\"} %d\n", coin, n, wk.Rejected)
	}
	g("wb_worker_best_share_difficulty", "Best share difficulty per worker.", "gauge")
	for _, wk := range s.Workers {
		fmt.Fprintf(w, "wb_worker_best_share_difficulty{coin=\"%s\",worker=\"%s\"} %g\n", coin, esc(wk.Name), wk.BestDiff)
	}
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	WritePrometheus(w, s.C.Snapshot())
}
