// Package config loads and validates the engine configuration: a JSON file
// (optional) overlaid with WB_* environment variables.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// Node configures the full node connection.
type Node struct {
	RPCURL           string `json:"rpc_url"`
	RPCUser          string `json:"rpc_user"`
	RPCPassword      string `json:"rpc_password"`
	RPCCookieFile    string `json:"rpc_cookie_file"`
	ZMQHashBlock     string `json:"zmq_hashblock"`
	PollIntervalMs   int    `json:"poll_interval_ms"`
	TemplateRefreshS int    `json:"template_refresh_s"`
	RPCTimeoutS      int    `json:"rpc_timeout_s"`
}

// Doge configures Dogecoin merged mining on top of a Litecoin parent. It
// is off when RPCURL is empty; with a node but no PayoutAddress the engine
// mines Litecoin only and says so.
type Doge struct {
	RPCURL         string `json:"rpc_url"`
	RPCUser        string `json:"rpc_user"`
	RPCPassword    string `json:"rpc_password"`
	RPCCookieFile  string `json:"rpc_cookie_file"`
	ZMQHashBlock   string `json:"zmq_hashblock"`
	PayoutAddress  string `json:"payout_address"`
	PollIntervalMs int    `json:"poll_interval_ms"`
	RefreshS       int    `json:"refresh_s"`
}

// Enabled reports whether a Dogecoin node is configured.
func (d Doge) Enabled() bool { return d.RPCURL != "" }

// Payout configures where block rewards go.
type Payout struct {
	Mode        string `json:"mode"` // fixed | miner
	Address     string `json:"address"`
	CoinbaseTag string `json:"coinbase_tag"`
}

// Stratum configures the miner-facing server.
type Stratum struct {
	Listen             string  `json:"listen"`
	PublicPort         int     `json:"public_port"` // port miners connect to, if different (e.g. Docker host mapping); UI display only
	Extranonce2Size    int     `json:"extranonce2_size"`
	VersionRollingMask string  `json:"version_rolling_mask"`
	MaxConnections     int     `json:"max_connections"`
	MaxConnsPerIP      int     `json:"max_connections_per_ip"`
	AuthTimeoutS       int     `json:"auth_timeout_s"`
	IdleTimeoutS       int     `json:"idle_timeout_s"`
	MaxLineBytes       int     `json:"max_line_bytes"`
	MsgRatePerS        float64 `json:"msg_rate_per_s"`
	MsgBurst           float64 `json:"msg_burst"`
}

// Vardiff configures per-connection difficulty.
type Vardiff struct {
	Initial      float64 `json:"initial"`
	Min          float64 `json:"min"`
	Max          float64 `json:"max"`
	TargetShareS float64 `json:"target_share_s"` // a.k.a. VARDIFF_TARGET_SECONDS
	FixedDiff    float64 `json:"fixed_diff"`     // > 0 disables vardiff (FIXED_DIFF)
	RetargetS    float64 `json:"retarget_s"`
	VariancePct  float64 `json:"variance_pct"`
}

// API configures the local stats endpoint.
type API struct {
	Listen     string `json:"listen"`
	Prometheus bool   `json:"prometheus"`
}

// UI configures the embedded web interface.
type UI struct {
	Listen string `json:"listen"` // "" or "off" disables the UI
	// AdminPassword protects the Settings section. Empty = settings read-only
	// (unless SettingsOpen).
	AdminPassword string `json:"admin_password"`
	// SettingsOpen makes Settings usable without a password, for a UI that
	// sits behind an authenticating reverse proxy (e.g. Umbrel's app_proxy).
	// A password can still be set from the Settings page.
	SettingsOpen bool `json:"settings_open"`
	// Style picks the page: "mine" (the pixel-art Mine and Ledger, default),
	// "simple" (one stats page, used by the BTC Umbrel app) or "stats" (plain
	// text pool and miner hashrates, the LTC default).
	Style string `json:"style"`
}

// Log configures logging.
type Log struct {
	Level  string `json:"level"`
	Format string `json:"format"`
}

// Config is the complete configuration.
type Config struct {
	Coin    string  `json:"coin"`
	Node    Node    `json:"node"`
	Doge    Doge    `json:"doge"`
	Payout  Payout  `json:"payout"`
	Stratum Stratum `json:"stratum"`
	Vardiff Vardiff `json:"vardiff"`
	API     API     `json:"api"`
	UI      UI      `json:"ui"`
	Log     Log     `json:"log"`
	DataDir string  `json:"data_dir"`
}

// SHAStartDiff is the BCH/BTC start difficulty: a rented 1 EH/s on one
// connection sends ~3500 shares/s here for the instant before vardiff's
// first step (well within the message burst), and a 1.2 TH/s Bitaxe gets
// its difficulty probed down within a minute or two.
const SHAStartDiff = 65536

// LTC vardiff defaults (Scrypt share units).
const (
	LTCStartDiff = 262144
	LTCMinDiff   = 1024
)

// Default returns the default configuration.
func Default() Config {
	return Config{
		Coin: "btc",
		Node: Node{RPCURL: "http://127.0.0.1:8332", PollIntervalMs: 1000, TemplateRefreshS: 30, RPCTimeoutS: 30},
		Doge: Doge{PollIntervalMs: 1000, RefreshS: 5},
		Payout: Payout{
			Mode: "fixed", CoinbaseTag: "/wizard-blocks/",
		},
		Stratum: Stratum{
			Listen: "0.0.0.0:62023", Extranonce2Size: 8, VersionRollingMask: "1fffe000",
			MaxConnections: 1024, MaxConnsPerIP: 64, AuthTimeoutS: 60, IdleTimeoutS: 600,
			MaxLineBytes: 16384, MsgRatePerS: 100, MsgBurst: 500,
		},
		Vardiff: Vardiff{Initial: SHAStartDiff, Min: 1, Max: 1e15, TargetShareS: 10, RetargetS: 60, VariancePct: 30},
		API:     API{Listen: "127.0.0.1:8080", Prometheus: true},
		UI:      UI{Listen: "0.0.0.0:8420"},
		Log:     Log{Level: "info", Format: "json"},
	}
}

// Load reads path (if non-empty) over the defaults, then applies env.
func Load(path string, env func(string) string) (Config, error) {
	c := Default()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return c, err
		}
		dec := json.NewDecoder(strings.NewReader(string(b)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			return c, fmt.Errorf("%s: %w", path, err)
		}
	}
	if err := c.applyEnv(env); err != nil {
		return c, err
	}
	// Scrypt share difficulty is 65536x easier per unit than SHA-256d, so
	// the SHA-256d defaults would be far too low: when vardiff was left at
	// its defaults, LTC starts at 262144 (the Elphapex DG Home 1's own
	// default, ~8 s per share at 2.1 GH/s) with a floor of 1024.
	if strings.EqualFold(c.Coin, "ltc") {
		def := Default().Vardiff
		if c.Vardiff.Initial == def.Initial && c.Vardiff.Min == def.Min {
			c.Vardiff.Initial, c.Vardiff.Min = LTCStartDiff, LTCMinDiff
		}
		// The LTC app's page is the plain stats page unless one was chosen.
		if c.UI.Style == "" {
			c.UI.Style = "stats"
		}
	}
	// An empty env value means "unset", so "off" is how env disables the UI.
	if strings.EqualFold(c.UI.Listen, "off") {
		c.UI.Listen = ""
	}
	return c, c.Validate()
}

func (c *Config) applyEnv(env func(string) string) error {
	str := map[string]*string{
		"WB_COIN": &c.Coin, "WB_RPC_URL": &c.Node.RPCURL, "WB_RPC_USER": &c.Node.RPCUser,
		"WB_RPC_PASSWORD": &c.Node.RPCPassword, "WB_RPC_COOKIE_FILE": &c.Node.RPCCookieFile,
		"WB_ZMQ_HASHBLOCK": &c.Node.ZMQHashBlock, "WB_PAYOUT_MODE": &c.Payout.Mode,
		"WB_PAYOUT_ADDRESS": &c.Payout.Address, "WB_COINBASE_TAG": &c.Payout.CoinbaseTag,
		"WB_STRATUM_LISTEN": &c.Stratum.Listen, "WB_VERSION_ROLLING_MASK": &c.Stratum.VersionRollingMask,
		"WB_API_LISTEN": &c.API.Listen, "WB_LOG_LEVEL": &c.Log.Level, "WB_LOG_FORMAT": &c.Log.Format,
		"WB_DOGE_RPC_URL": &c.Doge.RPCURL, "WB_DOGE_RPC_USER": &c.Doge.RPCUser, "WB_DOGE_RPC_PASSWORD": &c.Doge.RPCPassword,
		"WB_DOGE_RPC_COOKIE_FILE": &c.Doge.RPCCookieFile, "WB_DOGE_ZMQ_HASHBLOCK": &c.Doge.ZMQHashBlock,
		"WB_DOGE_PAYOUT_ADDRESS": &c.Doge.PayoutAddress,
		"WB_DATA_DIR":            &c.DataDir, "WB_UI_LISTEN": &c.UI.Listen, "WB_UI_STYLE": &c.UI.Style, "WB_UI_ADMIN_PASSWORD": &c.UI.AdminPassword,
	}
	for k, p := range str {
		if v, ok := lookup(env, k); ok {
			*p = v
		}
	}
	ints := map[string]*int{
		"WB_POLL_INTERVAL_MS": &c.Node.PollIntervalMs, "WB_TEMPLATE_REFRESH_S": &c.Node.TemplateRefreshS,
		"WB_EXTRANONCE2_SIZE": &c.Stratum.Extranonce2Size, "WB_MAX_CONNECTIONS": &c.Stratum.MaxConnections,
		"WB_MAX_CONNECTIONS_PER_IP": &c.Stratum.MaxConnsPerIP, "WB_STRATUM_PUBLIC_PORT": &c.Stratum.PublicPort,
		"WB_DOGE_POLL_INTERVAL_MS": &c.Doge.PollIntervalMs, "WB_DOGE_REFRESH_S": &c.Doge.RefreshS,
	}
	for k, p := range ints {
		if v, ok := lookup(env, k); ok {
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("%s: %w", k, err)
			}
			*p = n
		}
	}
	floats := map[string]*float64{
		"WB_VARDIFF_INITIAL": &c.Vardiff.Initial, "WB_VARDIFF_MIN": &c.Vardiff.Min,
		"WB_VARDIFF_MAX": &c.Vardiff.Max, "WB_VARDIFF_TARGET_SHARE_S": &c.Vardiff.TargetShareS,
		"WB_FIXED_DIFF": &c.Vardiff.FixedDiff,
	}
	for k, p := range floats {
		if v, ok := lookup(env, k); ok {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return fmt.Errorf("%s: %w", k, err)
			}
			*p = f
		}
	}
	// WB_VARDIFF_TARGET_SECONDS is the documented name; it wins over the
	// older WB_VARDIFF_TARGET_SHARE_S when both are set.
	if v, ok := lookup(env, "WB_VARDIFF_TARGET_SECONDS"); ok {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return fmt.Errorf("WB_VARDIFF_TARGET_SECONDS: %w", err)
		}
		c.Vardiff.TargetShareS = f
	}
	if v, ok := lookup(env, "WB_UI_SETTINGS_OPEN"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("WB_UI_SETTINGS_OPEN: %w", err)
		}
		c.UI.SettingsOpen = b
	}
	if v, ok := lookup(env, "WB_PROMETHEUS"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("WB_PROMETHEUS: %w", err)
		}
		c.API.Prometheus = b
	}
	return nil
}

func lookup(env func(string) string, k string) (string, bool) {
	if env == nil {
		return "", false
	}
	v := env(k)
	return v, v != ""
}

// StratumPort returns the port miners connect to: Stratum.PublicPort if
// set, otherwise the port of Stratum.Listen.
func (c *Config) StratumPort() int {
	if c.Stratum.PublicPort > 0 {
		return c.Stratum.PublicPort
	}
	_, p, _ := net.SplitHostPort(c.Stratum.Listen)
	n, _ := strconv.Atoi(p)
	return n
}

// VersionMask parses Stratum.VersionRollingMask.
func (c *Config) VersionMask() uint32 {
	v, _ := strconv.ParseUint(c.Stratum.VersionRollingMask, 16, 32)
	return uint32(v)
}

// Validate checks the configuration for internal consistency. Address
// validity is checked later, against the node.
func (c *Config) Validate() error {
	var errs []error
	add := func(f string, a ...any) { errs = append(errs, fmt.Errorf(f, a...)) }
	c.Coin = strings.ToLower(c.Coin)
	if c.Coin != "btc" && c.Coin != "bch" && c.Coin != "ltc" {
		add("coin must be btc, bch or ltc, got %q", c.Coin)
	}
	if c.Node.RPCURL == "" {
		add("node.rpc_url is required")
	}
	if c.Node.RPCUser == "" && c.Node.RPCCookieFile == "" {
		add("node.rpc_user/rpc_password or node.rpc_cookie_file is required")
	}
	if c.Node.ZMQHashBlock != "" && !strings.HasPrefix(c.Node.ZMQHashBlock, "tcp://") {
		add("node.zmq_hashblock must be tcp://host:port")
	}
	if c.Node.PollIntervalMs < 100 || c.Node.PollIntervalMs > 60000 {
		add("node.poll_interval_ms must be 100..60000")
	}
	if c.Node.TemplateRefreshS < 1 || c.Node.TemplateRefreshS > 600 {
		add("node.template_refresh_s must be 1..600")
	}
	if c.Node.RPCTimeoutS < 1 {
		add("node.rpc_timeout_s must be positive")
	}
	if c.Doge.Enabled() {
		if c.Coin != "ltc" {
			add("doge merged mining needs coin ltc (the Scrypt parent), got %q", c.Coin)
		}
		if c.Doge.RPCUser == "" && c.Doge.RPCCookieFile == "" {
			add("doge.rpc_user/rpc_password or doge.rpc_cookie_file is required")
		}
		if c.Doge.ZMQHashBlock != "" && !strings.HasPrefix(c.Doge.ZMQHashBlock, "tcp://") {
			add("doge.zmq_hashblock must be tcp://host:port")
		}
		if c.Doge.PollIntervalMs < 100 || c.Doge.PollIntervalMs > 60000 {
			add("doge.poll_interval_ms must be 100..60000")
		}
		if c.Doge.RefreshS < 1 || c.Doge.RefreshS > 600 {
			add("doge.refresh_s must be 1..600")
		}
	} else if c.Doge.PayoutAddress != "" {
		add("doge.payout_address is set but no doge.rpc_url")
	}
	switch c.Payout.Mode {
	case "fixed":
		// Without an address the engine can still start when the payout
		// address can be set from the password-protected web UI; it issues
		// no work until then.
		if strings.TrimSpace(c.Payout.Address) == "" && (c.UI.Listen == "" || (c.UI.AdminPassword == "" && !c.UI.SettingsOpen)) {
			add("payout.address is required in fixed mode (or enable the web UI with settings and set it there)")
		}
	case "miner":
	default:
		add("payout.mode must be fixed or miner, got %q", c.Payout.Mode)
	}
	switch c.UI.Style {
	case "", "mine", "simple", "stats":
	default:
		add("ui.style must be mine, simple or stats, got %q", c.UI.Style)
	}
	if len(c.Payout.CoinbaseTag) > 60 {
		add("payout.coinbase_tag must be at most 60 bytes")
	}
	if c.Stratum.Extranonce2Size < 2 || c.Stratum.Extranonce2Size > 8 {
		add("stratum.extranonce2_size must be 2..8")
	}
	if v, err := strconv.ParseUint(c.Stratum.VersionRollingMask, 16, 32); err != nil || len(c.Stratum.VersionRollingMask) > 8 {
		add("stratum.version_rolling_mask must be hex (e.g. 1fffe000)")
	} else if uint32(v)&0xe0001fff != 0 {
		add("stratum.version_rolling_mask may only contain BIP320 bits (subset of 1fffe000)")
	}
	if c.Stratum.PublicPort < 0 || c.Stratum.PublicPort > 65535 {
		add("stratum.public_port must be 0..65535")
	}
	if c.Stratum.MaxConnections < 1 || c.Stratum.MaxConnsPerIP < 1 {
		add("stratum connection limits must be positive")
	}
	if c.Stratum.AuthTimeoutS < 1 || c.Stratum.IdleTimeoutS < 1 {
		add("stratum timeouts must be positive")
	}
	if c.Stratum.MaxLineBytes < 1024 {
		add("stratum.max_line_bytes must be >= 1024")
	}
	if c.Stratum.MsgRatePerS <= 0 || c.Stratum.MsgBurst < 1 {
		add("stratum message rate limits must be positive")
	}
	v := c.Vardiff
	if !(v.Min > 0) || !(v.Max >= v.Min) || v.Initial < v.Min || v.Initial > v.Max {
		add("vardiff requires 0 < min <= initial <= max")
	}
	if v.TargetShareS < 1 || v.TargetShareS > 600 || v.RetargetS <= 0 || v.VariancePct < 0 {
		add("vardiff target_share_s (VARDIFF_TARGET_SECONDS) must be 1..600 and retarget_s positive")
	}
	if v.Min < 1e-12 || v.Max > 1e15 {
		add("vardiff min/max must be within 1e-12..1e15")
	}
	if v.FixedDiff != 0 && (v.FixedDiff < v.Min || v.FixedDiff > v.Max) {
		add("vardiff fixed_diff (FIXED_DIFF) must be 0 or within min..max")
	}
	for name, addr := range map[string]string{"stratum.listen": c.Stratum.Listen, "api.listen": c.API.Listen, "ui.listen": c.UI.Listen} {
		if addr == "" && name == "ui.listen" {
			continue
		}
		if _, port, err := net.SplitHostPort(addr); err != nil || port == "" {
			add("%s must be host:port, got %q", name, addr)
		}
	}
	return errors.Join(errs...)
}
