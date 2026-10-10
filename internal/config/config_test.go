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

func TestPayoutFromUI(t *testing.T) {
	base := map[string]string{"WB_RPC_USER": "u", "WB_RPC_PASSWORD": "p", "WB_COIN": "bch"}
	if _, err := Load("", env(base)); err == nil {
		t.Fatal("empty payout accepted without a UI admin password")
	}
	base["WB_UI_ADMIN_PASSWORD"] = "pw"
	if _, err := Load("", env(base)); err != nil {
		t.Fatalf("empty payout with UI settings refused: %v", err)
	}
	delete(base, "WB_UI_ADMIN_PASSWORD")
	base["WB_UI_SETTINGS_OPEN"] = "true"
	if c, err := Load("", env(base)); err != nil || !c.UI.SettingsOpen {
		t.Fatalf("empty payout with open settings refused: %v", err)
	}
	base["WB_UI_LISTEN"] = "off"
	if _, err := Load("", env(base)); err == nil {
		t.Fatal("empty payout accepted with the UI off")
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
		{"WB_COIN": "eth"},
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

func TestDifficultyEnv(t *testing.T) {
	base := map[string]string{"WB_RPC_USER": "u", "WB_PAYOUT_ADDRESS": "x"}
	with := func(kv map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range kv {
			m[k] = v
		}
		return m
	}
	c, err := Load("", env(with(map[string]string{
		"WB_VARDIFF_MIN": "16", "WB_VARDIFF_MAX": "65536", "WB_VARDIFF_TARGET_SECONDS": "15",
		"WB_VARDIFF_TARGET_SHARE_S": "99", "WB_FIXED_DIFF": "512", "WB_VARDIFF_INITIAL": "64",
	})))
	if err != nil {
		t.Fatal(err)
	}
	if c.Vardiff.Min != 16 || c.Vardiff.Max != 65536 || c.Vardiff.TargetShareS != 15 || c.Vardiff.FixedDiff != 512 {
		t.Fatalf("difficulty env not applied: %+v", c.Vardiff)
	}
	for _, bad := range []map[string]string{
		{"WB_FIXED_DIFF": "4", "WB_VARDIFF_MIN": "16"},     // fixed below min
		{"WB_FIXED_DIFF": "1e9", "WB_VARDIFF_MAX": "1000"}, // fixed above max
		{"WB_VARDIFF_TARGET_SECONDS": "0"},
		{"WB_VARDIFF_TARGET_SECONDS": "601"},
		{"WB_VARDIFF_MIN": "100", "WB_VARDIFF_MAX": "10"},
		{"WB_VARDIFF_MAX": "1e16"},
		{"WB_FIXED_DIFF": "abc"},
	} {
		if _, err := Load("", env(with(bad))); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

func TestStratumPorts(t *testing.T) {
	c, err := Load("", env(map[string]string{"WB_RPC_USER": "u", "WB_PAYOUT_ADDRESS": "x"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Stratum.Listen != "0.0.0.0:62023" || c.StratumPort() != 62023 {
		t.Fatalf("default stratum %s / %d", c.Stratum.Listen, c.StratumPort())
	}
	c, err = Load("", env(map[string]string{"WB_RPC_USER": "u", "WB_PAYOUT_ADDRESS": "x",
		"WB_STRATUM_LISTEN": "0.0.0.0:3333", "WB_STRATUM_PUBLIC_PORT": "3335"}))
	if err != nil || c.StratumPort() != 3335 {
		t.Fatalf("public port %d %v", c.StratumPort(), err)
	}
}

func TestUIOff(t *testing.T) {
	c, err := Load("", env(map[string]string{"WB_RPC_USER": "u", "WB_PAYOUT_ADDRESS": "x", "WB_UI_LISTEN": "off"}))
	if err != nil || c.UI.Listen != "" {
		t.Fatalf("WB_UI_LISTEN=off: %q %v", c.UI.Listen, err)
	}
}

func TestEnvFileOverrides(t *testing.T) {
	p := filepath.Join(t.TempDir(), "override.env")
	os.WriteFile(p, []byte("# node fallback\nWB_RPC_URL=http://10.0.0.5:8332\nexport WB_RPC_PASSWORD=\"s3cret\"\n"), 0o600)
	base := map[string]string{"WB_ENV_FILE": p, "WB_RPC_URL": "http://x:1", "WB_RPC_USER": "u"}
	env, used, err := WithEnvFile(func(k string) string { return base[k] })
	if err != nil || used != p {
		t.Fatal(used, err)
	}
	if env("WB_RPC_URL") != "http://10.0.0.5:8332" || env("WB_RPC_PASSWORD") != "s3cret" || env("WB_RPC_USER") != "u" {
		t.Fatal("override not applied")
	}
	base["WB_ENV_FILE"] = filepath.Join(t.TempDir(), "missing.env")
	if _, used, err := WithEnvFile(func(k string) string { return base[k] }); err != nil || used != "" {
		t.Fatal("missing file should be ignored", err)
	}
	os.WriteFile(p, []byte("PATH=/evil\n"), 0o600)
	base["WB_ENV_FILE"] = p
	if _, _, err := WithEnvFile(func(k string) string { return base[k] }); err == nil {
		t.Fatal("non-WB_ key accepted")
	}
}

func TestLTCVardiffDefaults(t *testing.T) {
	env := map[string]string{"WB_RPC_USER": "u", "WB_RPC_PASSWORD": "p", "WB_COIN": "ltc", "WB_PAYOUT_ADDRESS": "x"}
	c, err := Load("", func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if c.Vardiff.Initial != 262144 || c.Vardiff.Min != 1024 {
		t.Fatalf("LTC defaults: initial %v min %v", c.Vardiff.Initial, c.Vardiff.Min)
	}
	if c.UI.Style != "stats" {
		t.Fatalf("LTC default page: %q, want stats", c.UI.Style)
	}
	// An explicit choice is kept (the user's usual 200000).
	env["WB_VARDIFF_INITIAL"] = "200000"
	c, _ = Load("", func(k string) string { return env[k] })
	if c.Vardiff.Initial != 200000 {
		t.Fatalf("explicit initial overridden: %v", c.Vardiff.Initial)
	}
	// BTC keeps its own defaults.
	env = map[string]string{"WB_RPC_USER": "u", "WB_RPC_PASSWORD": "p", "WB_COIN": "btc", "WB_PAYOUT_ADDRESS": "x"}
	c, _ = Load("", func(k string) string { return env[k] })
	if c.Vardiff.Initial != 65536 || c.Vardiff.Min != 1 {
		t.Fatalf("BTC defaults changed: %v %v", c.Vardiff.Initial, c.Vardiff.Min)
	}
	if c.UI.Style != "" {
		t.Fatalf("BTC page changed: %q", c.UI.Style)
	}
}
