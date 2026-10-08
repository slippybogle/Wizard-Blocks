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

// Creature rarity from a job's best share difficulty as a percentage of the
// current network difficulty: how close the job came to being a block.
// Cutoffs (product spec, do not change), upper bounds exclusive:
// Common <50, Uncommon 50-63.3, Rare 63.3-76.7, Epic 76.7-90,
// Legendary 90-<100, Block >=100 (a real found block).
// A creature spawns when a job starts (Common, 0%) and evolves as better
// shares arrive. The dragon is the Legendary creature only.
var creatures = []struct {
	MaxPct float64
	Rarity string
	Name   string
}{
	{50, "Common", "Cave Mite"},
	{63.3, "Uncommon", "Rock Bat"},
	{76.7, "Rare", "Shadow Goblin"},
	{90, "Epic", "Lava Golem"},
	{100, "Legendary", "Legendary Dragon"},
}

// BlockTier is the tier of a job whose best share reached 100% of the
// network difficulty.
const BlockTier = 5

// creatureFor returns the tier (0..5), rarity and creature name for the best
// share's percentage of network difficulty.
func creatureFor(pct float64) (int, string, string) {
	for i, c := range creatures {
		if pct < c.MaxPct {
			return i, c.Rarity, c.Name
		}
	}
	return BlockTier, "Block", "Block found!"
}
