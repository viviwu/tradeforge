#!/usr/bin/env bash
# 券商联调环境 ATP：挂单并撤单（受理 ack + 终态回报）
set -uo pipefail
ROOT=/mnt/e/DevSpace/gitsync/tradeforge
ATP_SDK=/home/wuxw/workspace/archforce_quantapi_demo/sdk/v2.7.1.2-gcc4.8.5
ADDR=${BGW_ATP_ADDR:-127.0.0.1:15057}
SERVER=$ROOT/build/cpp-atp/brokergw_server
CLI=$ROOT/build/bgw-cli
CFG=${ATP_CONF:-$ROOT/build/atp.conf}
CID="cli-cancel-$(date +%s)"

export LD_LIBRARY_PATH="$ATP_SDK/lib:${LD_LIBRARY_PATH:-}"

"$SERVER" --addr "$ADDR" --adapter atp --atp-config "$CFG" --ring 4096 \
  > "$ROOT/build/bgw-atp-cancel-server.log" 2>&1 &
PID=$!
cleanup() { kill "$PID" 2>/dev/null || true; wait "$PID" 2>/dev/null || true; }
trap cleanup EXIT
sleep 2

echo "== CLI: submit resting BUY $CID then cancel =="
printf '1\n2\n8\n600000\nSH\nSELL\n118000\n100\n%s\n11 2\n9\n%s\n11 3\n10\n0\n' \
  "$CID" "$CID" | \
  BGW_CLI_ADDR="$ADDR" timeout 120 "$CLI"
echo "== gateway log =="
tail -60 "$ROOT/build/bgw-atp-cancel-server.log"
