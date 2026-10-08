// Package ui serves the Wizard-Blocks web interface: embedded static files,
// a JSON state endpoint and a Server-Sent Events stream. It only reads engine
// state; it never influences mining.
package ui

// CoinInfo holds every coin-specific detail the UI needs. Adding a coin to
// the UI means adding one entry here.
type CoinInfo struct {
	Coin        string            `json:"coin"`
	Name        string            `json:"name"`
	Ticker      string            `json:"ticker"`
	BlockTimeS  int               `json:"block_time_s"`
	Maturity    int               `json:"coinbase_maturity"` // confirmations shown as x/Maturity
	AddressHint map[string]string `json:"address_hint"`      // chain -> example prefix
	Explorer    map[string]string `json:"explorer"`          // chain -> block URL with {hash}
}

// Coins is the per-coin UI configuration.
var Coins = map[string]CoinInfo{
	"bch": {
		Coin: "bch", Name: "Bitcoin Cash", Ticker: "BCH", BlockTimeS: 600, Maturity: 100,
		AddressHint: map[string]string{
			"main": "bitcoincash:q…", "test": "bchtest:q…", "test4": "bchtest:q…",
			"chip": "bchtest:q…", "scale": "bchtest:q…", "regtest": "bchreg:q…",
		},
		Explorer: map[string]string{
			"main":  "https://blockchair.com/bitcoin-cash/block/{hash}",
			"test4": "https://tbch4.loping.net/block/{hash}",
			"chip":  "https://chipnet.imaginary.cash/block/{hash}",
		},
	},
	"btc": {
		Coin: "btc", Name: "Bitcoin", Ticker: "BTC", BlockTimeS: 600, Maturity: 100,
		AddressHint: map[string]string{
			"main": "bc1q…", "test": "tb1q…", "testnet4": "tb1q…", "signet": "tb1q…", "regtest": "bcrt1q…",
		},
		Explorer: map[string]string{
			"main":     "https://mempool.space/block/{hash}",
			"testnet4": "https://mempool.space/testnet4/block/{hash}",
			"signet":   "https://mempool.space/signet/block/{hash}",
		},
	},
}

// Creature tiers for a job's best share, by percent of network difficulty
// (one tier per decade; the last tier is a solved block).
var creatures = []struct {
	MaxPct float64
	Name   string
}{
	{1e-6, "Cave Mite"},
	{1e-5, "Glow Worm"},
	{1e-4, "Crystal Beetle"},
	{1e-3, "Rock Bat"},
	{1e-2, "Fungus Imp"},
	{1e-1, "Shadow Goblin"},
	{1, "Mine Troll"},
	{10, "Lava Golem"},
	{100, "Ancient Wyrm"},
}

// creatureFor returns the tier index (0..9) and name for pct.
func creatureFor(pct float64) (int, string) {
	for i, c := range creatures {
		if pct < c.MaxPct {
			return i, c.Name
		}
	}
	return len(creatures), "Block Dragon"
}
