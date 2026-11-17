#!/usr/bin/env bash
# 券商联调环境：ATP 交易 + AMA 行情旁路，验证 QuerySecurityInfo 拿到真实行情
set -uo pipefail
ROOT=/mnt/e/DevSpace/gitsync/tradeforge
ATP_SDK=/home/wuxw/workspace/archforce_quantapi_demo/sdk/v2.7.1.2-gcc4.8.5
AMA_SDK=/home/wuxw/workspace/archforce_ama_demo/c++
ADDR=${BGW_ATP_ADDR:-127.0.0.1:15058}
SERVER=$ROOT/build/cpp-atp/brokergw_server
CLI=$ROOT/build/bgw-cli
CFG=${ATP_CONF:-$ROOT/build/atp.conf}

export LD_LIBRARY_PATH="$ATP_SDK/lib:$AMA_SDK/lib/Lnx64:${LD_LIBRARY_PATH:-}"

"$SERVER" --addr "$ADDR" --adapter atp --atp-config "$CFG" --ring 4096 \
  > "$ROOT/build/bgw-atp-ama-server.log" 2>&1 &
PID=$!
cleanup() { kill "$PID" 2>/dev/null || true; wait "$PID" 2>/dev/null || true; }
trap cleanup EXIT
sleep 15   # 等 AMA 登录 + 订阅 + 首帧快照

echo "== CLI: attach, then query 600000 / 000001 security info =="
printf '1\n4\n600000\nSH\n4\n000001\nSZ\n7\n0\n' | \
  BGW_CLI_ADDR="$ADDR" timeout 90 "$CLI"
echo "== server log (ama lines) =="
grep -E '\[ama\]' "$ROOT/build/bgw-atp-ama-server.log" | head -20
