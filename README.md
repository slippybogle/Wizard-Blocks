# Wizard-Blocks

a wizard, a mine and a dream.

Self-hosted solo mining for Bitcoin Cash (BCH) with a 16-bit pixel-art web UI.
Uses your own node. 100% of every block reward goes to you. No fees.
Current release: **0.1.4-alpha**

## Install
- Umbrel: [docs/INSTALL-UMBREL.md](docs/INSTALL-UMBREL.md)
- Umbrel, Bitcoin (simple stats page, pruned node OK): [docs/INSTALL-UMBREL-BTC.md](docs/INSTALL-UMBREL-BTC.md)
- Testnets: [BCH](deploy/bch-testnet/README.md) · [BTC](deploy/btc-testnet/README.md)

## Connect a miner
| setting | value |
|---|---|
| URL | `stratum+tcp://umbrel.local:62023` |
| user | any name, e.g. `bitaxe1` |
| password | `x` (or `d=1024` to set your difficulty) |

Every miner gets a 16-bit character in The Mine, named after its user. Pick a race with `race=` in the password, e.g. `x,race=elf` or `d=1024,race=dwarf`: human, elf, dwarf, darkelf, halfling, halforc, gnome. No race = random (always the same for that miner).

## Web UI
- **The Mine:** a wizard digging a pixel-art mine with live hashrate, shares and luck.
- **The Ledger:** full stats plus Settings (payout, difficulty; optional settings password).

## Docs
[How it works](docs/DESIGN.md) · [Tests and results](docs/TESTING.md)
