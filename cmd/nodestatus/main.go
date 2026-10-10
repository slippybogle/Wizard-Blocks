// Command nodestatus serves one page with the live status of the Litecoin
// and Dogecoin nodes (the page of the Litecoin + Dogecoin Node Umbrel app).
// Configured by env:
//
//	NS_LISTEN       listen address (default 0.0.0.0:8080)
//	NS_POLL_S       seconds between checks (default 5)
//	NS_PAUSABLE     comma-separated node keys whose sync can be paused from
//	                the page (e.g. "doge")
//	NS_STATE_FILE   where pauses are kept across restarts (default: memory only)
//	LTC_RPC_URL, LTC_RPC_USER, LTC_RPC_PASS     Litecoin node (optional)
//	DOGE_RPC_URL, DOGE_RPC_USER, DOGE_RPC_PASS  Dogecoin node (optional)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fladnagmai/wizard-blocks/internal/node"
	"github.com/fladnagmai/wizard-blocks/internal/nodestatus"
)

var version = "dev"

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	listen := env("NS_LISTEN", "0.0.0.0:8080")
	poll, err := strconv.Atoi(env("NS_POLL_S", "5"))
	if err != nil || poll < 1 || poll > 3600 {
		fmt.Fprintln(os.Stderr, "NS_POLL_S must be 1..3600")
		os.Exit(2)
	}
	pausable := map[string]bool{}
	for _, k := range strings.Split(os.Getenv("NS_PAUSABLE"), ",") {
		if k = strings.TrimSpace(strings.ToLower(k)); k != "" {
			pausable[k] = true
		}
	}
	var nodes []nodestatus.Node
	for _, n := range []struct{ key, name, prefix string }{{"ltc", "Litecoin Node", "LTC"}, {"doge", "Dogecoin Node", "DOGE"}} {
		url := os.Getenv(n.prefix + "_RPC_URL")
		if url == "" {
			continue
		}
		nodes = append(nodes, nodestatus.Node{Key: n.key, Name: n.name, Pausable: pausable[n.key],
			RPC: node.NewClient(url, os.Getenv(n.prefix+"_RPC_USER"), os.Getenv(n.prefix+"_RPC_PASS"), "", 10*time.Second)})
		log.Info("watching node", "name", n.name, "url", url, "pausable", pausable[n.key])
	}
	if len(nodes) == 0 {
		fmt.Fprintln(os.Stderr, "set LTC_RPC_URL and/or DOGE_RPC_URL")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	p := nodestatus.NewPoller(nodes, time.Duration(poll)*time.Second, os.Getenv("NS_STATE_FILE"))
	go p.Run(ctx)
	srv := &http.Server{Addr: listen, Handler: nodestatus.Handler(p, version), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	log.Info("node status page", "listen", listen, "version", version)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("http", "err", err)
		os.Exit(1)
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
