# BCH testnet setup (chipnet / testnet4) on Umbrel

This stack is separate from the mainnet setup: its own compose project
(`wizard-blocks-bch-testnet`), its own Docker volumes, and its own ports.
It runs:

- **bchn**: Bitcoin Cash Node 29.2.0 on **chipnet** (default) or **testnet4**.
  RPC and ZMQ are only reachable inside the compose network.
- **engine**: Wizard-Blocks, built from this repository, connected to `bchn`.
  Stratum is on host port **3335**, and the stats API is on `127.0.0.1:8090`.

Both images are available for amd64 and arm64 (x86 Umbrel Home and Raspberry Pi).
Testnet coins have no value. The point of this setup is to see your miner find
real testnet blocks before you move to mainnet.

## 1. SSH into the Umbrel

```sh
ssh umbrel@umbrel.local        # password = your Umbrel dashboard password
```

## 2. Get the code

```sh
mkdir -p ~/wizard-blocks && cd ~/wizard-blocks
git clone https://github.com/slippybogle/Wizard-Blocks.git .
# no git? use:  curl -L https://github.com/slippybogle/Wizard-Blocks/archive/refs/heads/main.tar.gz | tar xz --strip-components=1
cd deploy/bch-testnet
```

(Use whichever branch holds this code if it is not merged to `main` yet:
`git checkout <branch>`.)

## 3. Configure

```sh
cp .env.example .env
sed -i "s/^RPC_PASSWORD=.*/RPC_PASSWORD=$(head -c 24 /dev/urandom | base64 | tr -d '/+=')/" .env
nano .env        # optional: BCH_NETWORK=testnet4, ports, pruning
```

Leave `PAYOUT_ADDRESS` empty for now.

## 4. Start the node and create a payout address

```sh
docker compose up -d bchn
source .env
alias bcli='docker compose exec bchn bitcoin-cli -$BCH_NETWORK -rpcport=18332 -rpcuser=$RPC_USER -rpcpassword=$RPC_PASSWORD'
bcli createwallet payout
bcli -rpcwallet=payout getnewaddress          # -> bchtest:q...
```

You can also use a `bchtest:` address from Electron Cash or Cashonize in
testnet/chipnet mode. Put the address into `.env`:

```sh
sed -i "s|^PAYOUT_ADDRESS=.*|PAYOUT_ADDRESS=bchtest:q...|" .env
```

## 5. Start the engine

```sh
docker compose up -d --build
docker compose logs -f engine
```

Expected log lines, in order:

1. `connected to node ... chain=chip` (or `chain=test4`)
2. `payout address verified`. If the address is invalid or for the wrong
   network, the engine stops here with `startup failed; refusing to run`.
3. `waiting for node to sync before issuing work`. This repeats until the node
   has synced. Chipnet and testnet4 are small and usually sync in under an hour.
   Check progress with `bcli getblockchaininfo`.
4. `node synced`, then `new block template`. The engine is now issuing work.

## 6. Point a miner at it

| setting | value |
|---|---|
| URL | `stratum+tcp://umbrel.local:3335` (or the Umbrel's IP address) |
| user | anything, e.g. `bitaxe1` (in `PAYOUT_MODE=miner`, use `bchtest:q....bitaxe1`) |
| password | `x` |

On a Bitaxe: open AxeOS, go to Settings, set Stratum URL `umbrel.local`, port
`3335`, user `bitaxe1`, then save and restart.

## 7. Watch it

```sh
docker compose logs -f engine | grep -E 'BLOCK|authorized|rejected'
curl -s 127.0.0.1:8090/stats | head -60     # on the Umbrel
```

- `pool.blocks_pending`: blocks submitted but not yet confirmed on the active chain.
- `pool.blocks_found`: blocks the node confirmed on the active chain. This is
  the only number that means "found".
- `blocks[]` lists every candidate with its status (`pending`, `accepted`,
  `orphaned`, `stale`, `rejected`).

Testnet note: chipnet and testnet4 allow a minimum-difficulty block once 20
minutes have passed since the last block. The engine handles that rule, so even
a single Bitaxe can find blocks there.

## Stop, update, remove

```sh
docker compose down                     # stop (chain data kept)
git pull && docker compose up -d --build   # update the engine
docker compose down -v                  # stop AND delete chain + engine data
```

## Troubleshooting

- **Port 3335 in use**: change `STRATUM_PORT` in `.env`, then run `docker compose up -d`.
- **`node not reachable`**: check `docker compose logs bchn` and make sure
  `RPC_USER`/`RPC_PASSWORD` are unchanged since the node first started.
- **`waiting for node to sync` never ends**: check that the node has peers with
  `bcli getconnectioncount`. Umbrel needs outbound internet access.
- **Disk space**: set `BCHN_PRUNE_MB=1000` in `.env`. The engine works with a
  pruned node.
