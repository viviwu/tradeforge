#!/usr/bin/env bash
# C++ BrokerGateway 契约层自检（在 WSL2 Linux 中运行）：
# 启动 brokergw_server(mock adapter) + bgw-probe，验证冻结的 broker-gateway.v1
# 全链路语义。真券商适配器联调复用同一探针，只换 --adapter。
set -euo pipefail
ROOT=/mnt/e/DevSpace/gitsync/tradeforge
GO=/tmp/tradeforge-go126/go/bin/go
ADDR=${BGW_CPP_ADDR:-127.0.0.1:15053}
SERVER=$ROOT/build/cpp/brokergw_server
PROBE=$ROOT/build/bgw-probe

cd "$ROOT"
export GOTOOLCHAIN=local
export GOPROXY=${GOPROXY:-https://goproxy.cn,direct}
export GOMODCACHE=/tmp/tf-gomod GOPATH=/tmp/tf-gopath GOCACHE=/tmp/tf-gocache

echo "== build bgw-probe =="
"$GO" build -o "$PROBE" ./cmd/bgw-probe

if [ ! -x "$SERVER" ]; then
  echo "brokergw_server not built; run build/deps/build_cpp_gateway.sh first" >&2
  exit 1
fi

echo "== start C++ gateway (mock) on $ADDR =="
"$SERVER" --addr "$ADDR" --adapter mock --ring 4096 > "$ROOT/build/bgw-cpp-server.log" 2>&1 &
PID=$!
cleanup() { kill "$PID" 2>/dev/null || true; wait "$PID" 2>/dev/null || true; }
trap cleanup EXIT
sleep 1

echo "== run bgw-probe =="
BGW_PROBE_ADDR="$ADDR" "$PROBE" --symbol AAPL --qty 10
echo "== C++ gateway e2e PASS =="
