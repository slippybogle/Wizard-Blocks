// Command nodestatus serves one page with the live status of the Litecoin
// and Dogecoin nodes (the page of the Litecoin + Dogecoin Node Umbrel app).
// Configured by env:
//
//	NS_LISTEN       listen address (default 0.0.0.0:8080)
//	NS_POLL_S       seconds between checks (default 5)
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
	var nodes []nodestatus.Node
	for _, n := range []struct{ name, prefix string }{{"Litecoin Node", "LTC"}, {"Dogecoin Node", "DOGE"}} {
		url := os.Getenv(n.prefix + "_RPC_URL")
		if url == "" {
			continue
		}
		nodes = append(nodes, nodestatus.Node{Name: n.name,
			RPC: node.NewClient(url, os.Getenv(n.prefix+"_RPC_USER"), os.Getenv(n.prefix+"_RPC_PASS"), "", 10*time.Second)})
		log.Info("watching node", "name", n.name, "url", url)
	}
	if len(nodes) == 0 {
		fmt.Fprintln(os.Stderr, "set LTC_RPC_URL and/or DOGE_RPC_URL")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	p := nodestatus.NewPoller(nodes, time.Duration(poll)*time.Second)
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
