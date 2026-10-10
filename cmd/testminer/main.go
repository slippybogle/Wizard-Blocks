// Command testminer is a CPU Stratum V1 miner with BIP310 version rolling,
// intended for regtest/testnet testing of the engine (it is far too slow for
// mainnet).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync/atomic"
	"time"

	"github.com/fladnagmai/wizard-blocks/internal/testminer"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:3333", "stratum host:port")
	user := flag.String("user", "testminer", "username (payout address in miner mode)")
	pass := flag.String("pass", "x", "password")
	threads := flag.Int("threads", 1, "hashing threads")
	zeros := flag.Int("min-zero-bits", 0, "only submit hashes with at least this many leading zero bits")
	mask := flag.Uint("mask", 0x1fffe000, "requested version-rolling mask (0 disables)")
	vmode := flag.String("version-mode", "bip310", "version bits encoding: bip310 | xor (ESP-Miner) | or (cgminer)")
	nonBlock := flag.Bool("non-block-shares", false, "submit only shares that do not solve a block (needs share difficulty < network)")
	interval := flag.Duration("share-interval", 500*time.Millisecond, "pause between shares in -non-block-shares mode")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	c, err := testminer.Dial(*addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer c.Close()
	if *mask != 0 {
		m, err := c.Configure(ctx, uint32(*mask))
		if err != nil {
			fmt.Fprintln(os.Stderr, "configure:", err)
			os.Exit(1)
		}
		fmt.Printf("version-rolling mask %08x\n", m)
	}
	if err := c.Subscribe(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "subscribe:", err)
		os.Exit(1)
	}
	r, err := c.Authorize(ctx, *user, *pass)
	if err != nil || !r.OK() {
		fmt.Fprintf(os.Stderr, "authorize failed: %v %s\n", err, r.Error)
		os.Exit(1)
	}
	var ok, bad atomic.Uint64
	go func() {
		for range time.Tick(10 * time.Second) {
			fmt.Printf("accepted=%d rejected=%d\n", ok.Load(), bad.Load())
		}
	}()
	err = c.Mine(ctx, testminer.MineOptions{
		Worker: *user, Threads: *threads, MinZeroBits: *zeros, RollVersion: *mask != 0, VersionMode: *vmode, NonBlockShares: *nonBlock, ShareInterval: *interval,
		OnResult: func(j *testminer.Job, r *testminer.Response, hash string, _ uint32) {
			if r.OK() {
				ok.Add(1)
			} else {
				bad.Add(1)
				fmt.Printf("rejected job=%s hash=%s err=%s\n", j.ID, hash, r.Error)
			}
		},
	})
	if err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
