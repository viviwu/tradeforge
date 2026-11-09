#!/usr/bin/env bash
# BrokerGateway 渠道链路自检（渠道视角）
# server(TradeForge 模拟交易所) + broker-stub(模拟柜台网关) 由本脚本拉起，
# bgw-probe 以 BrokerGatewayService 客户端完整走一遍契约：
#   Attach → 证券档案 → SELL 挂单(ACCEPTED) → 回报推送 → BUY 对冲(FILLED)
#   → 成交回报(seq 连续) → 订单/成交/账户查询 → 心跳 → 撤单(CANCELED)
# 与 scripts/e2e.sh（Forge CLI 直连链路）完全正交，两链路使用独立端口。
#
# 前置：PostgreSQL 可达（默认 127.0.0.1:55432/tradeforge）。
# 本机没有 psql 时请预先建好库；有 psql 时脚本会自动创建缺失的库。
# 使用（全部可覆盖）:
#   scripts/e2e-broker.sh
#   BGW_TEST_DB_DSN=postgres://tradeforge:tradeforge@127.0.0.1:55432/tradeforge_bgw?sslmode=disable
#   BGW_TEST_SERVER_GRPC=127.0.0.1:19590 BGW_TEST_SERVER_HTTP=127.0.0.1:18580
#   BGW_TEST_STUB_GRPC=127.0.0.1:15053
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DB_DSN="${BGW_TEST_DB_DSN:-postgres://tradeforge:tradeforge@127.0.0.1:55432/tradeforge?sslmode=disable}"
SERVER_GRPC="${BGW_TEST_SERVER_GRPC:-127.0.0.1:19590}"
SERVER_HTTP="${BGW_TEST_SERVER_HTTP:-127.0.0.1:18580}"
STUB_GRPC="${BGW_TEST_STUB_GRPC:-127.0.0.1:15053}"
STUB_METRICS="${BGW_TEST_STUB_METRICS:-127.0.0.1:15054}"

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }

# 本机 Go 代理与缓存（允许外部覆盖，CI 走官方 proxy 即可）
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
export GOMODCACHE="${GOMODCACHE:-/tmp/tf-gomod}" GOPATH="${GOPATH:-/tmp/tf-gopath}" GOCACHE="${GOCACHE:-/tmp/tf-gocache}"

# 0) 确保目标数据库存在：server 启动时会自建表结构与演示账号，但不会建库。
if command -v psql >/dev/null 2>&1; then
  ADMIN_DSN="${DB_DSN%%\?*}"                  # postgres://user:pass@host:port/dbname
  DB_NAME="${ADMIN_DSN##*/}"
  ADMIN_DSN="${ADMIN_DSN%/*}/postgres"
  EXISTS="$(psql "$ADMIN_DSN" -Atqc "SELECT 1 FROM pg_database WHERE datname='$DB_NAME'" 2>/dev/null || true)"
  if [ "$EXISTS" != "1" ]; then
    psql "$ADMIN_DSN" -v ON_ERROR_STOP=1 -c "CREATE DATABASE \"$DB_NAME\"" >/dev/null \
      || fail "创建数据库 $DB_NAME 失败（ADMIN_DSN=$ADMIN_DSN）"
    pass "数据库 $DB_NAME 已创建"
  fi
fi

SRV_BIN="${BGW_TEST_SERVER_BIN:-/tmp/tf-bgw-server}"
STUB_BIN="${BGW_TEST_STUB_BIN:-/tmp/tf-bgw-stub}"
PROBE_BIN="${BGW_TEST_PROBE_BIN:-/tmp/tf-bgw-probe}"
( cd "$ROOT" && go build -o "$SRV_BIN" ./cmd/server \
  && go build -o "$STUB_BIN" ./cmd/broker-stub \
  && go build -o "$PROBE_BIN" ./cmd/bgw-probe ) \
  || fail "二进制构建失败"

SRV_LOG="$(mktemp /tmp/tf-bgw-server.XXXXXX)"
STUB_LOG="$(mktemp /tmp/tf-bgw-stub.XXXXXX)"
cleanup() {
  [ -n "${SRV_PID:-}" ] && kill "$SRV_PID" 2>/dev/null || true
  [ -n "${STUB_PID:-}" ] && kill "$STUB_PID" 2>/dev/null || true
}
trap cleanup EXIT
echo "日志: server=$SRV_LOG stub=$STUB_LOG"

# 1) 启动模拟交易所
TRADEFORGE_GRPC_ADDR=":$(( ${SERVER_GRPC##*:} ))" \
TRADEFORGE_HTTP_ADDR=":$(( ${SERVER_HTTP##*:} ))" \
TRADEFORGE_DB_DSN="$DB_DSN" \
TRADEFORGE_JWT_SECRET="bgw-e2e" \
TRADEFORGE_LOG_LEVEL="warn" \
TRADEFORGE_BCRYPT_COST="${BGW_TEST_BCRYPT_COST:-4}" \
"$SRV_BIN" > "$SRV_LOG" 2>&1 &
SRV_PID=$!

HEALTH_OK=""
for _ in $(seq 1 100); do
  if curl -fsS "http://$SERVER_HTTP/healthz" >/dev/null 2>&1; then HEALTH_OK=1; break; fi
  if grep -q 'server stopped' "$SRV_LOG" 2>/dev/null; then fail "server 启动失败: $(tail -5 "$SRV_LOG")"; fi
  sleep 0.1
done
[ -n "$HEALTH_OK" ] || fail "server healthz 不可达: $(tail -5 "$SRV_LOG")"
pass "模拟交易所已就绪 ($SERVER_GRPC)"

# 2) 启动 broker-stub（登录重试最多 90s，等交易所就绪）
BROKER_STUB_LISTEN_ADDR="$STUB_GRPC" \
BROKER_STUB_METRICS_ADDR="$STUB_METRICS" \
BROKER_STUB_UPSTREAM_ADDR="$SERVER_GRPC" \
BROKER_STUB_USER="alice" \
BROKER_STUB_PASS="alice123" \
"$STUB_BIN" > "$STUB_LOG" 2>&1 &
STUB_PID=$!

STUB_OK=""
for _ in $(seq 1 100); do
  if (exec 3<>"/dev/tcp/${STUB_GRPC%%:*}/${STUB_GRPC##*:}") 2>/dev/null; then
    exec 3>&- 2>/dev/null || true
    STUB_OK=1
    break
  fi
  if grep -q 'broker-stub stopped' "$STUB_LOG" 2>/dev/null; then fail "broker-stub 启动失败: $(tail -5 "$STUB_LOG")"; fi
  sleep 0.2
done
[ -n "$STUB_OK" ] || fail "broker-stub 端口不可达 $STUB_GRPC: $(tail -5 "$STUB_LOG")"
curl -fsS "http://$STUB_METRICS/healthz" >/dev/null || fail "broker-stub metrics/healthz 不可达: $(tail -5 "$STUB_LOG")"
pass "broker-stub 已就绪 ($STUB_GRPC, metrics $STUB_METRICS)"

# 3) 契约全流程探测（probe 断言全部通过即 success）
BGW_PROBE_ADDR="$STUB_GRPC" "$PROBE_BIN" || fail "bgw-probe 失败"
pass "bgw-probe 全流程通过"

# 4) 网关侧关键路径指标已暴露（阶段验收门禁）
if ! curl -fsS "http://$STUB_METRICS/metrics" | grep 'brokergw_reports_published_total' >/dev/null; then
  fail "网关 metrics 缺少 brokergw_reports_published_total"
fi
pass "网关 metrics 正常 (brokergw_*)"

echo
echo "== ALL PASS: BrokerGateway 渠道链路自检通过 =="
