//go:build integration

package integration

import (
	"fmt"
	"testing"
	"time"
)

// TestRegtestMinerHandoff stops a miner the moment its block is recorded as
// accepted and connects a fresh one straight away, many times over. A block
// is marked accepted before the engine has published the next template, so a
// miner that connects in that window must not be handed the old tip's job:
// on regtest it would solve it at once, making a second block at a height we
// already own (orphaned or stale).
func TestRegtestMinerHandoff(t *testing.T) {
	track(t)
	suffix := randHex(3)
	netName := "wbit-handoff-" + suffix
	docker(t, "network", "create", netName)
	t.Cleanup(func() { docker(t, "network", "rm", netName) })
	a := startNode(t, "btc", netName, "wbit-handoff-"+suffix)
	var none any
	a.call(t, a.RPC, "createwallet", &none, "w")

	for _, zmq := range []bool{true, false} {
		t.Run(fmt.Sprintf("zmq=%v", zmq), func(t *testing.T) {
			cfg := engineConfig(a, "btc")
			cfg.Payout.Mode, cfg.Payout.Address = "fixed", a.newAddress(t, "bech32")
			if !zmq {
				cfg.Node.ZMQHashBlock = ""
			}
			cfg.DataDir = t.TempDir()
			en := startEngine(t, cfg, fmt.Sprintf("handoff-zmq-%v", zmq))
			rec := &recorder{m: map[string]submission{}}
			const handoffs = 40
			for i := 0; i < handoffs; i++ {
				before := en.acceptedBlocks()
				m := startMiner(t, en, rec, fmt.Sprintf("rig%d", i), "bip310")
				mineUntil(t, en, before+1, time.Minute)
				m.stop(t)
			}
			waitFor(t, "no pending blocks", 30*time.Second, func() bool {
				for _, b := range en.E.Stats().Blocks() {
					if b.Status == "pending" {
						return false
					}
				}
				return true
			})
			bad := 0
			for _, b := range en.E.Stats().Blocks() {
				if b.Status != "accepted" {
					bad++
					t.Errorf("block %s at height %d: %s (%s)", b.Hash, b.Height, b.Status, b.Reason)
				}
			}
			if n := en.acceptedBlocks(); n < handoffs {
				t.Fatalf("%d accepted blocks after %d handoffs", n, handoffs)
			}
			blocksVerified.Add(int64(en.acceptedBlocks()))
			t.Logf("%d handoffs: %d accepted, %d not accepted", handoffs, en.acceptedBlocks(), bad)
		})
	}
}
