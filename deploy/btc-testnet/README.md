# BTC testnet setup (testnet4) on Umbrel

This stack is separate from the mainnet setup and from `bch-testnet`. It has its
own compose project (`wizard-blocks-btc-testnet`), its own Docker volumes and its
own ports. It runs:

- **bitcoind**: Bitcoin Core 28.1 on **testnet4**. RPC and ZMQ are only
  reachable inside the compose network.
- **engine**: Wizard-Blocks, built from this repository, connected to `bitcoind`.
  - Stratum is on host port **3336**; stats JSON / Prometheus on `127.0.0.1:8091`.
  - The web UI is BCH-only for now, so it is switched off in this stack.

| stack | stratum | UI | stats |
|---|---|---|---|
| BCH mainnet (`deploy/docker-compose.umbrel.yml`) | 62023 | 8420 | 127.0.0.1:8080 |
| BTC mainnet (same file, `btc` profile) | 51492 | — | 127.0.0.1:8081 |
| BCH testnet (`deploy/bch-testnet`) | 3335 | 8421 | 127.0.0.1:8090 |
| **BTC testnet (this)** | **3336** | — | **127.0.0.1:8091** |

All of them can run side by side. Both images are available for amd64 and arm64
(x86 Umbrel Home and Raspberry Pi). Testnet coins have no value.

## 1. SSH into the Umbrel

```sh
ssh umbrel@umbrel.local        # password = your Umbrel dashboard password
```

## 2. Get the code

```sh
mkdir -p ~/wizard-blocks && cd ~/wizard-blocks
git clone https://github.com/fladnagmai/Wizard-Blocks.git .
# no git? use:  curl -L https://github.com/fladnagmai/Wizard-Blocks/archive/refs/heads/main.tar.gz | tar xz --strip-components=1
cd deploy/btc-testnet
```

(If this code is not merged to `main` yet, switch to the branch that holds it:
`git checkout <branch>`.)

## 3. Configure

```sh
cp .env.example .env
sed -i "s/^RPC_PASSWORD=.*/RPC_PASSWORD=$(head -c 24 /dev/urandom | base64 | tr -d '/+=')/" .env
nano .env        # optional: ports, pruning, difficulty
```

Leave `PAYOUT_ADDRESS` empty for now.

## 4. Start the node and create a payout address

```sh
docker compose up -d bitcoind
source .env
alias bcli='docker compose exec bitcoind bitcoin-cli -testnet4 -rpcport=18332 -rpcuser=$RPC_USER -rpcpassword=$RPC_PASSWORD'
bcli createwallet payout
bcli -rpcwallet=payout getnewaddress          # -> tb1q...
```

Any testnet4 address you control also works: `tb1q…`, `tb1p…` (taproot), or
legacy `m…` / `n…` / `2…`. Put it into `.env`:

```sh
sed -i "s|^PAYOUT_ADDRESS=.*|PAYOUT_ADDRESS=tb1q...|" .env
```

## 5. Start the engine

```sh
docker compose up -d --build
docker compose logs -f engine
```

Expected log lines, in order:

1. `connected to node ... chain=testnet4`
2. `payout address verified`. If the address is invalid, mainnet, or for the
   wrong coin, the engine stops here with `startup failed; refusing to run`.
3. `waiting for node to sync before issuing work`. This repeats until Bitcoin
   Core has synced testnet4 (usually a few hours; check with `bcli getblockchaininfo`).
4. `node synced`, then `new block template`. The engine is now issuing work.

## 6. Point a miner at it

| setting | value |
|---|---|
| URL | `stratum+tcp://umbrel.local:3336` (or the Umbrel's IP address) |
| user | anything, e.g. `bitaxe1` (in `PAYOUT_MODE=miner`, use `tb1q….bitaxe1`) |
| password | `x`, or `d=512` to pin this miner's difficulty |

On a Bitaxe: open AxeOS, go to Settings, set Stratum URL `umbrel.local`, port
`3336`, user `bitaxe1`, then save and restart.

## 7. Watch it

```sh
docker compose logs -f engine | grep -E 'BLOCK|authorized|rejected'
curl -s 127.0.0.1:8091/stats | head -60     # on the Umbrel
curl -s 127.0.0.1:8091/metrics | grep wb_   # Prometheus
```

- `pool.blocks_pending`: blocks submitted but not yet confirmed on the active chain.
- `pool.blocks_found`: blocks the node confirmed on the active chain. This is
  the only number that means "found".

Testnet4 allows a minimum-difficulty block once 20 minutes have passed since the
last block. The engine handles that rule, so even a single Bitaxe can find blocks.

## Difficulty

Set these in `.env`, then run `docker compose up -d`:

- `VARDIFF_MIN`, `VARDIFF_MAX`, `VARDIFF_TARGET_SECONDS`.
- `FIXED_DIFF`: a value > 0 disables vardiff.

Per miner, the password `d=512` pins that miner's difficulty, clamped to
VARDIFF_MIN..VARDIFF_MAX.

## Stop, update, remove

```sh
docker compose down                     # stop (chain data kept)
git pull && docker compose up -d --build   # update the engine
docker compose down -v                  # stop AND delete chain + engine data
```

## Troubleshooting

- **Port 3336 or 8091 in use**: change `STRATUM_PORT` / `API_PORT` in `.env`,
  then run `docker compose up -d`.
- **`node not reachable`**: check `docker compose logs bitcoind` and make sure
  `RPC_USER`/`RPC_PASSWORD` are unchanged since the node first started.
- **`waiting for node to sync` never ends**: check that the node has peers with
  `bcli getconnectioncount`. Umbrel needs outbound internet access.
- **Disk space**: set `BITCOIND_PRUNE_MB=1000` in `.env`. The engine works with
  a pruned node.
