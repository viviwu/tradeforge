#!/usr/bin/env bash
# 券商联调环境 ATP：先下单成交，再查当日委托/成交，验证查询返回数据
set -uo pipefail
ROOT=/mnt/e/DevSpace/gitsync/tradeforge
ATP_SDK=/home/wuxw/workspace/archforce_quantapi_demo/sdk/v2.7.1.2-gcc4.8.5
AMA_SDK=/home/wuxw/workspace/archforce_ama_demo/c++
ADDR=${BGW_ATP_ADDR:-127.0.0.1:15059}
SERVER=$ROOT/build/cpp-atp/brokergw_server
CLI=$ROOT/build/bgw-cli
CFG=${ATP_CONF:-$ROOT/build/atp.conf}
CID="cli-q-$(date +%s)"

export LD_LIBRARY_PATH="$ATP_SDK/lib:$AMA_SDK/lib/Lnx64:${LD_LIBRARY_PATH:-}"

"$SERVER" --addr "$ADDR" --adapter atp --atp-config "$CFG" --ring 4096 \
  > "$ROOT/build/bgw-atp-query-server.log" 2>&1 &
PID=$!
cleanup() { kill "$PID" 2>/dev/null || true; wait "$PID" 2>/dev/null || true; }
trap cleanup EXIT
sleep 3

echo "== CLI: attach -> submit 600000 BUY -> wait -> query orders/trades =="
printf '1\n8\n600000\nSH\nBUY\n108000\n100\n%s\n11 4\n5\n\n6\n\n0\n' \
  "$CID" | \
  BGW_CLI_ADDR="$ADDR" timeout 120 "$CLI"
