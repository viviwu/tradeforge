#!/usr/bin/env bash
# 券商联调环境 ATP 交易：菜单 CLI 下单 / 回报 / 查询 / 撤单 全链路
set -uo pipefail
ROOT=/mnt/e/DevSpace/gitsync/tradeforge
ATP_SDK=/home/wuxw/workspace/archforce_quantapi_demo/sdk/v2.7.1.2-gcc4.8.5
AMA_SDK=/home/wuxw/workspace/archforce_ama_demo/c++
ADDR=${BGW_ATP_ADDR:-127.0.0.1:15055}
SERVER=$ROOT/build/cpp-atp/brokergw_server
CLI=$ROOT/build/bgw-cli
CFG=${ATP_CONF:-$ROOT/build/atp.conf}
CID="cli-order-$(date +%s)"

export LD_LIBRARY_PATH="$ATP_SDK/lib:$AMA_SDK/lib/Lnx64:${LD_LIBRARY_PATH:-}"

echo "== start C++ gateway (ATP) on $ADDR =="
"$SERVER" --addr "$ADDR" --adapter atp --atp-config "$CFG" --ring 4096 \
  > "$ROOT/build/bgw-atp-order-server.log" 2>&1 &
PID=$!
cleanup() { kill "$PID" 2>/dev/null || true; wait "$PID" 2>/dev/null || true; }
trap cleanup EXIT
sleep 2

echo "== CLI: submit $CID (SH 600000 BUY 10.80 x100) =="
printf '1\n2\n8\n600000\nSH\nBUY\n108000\n100\n%s\n11 3\n5\n%s\n6\n%s\n9\n%s\n11 2\n5\n%s\n0\n' \
  "$CID" "$CID" "$CID" "$CID" "$CID" | \
  BGW_CLI_ADDR="$ADDR" timeout 120 "$CLI"
echo "== gateway log =="
tail -60 "$ROOT/build/bgw-atp-order-server.log"
