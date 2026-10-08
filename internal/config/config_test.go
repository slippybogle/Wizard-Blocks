package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaultsNeedPayoutAndAuth(t *testing.T) {
	if _, err := Load("", env(nil)); err == nil {
		t.Fatal("defaults without rpc auth / payout accepted")
	}
	c, err := Load("", env(map[string]string{
		"WB_RPC_USER": "u", "WB_RPC_PASSWORD": "p", "WB_PAYOUT_ADDRESS": "bc1qexample",
		"WB_COIN": "BCH", "WB_EXTRANONCE2_SIZE": "4", "WB_VARDIFF_INITIAL": "2048", "WB_PROMETHEUS": "false",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Coin != "bch" || c.Stratum.Extranonce2Size != 4 || c.Vardiff.Initial != 2048 || c.API.Prometheus {
		t.Fatalf("env not applied: %+v", c)
	}
	if c.VersionMask() != 0x1fffe000 {
		t.Fatal("mask")
	}
}

func TestFileAndValidation(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.json")
	os.WriteFile(p, []byte(`{"coin":"btc","node":{"rpc_cookie_file":"/x/.cookie"},"payout":{"mode":"miner"}}`), 0o600)
	c, err := Load(p, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Payout.Mode != "miner" || c.Node.RPCURL != "http://127.0.0.1:8332" {
		t.Fatal("file/defaults merge")
	}
	os.WriteFile(p, []byte(`{"coin":"btc","unknown_field":1}`), 0o600)
	if _, err := Load(p, env(nil)); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown field accepted: %v", err)
	}
	bad := []map[string]string{
		{"WB_COIN": "ltc"},
		{"WB_VERSION_ROLLING_MASK": "ffffffff"},
		{"WB_VERSION_ROLLING_MASK": "xyz"},
		{"WB_EXTRANONCE2_SIZE": "1"},
		{"WB_EXTRANONCE2_SIZE": "12"},
		{"WB_PAYOUT_MODE": "pool"},
		{"WB_COINBASE_TAG": strings.Repeat("x", 61)},
		{"WB_VARDIFF_MIN": "0"},
		{"WB_ZMQ_HASHBLOCK": "ipc:///tmp/x"},
		{"WB_POLL_INTERVAL_MS": "10"},
	}
	for _, b := range bad {
		m := map[string]string{"WB_RPC_USER": "u", "WB_PAYOUT_ADDRESS": "x"}
		for k, v := range b {
			m[k] = v
		}
		if _, err := Load("", env(m)); err == nil {
			t.Errorf("%v accepted", b)
		}
	}
}
