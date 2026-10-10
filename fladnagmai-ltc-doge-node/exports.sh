# Read by apps that depend on this one (Wizard-Blocks-LTC) and by this app's
# own docker-compose.yml. derive_entropy is umbrelOS's helper for stable
# per-install secrets (from the device seed).
export APP_LITECOIN_NODE_IP="10.21.21.140"
export APP_LITECOIN_RPC_PORT="9332"
export APP_LITECOIN_P2P_PORT="9333"
export APP_LITECOIN_ZMQ_HASHBLOCK_PORT="28332"
export APP_LITECOIN_RPC_USER="umbrel"
export APP_LITECOIN_RPC_PASS="$(derive_entropy "fladnagmai-ltc-doge-node-litecoin-rpc-password")"
export APP_LITECOIN_NETWORK="mainnet"

export APP_DOGECOIN_NODE_IP="10.21.21.141"
export APP_DOGECOIN_RPC_PORT="22555"
export APP_DOGECOIN_P2P_PORT="22556"
export APP_DOGECOIN_ZMQ_HASHBLOCK_PORT="28332"
export APP_DOGECOIN_RPC_USER="umbrel"
export APP_DOGECOIN_RPC_PASS="$(derive_entropy "fladnagmai-ltc-doge-node-dogecoin-rpc-password")"
export APP_DOGECOIN_NETWORK="mainnet"
