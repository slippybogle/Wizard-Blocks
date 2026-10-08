# Wizard-Blocks

Wizard fell down a mine shaft. Finds a BCH mine and starts searching for blocks.

A self-hosted **solo-mining Stratum V1 server** for **Bitcoin Cash (BCH)** and
**Bitcoin (BTC)**. One static Go binary (standard library only), one instance
per coin, talking to your own full node (Bitcoin Cash Node / Bitcoin Core) over
RPC + ZMQ. 100 % of every block reward plus fees goes to your payout address.
No web UI: a local JSON stats endpoint (and Prometheus `/metrics`) is provided
for one.

- Design and data flow: [docs/DESIGN.md](docs/DESIGN.md)
- What is tested and the results: [docs/TESTING.md](docs/TESTING.md)

## Features

- `getblocktemplate` (BCH: plain; BTC: `segwit` rules), `submitblock`, `validateaddress`,
  `getblockchaininfo`. Waits for the node to be fully synced before giving out work.
- New blocks detected via ZMQ `hashblock` (pure-Go ZMTP client), with
  `getbestblockhash` polling as a permanent fallback.
- Every template is verified before use: txids (and BTC wtxids) recomputed, BTC
  witness commitment recomputed, BCH canonical transaction order enforced.
- Coinbase: BIP34 height (exact `CScript() << height` semantics), coinbase tag,
  extranonce1 (4 B) + extranonce2 (configurable), ≤ 100-byte scriptSig, BCH
  100-byte minimum transaction size padding, BTC BIP141 commitment.
- Payout: CashAddr (P2PKH, P2SH, P2SH32, token-aware types, with or without
  prefix, any case), legacy Base58 (incl. BCH P2SH32), BTC P2PKH/P2SH/P2WPKH/P2WSH/P2TR.
  Each address is decoded locally **and** by the node; both scripts must match or
  the engine refuses to start / the miner is refused.
- Stratum: `mining.subscribe`, `mining.authorize`, `mining.configure`
  (BIP310 version rolling), `mining.set_difficulty`, `mining.notify`,
  `mining.submit`, `mining.extranonce.subscribe`, `mining.suggest_difficulty`.
- Stale / duplicate / low-difficulty / malformed share rejection with standard
  error codes; per-connection vardiff; connection, line-length and message-rate limits.
- Solved blocks are submitted immediately, retried on transport errors and then
  verified on the active chain. `blocks_found` only counts confirmed blocks;
  unconfirmed ones show as `pending`.

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
   docker run -d --name wizard-blocks-bch --restart unless-stopped \
     --stop-timeout 90 -p 3333:3333 -p 127.0.0.1:8080:8080 -v wb-bch:/data \
     -e WB_COIN=bch \
     -e WB_RPC_URL=http://<node-ip>:8332 -e WB_RPC_USER=bchrpc -e WB_RPC_PASSWORD=change-me \
     -e WB_ZMQ_HASHBLOCK=tcp://<node-ip>:28332 \
     -e WB_PAYOUT_ADDRESS=bitcoincash:q... \
     wizard-blocks
   docker logs -f wizard-blocks-bch
   ```

   Umbrel / compose: see [deploy/docker-compose.umbrel.yml](deploy/docker-compose.umbrel.yml)
   (BCH engine enabled; BTC engine behind the `btc` profile).

**BCH testnet (chipnet/testnet4) on Umbrel**, separate from mainnet:
[deploy/bch-testnet/README.md](deploy/bch-testnet/README.md).

Without Docker: `go build ./cmd/wizard-blocks && ./wizard-blocks -config deploy/config.example.bch.json`.

## Connecting a miner

| setting | value |
|---|---|
| URL | `stratum+tcp://<host>:3333` |
| user | anything in `fixed` mode (e.g. `bitaxe1`); in `miner` mode your payout address, optionally `.<worker>` (e.g. `bitcoincash:qq...xyz.bitaxe1` or just `qq...xyz.bitaxe1`) |
| password | anything; `d=<difficulty>` sets the starting difficulty |

Bitaxe / NerdQaxe (ESP-Miner), Antminers (cgminer/bmminer) and other version-rolling
firmware negotiate BIP310 automatically. Watch the log for `level=BLOCK` lines.

## Configuration

JSON file (`-config` or `WB_CONFIG`) over built-in defaults, then `WB_*`
environment variables. Full annotated schema in [docs/DESIGN.md](docs/DESIGN.md);
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
| `WB_STRATUM_LISTEN` | `0.0.0.0:3333` | |
| `WB_EXTRANONCE2_SIZE` | 8 | 2..8 |
| `WB_VERSION_ROLLING_MASK` | `1fffe000` | subset of BIP320 bits |
| `WB_VARDIFF_INITIAL` / `_MIN` / `_MAX` / `_TARGET_SHARE_S` | 1024 / 1 / 1e15 / 10 | |
| `WB_MAX_CONNECTIONS` / `WB_MAX_CONNECTIONS_PER_IP` | 1024 / 64 | |
| `WB_API_LISTEN` / `WB_PROMETHEUS` | `127.0.0.1:8080` / true | stats endpoint |
| `WB_LOG_LEVEL` / `WB_LOG_FORMAT` | `info` / `json` | |
| `WB_DATA_DIR` | empty | persists found blocks and best share |

## Stats API

- `GET /stats`: JSON with node status (synced, height, ZMQ), current template,
  pool totals (hashrate 1m/5m/1h, shares, rejects by reason, best share,
  `blocks_found`, `blocks_pending`), per-worker stats, and the block list (each with
  `status`: `pending` | `accepted` | `orphaned` | `stale` | `rejected`).
- `GET /metrics`: Prometheus text format.
- `GET /healthz`: 200 when node connected, synced and work available.

## Tests

```sh
go test -race ./...                 # unit tests (real mainnet block vectors)
scripts/regtest-test.sh bch         # + Docker regtest harness (BCH, 600 blocks)
scripts/regtest-test.sh all         # BCH + both BTC suites
```

See [docs/TESTING.md](docs/TESTING.md) for coverage and the latest results.
