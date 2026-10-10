//go:build integration && load

package integration

// Rental-readiness load test (loopback only): cmd/wbload drives simulated
// NiceHash / MiningRigRentals connections against a real engine on a
// regtest node, per coin. Run with:
//
//	go test -tags 'integration load' -run TestLoad -timeout 3h -v ./test/integration
//
// Results are logged as LOAD lines; nothing is asserted beyond the
// protocol checks, since the point is to find the limits.

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/fladnagmai/wizard-blocks/internal/config"
	"github.com/fladnagmai/wizard-blocks/internal/engine"
	"github.com/fladnagmai/wizard-blocks/internal/loadtest"
	"github.com/fladnagmai/wizard-blocks/internal/logging"
	"github.com/fladnagmai/wizard-blocks/internal/stratum"
)

var (
	wbloadOnce sync.Once
	wbloadBin  string
	wbloadErr  error
)

func wbload(t *testing.T) string {
	wbloadOnce.Do(func() {
		dir, err := os.MkdirTemp("", "wbload")
		if err != nil {
			wbloadErr = err
			return
		}
		wbloadBin = filepath.Join(dir, "wbload")
		out, err := exec.Command("go", "build", "-o", wbloadBin, "../../cmd/wbload").CombinedOutput()
		if err != nil {
			wbloadErr = fmt.Errorf("%v\n%s", err, out)
		}
	})
	if wbloadErr != nil {
		t.Fatal(wbloadErr)
	}
	return wbloadBin
}

// startQuietEngine is startEngine with info-level logs (debug logs every
// share, which would measure the disk rather than the engine).
func startQuietEngine(t *testing.T, cfg config.Config, name string) *Engine {
	t.Helper()
	f, path := logFile(t, name)
	log, _ := logging.New(f, "info", "text")
	e := engine.New(cfg, "load", log.With("instance", name))
	ctx, cancel := context.WithCancel(context.Background())
	if err := e.Start(ctx); err != nil {
		cancel()
		t.Fatalf("engine start: %v", err)
	}
	en := &Engine{E: e, cancel: cancel, done: make(chan error, 1), log: path}
	go func() { en.done <- e.Run(ctx); f.Close() }()
	t.Cleanup(func() { en.Stop(t) })
	select {
	case <-e.Manager().Ready():
	case <-time.After(60 * time.Second):
		t.Fatal("engine produced no work within 60s")
	}
	return en
}

func cpuSelf() time.Duration {
	var ru syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

type loadRun struct {
	Res      loadtest.Result
	EngCPU   time.Duration // engine (this process) CPU during the run
	PeakMem  uint64        // peak heap+stack in use during the run
	BaseMem  uint64
	PeakGors int
}

func memInUse() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapInuse + m.StackInuse
}

// runWbload runs cmd/wbload with args and returns its result plus what this
// (the engine's) process used meanwhile.
func runWbload(t *testing.T, args ...string) loadRun {
	t.Helper()
	runtime.GC()
	lr := loadRun{BaseMem: memInUse()}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tk := time.NewTicker(250 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tk.C:
				if m := memInUse(); m > lr.PeakMem {
					lr.PeakMem = m
				}
				if g := runtime.NumGoroutine(); g > lr.PeakGors {
					lr.PeakGors = g
				}
			}
		}
	}()
	c0 := cpuSelf()
	cmd := exec.Command(wbload(t), append(args, "-json")...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	lr.EngCPU = cpuSelf() - c0
	close(stop)
	wg.Wait()
	if err != nil {
		t.Fatalf("wbload %v: %v\n%s", args, err, out)
	}
	if err := json.Unmarshal(out, &lr.Res); err != nil {
		t.Fatalf("wbload output: %v\n%s", err, out)
	}
	return lr
}

func srcIPs(n int) string {
	ips := make([]string, n)
	for i := range ips {
		ips[i] = fmt.Sprintf("127.0.0.%d", i+1)
	}
	return strings.Join(ips, ",")
}

// netDiff is the regtest network difficulty in share units.
func netDiff(scrypt bool) float64 {
	t := new(big.Int).Lsh(big.NewInt(0x7fffff), 8*(0x20-3))
	d1 := new(big.Int).Lsh(big.NewInt(0xffff), 208)
	if scrypt {
		d1.Lsh(d1, 16)
	}
	f, _ := new(big.Float).Quo(new(big.Float).SetInt(d1), new(big.Float).SetInt(t)).Float64()
	return f
}

func TestLoadBCH(t *testing.T) { loadCoin(t, "bch") }
func TestLoadBTC(t *testing.T) { loadCoin(t, "btc") }
func TestLoadLTC(t *testing.T) { loadCoin(t, "ltc") }

func loadCoin(t *testing.T, coin string) {
	track(t)
	suffix := randHex(3)
	netName := "wbit-load-" + suffix
	docker(t, "network", "create", netName)
	t.Cleanup(func() { docker(t, "network", "rm", netName) })
	n := startNode(t, coin, netName, "wbit-load-"+coin+"-"+suffix)
	if coin == "bch" {
		// BCHN serves getblocktemplate only with a peer.
		startNode(t, coin, netName, "wbit-load-"+coin+"-b-"+suffix, "-connect="+n.Name)
	}
	var none any
	n.call(t, n.RPC, "createwallet", &none, "w")
	var hs []string
	payout := n.newAddress(t, map[bool]string{true: "legacy", false: "bech32"}[coin == "bch"])
	n.call(t, n.RPC, "generatetoaddress", &hs, 101, payout)

	cfg := engineConfig(n, coin)
	cfg.Payout.Mode, cfg.Payout.Address = "fixed", payout
	cfg.DataDir = t.TempDir()
	// Stratum limits as shipped in the Umbrel apps.
	def := config.Default().Stratum
	cfg.Stratum.MaxConnections, cfg.Stratum.MaxConnsPerIP = def.MaxConnections, def.MaxConnsPerIP
	cfg.Stratum.MsgRatePerS, cfg.Stratum.MsgBurst = def.MsgRatePerS, def.MsgBurst
	if coin == "ltc" {
		d := startNode(t, "doge", netName, "wbit-load-doge-"+suffix)
		var dogeAddr string
		d.call(t, d.RPC, "getnewaddress", &dogeAddr)
		d.call(t, d.RPC, "generatetoaddress", &hs, dogeAuxpowHeight+5, dogeAddr)
		cfg.Doge.RPCURL, cfg.Doge.RPCUser, cfg.Doge.RPCPassword = d.RPCURL, rpcUser, rpcPass
		cfg.Doge.ZMQHashBlock, cfg.Doge.PayoutAddress = d.ZMQ, dogeAddr
		cfg.Doge.PollIntervalMs, cfg.Doge.RefreshS = 500, 5
	}
	en := startQuietEngine(t, cfg, "load-"+coin)
	if coin == "ltc" {
		waitFor(t, "DOGE aux work", 30*time.Second, func() bool { w := en.E.Manager().Current(); return w != nil && w.Aux != nil })
	}
	addr := en.E.StratumAddr()
	scrypt := coin == "ltc"
	nd := netDiff(scrypt)
	common := []string{"-addr", addr, "-timeout", "15s"}
	if scrypt {
		common = append(common, "-scrypt")
	}
	logRun := func(name string, lr loadRun) {
		perConn := float64(0)
		if lr.Res.Opened > 0 && lr.PeakMem > lr.BaseMem {
			perConn = float64(lr.PeakMem-lr.BaseMem) / float64(lr.Res.Opened)
		}
		cpuPerShare := time.Duration(0)
		if lr.Res.Accepted > 0 {
			cpuPerShare = lr.EngCPU / time.Duration(lr.Res.Accepted)
		}
		t.Logf("LOAD %s %-28s %s | engine cpu=%v (%v/share) mem/conn=%.0fB goroutines=%d",
			strings.ToUpper(coin), name, lr.Res.String(), lr.EngCPU.Round(time.Millisecond), cpuPerShare, perConn, lr.PeakGors)
	}
	set := func(mut func(*stratum.DiffSettings)) {
		ds, _, _ := en.E.DiffSettings()
		ds.Min, ds.Max, ds.FixedDiff, ds.Overrides = 1e-12, 1e15, 0, nil
		ds.Start, ds.IgnorePasswordDiff, ds.IgnoreSuggest = nd*0.2, false, false
		if mut != nil {
			mut(&ds)
		}
		if err := en.E.UpdateDiffSettings(ds); err != nil {
			t.Fatal(err)
		}
	}
	diffsAre := func(name string, lr loadRun, want float64) {
		for ds, n := range lr.Res.Difficulties {
			d := mustFloat(ds)
			if d < want*0.999 || d > want*1.001 {
				t.Errorf("%s: %d connections at difficulty %g, want %g (%v)", name, n, d, want, lr.Res.Difficulties)
			}
		}
		if lr.Res.Opened == 0 || lr.Res.Failed > 0 {
			t.Errorf("%s: opened %d failed %d %v", name, lr.Res.Opened, lr.Res.Failed, lr.Res.FailReasons)
		}
	}

	// 1. Protocol: handshakes and difficulty precedence.
	set(nil)
	lr := runWbload(t, append(common, "-conns", "50", "-profile", "nicehash", "-duration", "2s")...)
	logRun("nicehash start-diff", lr)
	diffsAre("nicehash start-diff", lr, nd*0.2)
	lr = runWbload(t, append(common, "-conns", "50", "-profile", "mrr", "-d", fmt.Sprint(nd*0.4), "-duration", "2s")...)
	logRun("mrr d=", lr)
	diffsAre("mrr d=", lr, nd*0.4)
	lr = runWbload(t, append(common, "-conns", "50", "-profile", "mrr", "-suggest", fmt.Sprint(nd*0.6), "-duration", "2s")...)
	logRun("mrr suggest", lr)
	diffsAre("mrr suggest", lr, nd*0.6)
	set(func(d *stratum.DiffSettings) { d.IgnorePasswordDiff, d.IgnoreSuggest = true, true })
	lr = runWbload(t, append(common, "-conns", "50", "-profile", "mrr", "-d", fmt.Sprint(nd*0.4), "-suggest", fmt.Sprint(nd*0.6), "-duration", "2s")...)
	logRun("mrr d=+suggest ignored", lr)
	diffsAre("mrr d=+suggest ignored", lr, nd*0.2)
	set(func(d *stratum.DiffSettings) { d.Overrides = map[string]float64{"rentx.*": nd * 0.1} })
	lr = runWbload(t, append(common, "-conns", "50", "-profile", "mixed", "-d", fmt.Sprint(nd*0.4), "-duration", "2s")...)
	logRun("rentx.* override", lr)
	diffsAre("rentx.* override", lr, nd*0.1)

	lr = runWbload(t, append(common, "-conns", "20", "-rate", "2", "-duration", "5s")...)
	logRun("shares accepted", lr)
	if lr.Res.Accepted == 0 || len(lr.Res.Rejected) > 0 || lr.Res.Failed > 0 {
		t.Errorf("shares: accepted %d rejected %v failed %v", lr.Res.Accepted, lr.Res.Rejected, lr.Res.FailReasons)
	}
	if os.Getenv("WB_LOAD_SMOKE") != "" {
		return // protocol checks only
	}

	// 2. Connection limits as shipped (1024 total, 64 per IP).
	lr = runWbload(t, append(common, "-conns", "80", "-duration", "2s")...)
	logRun("defaults: 80 from one IP", lr)
	if lr.Res.Opened != def.MaxConnsPerIP {
		t.Errorf("one IP: opened %d, want %d", lr.Res.Opened, def.MaxConnsPerIP)
	}
	lr = runWbload(t, append(common, "-conns", "1100", "-src", srcIPs(40), "-duration", "2s")...)
	logRun("defaults: 1100 from 40 IPs", lr)
	if lr.Res.Opened != def.MaxConnections {
		t.Errorf("total: opened %d, want %d", lr.Res.Opened, def.MaxConnections)
	}

	// 3. Throughput at shipped limits: 1000 connections, rising share rate
	// (a rental's share rate is connections ÷ target seconds, whatever its
	// hashrate: vardiff raises difficulty with hashrate).
	for _, rate := range []string{"0.1", "0.5", "1", "2", "4", "8"} {
		lr = runWbload(t, append(common, "-conns", "1000", "-src", srcIPs(20), "-rate", rate, "-duration", "20s")...)
		logRun("1000 conns @"+rate+"/s each", lr)
		rej := uint64(0)
		for _, n := range lr.Res.Rejected {
			rej += n
		}
		offered := 1000 * 20 * mustFloat(rate)
		if lr.Res.SubmitP99 > time.Second || float64(lr.Res.Accepted) < 0.9*offered || rej > lr.Res.Accepted/100 || lr.Res.Disconnected > 0 {
			t.Logf("LOAD %s throughput limit reached at %s shares/s per connection", strings.ToUpper(coin), rate)
			break
		}
	}

	// 4. Connection scale with the limits raised (needs WB_MAX_CONNECTIONS /
	// WB_MAX_CONNECTIONS_PER_IP set on the app).
	en.Stop(t)
	cfg.Stratum.MaxConnections, cfg.Stratum.MaxConnsPerIP = 19000, 19000
	en = startQuietEngine(t, cfg, "load-"+coin+"-raised")
	common[1] = en.E.StratumAddr()
	set(func(d *stratum.DiffSettings) { d.Overrides = map[string]float64{"rentx.*": nd * 0.1} })
	for _, conns := range []string{"2000", "5000", "9000"} {
		lr = runWbload(t, append(common, "-conns", conns, "-src", srcIPs(50), "-rate", "0.1", "-duration", "20s")...)
		logRun("raised: "+conns+" conns @0.1/s", lr)
		if lr.Res.Failed > 0 || lr.Res.Refused > 0 || lr.Res.Disconnected > 0 || lr.Res.SubmitP99 > time.Second {
			t.Logf("LOAD %s connection limit reached at %s", strings.ToUpper(coin), conns)
			break
		}
	}
}

func mustFloat(s string) float64 {
	var f float64
	fmt.Sscan(s, &f)
	return f
}
