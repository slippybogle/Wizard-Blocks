# Wizard-Blocks

Wizard fell down a mine shaft. Finds a BCH mine and starts searching for blocks.

A self-hosted **solo-mining Stratum V1 server** for **Bitcoin Cash (BCH)**,
with BTC support in the engine. It is one static Go binary (standard library
only) and runs one instance per coin. It talks to your own full node (Bitcoin
Cash Node / Bitcoin Core) over RPC + ZMQ, and 100 % of every block reward plus
fees goes to your payout address. The binary also serves a 16-bit pixel-art web
UI with two pages: **The Mine** and **The Ledger**.

- Design and data flow: [docs/DESIGN.md](docs/DESIGN.md)
- What is tested and the results: [docs/TESTING.md](docs/TESTING.md)
- BCH testnet on Umbrel: [deploy/bch-testnet/README.md](deploy/bch-testnet/README.md)

## Features

**Engine**
- `getblocktemplate`, `submitblock`, `validateaddress` and `getblockchaininfo`.
  Work is only issued once the node is fully synced.
- New blocks are detected via ZMQ `hashblock` (pure-Go ZMTP client), with
  `getbestblockhash` polling as a permanent fallback.
- Every template is verified before use: txids are recomputed and BCH canonical
  transaction order is enforced (BTC: wtxids and witness commitment too).
- Coinbase:
  - BIP34 height and a configurable tag;
  - extranonce1 (4 B) + extranonce2;
  - scriptSig of at most 100 bytes;
  - BCH 100-byte minimum transaction size.
- Payout address formats:
  - CashAddr: P2PKH, P2SH, P2SH32, token-aware; with or without prefix, any case;
  - legacy Base58.
- Every payout address is decoded locally **and** by the node. If the two
  scripts differ, the engine refuses to start and the miner is refused.
- Stratum methods:
  - `mining.subscribe`, `mining.authorize`, `mining.configure` (BIP310 version rolling);
  - `mining.set_difficulty`, `mining.notify`, `mining.submit`;
  - `mining.extranonce.subscribe`, `mining.suggest_difficulty`.
- Share handling:
  - stale, duplicate, low-difficulty and malformed shares are rejected with standard error codes;
  - per-connection vardiff, or FIXED_DIFF;
  - a miner can pin its own difficulty with the password `d=<diff>`;
  - connection, line-length and message-rate limits.
- Solved blocks are submitted immediately and then verified on the active
  chain. `blocks_found` only counts confirmed blocks; unconfirmed ones are `pending`.

**Web UI** (embedded files, no external fonts/scripts/CDNs, strict CSP, live via Server-Sent Events)
- **The Mine**: an endless pixel-art mine with parallax, palette-cycling crystals
  whose shimmer follows your hashrate, and a wizard in a rainbow robe.
  - The wizard swings his pickaxe on every accepted share.
  - Each job spawns a creature, rated by that job's best share as a % of network difficulty:
    - Common < 50 %
    - Uncommon 50–63.3 %
    - Rare 63.3–76.7 %
    - Epic 76.7–90 %
    - Legendary (the dragon) 90–< 100 %
    - Block ≥ 100 %
  - The HUD shows:
    - pool hashrate (now / 1 h / 24 h) and active workers;
    - best share this job and all-time;
    - network difficulty, height and node sync;
    - estimated time to block and blocks found;
    - a **MANA** bar: the luck percentile since your last found block (resets only when you find one);
    - a trophy wall of found blocks with x/100 confirmations.
  - A block is celebrated only after the node confirms it on the active chain:
    the wizard shows a rainbow with "CONGRATULATIONS WIZARD!".
  - Optional CRT scanlines, generated chiptune audio (off by default).
- **The Ledger**: all the detail.
  - Node: version, sync, peers, network hashrate, mempool, uptime and disk.
  - Hashrate charts (1 h / 24 h / 7 d), shares, per-worker table.
  - Block odds per day / week / year, masked payout address and miner connection info.
  - Block history with explorer links and confirmations.
  - Creature log.
  - **Settings**: password-protected live difficulty settings (below).

## Quick start (BCH, Docker)

1. Configure your BCHN node (`bitcoin.conf`):

   ```ini
   server=1
   rpcuser=bchrpc
   rpcpassword=change-me
   rpcbind=0.0.0.0
   rpcallowip=172.16.0.0/12     # your Docker network
   zmqpubhashblock=tcp://0.0.0.0:28332
   ```

2. Build and run:

   ```sh
   docker build -t wizard-blocks .
   docker run -d --name wizard-blocks-bch --restart unless-stopped --stop-timeout 90 \
     -p 1776:1776 -p 8420:8420 -p 127.0.0.1:8080:8080 -v wb-bch:/data \
     -e WB_COIN=bch \
     -e WB_RPC_URL=http://<node-ip>:8332 -e WB_RPC_USER=bchrpc -e WB_RPC_PASSWORD=change-me \
     -e WB_ZMQ_HASHBLOCK=tcp://<node-ip>:28332 \
     -e WB_PAYOUT_ADDRESS=bitcoincash:q... \
     -e WB_UI_ADMIN_PASSWORD=<a long password> \
     wizard-blocks
   docker logs -f wizard-blocks-bch
   ```

3. Open the UI at `http://<host>:8420`.

For Umbrel / compose, see [deploy/docker-compose.umbrel.yml](deploy/docker-compose.umbrel.yml).
It maps host port `STRATUM_PORT` (default **1776**) to the engine's port 1776 and
UI port `UI_PORT` (default 8420). The BTC engine sits behind the `btc` profile.

Without Docker: `go build ./cmd/wizard-blocks && ./wizard-blocks -config deploy/config.example.bch.json`.

## Connecting a miner

| setting | value |
|---|---|
| URL | `stratum+tcp://<host>:1776` |
| user | `fixed` mode: anything (e.g. `bitaxe1`). `miner` mode: your payout address, optionally `.<worker>` (e.g. `bitcoincash:qq…xyz.bitaxe1` or just `qq…xyz.bitaxe1`) |
| password | `x`. `d=<difficulty>` pins this miner's difficulty, clamped to VARDIFF_MIN..VARDIFF_MAX |

Bitaxe / NerdQaxe (ESP-Miner), Antminers (cgminer/bmminer) and other version-rolling
firmware negotiate BIP310 automatically. Watch the log for `level=BLOCK` lines.

## Difficulty

| setting (env / compose `.env`) | default | meaning |
|---|---|---|
| `WB_VARDIFF_MIN` / `VARDIFF_MIN` | 1 | lowest share difficulty |
| `WB_VARDIFF_MAX` / `VARDIFF_MAX` | 1e15 | highest share difficulty |
| `WB_VARDIFF_TARGET_SECONDS` / `VARDIFF_TARGET_SECONDS` | 10 | vardiff aims for one share per this many seconds (1–600) |
| `WB_FIXED_DIFF` / `FIXED_DIFF` | 0 | > 0 disables vardiff; every miner gets this difficulty (must be within min..max) |

Precedence for each connection, highest first:
1. a per-worker override set on the Settings page;
2. the miner's password `d=<diff>`;
3. `FIXED_DIFF`;
4. vardiff.

Overrides and `d=` values are clamped to min..max. Share difficulty is
always capped at the network difficulty, so a block-solving share is never withheld.

### Live settings (The Ledger → Settings)

- Edit VARDIFF_MIN, VARDIFF_MAX, VARDIFF_TARGET_SECONDS, FIXED_DIFF and per-worker
  overrides. Changes apply to connected miners immediately (new difficulty plus a fresh job).
- Changes are validated: min ≤ max, values within 1e-12..1e15, target 1–600 s, FIXED_DIFF within min..max.
- They are saved to `<data_dir>/settings-<coin>.json`. Saved values override the
  config/env on restart. **Reset to config** deletes the file.
- Settings require a login with `WB_UI_ADMIN_PASSWORD`. If it is empty, the
  settings are read-only. They are served only on the UI port, never on the
  Stratum or stats API ports.
- Sessions use an HttpOnly, SameSite=Strict cookie. Changes require a same-origin
  custom header. Logins are rate-limited.

## Configuration

A JSON file (`-config` or `WB_CONFIG`) is applied over built-in defaults, then
`WB_*` environment variables. The full schema is in [docs/DESIGN.md](docs/DESIGN.md);
example: [deploy/config.example.bch.json](deploy/config.example.bch.json).

| env | default | meaning |
|---|---|---|
| `WB_COIN` | `btc` | `bch` or `btc` |
| `WB_RPC_URL` / `WB_RPC_USER` / `WB_RPC_PASSWORD` / `WB_RPC_COOKIE_FILE` | `http://127.0.0.1:8332` | node RPC |
| `WB_ZMQ_HASHBLOCK` | empty (poll only) | e.g. `tcp://127.0.0.1:28332` |
| `WB_POLL_INTERVAL_MS` / `WB_TEMPLATE_REFRESH_S` | 1000 / 30 | tip polling, mempool refresh |
| `WB_PAYOUT_MODE` | `fixed` | `fixed` (one address) or `miner` (username = address) |
| `WB_PAYOUT_ADDRESS` | — | required in `fixed` mode |
| `WB_COINBASE_TAG` | `/wizard-blocks/` | ≤ 60 bytes |
| `WB_STRATUM_LISTEN` | `0.0.0.0:1776` | Stratum listener |
| `WB_STRATUM_PUBLIC_PORT` | listen port | port shown to miners in the UI, if Docker maps a different host port |
| `WB_EXTRANONCE2_SIZE` | 8 | 2..8 |
| `WB_VERSION_ROLLING_MASK` | `1fffe000` | subset of BIP320 bits |
| `WB_VARDIFF_INITIAL` | 1024 | starting difficulty under vardiff |
| `WB_MAX_CONNECTIONS` / `WB_MAX_CONNECTIONS_PER_IP` | 1024 / 64 | |
| `WB_UI_LISTEN` | `0.0.0.0:8420` | web UI (`""` disables it) |
| `WB_UI_ADMIN_PASSWORD` | empty | enables the Settings page |
| `WB_API_LISTEN` / `WB_PROMETHEUS` | `127.0.0.1:8080` / true | stats JSON / metrics |
| `WB_LOG_LEVEL` / `WB_LOG_FORMAT` | `info` / `json` | |
| `WB_DATA_DIR` | empty | persists blocks, best share, luck, hashrate history and saved settings |

## APIs

- UI port:
  - `/api/state`: everything the UI shows, as JSON;
  - `/api/events`: Server-Sent Events stream of the same state, once per second;
  - `/api/history?range=1h|24h|7d`;
  - `/api/blocks`.
- Stats port (local only by default):
  - `/stats` (JSON);
  - `/metrics` (Prometheus);
  - `/healthz`.

## Tests

```sh
go test -race ./...                 # unit tests (real mainnet block vectors, UI/auth, font coverage)
scripts/regtest-test.sh bch         # + Docker regtest harness (BCH, 600 blocks)
scripts/regtest-test.sh all         # BCH + both BTC suites
```

See [docs/TESTING.md](docs/TESTING.md) for coverage and the latest results.
