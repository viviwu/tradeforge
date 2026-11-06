# Benchmarks

TradeForge 压测记录。新增结果时在最前面追加，并注明环境。

## 2026-11-05 · P4 主链路基线

### 环境

| 项 | 值 |
|---|---|
| 机器 | Apple M 系列开发机（macOS，Deck 本机运行服务端与 Postgres） |
| 服务端 | `tradeforge-server` 单进程，gRPC 直连（127.0.0.1:19090） |
| 数据库 | Docker PostgreSQL 17-alpine，volume 本机，`localhost:55432` |
| Go | 1.26 |
| 客户端 | `forge stress login` + `build/bench`（TradeApi SDK，16 并发 worker） |

> 与默认生产配置的差别：压测使用 `TRADEFORGE_BCRYPT_COST=4`（默认 10）、
> `TRADEFORGE_LOGIN_RPS/BURST=10000`（默认 20/40）。默认限流下登录压测会按设计被拒，
> 登录吞吐取决于所在 IP 的限流阈值。

### 结果

| 场景 | 样本 | 并发 | 吞吐 | avg | p50 | p95 | p99 | max | 错误 |
|---|---|---|---|---|---|---|---|---|---|
| 登录（含 bcrypt 校验 + 事务发 token） | 500 | 50 | — | 18.3ms | 14.2ms | 58.2ms | 66.8ms | 67.4ms | 0 |
| 挂单路径（不成交，engine 记账 + DB 事务 + 冻结资金） | 2992 | 16 | 753 rps | 20.5ms | 10.6ms | 71.6ms | 118.8ms | 254.8ms | 0 |
| 成交路径（买卖双方对价成交，结算账户+持仓+双份 trade 记录） | 2992 | 16 | 472 rps | 33.7ms | 34.2ms | 42.0ms | 50.0ms | 81.5ms | 0 |

### 压测后一致性检查

- `trades` 共 2802 笔、28,020 股，与成交单量一致；
- `orders` 对 `trades` 逐一核对 `filled_qty`，无漂移（对账 SQL 返回空）；
- 买方剩余挂单的相关冻结资金与余额自洽（卖方 `frozen_cash=0`）。

### 分析

1. **单点串行是当前瓶颈**：ADR-003 用一把全局互斥锁保证撮合结果落库顺序，
   成交路径吞吐约 470 rps，主要受限于「撮合 → 单事务落库」的串行窗口。
2. **登录延迟主要来自 bcrypt**（cost=4 时 p50 也要做至少一次哈希），生产默认
   cost=10 时单次校验会显著变慢，好在有 per-IP 限流兜底。
3. **挂单路径 p99 (119ms) 波动大**：受 Postgres 写盘与 GC 停顿影响，
   成交路径延迟分布反而更平（引擎内先排好队）。

### 后续优化候选

1. 按 symbol 分片互斥锁（ADR-003 中预留的演进方向），预计可随标的数近似线性扩展；
2. 市价单执行前校验资金（按对手盘口最优价估算冻结额);
3. 订单簿持久化快照，缩短重启后 `Recover` 的列表长度;
4. p4 级 pprof profiling，定位 254ms 尾延迟（当前怀疑为连接复用与事务 BEGIN 往返）。

## 记录方法

```bash
# 起 PostgreSQL 与服务端（压测参数）
docker run -d --name tf-test-postgres \
  -e POSTGRES_USER=tradeforge -e POSTGRES_PASSWORD=tradeforge -e POSTGRES_DB=tradeforge \
  -p 55432:5432 postgres:17-alpine

# 服务端（bcrypt/限流按上文说明放宽）
TRADEFORGE_GRPC_ADDR=127.0.0.1:19090 TRADEFORGE_HTTP_ADDR=127.0.0.1:18080 \
TRADEFORGE_DB_DSN='postgres://tradeforge:tradeforge@localhost:55432/tradeforge_bench?sslmode=disable' \
TRADEFORGE_BCRYPT_COST=4 TRADEFORGE_LOGIN_RPS=10000 TRADEFORGE_LOGIN_BURST=10000 \
  go run ./cmd/server

# 登录压测
forge --address 127.0.0.1:19090 stress login --n 500 --concurrency 50 \
  --user alice --password alice123

# 交易路径压测（挂单 / 成交）
go build -o /tmp/tf-bench ./build/bench
/tmp/tf-bench -mode rest  -n 3000 -c 16
/tmp/tf-bench -mode cross -n 3000 -c 16   # 需先给卖方账户 SQL 灌入 AAPL 持仓
```
