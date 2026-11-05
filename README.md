# TradeForge

TradeForge 是一个用 Go 编写的**模拟交易系统**，用于练习高并发、gRPC、事件驱动和交易系统设计。系统支持 CLI 高并发登录、下单、撤单、查询，以及订单/策略状态和行情的实时订阅，并提供 Web 管理端。

> 当前状态：**P0 骨架进行中**，`tradeforge.v1` proto 契约已建立，服务端与 CLI 待实现。需求与技术方案见 [PR-TD.doc.md](./PR-TD.doc.md)，进度与决策见 [dev_ledger.md](./dev_ledger.md)。

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
- **forge**：基于 TradeApi 的 CLI，支持登录、下单、撤单、查询、订阅和内置压测。
- **TradeApi**：Go SDK，负责连接池、Token 刷新、断线重连与订阅恢复。
- **Forge Console**：查看订单、持仓、策略状态和行情，并执行人工干预。

## 核心设计

- **单写者撮合**：所有下单/撤单进入同一命令 channel，由单个 goroutine 顺序处理订单簿。
- **OMS 按账户串行**：每个账户独立 worker 消费成交事件，避免行锁竞争。
- **事件驱动**：统一事件信封带 `seq`，订单/成交事件不可静默丢弃，行情可丢弃旧快照并告警。
- **幂等与对账**：`client_order_id` 唯一索引保证下单幂等，`trade_seq` 保证成交消费幂等，定期对账发现漂移。
- **金额整数化**：价格与金额统一使用 `int64` 最小单位，禁止浮点。

## 功能范围

- 认证：用户名密码登录、Token 刷新/登出、gRPC 鉴权、RBAC。
- 交易：限价/市价下单、撤单、订单状态机、幂等下单、异步成交回报。
- 查询：订单、成交、持仓与资金、策略状态。
- 订阅：订单/成交、策略状态、行情，支持断线重连与有界补发。
- 行情：模拟 tick、L1 盘口、历史回放。
- 管理：Forge Console 查询与人工撤单/暂停策略，操作审计。

## 快速开始

开发环境尚未就绪。P0 阶段完成后将提供：

```bash
docker compose -f deploy/docker-compose.yml up -d
forge login --user alice
forge order submit --symbol AAPL --side BUY --type LIMIT --price 100 --qty 10
forge sub orders
```

## 开发

需要 Go 1.26+、buf 与 Docker（数据库）。首次使用可安装 buf：

```bash
go install github.com/bufbuild/buf/cmd/buf@v1.75.0
make proto    # buf lint + 生成 gRPC/gateway/OpenAPI 代码
make build    # 编译所有 Go 包
make test     # 运行测试
make help     # 查看全部命令
```

## 技术栈

Go 1.26+ · gRPC + Protobuf · grpc-gateway · Cobra · PostgreSQL 16 · pgx/sqlc · golang-migrate · Prometheus · OpenTelemetry · Docker Compose

## 开发路线

| 阶段 | 内容 |
|---|---|
| P0 | 骨架、proto、Auth、数据库迁移、docker-compose |
| P1 | 撮合引擎、OMS 订单状态、下单/撤单、订单查询 |
| P2 | 持仓投影、事件订阅、TradeApi 重连 |
| P3 | 模拟行情、行情订阅、Forge Console |
| P4 | 压测、限流、背压、对账、故障恢复 |
| P5 | 可选：拆分服务、NATS、L2 行情、策略沙箱 |

## 文档

- [PR-TD.doc.md](./PR-TD.doc.md)：产品需求与技术设计
- [dev_ledger.md](./dev_ledger.md)：ADR、里程碑、风险与变更记录
