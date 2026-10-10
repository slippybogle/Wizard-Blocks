# Install on Umbrel (BTC mainnet)

A simple Bitcoin version of Wizard-Blocks, installed as a second community app
from the same store. It is a functional placeholder: one plain stats page, no
pixel-art Mine yet. The BCH app (`fladnagmai-wizard-blocks`) is unchanged.

- App: `fladnagmai-wizard-blocks-btc` ("Wizard-Blocks-BTC").
- Needs the official **Bitcoin Node** app (`bitcoin`), installed and synced.
  **Pruned is fine and txindex is not needed**: the engine only uses
  `getblocktemplate`, `submitblock`, `getblockchaininfo`, `getblockheader`,
  `validateaddress` and similar node-state calls.
- Stratum: `umbrel.local:51492`. Web UI: from the Umbrel dashboard (port 8421,
  behind the Umbrel login). The stats API is not exposed.
- Data lives in `~/umbrel/app-data/fladnagmai-wizard-blocks-btc/data`.

Installed next to the BCH app, nothing is shared: different app id, container
names (`fladnagmai-wizard-blocks-btc_*`), data directory, Stratum port (51492
vs 62023) and dashboard port (8421 vs 8420).

## Node connection

From the Bitcoin Node app (`bitcoin/exports.sh`, plus the RPC credentials
umbrelOS provides to its dependants, as for Public Pool and Electrs):

| engine setting | from |
|---|---|
| `WB_RPC_URL` | `http://${APP_BITCOIN_NODE_IP}:${APP_BITCOIN_RPC_PORT}` (10.21.21.8:8332) |
| `WB_RPC_USER` / `WB_RPC_PASSWORD` | `APP_BITCOIN_RPC_USER` / `APP_BITCOIN_RPC_PASS` |
| `WB_ZMQ_HASHBLOCK` | `tcp://${APP_BITCOIN_NODE_IP}:${APP_BITCOIN_ZMQ_HASHBLOCK_PORT}` (28334) |

The chain (mainnet, testnet4, signet…) is read from the node, so the app also
works if the Bitcoin Node app runs a test network. If the node connection is
not detected, put `WB_RPC_URL=…`, `WB_RPC_USER=…`, `WB_RPC_PASSWORD=…` (and
optionally `WB_ZMQ_HASHBLOCK=…`) in
`~/umbrel/app-data/fladnagmai-wizard-blocks-btc/data/override.env` and restart
the app.

## Install


1. In umbrelOS, **App Store**, **⋯**, **Community App Stores**: the
   `https://github.com/fladnagmai/Wizard-Blocks` store is already added if
   you use the BCH app (otherwise add it).
2. Open **Wizard-Blocks App Store**, select **Wizard-Blocks-BTC**, **Install**.
3. Open it, paste your payout address (`bc1…`, `1…` or `3…`) in **Settings**,
   click **Save**. Your node checks it; miners get no work until it is set.
   Optional: set a settings password.
4. Point your miner at `stratum+tcp://umbrel.local:51492`, user anything (e.g.
   `bitaxe1`), password `x` (or `d=1024` to pin the difficulty).

## The page

One page, refreshed every 5 s: Node (synced, height, ZMQ), Pool (hashrate
1m/5m/1h, workers, accepted/rejected/bad messages, best share, network
difficulty, effort since the last block, blocks found), Settings, a workers
table and the blocks found (with mempool.space links).

## Release

1. Merge to `main`.
2. Run the `image` workflow with the new image version.
3. Put its `ghcr.io/fladnagmai/wizard-blocks:<version>@sha256:…` on the
   `image:` line of `fladnagmai-wizard-blocks-btc/docker-compose.yml` and push.
