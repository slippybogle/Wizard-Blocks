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
// shares arrive. Which creature of the tier it is comes from the job height
// (speciesFor).
var creatures = []struct {
	MaxPct float64
	Rarity string
}{
	{50, "Common"},
	{63.3, "Uncommon"},
	{76.7, "Rare"},
	{90, "Epic"},
	{100, "Legendary"},
}

// Species are the creatures of each tier (20 / 15 / 10 / 7 / 5); within a
// tier later ones are rarer, and the Dragon is the rarest Legendary. Names
// and order must match SPECIES in static/js/creatures.js (sprites).
var Species = [][]string{
	{"Cave Mite", "Rat", "Beetle", "Slime", "Worm", "Snail", "Moth", "Spider", "Mole", "Frog", "Bat Pup", "Crab", "Grub", "Centipede", "Gecko", "Leech", "Glow Shroom", "Scorpion", "Firefly Swarm", "Crystal Tick"},
	{"Rock Bat", "Giant Rat", "Mud Crawler", "Fungal Toad", "Bone Snake", "Kobold", "Rust Beetle", "Cave Owl", "Imp", "Gel Cube", "Stone Turtle", "Ice Lizard", "Ghost", "Fire Salamander", "Mimic Chest"},
	{"Shadow Goblin", "Cave Troll", "Crystal Spider", "Wraith", "Gargoyle", "Basilisk", "Harpy", "Myconid Lord", "Ogre", "Lich Apprentice"},
	{"Lava Golem", "Minotaur", "Cyclops", "Frost Giant", "Gloom Eye", "Chimera", "Bone Colossus"},
	{"Void Serpent", "Cave Kraken", "Phoenix", "Demon Lord", "Dragon"},
}

// BlockTier is the tier of a job whose best share reached 100% of the
// network difficulty.
const BlockTier = 5

// creatureFor returns the tier (0..5), rarity and creature name for the best
// share's percentage of network difficulty.
// VariantCounts is the number of colour variants per tier: the rarer the
// tier, the fewer variants. Within a tier variant i has weight n-i, so the
// last variant is the rarest. The palettes live in static/js/sprites.js.
var VariantCounts = [...]int{20, 15, 10, 7, 5}

// variantFor picks a creature's colour variant from its job height, so every
// viewer (and a mounted head) sees the same colour.
func variantFor(tier int, jobHeight int64) int {
	if tier < 0 || tier >= len(VariantCounts) {
		return 0
	}
	return weightedPick(VariantCounts[tier], mix(jobHeight, tier, 0))
}

// speciesFor picks which creature of the tier a job gets, from its height:
// the same job always spawns the same creature on every screen.
func speciesFor(tier int, jobHeight int64) int {
	if tier < 0 || tier >= len(Species) {
		return 0
	}
	return weightedPick(len(Species[tier]), mix(jobHeight, tier, 0x5bd1e995))
}

// mix is splitmix64 of the height, tier and a salt.
func mix(jobHeight int64, tier int, salt uint64) uint64 {
	z := uint64(jobHeight)*0x9e3779b97f4a7c15 + uint64(tier+1)*0xbf58476d1ce4e5b9 + salt*0x94d049bb133111eb
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// weightedPick returns 0..n-1 where item i has weight n-i (the last is the
// rarest).
func weightedPick(count int, z uint64) int {
	n := uint64(count)
	r := z % (n * (n + 1) / 2)
	for i := uint64(0); i < n; i++ {
		w := n - i
		if r < w {
			return int(i)
		}
		r -= w
	}
	return int(n - 1)
}

// creatureFor returns the tier, rarity, species and creature name for a
// job's best share (% of network difficulty) at that job height.
func creatureFor(pct float64, jobHeight int64) (int, string, int, string) {
	for i, c := range creatures {
		if pct < c.MaxPct {
			sp := speciesFor(i, jobHeight)
			return i, c.Rarity, sp, Species[i][sp]
		}
	}
	return BlockTier, "Block", 0, "Block found!"
}
