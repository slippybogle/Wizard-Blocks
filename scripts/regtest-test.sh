#!/usr/bin/env bash
# Runs unit tests and the Docker regtest integration harness.
# Usage: scripts/regtest-test.sh [bch|btc|all]   (default: bch)
set -euo pipefail
cd "$(dirname "$0")/.."
which=${1:-bch}
if ! docker info >/dev/null 2>&1; then
  echo "docker daemon not reachable" >&2; exit 1
fi
for img in zquestz/bitcoin-cash-node:latest bitcoin/bitcoin:28.1; do
  docker image inspect "$img" >/dev/null 2>&1 || docker pull -q "$img" >/dev/null
done
echo "== unit tests (race detector)"
go test -race -count=1 ./...
case "$which" in
  bch) run='TestRegtestBCH' ;;
  btc) run='TestRegtestBTC' ;;
  all) run='TestRegtest' ;;
  *) echo "unknown target $which" >&2; exit 2 ;;
esac
mkdir -p .regtest-logs
echo "== regtest integration ($run); engine logs in .regtest-logs/"
WB_IT_LOGDIR="$PWD/.regtest-logs" go test -race -tags integration -count=1 -v -timeout 60m -run "$run" ./test/integration
