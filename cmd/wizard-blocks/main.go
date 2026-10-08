// Command wizard-blocks is a solo-mining Stratum V1 server for BTC and BCH.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/slippybogle/wizard-blocks/internal/config"
	"github.com/slippybogle/wizard-blocks/internal/engine"
	"github.com/slippybogle/wizard-blocks/internal/logging"
)

var version = "dev"

func main() {
	cfgPath := flag.String("config", os.Getenv("WB_CONFIG"), "path to JSON config file (optional; WB_* env vars override)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("wizard-blocks", version)
		return
	}
	cfg, err := config.Load(*cfgPath, os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		os.Exit(2)
	}
	log, err := logging.New(os.Stderr, cfg.Log.Level, cfg.Log.Format)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		os.Exit(2)
	}
	log = log.With("coin", cfg.Coin)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	e := engine.New(cfg, version, log)
	if err := e.Start(ctx); err != nil {
		log.Error("startup failed; refusing to run", "err", err)
		os.Exit(1)
	}
	if err := e.Run(ctx); err != nil {
		log.Error("engine stopped with error", "err", err)
		os.Exit(1)
	}
}
