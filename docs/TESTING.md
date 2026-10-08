# Testing: what was tested and the results

Run on 2026-10-08 (Linux amd64, Go 1.24.7, Docker 29.8). Every test
listed below passed on the final code, including under the race detector (`-race`).
Reproduce with `go test -race ./...` and `scripts/regtest-test.sh all`.

## Unit tests (`go test -race ./...`): 59 tests, all pass

| package | what is checked |
|---|---|
| `bitcoin` | **Real mainnet blocks**: BTC 413567 (pre-segwit, 1557 txs), BTC 702861 `0000…dafae` (segwit, 2500 txs, 2064 with witness), BCH 877227 (CTOR, 24069 txs). For each block: parse every tx, recompute txids and the merkle root against the miner-produced header, rebuild the root from the Stratum coinbase branch, check the header hash against PoW (and the known hash where available), decode the BIP34 height. BTC also checks the BIP141 witness commitment against wtxids. BCH also checks that CTOR order holds and that the opposite ordering does not, which proves the comparison direction. Genesis block hash/merkle root. Compact target encode/decode (including negative and overflow rejection), difficulty math, BIP34 encodings 1…8388608 (OP_1..OP_16, sign byte), merkle branch for every tree size from 1 to 40, garbage/truncation never panics. |
| `address` | Bitcoin Core `key_io_valid/invalid.json`, BCHN `key_io_valid/invalid.json` (including legacy P2SH32), BCHN `cashaddr_token_types.json` (all types and sizes, round-trip), CashAddr spec examples (prefixless and uppercase), BIP173/BIP350 vectors, cross-network rejection, undefined witness versions refused, BCHN chain names (`chip`, `test4`, …), 20 s fuzz run (1.1 M inputs, no panics). |
| `work` | Templates built from the real blocks above go through template → job → assembled block → parse → merkle root, coinbase, BIP34, payout and witness commitment. Tampered txid, witness commitment and target/bits are rejected. A shuffled BCH template is restored to the real block's exact CTOR order. Empty regtest template (height 1 = OP_1). 100-byte scriptSig limit. BCH 100-byte coinbase padding. Stratum prevhash encoding. Testnet min-difficulty `MinShareTime`. |
| `stratum` | subscribe/configure (mask intersection)/authorize (`d=` password)/submit error codes, malformed JSON, disconnect after repeated garbage, slow client disconnected, message rate limit, per-IP connection limit, oversized line, auth timeout, vardiff convergence from 0.5 TH/s to 2 PH/s, quiet-miner decrease and min clamp, the three version-rolling interpretations, 30 s fuzz of the message handler (no panics). |
| `stratum` (difficulty) | `DiffSettings` validation (min ≤ max, 1e-12..1e15, target 1–600, FIXED_DIFF within range); `d=` password parsing and clamping; FIXED_DIFF disables vardiff; precedence override > `d=` > FIXED_DIFF > vardiff; live settings re-applied to connected sessions (new difficulty + fresh job). |
| `work` (submit) | `TestBlockAnnouncedOnce`: one accepted block logs `BLOCK ACCEPTED` exactly once, even when verified repeatedly. |
| `node`, `stats`, `config` | ZMTP framing/metadata limits; hashrate windows; `pending` blocks not counted as found; Prometheus output; persistence round-trip; per-job rounds; luck resets only when a block is found (not on new jobs); config env overrides and validation (difficulty env, stratum public port, `WB_UI_LISTEN=off`). |
| `ui` | Settings auth: disabled without a password, login/logout flow, tampered cookie refused, login rate limit (5 failures / 5 min), cross-origin and header-less writes refused, settings absent from the stratum/stats ports. Static files and `/api/state`. Creature tiers at the exact cutoffs (50 / 63.3 / 76.7 / 90 / 100 %), a share ≥ 100 % or a found block is Block tier (never Legendary). Luck-since-last-block percentage. Every character the UI renders has a pixel-font glyph. |

## Regtest integration harness (`test/integration`, Docker)

Real nodes: **Bitcoin Cash Node v29.2.0** (two connected nodes, because BCHN only
serves GBT when it has a peer) and **Bitcoin Core v28.1**. The engine runs in-process.
The built-in CPU miner (independent cgminer-style header code that uses only
`crypto/sha256`) mines through Stratum with BIP310 version rolling.

### Checks applied to every block the engine submitted

- `status` is `accepted` (confirmed on the active chain) unless it is one of
  the deliberately provoked cases, which must end exactly as expected.
- **Exact header match**: the block hash is one our miner hashed and submitted.
  The block version equals the version the miner built, and the recorded
  version-rolling interpretation includes the miner's own mode (no fallback).
- On-chain at the recorded height; BIP34 height correct; coinbase tag present
  (or absent, in the empty-tag phase).
- **Exact payout**: output 0 pays the payout address's script (taken from
  `validateaddress`) exactly `subsidy(height) + totalfee`
  (from `getblockstats`). The coinbase output total equals that amount and equals
  the template's `coinbasevalue`. The subsidy follows the 150-block regtest
  halving schedule.
- BCH: exactly one coinbase output, coinbase ≥ 100 bytes, CTOR order.
  BTC: payout output plus a 0-value BIP141 commitment output.
- **Chain continuity**: final height = start + our accepted blocks + external
  blocks. Every height is either ours (matching hash) or a known external block.

### Results (final runs)

| suite | blocks verified | highlights |
|---|---|---|
| **TestRegtestBCH** (priority) | **603** accepted (heights 2–606) | 5 subsidy eras (50 → 3.125 BCH); a 1000-tx mempool drained into our blocks (largest 578–1001 txs across runs); BCHN restarted under the running engine (sessions kept, RPC and ZMQ reconnected, 11 more blocks); 100 idle sessions all followed every tip while 4 miners raced (47 lost same-height races recorded `orphaned`/`stale`, none counted as found); 10 payout forms: CashAddr P2PKH, prefixless, UPPERCASE, legacy Base58 P2PKH, token-aware P2PKH, P2SH20, P2SH32, token-aware P2SH32, legacy Base58 P2SH32; empty coinbase tag with 22 coinbases padded to the 100-byte minimum |
| TestRegtestBTC (testdummy bit 28 signalling inside the mask) | 215 accepted | 69 blocks on in-mask templates, split by miner mode (bip310 26 / xor 24 / or 19), each the exact header that miner hashed |
| TestRegtestBTCNoSignal (`-vbparams=testdummy:-2:0`) | 212 accepted | every template `0x20000000`; all interpretations coincide (`bip310+xor+or`) |

### Scenarios in each suite

- **Bad payout refused at startup**: garbage, corrupted checksum, mainnet address,
  wrong-network prefix (`bchtest:` on regtest), other-coin address, undefined witness
  version (BTC), empty address. All are refused with an address error. In miner
  mode, invalid usernames and addresses are refused at `mining.authorize`.
- **Empty templates** (initial blocks) and **templates with many txs** (300 BTC / 1000 BCH).
- **Miner disconnect/reconnect** (server connection count drops to 0, then mining resumes).
- **Protocol errors** with the exact codes: submit before subscribe (25),
  unauthorized worker (24), unknown job (21), short extranonce2 / bad hex /
  ntime too old / ntime too far ahead / version bits outside mask / non-array or
  non-string params / unsupported method (20), low difficulty (23),
  duplicate in the same write (22). Malformed JSON keeps the connection; an
  oversized line disconnects.
- **Same-height race**: two pipelined block solutions for one job. Both shares are
  accepted. Exactly one becomes the block, the other is recorded `orphaned` and
  `blocks_found` grows by exactly 1.
- **New block mid-job**: an external block via `generatetoaddress` results in a
  `clean_jobs=true` notify in about 4 ms over ZMQ. A share on the old job is
  rejected as stale (21); because it still solved its own block, it is submitted
  and recorded `stale`.
- **Polling fallback**: ZMQ disabled; an external block is detected in about 490 ms (poll 500 ms).
- **Stats API**: `/stats` (`blocks_found` = confirmed blocks, `blocks_pending` = 0
  at rest), `/metrics`, `/healthz`.
- **Graceful shutdown** of every engine instance (in-flight submissions awaited).
- **LiveDifficultySettings** (BCH): the settings API is 404 on the stats port
  and 401 without login. After login, FIXED_DIFF and per-worker overrides reach
  connected miners immediately (new difficulty, then a fresh job), overrides are
  clamped, invalid values are refused, and changes are saved to disk and
  removed by reset.
- **CreaturesAndLuck** (BCH): with FIXED_DIFF below network difficulty, shares
  that do not solve blocks give the current job a creature tier from its % of
  network difficulty, and the luck-since-last-block percentage fills.

## Testnet stack (`deploy/bch-testnet`)

Smoke-tested in this environment: BCHN 29.2.0 started on chipnet (reports
`chain=chip`), the engine validated a node-generated `bchtest:` address, bound
its ports and entered "waiting for node to sync". Shutdown persisted state to
the named volume. **Not tested here:** syncing chipnet and mining a real testnet
block, because this sandbox has no P2P internet access.

## Testnet stack (`deploy/btc-testnet`)

Smoke-tested in this environment: Bitcoin Core 28.1 on testnet4, the engine
validated a node-generated `tb1q…` address, bound stratum 3336 and stats
127.0.0.1:8091 with the UI off, and entered "waiting for node to sync".
**Not tested here:** syncing testnet4 and mining a real block (no P2P internet).

## Web UI (manual)

Screenshots checked at 1440×900 desktop and iPhone portrait/landscape (390×844):
no console errors, no labels in the scene, top HUD panels and the bottom strip
never cover the wizard or creatures.

## Not covered / known limits

- No real ASIC hardware was available. Bitaxe/NerdQaxe/Antminer compatibility
  rests on matching their version-rolling encodings (checked against
  ESP-Miner, cgminer, ckpool and public-pool sources) and on standard Stratum framing.
- No mainnet blocks were mined (expected). Mainnet behaviour is covered by the
  real-block vectors and the identical code path on regtest.
- The 60 s auth timeout and slow-client paths are covered by unit tests with
  shortened timeouts, not by the integration harness.
