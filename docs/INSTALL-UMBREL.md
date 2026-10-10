# Install on Umbrel (BCH mainnet)

Wizard-Blocks installs as a **community app** from this repository, so umbrelOS
starts it on every boot and offers updates in the App Store.

- App: `fladnagmai-wizard-blocks` ("Wizard-Blocks-BCH"), from the "Wizard-Blocks" community store.
- Needs the official **Bitcoin Cash Node** app (`bitcoin-cash-node`), installed and synced.
- Stratum: `umbrel.local:62023`. The web UI opens from the Umbrel dashboard,
  behind the Umbrel login. The stats API is not exposed.
- Settings need no extra password by default (optional settings password).
- Data (blocks, history, payout and difficulty settings, settings password hash) lives in
  `~/umbrel/app-data/fladnagmai-wizard-blocks/data` and survives restarts,
  reboots and updates.

## One-time setup on GitHub

Umbrel downloads the app store and the image anonymously, so both must be public.

1. **Make the repository public**: GitHub, `fladnagmai/Wizard-Blocks`, Settings,
   General, Danger Zone, Change visibility, Public.
2. **Make the image public**: your GitHub profile, Packages, `wizard-blocks`,
   Package settings, Change visibility, Public. The `0.1.4-alpha` image (x86 and
   Raspberry Pi) is already built by the `image` workflow and pinned by digest
   in `fladnagmai-wizard-blocks/docker-compose.yml`.

## Install

1. In umbrelOS, open **App Store**, click the **⋯** menu (top right), then
   **Community App Stores**.
2. Paste `https://github.com/fladnagmai/Wizard-Blocks` and click **Add**.
3. Open **Wizard-Blocks App Store**, select **Wizard-Blocks-BCH**, click **Install**.
   Umbrel checks that Bitcoin Cash Node is installed.
4. Open the app (Umbrel asks for your Umbrel login).
5. Go to **The Ledger** and scroll to **Settings**. No extra password is needed:
   Settings are open to anyone logged into Umbrel. Optionally use **Set settings
   password** there; it is stored hashed in the app's data dir and then required
   for every change. **Remove password** goes back to no password.
6. Paste your payout address (`bitcoincash:q…`) and click **Verify & save address**.
   The engine and your node both check it. Until it is saved, miners are refused
   and the Mine shows "NO PAYOUT ADDRESS".
7. Point your miner at **`umbrel.local:62023`** (or the Umbrel's IP):

   | setting | value |
   |---|---|
   | URL | `stratum+tcp://umbrel.local:62023` |
   | user | anything, e.g. `bitaxe1` |
   | password | `x`, or `d=512` to pin this miner's difficulty |

   Each miner gets a 16-bit character in The Mine, named after its user. Add
   `race=` to the password to pick its race, e.g. `x,race=elf` or `d=512,race=gnome`
   (human, elf, dwarf, darkelf, halfling, halforc, gnome). Without it the race is
   random but always the same for that miner. Hover or tap a character to see
   who it is and its hashrate.

   On a Bitaxe: AxeOS, Settings, Stratum URL `umbrel.local`, port `62023`,
   user `bitaxe1`, Save, Restart. The miner appears in The Mine within a few seconds.

## If the node connection is not detected

The app reads the node settings that the Bitcoin Cash Node app exports
(`APP_BITCOIN_CASH_NODE_NODE_IP`, `_RPC_PORT`, `_RPC_USER`, `_RPC_PASS`,
`_ZMQ_HASHBLOCK_PORT`). If the app log shows `waiting for node RPC` or
`unauthorized`, set them by hand in a file the app reads on start:

```sh
ssh umbrel@umbrel.local
# the node's RPC password, from the running BCHN container:
docker inspect bitcoin-cash-node_bitcoind_1 --format '{{join .Args " "}}' | tr ' ' '\n' | grep -E 'rpcuser|rpcpassword'
nano ~/umbrel/app-data/fladnagmai-wizard-blocks/data/override.env
```

```ini
WB_RPC_URL=http://10.21.21.50:8332
WB_RPC_USER=umbrel
WB_RPC_PASSWORD=<rpcpassword from above>
WB_ZMQ_HASHBLOCK=tcp://10.21.21.50:28332
```

Then restart the app (right-click its icon, **Restart**). The log line
`settings overridden from env file` confirms it was read. The file is in the
app's data dir, so it survives updates. Delete it to go back to auto-detection.

## Updates

Releases use normal version numbers: this one is `0.1.4-alpha`, the next is
the beta. Run the `image` workflow (Actions, `image`, Run workflow) with
the new version.
Its summary prints `ghcr.io/fladnagmai/wizard-blocks:<version>@sha256:…`: put that
on the `image:` line of `fladnagmai-wizard-blocks/docker-compose.yml`, bump
`version` in its `umbrel-app.yml`, and push to `main`. umbrelOS then offers the update; data is kept.

## Logs

Right-click the app icon, then **Troubleshoot**, or over SSH:
`docker logs -f fladnagmai-wizard-blocks_engine_1`.

## Why not build on the Umbrel?

umbrelOS installs community apps by pulling their images; it does not build from
source. The image therefore comes from GHCR, built by `.github/workflows/image.yml`
for `linux/amd64` and `linux/arm64`. Without GitHub, use the manual compose stack
in [deploy/docker-compose.umbrel.yml](../deploy/docker-compose.umbrel.yml): it
builds locally and restarts on boot, but is not managed by the App Store.
