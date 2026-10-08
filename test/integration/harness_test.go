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
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/slippybogle/wizard-blocks/internal/config"
	"github.com/slippybogle/wizard-blocks/internal/engine"
	"github.com/slippybogle/wizard-blocks/internal/logging"
	"github.com/slippybogle/wizard-blocks/internal/node"
)

const (
	btcImage = "bitcoin/bitcoin:28.1"
	bchImage = "zquestz/bitcoin-cash-node:latest"
	rpcUser  = "wbtest"
	rpcPass  = "wbtestpass"
)

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
	image := btcImage
	args := []string{
		"-regtest", "-server", "-rpcuser=" + rpcUser, "-rpcpassword=" + rpcPass,
		"-rpcbind=0.0.0.0", "-rpcallowip=0.0.0.0/0", "-zmqpubhashblock=tcp://0.0.0.0:28332",
		"-fallbackfee=0.0002", "-listen=1", "-printtoconsole=1", "-rpcworkqueue=256",
	}
	if coin == "btc" {
		args = append(args, "-limitancestorcount=1000", "-limitdescendantcount=1000",
			"-limitancestorsize=2000", "-limitdescendantsize=2000")
	} else {
		image = bchImage
	}
	args = append(args, extra...)
	// Fixed host ports: Docker reassigns ephemeral ports when a container
	// restarts, and the node-restart test needs stable endpoints.
	run := []string{"run", "-d", "--name", name, "--network", network, "--network-alias", name,
		"-p", fmt.Sprintf("127.0.0.1:%d:18443", freePort(t)), "-p", fmt.Sprintf("127.0.0.1:%d:28332", freePort(t)),
		"--entrypoint", "bitcoind", image}
	docker(t, append(run, args...)...)
	t.Cleanup(func() {
		if t.Failed() {
			out, _ := exec.Command("docker", "logs", "--tail", "40", name).CombinedOutput()
			t.Logf("--- %s logs ---\n%s", name, out)
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
	c.Vardiff.Min = 1e-12
	c.Vardiff.Initial = 1
	c.Stratum.MsgRatePerS = 2000
	c.Stratum.MsgBurst = 5000
	return c
}

func logFile(t *testing.T, name string) (*os.File, string) {
	dir := os.Getenv("WB_IT_LOGDIR")
	if dir == "" {
		dir = t.TempDir()
	}
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	return f, p
}

func startEngine(t *testing.T, cfg config.Config, name string) *Engine {
	t.Helper()
	f, path := logFile(t, name+".log")
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

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
