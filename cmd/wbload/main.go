// Command wbload is the rental-readiness load tool: many simulated
// rental-style connections (NiceHash / MiningRigRentals handshakes, rig
// names, extranonce.subscribe, suggest_difficulty, d= passwords) against a
// Wizard-Blocks engine. Loopback only: it refuses any other address.
//
// The engine must give these connections a tiny difficulty (fixed
// difficulty 1e-12, or a "rentx.*" override) so shares can be made without
// real hashing; shares never meet the network target.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/fladnagmai/wizard-blocks/internal/loadtest"
)

func main() {
	var cfg loadtest.Config
	var srcs string
	var profile string
	var asJSON bool
	flag.StringVar(&cfg.Addr, "addr", "127.0.0.1:62023", "engine stratum address (loopback only)")
	flag.StringVar(&srcs, "src", "", "comma-separated loopback source addresses (e.g. 127.0.0.1,127.0.0.2); empty = default")
	flag.IntVar(&cfg.Conns, "conns", 100, "connections")
	flag.DurationVar(&cfg.Ramp, "ramp", 0, "spread connection opening over this long")
	flag.StringVar(&profile, "profile", "mixed", "handshake: nicehash | mrr | mixed")
	flag.BoolVar(&cfg.Scrypt, "scrypt", false, "Litecoin-style (Scrypt) shares")
	flag.StringVar(&cfg.NameFmt, "names", "rentx.rig%05d", "worker name format (one %d)")
	flag.Float64Var(&cfg.PwDiff, "d", 0, "MRR: send x,d=<n> as the password")
	flag.Float64Var(&cfg.Suggest, "suggest", 0, "MRR: send mining.suggest_difficulty <n>")
	flag.BoolVar(&cfg.SameName, "same-name", false, "every connection uses the same worker name (MRR rigs under one pool profile)")
	flag.Float64Var(&cfg.ShareRate, "rate", 0, "shares per second per connection (0 = idle)")
	flag.DurationVar(&cfg.Duration, "duration", 30*time.Second, "submit phase length")
	flag.DurationVar(&cfg.Timeout, "timeout", 10*time.Second, "per request timeout")
	flag.BoolVar(&asJSON, "json", false, "print the result as JSON")
	flag.Parse()
	cfg.Profile = loadtest.Profile(profile)
	if srcs != "" {
		cfg.SourceIPs = strings.Split(srcs, ",")
	}
	if err := loopbackOnly(cfg.Addr, cfg.SourceIPs); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	res, err := loadtest.Run(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if asJSON {
		json.NewEncoder(os.Stdout).Encode(res)
		return
	}
	fmt.Println(res)
}

func loopbackOnly(addr string, srcs []string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	for _, h := range append([]string{host}, srcs...) {
		ip := net.ParseIP(h)
		if ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("refusing %q: wbload runs against loopback addresses only", h)
		}
	}
	return nil
}
