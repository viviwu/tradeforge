#!/usr/bin/env bash
# 券商联调环境 ATP 交易菜单 CLI 联调（在 WSL2 中运行）
set -uo pipefail
ROOT=/mnt/e/DevSpace/gitsync/tradeforge
ATP_SDK=/home/wuxw/workspace/archforce_quantapi_demo/sdk/v2.7.1.2-gcc4.8.5
AMA_SDK=/home/wuxw/workspace/archforce_ama_demo/c++
ADDR=${BGW_ATP_ADDR:-127.0.0.1:15054}
SERVER=$ROOT/build/cpp-atp/brokergw_server
CLI=$ROOT/build/bgw-cli
CFG=${ATP_CONF:-$ROOT/build/atp.conf}

export LD_LIBRARY_PATH="$ATP_SDK/lib:$AMA_SDK/lib/Lnx64:${LD_LIBRARY_PATH:-}"

echo "== start C++ gateway (ATP) on $ADDR =="
"$SERVER" --addr "$ADDR" --adapter atp --atp-config "$CFG" --ring 4096 \
  > "$ROOT/build/bgw-atp-server.log" 2>&1 &
PID=$!
cleanup() { kill "$PID" 2>/dev/null || true; wait "$PID" 2>/dev/null || true; }
trap cleanup EXIT
sleep 2

echo "== CLI menu: attach / subscribe / account / security / orders / trades / heartbeat =="
printf '1\n2\n7\n4\n600000\nSH\n5\n\n6\n\n10\n0\n' | \
  BGW_CLI_ADDR="$ADDR" timeout 90 "$CLI"
echo "== gateway log =="
cat "$ROOT/build/bgw-atp-server.log" | tail -40
