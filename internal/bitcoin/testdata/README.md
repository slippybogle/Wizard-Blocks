# Test vectors (real mainnet blocks)

| file | chain | source |
|---|---|---|
| `block413567.raw.bz2` | BTC/BCH common history (pre-fork, pre-segwit) | Bitcoin Core `src/bench/data/block413567.raw` (bzip2 -9) |
| `block_000000000000000000000c835b2adcaedc20fdf6ee440009c249452c726dafae.raw.bz2` | BTC, segwit | rust-bitcoin `bitcoin/tests/data/mainnet_block_…dafae.raw` (bzip2 -9) |
| `bch_block877227.raw.bz2` | BCH, post-CTOR (2024) | Bitcoin Cash Node `src/bench/data/block877227.raw.bz2` (as-is) |

Each block's merkle root, witness commitment and proof-of-work were produced
by real miners and are checked against values computed by this package.
