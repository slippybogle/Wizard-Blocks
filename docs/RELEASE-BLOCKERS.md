# Release blockers

## Wizard-Blocks-LTC: mainnet MWEB block test

The Litecoin app must not ship until a real **mainnet** Litecoin block, taken
from the user's own synced Litecoin node, passes the byte-for-byte MWEB test
(`go test -tags release ./internal/bitcoin`). That test fails until the block
is in `internal/bitcoin/testdata/ltc/mainnet-<height>.hex`.

When the Litecoin node app is installed and synced, run this one line on the
Umbrel (it saves the current tip block as hex in the home folder):

```
C=fladnagmai-litecoin-node_litecoind_1; sudo docker exec $C litecoin-cli -datadir=/data getblock "$(sudo docker exec $C litecoin-cli -datadir=/data getbestblockhash)" 0 > ~/ltc-mainnet-block.hex
```

Then send `~/ltc-mainnet-block.hex`. The container name and data directory
above are the ones the Litecoin node app will use; if they change while the
app is built, this command is updated in the same change.

Status: **open**.
