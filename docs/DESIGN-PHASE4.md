# Phase 4 design: two-chain stats, rental-friendly difficulty, DOGE payout

Scope: the Wizard-Blocks-LTC engine and its simple page. Doc only; no code
yet. Everything here is also available to the BCH/BTC engines unless it is
DOGE-specific.

## 1. What already exists (no change needed)

| Feature | Where |
|---|---|
| Live vardiff min / max / target seconds, fixed difficulty, per-worker overrides, saved across restarts | `internal/stratum/diffsettings.go`, Settings API on the UI port |
| Miner `d=<n>` in the password pins that connection's difficulty (clamped to min/max) | `parsePasswordDiff` |
| `mining.suggest_difficulty` sets a starting point; vardiff continues from it | `handleSuggestDifficulty` |
| `mining.extranonce.subscribe` acknowledged (extranonce1 never changes, so nothing to push) | `session.go` |
| Share difficulty capped at the lower of the LTC and DOGE network difficulties | Phase 3 |
| DOGE blocks recorded separately (`aux_blocks` in `/api/state`) | Phase 3 |

## 2. Separate LTC and DOGE stats

**What changes**
- `/api/state` gets a `chains` object, one entry per chain:
  `{ "ltc": {...}, "doge": {...} }`, each with `network_difficulty`,
  `height`, `blocks_found`, `blocks_pending`, `last_block`, `effort_pct`
  (shares since that chain's last block ÷ its network difficulty),
  `expected_block_seconds`, odds per day/week/year, node status
  (`connected`, `synced`, `zmq`), and for DOGE `merged: true|false` plus
  the reason when false (no address, node down, node syncing).
- Hashrate stays one number (the same shares mine both chains).
- Effort is tracked per chain: an LTC block resets only LTC effort, a DOGE
  block only DOGE effort. Both persist across restarts.
- Blocks table: one table with a Chain column (LTC / DOGE), DOGE rows link
  the LTC share header (`parent_hash`).
- Prometheus: `wb_blocks_total{chain="doge",status=...}`, per-chain network
  difficulty and effort gauges.

**Tests**
- Unit (`stats`): DOGE block does not touch LTC effort and vice versa;
  per-chain effort survives save/load; old state files (no `chains`) load.
- Unit (`ui`): `/api/state` shape with DOGE on, DOGE off (no address), DOGE
  node down; odds and expected time use each chain's own difficulty.
- Regtest (merged test): after mining, `chains.ltc.blocks_found` and
  `chains.doge.blocks_found` equal what the two nodes have on chain, and
  `/metrics` shows the same counts.

## 3. Rental-friendly difficulty controls

Rental hashrate (NiceHash, MiningRigRentals) arrives as many connections,
often from one IP, with worker names like `rental.rig123`. The controls:

| Setting | Values | Default (LTC app) |
|---|---|---|
| Mode | `vardiff` or `fixed` | vardiff |
| Start difficulty (new) | any; first job of every new connection | **262144** |
| Min / Max | vardiff bounds | 1024 / 1e12 |
| Target seconds per share | vardiff aim | 10 |
| Fixed difficulty | used in fixed mode | — |
| Per-worker overrides (extended) | exact name **or prefix pattern** (`rental.*`) → difficulty | none |
| Honour miner `d=` (new switch) | on / off | on |
| Honour `suggest_difficulty` (new switch) | on / off | on |

Rules, in order: an override match wins; then miner `d=` (if the switch is
on); then fixed mode; then vardiff, starting from `suggest_difficulty` (if
on) or else the start difficulty. Everything is clamped to min/max and then
capped at the network difficulty (LTC and DOGE), as now. A change applies
to connected miners at once (new difficulty, then a fresh job), as it does
today.

Why the switches: some rental services send their own `d=` or suggestions
that do not suit a solo pool; turning them off makes the pool's settings
authoritative. Prefix overrides cover a rental's changing rig names with
one rule.

**Tests**
- Unit (`stratum`): precedence table above, case by case; prefix match
  (`rental.*` matches `rental.rig1`, not `rentalx`); switches off ignore `d=`
  and `suggest_difficulty`; start difficulty used for the first job; values
  outside min/max clamp; invalid settings refused with a clear message;
  settings persist and reload.
- Regtest (extend LiveDifficultySettings): change each setting live and
  check connected miners get the new difficulty followed by a fresh job; a
  `rental.*` override applies to two workers with different rig names.
- The rental-readiness test (later) re-checks this at scale with many
  connections from one IP.

## 4. DG Home 1 start difficulty: 262144

- The LTC app's default start difficulty is **262144** (Scrypt share
  units), the DG Home 1's own default. At 2.1 GH/s that is one share about
  every 8 s (262144 × 65536 ÷ 2.1e9), close to the 10 s vardiff target, so
  vardiff barely moves.
- The user's usual 200000 works too: set it as the start difficulty, as a
  per-worker override, or keep `d=200000` in the miner's password.
- The page shows the expected seconds per share for the chosen start
  difficulty at a typed-in hashrate, so rented hashrate can be sized.

**Tests**
- Unit: a new connection's first `mining.set_difficulty` is 262144 with
  default LTC settings; with `d=200000` it is 200000.
- Unit: seconds-per-share helper: 262144 at 2.1 GH/s ≈ 8.2 s.

## 5. DOGE payout field and the "mining Litecoin only" banner

**What changes**
- Settings gets a second address field: **DOGE payout address** (optional),
  next to the LTC one. Saving checks it locally and with the Dogecoin node
  (`validateaddress`, same scriptPubKey), like the LTC address. Clearing it
  turns merged mining off.
- The change applies without a restart: the engine starts or stops the
  DOGE aux source and sends miners fresh (clean) jobs, with or without the
  merged-mining tag.
- A banner at the top of the page whenever DOGE is not being merged:
  - no DOGE address: "Mining Litecoin only. Add a DOGE payout address in
    Settings to also mine Dogecoin."
  - Dogecoin node unreachable or syncing: "Mining Litecoin only: the
    Dogecoin node is <down / syncing>. Dogecoin mining resumes on its own."
  The banner disappears as soon as merged mining is on.
- `/api/state` exposes `doge.merged` and `doge.reason` (used by the banner).

**Tests**
- Unit (`engine`/`ui`): setting a valid DOGE address enables aux work;
  invalid, wrong-network or node-refused addresses are rejected with the
  reason; clearing it disables aux work; the address persists across
  restarts; the settings password and CSRF rules apply as for the LTC
  address.
- Regtest (merged test): start with no DOGE address (banner reason "no
  address", no tag in coinbases), set the address from the Settings API,
  check the next jobs are clean and DOGE blocks follow; stop the Dogecoin
  node and check `doge.reason` becomes "node down"; start it again and check
  merged mining resumes and the reason clears.
- Page check: a headless-browser screenshot of the banner in each state and
  of the two-chain stats, on desktop and phone widths.

## 6. Out of scope for Phase 4

Node apps, the Umbrel LTC app, the testnet trial (Phases 5 and 6), and the
load and public-port tests (rental-readiness test).

## 7. Done means

All unit tests pass with the race detector; the regtest suite passes 30/30
with full logs; screenshots of the page states; a plain-English summary;
then stop for approval.
