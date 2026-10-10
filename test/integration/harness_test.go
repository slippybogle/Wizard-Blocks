//go:build integration

// Package integration is the regtest harness: it starts real bitcoind and
// BCHN nodes in Docker, runs the engine in-process and mines with the
// built-in CPU Stratum miner. Run with scripts/regtest-test.sh or
//
//	go test -tags integration -v -timeout 60m ./test/integration
package integration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fladnagmai/wizard-blocks/internal/config"
	"github.com/fladnagmai/wizard-blocks/internal/engine"
	"github.com/fladnagmai/wizard-blocks/internal/logging"
	"github.com/fladnagmai/wizard-blocks/internal/node"
)

const (
	btcImage = "bitcoin/bitcoin:28.1"
	bchImage = "zquestz/bitcoin-cash-node:latest"
	// Litecoin Core is built locally from the official release (see
	// litecoind/Dockerfile and ensureLitecoinImage).
	ltcVersion = "0.21.5.8"
	ltcSHA256  = "43200c9f9d65ebc126ea5833ca9429e144c4b3273da6bb9f4e89fd7450ab1be9" // SHA256SUMS.asc, signed by D35621D53A1CC6A3456758D03620E9D387E55666
	ltcImage   = "wbit-litecoind:" + ltcVersion
	// Dogecoin Core, likewise (dogecoind/Dockerfile).
	dogeVersion = "1.14.9"
	dogeSHA256  = "4f227117b411a7c98622c970986e27bcfc3f547a72bef65e7d9e82989175d4f8" // SHA256SUMS.asc, signed by DC6EF4A8BF9F1B1E4DE1EE522D3A345B98D0DC1F
	dogeImage   = "wbit-dogecoind:" + dogeVersion
	rpcUser     = "wbtest"
	rpcPass     = "wbtestpass"
)

// Run accounting: every top-level test calls track, and TestMain prints how
// many really ran against live nodes. Nothing here skips: if Docker or a
// node cannot start, the run fails.
var (
	testsRun, testsPassed, nodesStarted, blocksVerified atomic.Int64
)

func track(t *testing.T) {
	testsRun.Add(1)
	t.Cleanup(func() {
		if !t.Failed() {
			testsPassed.Add(1)
		}
	})
}

func TestMain(m *testing.M) {
	if out, err := exec.Command("docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: Docker is not available, regtests cannot run: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	run, passed := testsRun.Load(), testsPassed.Load()
	fmt.Printf("REGTEST SUMMARY: %d tests executed, %d passed, %d failed; %d nodes started; %d blocks verified on chain\n",
		run, passed, run-passed, nodesStarted.Load(), blocksVerified.Load())
	if code == 0 && run == 0 {
		fmt.Fprintln(os.Stderr, "FAIL: no regtest executed")
		code = 1
	}
	os.Exit(code)
}

func docker(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Node is a regtest full node in a container.
type Node struct {
	Name    string
	RPCURL  string
	ZMQ     string
	RPC     *node.Client
	Wallet  *node.Client
	isBCH   bool
	network string
}

func startNode(t *testing.T, coin, network, name string, extra ...string) *Node {
	t.Helper()
	image, entry := btcImage, "bitcoind"
	args := []string{
		"-regtest", "-server", "-rpcuser=" + rpcUser, "-rpcpassword=" + rpcPass, "-rpcport=18443",
		"-rpcbind=0.0.0.0", "-rpcallowip=0.0.0.0/0", "-zmqpubhashblock=tcp://0.0.0.0:28332",
		"-fallbackfee=0.0002", "-listen=1", "-printtoconsole=1", "-rpcworkqueue=256",
	}
	switch coin {
	case "btc":
		args = append(args, "-limitancestorcount=1000", "-limitdescendantcount=1000",
			"-limitancestorsize=2000", "-limitdescendantsize=2000")
	case "ltc":
		ensureLitecoinImage(t)
		image, entry = ltcImage, "litecoind"
	case "doge":
		ensureDogecoinImage(t)
		image, entry = dogeImage, "dogecoind"
	default:
		image = bchImage
	}
	args = append(args, extra...)
	// Fixed host ports: Docker reassigns ephemeral ports when a container
	// restarts, and the node-restart test needs stable endpoints.
	// A port picked by freePort can be taken before Docker binds it, so a
	// clash is retried with new ports rather than failing the test.
	for attempt := 1; ; attempt++ {
		run := []string{"run", "-d", "--name", name, "--network", network, "--network-alias", name,
			"-p", fmt.Sprintf("127.0.0.1:%d:18443", freePort(t)), "-p", fmt.Sprintf("127.0.0.1:%d:28332", freePort(t)),
			"--entrypoint", entry, image}
		out, err := exec.Command("docker", append(run, args...)...).CombinedOutput()
		if err == nil {
			break
		}
		clash := strings.Contains(string(out), "port is already allocated") || strings.Contains(string(out), "address already in use")
		if !clash || attempt == 5 {
			t.Fatalf("docker run %s: %v\n%s", name, err, out)
		}
		t.Logf("host port clash starting %s (attempt %d), retrying: %s", name, attempt, strings.TrimSpace(string(out)))
		exec.Command("docker", "rm", "-f", name).Run()
	}
	t.Cleanup(func() {
		// With WB_IT_LOGDIR set, keep every node's full log.
		if dir := os.Getenv("WB_IT_LOGDIR"); dir != "" {
			out, _ := exec.Command("docker", "logs", name).CombinedOutput()
			p := filepath.Join(dir, logName(t, name))
			_ = os.WriteFile(p, out, 0o644)
			if t.Failed() {
				t.Logf("--- %s full log: %s", name, p)
			}
		}
		if t.Failed() {
			out, _ := exec.Command("docker", "logs", "--tail", "40", name).CombinedOutput()
			t.Logf("--- %s logs (last 40 lines) ---\n%s", name, out)
		}
		exec.Command("docker", "rm", "-f", name).Run()
	})
	rpcHost := docker(t, "port", name, "18443/tcp")
	zmqHost := docker(t, "port", name, "28332/tcp")
	rpcHost = strings.Split(rpcHost, "\n")[0]
	zmqHost = strings.Split(zmqHost, "\n")[0]
	n := &Node{Name: name, RPCURL: "http://" + rpcHost, ZMQ: "tcp://" + zmqHost, isBCH: coin == "bch", network: network}
	n.RPC = node.NewClient(n.RPCURL, rpcUser, rpcPass, "", 60*time.Second)
	n.Wallet = node.NewClient(n.RPCURL+"/wallet/w", rpcUser, rpcPass, "", 120*time.Second)
	ctx := context.Background()
	deadline := time.Now().Add(60 * time.Second)
	for {
		if _, err := n.RPC.GetBlockchainInfo(ctx); err == nil {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("node %s not ready: %v", name, err)
		}
		time.Sleep(300 * time.Millisecond)
	}
	nodesStarted.Add(1)
	return n
}

func (n *Node) call(t *testing.T, c *node.Client, method string, out any, params ...any) {
	t.Helper()
	if err := c.Call(context.Background(), method, params, out); err != nil {
		t.Fatalf("%s %v: %v", method, params, err)
	}
}

func (n *Node) height(t *testing.T) int64 {
	var h int64
	n.call(t, n.RPC, "getblockcount", &h)
	return h
}

func (n *Node) newAddress(t *testing.T, typ string) string {
	var a string
	if typ == "" {
		n.call(t, n.Wallet, "getnewaddress", &a)
	} else {
		n.call(t, n.Wallet, "getnewaddress", &a, "", typ)
	}
	return a
}

func (n *Node) scriptOf(t *testing.T, addr string) string {
	var v node.ValidateAddressResult
	n.call(t, n.RPC, "validateaddress", &v, addr)
	if !v.IsValid {
		t.Fatalf("node says %s invalid", addr)
	}
	return v.ScriptPubKey
}

// Engine is an in-process engine instance.
type Engine struct {
	E      *engine.Engine
	cancel context.CancelFunc
	done   chan error
	log    string
}

func engineConfig(n *Node, coin string) config.Config {
	c := config.Default()
	c.Coin = coin
	c.Node.RPCURL = n.RPCURL
	c.Node.RPCUser, c.Node.RPCPassword = rpcUser, rpcPass
	c.Node.ZMQHashBlock = n.ZMQ
	c.Node.PollIntervalMs = 500
	c.Node.TemplateRefreshS = 2
	c.Payout.CoinbaseTag = "/wizard-blocks-it/"
	c.Stratum.Listen = "127.0.0.1:0"
	c.API.Listen = "127.0.0.1:0"
	c.UI.Listen = "127.0.0.1:0"
	c.Vardiff.Min = 1e-12
	c.Vardiff.Initial = 1
	c.Stratum.MsgRatePerS = 2000
	c.Stratum.MsgBurst = 5000
	c.Stratum.MaxConnsPerIP = 256 // all harness clients share 127.0.0.1
	return c
}

func logFile(t *testing.T, name string) (*os.File, string) {
	dir := os.Getenv("WB_IT_LOGDIR")
	if dir == "" {
		dir = t.TempDir()
	}
	p := filepath.Join(dir, logName(t, name))
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	return f, p
}

// logName prefixes a log file with the test name, so tests that reuse an
// engine or node name do not overwrite each other's logs.
func logName(t *testing.T, name string) string {
	return strings.NewReplacer("/", "_", " ", "_", "=", "-").Replace(t.Name()) + "__" + name + ".log"
}

func startEngine(t *testing.T, cfg config.Config, name string) *Engine {
	t.Helper()
	f, path := logFile(t, name)
	log, _ := logging.New(f, "debug", "text")
	e := engine.New(cfg, "it", log.With("instance", name))
	ctx, cancel := context.WithCancel(context.Background())
	if err := e.Start(ctx); err != nil {
		cancel()
		t.Fatalf("engine start: %v", err)
	}
	en := &Engine{E: e, cancel: cancel, done: make(chan error, 1), log: path}
	go func() { en.done <- e.Run(ctx); f.Close() }()
	t.Cleanup(func() {
		en.Stop(t)
		if t.Failed() {
			b, _ := os.ReadFile(path)
			lines := strings.Split(string(b), "\n")
			if len(lines) > 60 {
				lines = lines[len(lines)-60:]
			}
			t.Logf("--- engine %s log tail (%s) ---\n%s", name, path, strings.Join(lines, "\n"))
		}
	})
	select {
	case <-e.Manager().Ready():
	case <-time.After(60 * time.Second):
		t.Fatal("engine produced no work within 60s")
	}
	return en
}

// Stop shuts the engine down gracefully (idempotent).
func (en *Engine) Stop(t *testing.T) {
	if en.cancel == nil {
		return
	}
	en.cancel()
	en.cancel = nil
	select {
	case err := <-en.done:
		if err != nil {
			t.Errorf("engine run: %v", err)
		}
	case <-time.After(90 * time.Second):
		t.Error("engine did not shut down within 90s")
	}
}

func (en *Engine) acceptedBlocks() int {
	n := 0
	for _, b := range en.E.Stats().Blocks() {
		if b.Status == "accepted" {
			n++
		}
	}
	return n
}

// waitFor polls cond until true or timeout.
func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// parseSats converts a JSON amount to satoshis exactly: integers are taken as
// satoshis (Core getblockstats), decimals as coins (BCHN).
func parseSats(t *testing.T, raw json.RawMessage) int64 {
	s := strings.TrimSpace(string(raw))
	if !strings.Contains(s, ".") && !strings.ContainsAny(s, "eE") {
		v, ok := new(big.Int).SetString(s, 10)
		if !ok {
			t.Fatalf("amount %q", s)
		}
		return v.Int64()
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		t.Fatalf("amount %q", s)
	}
	r.Mul(r, big.NewRat(100_000_000, 1))
	if !r.IsInt() {
		t.Fatalf("amount %q not an integral number of sats", s)
	}
	return r.Num().Int64()
}

// apiGet fetches a JSON document from the engine API.
func apiGet(t *testing.T, en *Engine, path string) []byte {
	t.Helper()
	out, err := exec.Command("curl", "-sf", "http://"+en.E.APIAddr()+path).Output()
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return out
}

// ensureLitecoinImage and ensureDogecoinImage build the node test images
// once from the official release tarballs, each checked against the SHA-256
// from its signed SHA256SUMS.asc, on a digest-pinned Debian base.
func ensureLitecoinImage(t *testing.T) {
	ensureReleaseImage(t, ltcImage, "litecoind", "litecoin-"+ltcVersion+"-x86_64-linux-gnu.tar.gz",
		"https://github.com/litecoin-project/litecoin/releases/download/v"+ltcVersion+"/", ltcSHA256,
		"LTC_VERSION="+ltcVersion, "LTC_SHA256="+ltcSHA256)
}

func ensureDogecoinImage(t *testing.T) {
	ensureReleaseImage(t, dogeImage, "dogecoind", "dogecoin-"+dogeVersion+"-x86_64-linux-gnu.tar.gz",
		"https://github.com/dogecoin/dogecoin/releases/download/v"+dogeVersion+"/", dogeSHA256,
		"DOGE_VERSION="+dogeVersion, "DOGE_SHA256="+dogeSHA256)
}

func ensureReleaseImage(t *testing.T, image, dockerDir, name, baseURL, sha string, buildArgs ...string) {
	t.Helper()
	if exec.Command("docker", "image", "inspect", image).Run() == nil {
		return
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = os.TempDir()
	}
	dir := filepath.Join(cache, "wizard-blocks-it", strings.TrimSuffix(name, "-x86_64-linux-gnu.tar.gz"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tarball := filepath.Join(dir, name)
	sum := func() string {
		b, err := os.ReadFile(tarball)
		if err != nil {
			return ""
		}
		h := sha256.Sum256(b)
		return hex.EncodeToString(h[:])
	}
	if sum() != sha {
		if out, err := exec.Command("curl", "-fsSL", "-m", "600", "-o", tarball, baseURL+name).CombinedOutput(); err != nil {
			t.Fatalf("download %s: %v\n%s", baseURL+name, err, out)
		}
		if got := sum(); got != sha {
			t.Fatalf("%s: SHA-256 %s, signed release says %s", name, got, sha)
		}
	}
	src, err := os.ReadFile(filepath.Join(dockerDir, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"build", "-q", "-t", image}
	for _, a := range buildArgs {
		args = append(args, "--build-arg", a)
	}
	if out, err := exec.Command("docker", append(args, dir)...).CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", image, err, out)
	}
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
