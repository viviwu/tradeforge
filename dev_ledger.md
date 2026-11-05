# 开发台账

本台账记录从 v0.0.1 启动起的架构决策、里程碑、计划、风险与变更。

## 0. 使用约定

- **ADR**（架构决策记录）一经接受不修改内容。若要推翻，新增一条 ADR 并把旧条目的状态改为「被 ADR-XXX 取代」。
- **里程碑**状态取值：`未开始` / `进行中` / `已完成` / `阻塞`。
- **每次完成一个里程碑**，更新状态表并在变更日志中记一行。
- **风险**发生或被消除时更新风险登记表。
- 台账只记录决定与进展，设计细节写在 `PR-TD.doc.md` 或代码注释中。

---

## 1. 架构决策记录（ADR）

| 编号 | 标题 | 状态 | 日期 |
|---|---|---|---|
| ADR-001 | 统一命名体系：TradeForge / forge / TradeApi / Forge Console | 已接受 | 2026-11-05 |
| ADR-002 | Protobuf 契约由 buf v2 管理，生成代码入库 | 已接受 | 2026-11-05 |

### ADR-001 统一命名体系

- **背景**：项目启动后需要统一产品名、CLI、SDK、Web 管理端与代码路径，避免文档和代码中命名漂移。
- **决策**：
  - 项目/系统/仓库名：**TradeForge**，Go module `github.com/viviwu/tradeforge`。
  - CLI：**forge**，可执行文件 `cmd/forge`，配置目录 `~/.config/forge/`。
  - Go SDK：**TradeApi**，包 `pkg/tradeapi`，代码中为 `tradeapi.Client`。
  - Web 管理端：**Forge Console**，REST 由 grpc-gateway 生成。
  - proto 包名：`tradeforge.v1`。
- **影响**：`PR-TD.doc.md`、`README.md`、后续代码与部署产物均以命名表为准；文档中不再出现 `trade-system` 等旧名。

### ADR-002 Protobuf 契约由 buf v2 管理，生成代码入库

- **背景**：需要统一的 proto 目录、lint、破坏性变更检测和可复现的代码生成；同时不希望 `go build` 依赖本地安装 buf。
- **决策**：
  - proto 源文件位于 `api/proto/tradeforge/v1/`，包名 `tradeforge.v1`。
  - 使用 buf v2 管理依赖、lint、breaking 检查与远程插件生成，配置见 `buf.yaml` / `buf.gen.yaml`。
  - 生成代码输出到 `gen/`，并**提交入库**；CI 通过 `make proto` 后检查工作区无差异来防止漂移。
  - Go import 路径统一为 `github.com/viviwu/tradeforge/gen/tradeforge/v1`。
- **影响**：修改 proto 后必须重新生成并提交 `gen/`；本地无 buf 时无法完成 codegen。
- **状态**：已接受，`gen/` 生成待执行（见风险 R-001）。

---

## 2. 里程碑与状态

| 编号 | 名称 | 主要内容 | 完成标准 | 估计工期 | 状态 |
|---|---|---|---|---|---|
| M0 | 仓库基线 | 命名体系、设计文档、工程配置、Go module | 文档与配置入库 | - | 已完成 |
| M1 | P0 骨架 | proto、gRPC server、Auth、PostgreSQL 迁移、docker-compose | `forge login` 成功；无 token 被拒 | 1 周 | 进行中 |
| M2 | P1 交易主链路 | 撮合引擎、OMS 订单状态、下单/撤单、订单查询 | 下单→成交→FILLED；幂等不重复成交 | 2 周 | 未开始 |
| M3 | P2 持仓与订阅 | 持仓投影、事件订阅、TradeApi 重连 | 订阅实时；断线恢复；对账一致 | 2 周 | 未开始 |
| M4 | P3 行情与 Forge Console | 行情 Provider、行情订阅、REST、页面 | 行情订阅可用；Console 可查可撤 | 2 周 | 未开始 |
| M5 | P4 压测与加固 | 限流、背压、metrics、对账、故障恢复 | 达到 NFR 目标；重启后数据一致 | 1-2 周 | 未开始 |
| M6 | P5 可选扩展 | 拆分服务、NATS、L2 行情、策略沙箱 | 按兴趣选择 | 持续 | 未开始 |

### 2.1 M1（P0）任务清单

| 任务 | 交付物 | 状态 |
|---|---|---|
| Go module 与工程配置 | `go.mod`、`.gitignore`、`Makefile` | 已完成 |
| Protobuf 契约 | `api/proto/tradeforge/v1/{common,auth,trade,stream}.proto` | 已完成 |
| buf 配置 | `buf.yaml`、`buf.gen.yaml` | 已完成 |
| 生成 gRPC / gateway / OpenAPI 代码 | `gen/` | 阻塞（本机无 buf、无网络） |
| 初始数据库迁移 | `migrations/`（users、refresh_tokens、orders、trades、positions、accounts、strategies） | 未开始 |
| pgx store 与配置加载 | `internal/store/`、`internal/config/` | 未开始 |
| Auth 服务 | `internal/auth/`：Argon2id/bcrypt、JWT、refresh token | 未开始 |
| gRPC interceptor | 鉴权、限流、metrics、panic recover | 未开始 |
| `tradeforge-server` 骨架 | `cmd/server/main.go`、health check | 未开始 |
| `forge login` | `cmd/forge/main.go`、`pkg/tradeapi` | 未开始 |
| 本地依赖编排 | `deploy/docker-compose.yml` | 未开始 |
| P0 验收 | 登录成功、无 token 被拒、`go test ./...` 通过 | 未开始 |

### 2.2 M0 验收记录

| 验收项 | 结果 |
|---|---|
| 命名体系统一并写入文档 | 通过 |
| 设计文档 v0.2 | 通过 |
| README 与台账建立 | 通过 |
| 清理无关 C++ 配置，更新 Go `.gitignore` | 通过 |

---

## 3. 计划与验收

### 3.1 下一步执行顺序（M1）

1. 恢复网络后安装 buf，执行 `make proto` 并提交 `gen/`。
2. 编写首个迁移：`users`、`refresh_tokens`、`orders`。
3. 实现配置加载与 pgx 连接池。
4. 实现 Auth 服务与 gRPC 鉴权 interceptor。
5. 搭建 `tradeforge-server` 与 `forge login` 最小链路。
6. 添加 docker-compose 与 P0 集成测试，完成 M1 验收。

### 3.2 阶段验收门禁

每个里程碑必须满足：

1. `go test ./...` 通过；
2. 关键路径有 metrics 和结构化日志；
3. 集成测试覆盖该阶段主流程；
4. 更新 README、API 文档和本台账；
5. 压测结果记录到 `docs/benchmarks.md`（M5 起）。

---

## 4. 风险登记

| 编号 | 风险 | 影响 | 对策 | 状态 |
|---|---|---|---|---|
| R-001 | 本地无 buf 且网络不可用，远程插件无法生成代码 | `gen/` 缺失，P0 阻塞 | 网络恢复后执行 `make proto`；或改用本地 protoc 插件 | 开放 |
| R-002 | 生成代码入库后与 proto 漂移 | 编译结果与契约不一致 | CI 执行 `make proto` 并检查 diff | 待建立 CI |
| R-003 | 事件与 DB 不一致 | 订单/持仓状态错误 | 幂等消费 + 序列号 + 对账 | 设计已覆盖 |
| R-004 | 慢消费者拖垮 Gateway | 推送延迟升高 | 独立缓冲 + 行情丢弃 + 背压指标 | 设计已覆盖 |
| R-005 | 金额浮点误差 | 成交金额错误 | 全部使用 int64 最小单位 | 已约定 |

---

## 5. 变更日志

| 日期 | 变更 |
|---|---|
| 2026-11-05 | 初始化 `dev_ledger.md`，建立 ADR、里程碑与风险结构 |
| 2026-11-05 | ADR-001：统一命名体系；设计文档升级 v0.2；新增 README |
| 2026-11-05 | 删除 C++ 专用 `.clang-format` / `.clang-tidy`，`.gitignore` 切换为 Go 项目 |
| 2026-11-05 | 初始化 Go module；新增 `tradeforge.v1` proto 契约与 buf 配置；M0 完成，M1 启动 |
