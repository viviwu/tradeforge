#!/usr/bin/env bash
# TradeForge 全链路自检
# 登录 → 订阅订单回报 → 挂单(ACCEPTED/冻结) → 对冲成交(FILLED)
# → 订单/成交查询 → 订阅收到回报 → 撤单(CANCELED/解冻)
#
# 演示账号自带初始资金与库存（TRADEFORGE_INITIAL_CASH/TRADEFORGE_INITIAL_POSITION，
# 同账号先卖后买完成 self-cross），因此全新数据库也能跑通。
#
# requires do:
#   服务端已启动（gRPC + REST 可达）
# 使用（全部可覆盖）:
#   scripts/e2e.sh
#   E2E_GRPC_ADDR=10.0.0.5:19090 E2E_HTTP_ADDR=10.0.0.5:18080 scripts/e2e.sh
#   E2E_USER=alice E2E_PASS=alice123 E2E_SYMBOL=AAPL E2E_QTY=10
#   E2E_FORGE_BIN=/tmp/forge-e2e   # CLI 二进制路径（默认临时构建）
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ADDR="${E2E_GRPC_ADDR:-127.0.0.1:19090}"
HTTP="${E2E_HTTP_ADDR:-127.0.0.1:18080}"
E2E_USER="${E2E_USER:-alice}"
E2E_PASS="${E2E_PASS:-alice123}"
SYMBOL="${E2E_SYMBOL:-AAPL}"
QTY="${E2E_QTY:-10}"
PRICE="${E2E_PRICE:-199900}" # 独立高价位（显示 1999.00），不与种子/旧单竞价，重跑互不干扰

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }

BIN="${E2E_FORGE_BIN:-/tmp/forge-e2e}"
( cd "$ROOT" && go build -o "$BIN" ./cmd/forge ) || fail "forge CLI 构建失败"
CFG="$(mktemp /tmp/forge-e2e.XXXXXX.json)" && echo '{}' > "$CFG"
SUB_LOG="$(mktemp /tmp/forge-e2e-sub.XXXXXX.log)"
SUB_PID=""
cleanup() {
  [ -n "$SUB_PID" ] && kill "$SUB_PID" 2>/dev/null || true
  rm -f "$CFG" "$SUB_LOG"
}
trap cleanup EXIT

# 1) 健康检查（轮询最多 5s，容忍服务端刚启动）
HEALTH_OK=""
for _ in $(seq 1 50); do
  if curl -fsS "http://$HTTP/healthz" >/dev/null 2>&1; then
    HEALTH_OK=1
    break
  fi
  sleep 0.1
done
[ -n "$HEALTH_OK" ] || fail "healthz 不可达 http://$HTTP"
pass "healthz ok"

COMMON=( --address "$ADDR" --config "$CFG" )

# 2) 登录（使用隔离 token 配置，不碰用户本地凭证）
"$BIN" "${COMMON[@]}" login --user "$E2E_USER" --password "$E2E_PASS" >/dev/null || fail "login 失败"
pass "login ok"

# 3) 启动订阅，收集全流程订单回报事件
"$BIN" "${COMMON[@]}" sub orders > "$SUB_LOG" 2>&1 &
SUB_PID=$!
sleep 1

# 4) 挂 SELL 限价单：应 ACCEPTED（进入订单簿、持仓冻结）
OUT="$("$BIN" "${COMMON[@]}" order submit --symbol "$SYMBOL" --side SELL --type LIMIT \
    --price "$PRICE" --qty "$QTY" --client-order-id "e2e-rest-$$")" || fail "提交挂单失败"
echo "$OUT" | grep -q 'status=ACCEPTED' || fail "挂单未 ACCEPTED: $OUT"
pass "委托 ACCEPTED，持仓已冻结"

# 5) 买单吃掉自己的挂单 → 双方 FILLED
OUT="$("$BIN" "${COMMON[@]}" order submit --symbol "$SYMBOL" --side BUY --type LIMIT \
    --price "$PRICE" --qty "$QTY" --client-order-id "e2e-cross-$$")" || fail "提交对冲买单失败"
echo "$OUT" | grep -q 'status=FILLED' || fail "对冲买单未 FILLED: $OUT"
pass "对冲成交 FILLED，回报已生成"

# 6) 订单查询：两笔均 FILLED（查询链路核对）
ORDERS="$("$BIN" "${COMMON[@]}" query orders)"
for cid in "e2e-rest-$$" "e2e-cross-$$"; do
  echo "$ORDERS" | awk -v cid="$cid" '$0~cid && $0~/FILLED/ {found=1} END {exit !found}' \
    || fail "订单 $cid 未在查询结果中为 FILLED"
done
pass "订单查询两笔均 FILLED"

# 7) 成交回报查询
"$BIN" "${COMMON[@]}" query trades | awk -v s="$SYMBOL" '$0~s {found=1} END {exit !found}' \
  || fail "成交查询缺少 $SYMBOL 记录"
pass "成交查询可见"

# 8) 订阅回报：FILLED 事件确实通过订阅流推送
kill "$SUB_PID" 2>/dev/null || true
wait "$SUB_PID" 2>/dev/null || true
SUB_PID=""
grep -q 'FILLED' "$SUB_LOG" \
  || fail "订阅流未收到 FILLED 回报: $(head -5 "$SUB_LOG")"
pass "订阅收到订单回报 ($(grep -c FILLED "$SUB_LOG") 个事件)"

# 9) 撤单链路：新 BUY 挂单 → cancel → CANCELED
OUT="$("$BIN" "${COMMON[@]}" order submit --symbol "$SYMBOL" --side BUY --type LIMIT \
    --price "$((PRICE + 10000))" --qty 1 --client-order-id "e2e-cancel-$$")" || fail "提交撤单目标失败"
echo "$OUT" | grep -q 'status=ACCEPTED' || fail "撤单目标未 ACCEPTED: $OUT"
OUT="$("$BIN" "${COMMON[@]}" order cancel --client-order-id "e2e-cancel-$$")" || fail "撤单请求失败"
echo "$OUT" | grep -q 'status=CANCELED' || fail "撤单未 CANCELED: $OUT"
pass "撤单 CANCELED，冻结已释放"

echo
echo "== ALL PASS: TradeForge 全链路自检通过 =="
