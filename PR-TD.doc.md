# TradeForge 产品需求与技术设计书

| 项目 | 内容 |
|---|---|
| 文档版本 | v0.2 |
| 状态 | 设计评审中 |
| 最后更新 | 2026-11-05 |
| 关联台账 | `dev_ledger.md`（ADR / 里程碑 / 风险） |
| 代码仓库 | `github.com/viviwu/tradeforge` |

## 1. 文档定位

本文档定义 **TradeForge** 的产品范围、功能需求、非功能目标、技术架构、接口契约、数据模型、并发与一致性策略、测试验收和分阶段计划。后续开发以本文档为准，每个阶段结束需满足对应验收标准。

**核心目标**：用 Go 实现一个支持 CLI 高并发登录、下单、查询、订阅（订单/策略状态变化和行情）的模拟交易系统，同时具备 B/S 管理端和 Protobuf Gateway，作为 Go 并发、gRPC、事件驱动与交易系统设计的综合练习项目。

**设计原则**：

1. **单进程模块化起步**，包边界清晰，保留后续拆分点，避免第一版陷入分布式调试。
2. **IDL 先行**：先写 `.proto`，再生成 gRPC/REST 代码，接口即契约。
3. **单一写者**：撮合引擎单 goroutine、OMS 按账户串行，热点路径尽量无锁。
4. **事件驱动**：组件间通过事件总线通信，事件带序列号，可重放、可对账。
5. **金额整数化**：价格与金额统一使用 `int64` 最小单位，禁止 `float` 参与计价。
6. **可观测优先**：关键路径从第一版就带 metrics、结构化日志和 trace。

---

## 2. 命名体系与术语

项目采用统一命名，代码、文档、命令行、部署产物保持一致。

| 名称 | 定位 | 约定 |
|---|---|---|
| **TradeForge** | 项目 / 系统 / 仓库名 | Go module：`github.com/viviwu/tradeforge`；文档与产品名称统一使用 TradeForge |
| **forge** | 命令行客户端（CLI） | 可执行文件名为 `forge`，源码位于 `cmd/forge`；所有子命令以 `forge` 开头 |
| **TradeApi** | Go 客户端 SDK | Go 包名 `tradeapi`，路径 `pkg/tradeapi`；对外称 TradeApi，代码中写作 `tradeapi.Client` |
| **Forge Console** | Web 管理端 | 由 grpc-gateway 生成的 REST API + 静态页面，部署产物与文档统一称 Forge Console |
| **tradeforge-server** | 服务端进程 | 部署单元，源码位于 `cmd/server`；一期为单进程模块化 |

其他术语：

| 术语 | 含义 |
|---|---|
| Gateway | 协议接入层：gRPC、REST、鉴权、限流、订阅管理 |
| OMS | 订单管理：订单生命周期、持仓、资金、策略状态 |
| Matching Engine | 撮合引擎：维护订单簿，产出成交事件 |
| Market Data Provider | 模拟行情提供者：生成/回放 tick 与盘口 |
| Event Bus | 进程内事件总线，接口可按 NATS/Redis Streams 替换 |
| Account | 交易账户，是订单与持仓的所有者 |
| Seq | 事件序列号，全局或按流单调递增 |

命名约束：

- proto 包名统一为 `tradeforge.v1`，`go_package` 为 `github.com/viviwu/tradeforge/gen/tradeforge/v1;tradeforgev1`。
- CLI 用户配置文件目录约定为 `~/.config/forge/`，token 缓存文件权限 `0600`。
- 日志、指标、trace 的 service name 统一为 `tradeforge`。
- 文档中不再使用 `trade-system`、`trade` 等旧名。

---

## 3. 产品范围与角色

### 3.1 系统边界

包含：

- **tradeforge-server**：数据库、模拟交易通道、模拟撮合引擎、模拟行情 Provider、OMS（订单/持仓/策略状态管理）、对外 Gateway（gRPC + Protobuf）、Forge Console 的 REST API。
- **TradeApi**：Go 客户端 SDK，封装 Gateway 的 gRPC 调用，提供重连、Token 刷新、订阅管理、幂等下单等能力。
- **forge CLI**：基于 TradeApi 的命令行客户端，支持登录、下单、撤单、查询、订阅。
- **Forge Console**：通过 grpc-gateway 生成的 REST API，提供订单、持仓、策略状态、行情查看和人工干预。

不包含（第一版）：

- 真实交易所对接、真实资金清算、复杂风控、多租户计费、分布式多机房。
- 策略回测框架和策略执行引擎；仅提供“策略状态管理”轻量服务，供订阅练习。

### 3.2 角色

| 角色 | 说明 |
|---|---|
| CLI 用户 | 通过 TradeApi 登录、交易、查询、订阅 |
| Web 管理员 | 通过 Forge Console 查看全局订单/持仓/策略状态，执行撤单、暂停策略等管理操作 |
| 模拟行情 Provider | 系统内部组件，生成 tick/盘口/K 线 |
| 撮合引擎 | 系统内部组件，维护订单簿并产生成交 |
| OMS | 系统内部组件，管理订单生命周期、持仓、资金、策略状态 |

### 3.3 组件依赖方向

```
forge CLI ──▶ TradeApi ──▶ Gateway ──▶ OMS / Matching / MarketData
Forge Console ──(REST)────▶ Gateway
```

依赖约束：

- CLI 只依赖 TradeApi，不直接依赖生成的 gRPC stub。
- Gateway 只做协议转换、鉴权、限流、订阅管理，不写业务状态。
- 撮合引擎只产事件，不直接写 OMS 数据库。
- OMS 是订单与持仓状态的唯一写入方。

---

## 4. 功能需求

### 4.1 认证与会话（FR-AUTH）

| 编号 | 需求 | 验收标准 |
|---|---|---|
| AUTH-1 | CLI 用户名密码登录 | 返回 access token（短期）和 refresh token（长期）；密码使用 Argon2id/bcrypt 哈希存储 |
| AUTH-2 | Token 刷新与登出 | access token 过期前可刷新；登出后 refresh token 失效 |
| AUTH-3 | gRPC 鉴权拦截器 | 除 Login/Refresh 外所有 RPC 必须携带 metadata token；无 token 返回 `Unauthenticated` |
| AUTH-4 | 高并发登录 | 1000 并发登录，P99 < 200ms，无错误率突增；登录限流防暴力破解 |
| AUTH-5 | Forge Console RBAC | 至少 `admin`、`viewer` 两种角色；管理操作仅 admin |

### 4.2 交易通道与下单（FR-ORDER）

| 编号 | 需求 | 验收标准 |
|---|---|---|
| ORDER-1 | 提交限价单/市价单 | `forge` 可提交 `LIMIT`、`MARKET`；返回 `client_order_id`、`order_id`、`ACCEPTED/REJECTED` |
| ORDER-2 | 撤单 | 支持按 `order_id` 或 `client_order_id` 撤单；返回撤单结果 |
| ORDER-3 | 幂等下单 | 同一账户下 `client_order_id` 重复提交返回首次结果，不重复撮合 |
| ORDER-4 | 异步成交回报 | 下单 ACK 与成交回报分离；成交通过 ExecutionReport stream 推送 |
| ORDER-5 | 订单状态机 | `PENDING_NEW → ACCEPTED → PARTIALLY_FILLED → FILLED`，或 `CANCELED/REJECTED`；非法迁移拒绝 |
| ORDER-6 | 高并发下单 | 练习目标 500 下单 TPS，撮合 P99 < 50ms（单机模拟） |

订单状态迁移约束：

| 当前状态 | 允许迁移到 | 说明 |
|---|---|---|
| `PENDING_NEW` | `ACCEPTED` / `REJECTED` | 入库后等待撮合确认 |
| `ACCEPTED` | `PARTIALLY_FILLED` / `FILLED` / `CANCELED` | 已进入订单簿 |
| `PARTIALLY_FILLED` | `PARTIALLY_FILLED` / `FILLED` / `CANCELED` | 可继续成交或撤余量 |
| `FILLED` / `CANCELED` / `REJECTED` | 无 | 终态，不可再迁移 |

### 4.3 查询（FR-QUERY）

| 编号 | 需求 | 验收标准 |
|---|---|---|
| QUERY-1 | 查询订单 | 按订单号、client_order_id、状态、时间范围分页查询 |
| QUERY-2 | 查询成交 | 按订单/账户/时间分页查询 |
| QUERY-3 | 查询持仓与资金 | 返回持仓数量、可用/冻结资金、均价、浮动盈亏（模拟行情估值） |
| QUERY-4 | 查询策略状态 | 返回策略列表及状态 `CREATED/RUNNING/PAUSED/STOPPED/ERROR` |
| QUERY-5 | 查询性能 | 练习目标 2000 QPS，P99 < 100ms；热点查询可加短 TTL 缓存 |

### 4.4 订阅（FR-STREAM）

| 编号 | 需求 | 验收标准 |
|---|---|---|
| STREAM-1 | 订阅订单/成交状态 | `forge` 通过 gRPC server-streaming 订阅本账户订单和成交事件 |
| STREAM-2 | 订阅策略状态变化 | 订阅策略状态变更事件，支持按策略 ID 过滤 |
| STREAM-3 | 订阅行情 | 订阅指定 symbol 的 tick/盘口；支持多 symbol 多路复用 |
| STREAM-4 | 断线重连与续订 | TradeApi 自动重连并按本地订阅表恢复订阅；支持 `from_seq` 补发最近 N 条（有界） |
| STREAM-5 | 背压与慢消费者 | 每订阅者独立缓冲；缓冲满时按策略丢弃行情快照并发送 `SLOW_CONSUMER` 警告，订单/成交事件不可静默丢弃 |
| STREAM-6 | 高并发订阅 | 练习目标 2000 个并发 stream 连接，行情推送 20k msg/s（过滤前），P99 推送延迟 < 100ms |

### 4.5 模拟行情 Provider（FR-MD）

| 编号 | 需求 | 验收标准 |
|---|---|---|
| MD-1 | 生成 tick | 按 symbol 定时生成价格（随机游走 + 波动率），带 `seq` 和时间戳 |
| MD-2 | 生成盘口快照 | 至少 L1 买卖价量；可扩展 L2 |
| MD-3 | 历史回放 | 支持从文件/DB 回放历史 tick，用于模拟和测试 |
| MD-4 | 可替换接口 | 定义 `MarketDataProvider` 接口，模拟实现与真实适配器可互换 |

### 4.6 OMS 与策略状态（FR-OMS）

| 编号 | 需求 | 验收标准 |
|---|---|---|
| OMS-1 | 订单生命周期管理 | 持久化订单及状态迁移；每个状态变更有事件和序列号 |
| OMS-2 | 持仓投影 | 根据成交事件更新持仓、均价、可用资金；按账户串行化 |
| OMS-3 | 幂等消费 | 成交事件按 `trade_seq` 幂等消费，重复事件不重复更新持仓 |
| OMS-4 | 对账 | 启动时从 DB/事件日志重建；提供定期对账任务，比较订单累计成交与持仓变化 |
| OMS-5 | 策略状态管理 | 策略注册、启动、暂停、停止、错误状态；状态变化发布事件 |
| OMS-6 | Web 管理干预 | 管理员可撤单、暂停/停止策略，操作写审计日志 |

### 4.7 Forge Console（FR-WEB）

| 编号 | 需求 | 验收标准 |
|---|---|---|
| WEB-1 | REST API | 由同一份 `.proto` 经 grpc-gateway 生成；提供登录、订单、持仓、策略、行情查询 |
| WEB-2 | 简单页面 | 订单列表、持仓视图、策略状态、手动撤单/暂停按钮 |
| WEB-3 | 审计 | 管理操作记录操作人、时间、参数、结果 |

### 4.8 forge CLI（FR-CLI）

| 命令示例 | 功能 |
|---|---|
| `forge login --user alice` | 登录并缓存 token |
| `forge order submit --symbol AAPL --side BUY --type LIMIT --price 100 --qty 10` | 下单 |
| `forge order cancel --id <order_id>` | 撤单 |
| `forge query orders --status FILLED --page 1` | 查询订单 |
| `forge query positions` | 查询持仓 |
| `forge sub orders` | 订阅订单/成交 |
| `forge sub strategy --id s1` | 订阅策略状态 |
| `forge sub market --symbol AAPL` | 订阅行情 |
| `forge stress login --n 1000 --concurrency 100` | 内置压测辅助命令 |

CLI 使用 Cobra；TradeApi 提供 Go SDK，CLI 不直接依赖生成的 gRPC stub。输出支持 `--output table|json`，便于脚本化与压测结果解析。

---

## 5. 非功能需求（NFR）

| 类别 | 目标（练习目标） | 说明 |
|---|---|---|
| 并发连接 | 2000 个 CLI/gRPC stream 连接 | 单机；可水平扩展 Gateway |
| 登录 | 1000 并发，P99 < 200ms | 限流、密码哈希成本可调 |
| 下单 | 500 TPS，撮合 P99 < 50ms | 单撮合 goroutine，避免锁竞争 |
| 查询 | 2000 QPS，P99 < 100ms | 分页、索引、必要缓存 |
| 行情推送 | 20k msg/s 过滤前，P99 < 100ms | 每订阅者缓冲 + 背压 |
| 可靠性 | 进程崩溃后可恢复订单/持仓 | DB 持久化 + 事件重放/对账 |
| 一致性 | 成交与持仓最终一致，可对账 | 幂等消费 + 序列号 + 对账任务 |
| 安全 | TLS（生产）、Token、RBAC、审计 | 练习环境可自签证书 |
| 可观测性 | Prometheus metrics、结构化日志、trace | 关键路径打点 |
| 部署 | docker-compose 一键启动 | PostgreSQL + tradeforge-server + Forge Console |

---

## 6. 总体架构

### 6.1 第一版部署形态：单进程模块化

```
┌─────────────────────────────────────────────────────────────┐
│ tradeforge-server 进程                                        │
│                                                             │
│  ┌──────────┐   ┌──────────┐   ┌──────────────┐             │
│  │ Gateway  │   │  OMS     │   │ 撮合引擎      │             │
│  │ gRPC/REST│──▶│ 订单/持仓 │◀─▶│ 单 goroutine  │             │
│  └────┬─────┘   │ 策略状态  │   │ 订单簿        │             │
│       │         └────┬─────┘   └──────┬───────┘             │
│       │              │                │                     │
│       │         ┌────▼────────────────▼────┐                │
│       │         │ 进程内事件总线 pub/sub      │                │
│       │         │ 带 seq、缓冲、背压策略       │                │
│       │         └────▲────────────────▲────┘                │
│       │              │                │                     │
│  ┌────▼─────┐   ┌────┴─────┐   ┌──────┴───────┐             │
│  │ 订阅管理  │   │ 模拟行情   │   │ PostgreSQL   │             │
│  │ Streams  │   │ Provider  │   │ 订单/成交/持仓 │             │
│  └──────────┘   └──────────┘   └──────────────┘             │
└─────────────────────────────────────────────────────────────┘
          ▲ gRPC                         ▲ REST
          │                             │
   ┌──────┴──────┐               ┌──────┴────────┐
   │ TradeApi    │               │ Forge Console │
   │ + forge CLI │               │ grpc-gateway  │
   └─────────────┘               └───────────────┘
```

**原则**：

- 撮合引擎、OMS、行情 Provider、Gateway 作为独立 Go package，边界清晰。
- 第一版通过进程内事件总线通信；接口定义为 `EventBus`，后期可替换为 NATS/Redis Streams。
- 撮合引擎不直接写 OMS 数据库；OMS 消费成交事件更新订单/持仓。
- Gateway 只做协议转换、鉴权、限流、订阅管理，不写业务状态。
- 后续若需拆分：优先拆撮合引擎，再拆 OMS；Gateway 可多实例。

### 6.2 技术选型

| 层 | 选型 | 说明 |
|---|---|---|
| 语言 | Go 1.26+ | 泛型、`log/slog`、context；依赖 grpc-go v1.85 的最低版本要求 |
| RPC | gRPC + Protobuf | IDL-first；server-streaming 做订阅 |
| REST | grpc-gateway | 同一份 proto 生成 REST |
| CLI | Cobra + TradeApi | 不直接调 stub |
| DB | PostgreSQL 16 | 订单、成交、持仓、策略、审计 |
| DB 访问 | `database/sql` + `pgx`；可选 sqlc | 练习建议 sqlc 生成类型安全查询 |
| 迁移 | golang-migrate | 版本化 schema |
| 事件总线 | 进程内 channel pub/sub | 接口可替换 NATS |
| 日志/指标 | `slog` + Prometheus + OpenTelemetry | 关键路径 trace |
| 测试 | `testing` + testify + ghz/k6 | 单元、集成、压测 |
| 部署 | Docker Compose | PostgreSQL + tradeforge-server + Forge Console |

---

## 7. 关键设计

### 7.1 并发模型

1. **撮合引擎单写者**：所有 `SubmitOrder/CancelOrder` 命令进入一个命令 channel，由单个 goroutine 顺序处理订单簿，hot path 无锁。输出成交事件到事件总线。
2. **OMS 按账户串行化**：每账户一个 actor/worker，消费该账户的成交事件，串行更新订单和持仓，避免行锁竞争。
3. **Gateway 无状态**：每个 stream 一个 goroutine，从订阅管理器读取事件并发送；发送使用带缓冲 channel，缓冲满触发背压策略。
4. **行情 Provider 独立 goroutine**：定时生成 tick，发布到总线；不与撮合共享锁。
5. **DB 写入批量/异步**：成交和订单状态更新可先写 WAL/outbox 再异步落库；练习第一版可同步写，但保留 outbox 表设计。

### 7.2 事件模型与序列号

统一事件信封：

```go
type Event struct {
    Seq       uint64    // 全局单调递增，由事件总线分配
    Type      string    // ORDER_ACCEPTED, ORDER_FILLED, TRADE, POSITION_UPDATED, STRATEGY_STATUS, MARKET_TICK
    AccountID string
    Symbol    string
    Ts        time.Time
    Payload   []byte    // JSON/Protobuf
}
```

事件类型：

| 类型 | 产生方 | 消费方 | 可丢弃 |
|---|---|---|---|
| `ORDER_ACCEPTED` / `ORDER_REJECTED` / `ORDER_CANCELED` / `ORDER_FILLED` | 撮合引擎 / OMS | OMS、订阅 | 否 |
| `TRADE` | 撮合引擎 | OMS、订阅 | 否 |
| `POSITION_UPDATED` | OMS | 订阅、Forge Console | 否 |
| `STRATEGY_STATUS` | OMS | 订阅、Forge Console | 否 |
| `MARKET_TICK` / `MARKET_BOOK` | 行情 Provider | 订阅 | 是（慢消费者可丢旧快照） |

规则：

- 每类事件有独立序列号或全局序列号；订阅支持 `from_seq` 有界补发（例如最近 1000 条）。
- 订单/成交事件**不可静默丢弃**；行情事件在慢消费者时允许丢弃旧快照并告警。
- OMS 消费成交事件按 `trade_seq` 幂等：`processed_trade` 表记录已处理序列号。

### 7.3 幂等与一致性

- **下单幂等**：`(account_id, client_order_id)` 唯一索引；重复提交返回原订单。
- **撤单幂等**：已撤/已成交返回对应终态，不报错。
- **成交消费幂等**：OMS 用 `trade_seq` 去重；持仓更新与去重记录在同一 DB 事务。
- **Outbox**：订单状态变更和待发布事件写同一事务的 `outbox` 表，后台投递到事件总线；练习第一版可简化为“先 DB 后事件 + 对账”，但需记录风险。
- **对账任务**：定期比较订单累计成交量与成交表汇总、持仓变化与成交汇总；不一致告警并输出修复建议。

### 7.4 高并发登录与限流

- 登录接口按 IP + 用户名令牌桶限流；密码哈希成本参数化（压测时降低，生产提高）。
- access token JWT 短期（15min），refresh token 存 DB 可撤销。
- gRPC interceptor 做鉴权、限流、metrics、panic recover。
- CLI 压测模式复用连接池，避免每次新建 TCP；TradeApi 内置连接池和重连退避。

### 7.5 订阅与背压

- 订阅管理器维护 `subscriptionID -> filters + sendCh`。
- 行情订阅：每订阅者缓冲 256 条；满时丢弃最旧行情并发送 `SLOW_CONSUMER` 控制消息。
- 订单/成交订阅：缓冲 1024 条；满时暂停从总线读取（背压到总线），不丢弃。
- 第一版**每个订阅一个 server-stream**，实现简单；后续合并为双向流多路复用。

### 7.6 安全

- 练习环境可 HTTP/明文 gRPC；生产启用 TLS/mTLS。
- Forge Console 使用 JWT + RBAC；管理操作审计表。
- 输入校验：数量、价格、symbol 白名单；防负数量、超限。
- 日志脱敏：不记录密码、完整 token。

### 7.7 配置与运行

| 配置项 | 说明 | 默认 |
|---|---|---|
| `TRADEFORGE_GRPC_ADDR` | gRPC 监听地址 | `:9090` |
| `TRADEFORGE_HTTP_ADDR` | REST / Forge Console 地址 | `:8080` |
| `TRADEFORGE_DB_DSN` | PostgreSQL 连接串 | 由 compose 注入 |
| `TRADEFORGE_JWT_SECRET` | JWT 签名密钥 | 仅开发环境默认值 |
| `TRADEFORGE_LOG_LEVEL` | 日志级别 | `info` |
| `TRADEFORGE_MD_INTERVAL` | 行情生成间隔 | `100ms` |

配置优先级：命令行参数 > 环境变量 > 配置文件 > 默认值。禁止在仓库中提交真实密钥。

### 7.8 可观测性

- **Metrics**：登录耗时/失败数、下单 TPS、撮合延迟、订单状态分布、订阅数与慢消费者数、事件总线积压。
- **Logs**：结构化 `slog`，关键字段 `request_id`、`account_id`、`order_id`、`seq`。
- **Tracing**：从 gRPC 入口贯穿到事件消费，至少覆盖下单与成交主链路。

---

## 8. 接口契约草案（Protobuf）

```proto
syntax = "proto3";
package tradeforge.v1;
option go_package = "github.com/viviwu/tradeforge/gen/tradeforge/v1;tradeforgev1";

service AuthService {
  rpc Login(LoginRequest) returns (LoginResponse);
  rpc Refresh(RefreshRequest) returns (LoginResponse);
  rpc Logout(LogoutRequest) returns (Empty);
}

service TradeService {
  rpc SubmitOrder(SubmitOrderRequest) returns (SubmitOrderResponse);
  rpc CancelOrder(CancelOrderRequest) returns (CancelOrderResponse);
  rpc GetOrders(GetOrdersRequest) returns (GetOrdersResponse);
  rpc GetTrades(GetTradesRequest) returns (GetTradesResponse);
  rpc GetPositions(GetPositionsRequest) returns (GetPositionsResponse);
  rpc GetStrategies(GetStrategiesRequest) returns (GetStrategiesResponse);
}

service StreamService {
  rpc SubscribeOrders(SubscribeOrdersRequest) returns (stream OrderEvent);
  rpc SubscribeStrategies(SubscribeStrategiesRequest) returns (stream StrategyEvent);
  rpc SubscribeMarketData(SubscribeMarketDataRequest) returns (stream MarketEvent);
}

message SubmitOrderRequest {
  string request_id = 1;      // 链路追踪
  string client_order_id = 2; // 幂等键
  string symbol = 3;
  Side side = 4;
  OrderType type = 5;
  int64 price = 6;            // 整数最小单位，避免浮点
  int64 quantity = 7;
}

message SubmitOrderResponse {
  string order_id = 1;
  string client_order_id = 2;
  OrderStatus status = 3;
  string reject_reason = 4;
}

message OrderEvent {
  uint64 seq = 1;
  string order_id = 2;
  string client_order_id = 3;
  OrderStatus status = 4;
  int64 filled_qty = 5;
  int64 avg_price = 6;
  string reason = 7;
  int64 ts_unix_nano = 8;
}

message MarketEvent {
  uint64 seq = 1;
  string symbol = 2;
  int64 bid_price = 3;
  int64 bid_qty = 4;
  int64 ask_price = 5;
  int64 ask_qty = 6;
  int64 last_price = 7;
  int64 ts_unix_nano = 8;
}
```

约定：

- 金额/价格用 `int64` 最小单位（如分），禁止 float。
- 所有请求带 `request_id` 用于日志追踪。
- 错误用 gRPC status code + `google.rpc.ErrorInfo` 扩展业务码。
- 分页统一使用 `page_size` + `page_token`，禁止无界查询。

业务错误码（`ErrorInfo.reason`）：

| Reason | gRPC Code | 场景 |
|---|---|---|
| `INVALID_ARGUMENT` | `InvalidArgument` | 参数校验失败 |
| `UNAUTHENTICATED` | `Unauthenticated` | 缺少或无效 token |
| `PERMISSION_DENIED` | `PermissionDenied` | RBAC 拒绝 |
| `ORDER_NOT_FOUND` | `NotFound` | 订单不存在 |
| `ORDER_TERMINAL` | `FailedPrecondition` | 对终态订单撤单 |
| `RATE_LIMITED` | `ResourceExhausted` | 触发限流 |
| `SLOW_CONSUMER` | `ResourceExhausted` | 订阅消费过慢 |

---

## 9. 数据模型（核心表）

```sql
-- 用户
users(id, username unique, password_hash, role, created_at)
refresh_tokens(id, user_id, token_hash, expires_at, revoked)

-- 订单
orders(
  id uuid pk, account_id, client_order_id, symbol, side, type,
  price int64, quantity int64, filled_qty int64, avg_price int64,
  status, reject_reason, created_at, updated_at,
  unique(account_id, client_order_id)
)

-- 成交
trades(
  id uuid pk, trade_seq bigint unique, order_id, account_id,
  symbol, side, price int64, quantity int64, created_at
)

-- 持仓
positions(
  account_id, symbol, quantity int64, avg_price int64,
  frozen_qty int64, updated_at,
  primary key(account_id, symbol)
)

-- 资金账户（简化）
accounts(account_id pk, available_cash int64, frozen_cash int64, updated_at)

-- 策略
strategies(id, account_id, name, status, params jsonb, updated_at)

-- 幂等消费
processed_trades(trade_seq bigint pk, processed_at)

-- Outbox（可选但建议）
outbox(id bigserial pk, aggregate_type, aggregate_id, event_type, payload jsonb,
       published bool, created_at)

-- 审计
audit_logs(id, actor, action, target, params jsonb, result, created_at)
```

索引：`orders(account_id, status, created_at)`、`trades(account_id, created_at)`、`trades(order_id)`。

---

## 10. 代码目录

```
tradeforge/
  api/proto/tradeforge/v1/*.proto
  cmd/server/main.go           # tradeforge-server
  cmd/forge/main.go            # forge CLI
  internal/gateway/            # gRPC/REST、interceptor、订阅管理
  internal/auth/
  internal/oms/                # 订单、持仓、策略、对账
  internal/matching/           # 撮合引擎
  internal/marketdata/         # 模拟行情 Provider、回放
  internal/eventbus/           # 进程内 pub/sub 接口与实现
  internal/store/              # pgx/sqlc、迁移
  internal/observability/
  pkg/tradeapi/                # TradeApi SDK：重连、token、订阅
  web/                         # Forge Console 静态页/简单前端
  migrations/
  deploy/docker-compose.yml
  tests/                       # 集成、压测脚本
```

---

## 11. 测试与验收

### 11.1 测试分层

- **单元测试**：撮合价格-时间优先、订单状态机、幂等、持仓均价计算。
- **集成测试**：Testcontainers 启动 PostgreSQL；CLI→Gateway→OMS→撮合→事件→订阅全链路。
- **压测**：
  - 登录：`ghz` 或 `forge stress login`，1000 并发。
  - 下单：500 TPS 持续 5 分钟，检查错误率、P99、撮合正确性。
  - 订阅：2000 stream，行情 20k msg/s，检查慢消费者行为。
- **故障测试**：杀进程后重启，订单/持仓可恢复；事件重复投递不重复持仓。
- **对账测试**：人为制造不一致，对账任务能发现。

### 11.2 阶段验收门禁

每阶段必须：

1. `go test ./...` 通过；
2. 关键路径有 metrics 和日志；
3. 集成测试覆盖该阶段主流程；
4. 更新 README 和 API 文档；
5. 压测结果记录在 `docs/benchmarks.md`；
6. 更新 `dev_ledger.md` 的里程碑与变更日志。

---

## 12. 分阶段开发计划

| 阶段 | 周期建议 | 交付物 | 验收 |
|---|---|---|---|
| **P0 骨架** | 1 周 | Go module、proto、gRPC server、Auth、PostgreSQL 迁移、docker-compose | `forge login` 成功；无 token 被拒 |
| **P1 交易主链路** | 2 周 | 撮合引擎（单 goroutine）、OMS 订单状态、Submit/Cancel、订单查询 | `forge order submit` → 撮合 → 成交 → 订单 FILLED；幂等重复下单不重复成交 |
| **P2 持仓与订阅** | 2 周 | 持仓投影、成交事件、订单/策略订阅、TradeApi 重连 | `forge sub orders` 实时收到事件；断线重连恢复订阅；持仓与成交对账一致 |
| **P3 行情与 Forge Console** | 2 周 | 模拟行情 Provider、行情订阅、grpc-gateway、简单 Web 页面 | `forge sub market` 收到行情；Forge Console 查看订单/持仓/策略并撤单 |
| **P4 压测与加固** | 1-2 周 | 限流、背压、metrics、压测脚本、对账任务、故障恢复 | 达到 NFR 练习目标；杀进程重启数据一致 |
| **P5 可选扩展** | 持续 | 撮合引擎拆进程、NATS 事件总线、L2 行情、策略执行沙箱 | 按兴趣选择 |

**建议**：P0-P2 不要引入 Forge Console 和复杂行情；先把“下单→成交→持仓→订阅”事件链路跑通，这是系统正确性的核心。

---

## 13. 主要风险与对策

| 风险 | 对策 |
|---|---|
| 事件与 DB 不一致 | 幂等消费 + 序列号 + outbox/对账；第一版至少实现对账 |
| 慢消费者拖垮 Gateway | 每订阅缓冲 + 行情丢弃策略 + 背压指标 |
| 撮合与 OMS 状态漂移 | 撮合只产事件；OMS 唯一写订单/持仓；定期对账 |
| 高并发登录压测失真 | 连接复用、哈希成本可调、限流参数化 |
| 过早微服务化 | 第一版单进程模块化，接口预留拆分点 |
| 金额浮点误差 | 全部整数最小单位；禁止 float 参与计价 |
| 命名漂移 | CLI、SDK、Web、模块路径以第 2 节为准，评审时检查 |

---

## 14. 开发练习建议

1. **先写 proto 和状态机测试**，再写实现；订单状态非法迁移必须单测覆盖。
2. **撮合引擎第一版只支持限价单 + GTC**，跑通后再加市价、IOC/FOK。
3. **事件总线先进程内实现**，但接口按“可替换 NATS”设计：`Publish`、`Subscribe(filter, fromSeq)`。
4. **CLI 压测命令内置**，方便每阶段回归 NFR。
5. **每个阶段写一篇短设计记录**（决策、压测数据、已知问题），这比代码更能体现练习价值。

---

本文档是 TradeForge 的开发路线图。下一步应拆成 **P0/P1 详细任务清单（含完整 proto、迁移文件、撮合接口与单测用例列表）**，并在 `dev_ledger.md` 中登记对应里程碑。
