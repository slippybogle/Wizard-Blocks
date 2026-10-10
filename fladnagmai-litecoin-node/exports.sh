# Read by apps that depend on this one (Wizard-Blocks-LTC). derive_entropy
# is umbrelOS's helper for stable per-install secrets (from the device seed).
export APP_LITECOIN_NODE_IP="10.21.21.140"
export APP_LITECOIN_RPC_PORT="9332"
export APP_LITECOIN_P2P_PORT="9333"
export APP_LITECOIN_ZMQ_HASHBLOCK_PORT="28332"
export APP_LITECOIN_RPC_USER="umbrel"
export APP_LITECOIN_RPC_PASS="$(derive_entropy "fladnagmai-litecoin-node-rpc-password")"
export APP_LITECOIN_NETWORK="mainnet"
