# TradeForge

TradeForge 是一个用 Go 编写的**模拟交易系统**，用于练习高并发、gRPC、事件驱动和交易系统设计。系统支持 CLI 高并发登录、下单、撤单、查询，以及订单/策略状态和行情的实时订阅，并提供 Web 管理端。

> 当前状态：**P0–P4 主链路 MVP 已完成；M6 渠道对接（BrokerGateway v1）已完成、契约冻结**。服务端、CLI、Go SDK、撮合/OMS、订阅、行情、Forge Console、Docker 部署与 Postgres 集成测试均已可运行；渠道侧通过模拟券商网关 `broker-stub` 与 `bgw-probe` 验证统一契约。需求与技术方案见 [PR-TD.doc.md](./PR-TD.doc.md)，进度与决策见 [dev_ledger.md](./dev_ledger.md)。

## 命名体系

| 名称 | 定位 |
|---|---|
| **TradeForge** | 项目 / 系统 / 仓库名 |
| **forge** | 命令行客户端，可执行文件 `forge` |
| **TradeApi** | Go 客户端 SDK，包路径 `pkg/tradeapi` |
| **Forge Console** | Web 管理端，基于 grpc-gateway 的 REST API |

## 系统组成

```
┌──────────────────────── tradeforge-server ────────────────────────┐
│                                                                   │
│  Gateway (gRPC/REST) ──▶ OMS (订单/持仓/策略) ◀──▶ 撮合引擎        │
│         │                       │                  单 goroutine     │
│         │                 事件总线 (seq / 背压)                     │
│         │                       │                                  │
│   订阅管理 Streams        模拟行情 Provider         PostgreSQL      │
└───────────────────────────────────────────────────────────────────┘
          ▲ gRPC                            ▲ REST
   TradeApi + forge CLI              Forge Console
```

- **tradeforge-server**：单进程模块化服务，包含 Gateway、撮合引擎、OMS、行情 Provider 与订阅管理。
- **forge**：基于 TradeApi 的 CLI，支持登录、下单、撤单、查询、订阅和内置压测（`forge stress login/order`）。
- **TradeApi**：Go SDK，负责连接池、Token 刷新、断线重连与订阅恢复。
- **Forge Console**：查看订单、持仓、策略状态和行情，并执行人工干预。

## 渠道对接（M6 / BrokerGateway v1）

```
渠道核心（Go；本仓库内由 cmd/bgw-probe 扮演）
    │ broker-gateway.v1（gRPC，渠道核心是 client）
broker-stub（模拟券商适配网关：cmd/broker-stub + internal/brokergw）
    │ TradeApi SDK（登录/刷新/重连/补发）
tradeforge-server（模拟交易所，现状零改动）
```

- 每个真实券商 SDK 未来各封装为一个独立适配进程，对上游实现同一个 `BrokerGatewayService` server；stub 与真网关同构，不直连进程内 OMS。
- 契约以 `client_order_id` 为幂等主键，受理 ack 与终态回报分离；回报 `seq` 由网关本地分配，会话内按游标补发，缺口/窗口过期走查询补齐。
- 价格统一 `1 unit = 0.0001 元`；单券商差异进 `ext_json` 并登记到差异表。完整语义见 [docs/broker-gateway.md](./docs/broker-gateway.md)。

## 核心设计

- **单写者撮合**：所有下单/撤单进入同一命令 channel，由单个 goroutine 顺序处理订单簿。
- **按标的串行落库**：每个 symbol 一把结算互斥锁，同一标的按撮合顺序落库、不同标可并行结算；账户/订单行锁保证资金与持仓一致。
- **事件驱动**：统一事件信封带 `seq`，订单/成交事件不可静默丢弃，行情可丢弃旧快照并告警。
- **市价买预算冻结**：以当前最优卖价估算冻结额（记录在订单 `price` 字段），结算按实际成交取用、终态释放余量；空盘口市价买直接拒绝。
- **重启恢复**：启动时加载 `PENDING_NEW` / `ACCEPTED` / `PARTIALLY_FILLED` 订单，GTC 限价单重新挂回内存订单簿，非 GTC 单自动撤销并释放冻结。
- **幂等与对账**：`client_order_id` 唯一索引保证下单幂等，`trade_seq` 保证成交消费幂等，定期对账发现漂移。
- **金额整数化**：价格与金额统一使用 `int64` 最小单位，禁止浮点。
- **按账号限流**：下单/撤单、查询、订阅分别按账号令牌桶限流，超配返回 429（`ResourceExhausted`），并暴露 `tradeforge_rate_limited_total` 指标；登录限流为按 IP 防爆破。

## 功能范围

- 认证：用户名密码登录、Token 刷新/登出、gRPC 鉴权、RBAC。
- 交易：限价/市价下单、撤单、订单状态机、幂等下单、异步成交回报。
- 查询：订单、成交、持仓与资金、策略状态。
- 订阅：订单/成交、策略状态、行情，支持断线重连与有界补发。
- 行情：模拟 tick、L1 盘口、历史回放。
- 管理：Forge Console 查询与人工撤单/暂停策略，操作审计。

## 快速开始

```bash
# 启动 PostgreSQL + 服务端（首次会构建镜像并自动迁移、写入演示数据）
docker compose -f deploy/docker-compose.yml up -d --build

# 构建 CLI
go build -o bin/forge ./cmd/forge

# 登录（演示账号 alice/alice123、admin/admin123）
bin/forge login --user alice --password alice123

# 下单、查询、订阅
bin/forge order submit --symbol AAPL --side BUY --type LIMIT --price 10000 --qty 10
bin/forge query orders
bin/forge query positions
bin/forge sub market --symbol AAPL
bin/forge sub orders

# 全链路自检（登录→挂单→成交→回报订阅→撤单，需服务端已启动）
scripts/e2e.sh

# 渠道视角自检（脚本自行拉起 server + broker-stub + bgw-probe 三进程，需 PostgreSQL 可达）
scripts/e2e-broker.sh
```

价格与金额均为最小单位整数（示例中 `10000` 显示为 `100.00`）。CLI 支持 `--output table|json`，Token 缓存在 `~/.config/forge/config.json`（`FORGE_CONFIG` 可覆盖）。

Forge Console：<http://localhost:8080>，指标：<http://localhost:8080/metrics>，健康检查：<http://localhost:8080/healthz>。`docker compose` 默认同时启动 `broker-stub`，网关 gRPC 端口为 `50053`、指标端口为 `50054`；单独验证渠道链路用 `scripts/e2e-broker.sh`（独立端口 19590/18580/15053/15054，不与上面 CLI 链路冲突）。

### REST 示例

```bash
TOKEN=$(curl -s -X POST http://localhost:8080/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"alice","password":"alice123"}' | jq -r .accessToken)

curl -s http://localhost:8080/v1/orders -H "Authorization: Bearer $TOKEN"
```

### 配置

服务端通过环境变量配置，常用项：

| 变量 | 默认值 | 说明 |
|---|---|---|
| `TRADEFORGE_GRPC_ADDR` | `:9090` | gRPC 监听地址 |
| `TRADEFORGE_HTTP_ADDR` | `:8080` | REST / Console 监听地址 |
| `TRADEFORGE_DB_DSN` | 本地 `tradeforge` 库 | PostgreSQL DSN |
| `TRADEFORGE_JWT_SECRET` | `dev-secret-change-me` | 生产必须替换 |
| `TRADEFORGE_SEED` | `true` | 是否写入演示用户与策略 |
| `TRADEFORGE_SYMBOLS` | `AAPL,MSFT,TSLA,NVDA` | 可交易标的 |
| `TRADEFORGE_INITIAL_CASH` | `100000000` | 种子账号初始可用资金（最小单位） |
| `TRADEFORGE_INITIAL_POSITION` | `10000` | 种子账号初始持仓股数（0 关闭，便于先卖后买地测试成交链路） |
| `TRADEFORGE_ORDER_RPS` / `TRADEFORGE_ORDER_BURST` | `50` / `100` | 每账号每秒下单/撤单数（0 关闭限流） |
| `TRADEFORGE_QUERY_RPS` / `TRADEFORGE_QUERY_BURST` | `200` / `400` | 每账号每秒交易查询数（0 关闭限流） |
| `TRADEFORGE_MAX_STREAMS` | `16` | 每账号最大并发订阅数（0 不限制） |

`broker-stub`（模拟券商网关）常用项：

| 变量 | 默认值 | 说明 |
|---|---|---|
| `BROKER_STUB_LISTEN_ADDR` | `:50053` | 网关 gRPC 监听地址 |
| `BROKER_STUB_METRICS_ADDR` | `:50054` | 网关 Prometheus `/metrics` 与 `/healthz` |
| `BROKER_STUB_UPSTREAM_ADDR` | `127.0.0.1:50051` | 模拟交易所 gRPC（compose 内为 `tradeforge:9090`） |
| `BROKER_STUB_USER` / `BROKER_STUB_PASS` | `alice` / `alice123` | 上游登录账号（单一柜台账号映射） |
| `BROKER_STUB_FUND_ACCOUNT` / `BROKER_STUB_TRADE_ACCOUNT` | `F001` / `A0001` | 契约侧资金账号 / 股东账号 |
| `BROKER_STUB_REPORT_RING` | `4096` | 会话内回报补发窗口 |
| `BROKER_STUB_HALTED_SYMBOLS` / `BROKER_STUB_LIMIT_PCT` | 空 / `0` | 可选的停牌、涨跌停模拟 |

完整变量表与契约语义见 [docs/broker-gateway.md](./docs/broker-gateway.md)。

> 演示密码与默认 JWT secret 仅用于本地开发，部署到共享环境前必须修改。

## 开发

需要 Go 1.26+、buf 与 Docker（数据库）。首次使用可安装 buf：

```bash
go install github.com/bufbuild/buf/cmd/buf@v1.75.0
make proto              # buf lint + 生成 gRPC/gateway/OpenAPI 代码
make build              # 编译所有 Go 包
make test               # 单元测试
make test-integration   # PostgreSQL 集成测试（默认 localhost:55432）
make help               # 查看全部命令
```

集成测试使用独立数据库，先启动一次性 PostgreSQL：

```bash
docker run -d --name tf-test-postgres \
  -e POSTGRES_USER=tradeforge -e POSTGRES_PASSWORD=tradeforge -e POSTGRES_DB=tradeforge \
  -p 55432:5432 postgres:17-alpine
make test-integration
```

## 技术栈

Go 1.26+ · gRPC + Protobuf · grpc-gateway · Cobra · PostgreSQL 17 · pgx · 内嵌 SQL 迁移 · Prometheus · Docker Compose

## 开发路线

| 阶段 | 内容 |
|---|---|
| P0 | 骨架、proto、Auth、数据库迁移、docker-compose（已完成） |
| P1 | 撮合引擎、OMS 订单状态、下单/撤单、订单查询（已完成） |
| P2 | 持仓投影、事件订阅、TradeApi 重连（已完成） |
| P3 | 模拟行情、行情订阅、Forge Console（已完成） |
| P4 | 压测、限流、背压、对账、故障恢复（已完成；订单簿快照经评审决策不做） |
| M6 | 渠道对接（已完成）：`broker-gateway.v1` 契约、broker-stub 网关、渠道视角 e2e、契约冻结 |
| P5 | 可选：拆分服务、NATS、L2 行情、策略沙箱（搁置，按需重启） |

## 文档

- [PR-TD.doc.md](./PR-TD.doc.md)：产品需求与技术设计
- [dev_ledger.md](./dev_ledger.md)：ADR、里程碑、风险与变更记录
- [docs/broker-gateway.md](./docs/broker-gateway.md)：BrokerGateway v1 契约语义、seq/重连规则、账号映射与券商差异登记表
- [docs/benchmarks.md](./docs/benchmarks.md)：P4 压测基线
