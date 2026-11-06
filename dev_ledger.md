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
| ADR-003 | 撮合结果串行落库，启动时恢复活动订单 | 已接受 | 2026-11-05 |

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
- **状态**：已接受，`gen/` 已生成并入库；插件版本固定为 protoc-gen-go v1.36.12、protoc-gen-go-grpc v1.6.2、grpc-gateway/openapiv2 v2.33.0。

### ADR-003 撮合结果串行落库，启动时恢复活动订单

- **背景**：撮合引擎单 goroutine 顺序产生结果，但若各请求并发写库，后产生的成交可能先提交，覆盖同一 maker 订单的累计成交量；进程重启后内存订单簿也会丢失。
- **决策**：
  - 在 OMS 内用一把互斥锁覆盖「提交/撤单到撮合 + 结果事务落库」区间，使数据库提交顺序与撮合顺序一致；不同标的暂不并行。
  - 启动时读取 `PENDING_NEW`、`ACCEPTED`、`PARTIALLY_FILLED` 订单；GTC 限价单重新挂回订单簿，`PENDING_NEW` 视为已接受；非 GTC 单撤销并释放冻结。
  - `PENDING_NEW` 崩溃恢复不重放撮合，只恢复挂单，避免重复成交。
- **权衡**：全局串行降低多标的并行度，但换来资金/持仓强一致；后续可按 symbol 分片加锁。
- **状态**：已实现并有集成测试覆盖。

---

## 2. 里程碑与状态

| 编号 | 名称 | 主要内容 | 完成标准 | 估计工期 | 状态 |
|---|---|---|---|---|---|
| M0 | 仓库基线 | 命名体系、设计文档、工程配置、Go module | 文档与配置入库 | - | 已完成 |
| M1 | P0 骨架 | proto、gRPC server、Auth、PostgreSQL 迁移、docker-compose | `forge login` 成功；无 token 被拒 | 1 周 | 已完成 |
| M2 | P1 交易主链路 | 撮合引擎、OMS 订单状态、下单/撤单、订单查询 | 下单→成交→FILLED；幂等不重复成交 | 2 周 | 已完成 |
| M3 | P2 持仓与订阅 | 持仓投影、事件订阅、TradeApi 重连 | 订阅实时；断线恢复；对账一致 | 2 周 | 已完成 |
| M4 | P3 行情与 Forge Console | 行情 Provider、行情订阅、REST、页面 | 行情订阅可用；Console 可查可撤 | 2 周 | 已完成 |
| M5 | P4 压测与加固 | 限流、背压、metrics、对账、故障恢复 | 达到 NFR 目标；重启后数据一致 | 1-2 周 | 进行中 |
| M6 | P5 可选扩展 | 拆分服务、NATS、L2 行情、策略沙箱 | 按兴趣选择 | 持续 | 未开始 |

### 2.1 M1（P0）任务清单

| 任务 | 交付物 | 状态 |
|---|---|---|
| Go module 与工程配置 | `go.mod`、`.gitignore`、`Makefile` | 已完成 |
| Protobuf 契约 | `api/proto/tradeforge/v1/{common,auth,trade,stream}.proto` | 已完成 |
| buf 配置 | `buf.yaml`、`buf.gen.yaml` | 已完成 |
| 生成 gRPC / gateway / OpenAPI 代码 | `gen/`（`buf.lock` + 固定插件版本） | 已完成 |
| 初始数据库迁移 | `migrations/`（users、refresh_tokens、orders、trades、positions、accounts、strategies） | 已完成 |
| pgx store 与配置加载 | `internal/store/`、`internal/config/` | 已完成 |
| Auth 服务 | `internal/auth/`：bcrypt、JWT、refresh token 轮换 | 已完成 |
| gRPC interceptor | 鉴权、metrics、panic recover、结构化日志 | 已完成 |
| `tradeforge-server` 骨架 | `cmd/server/main.go`、health check、REST、metrics | 已完成 |
| `forge login` | `cmd/forge/main.go`、`pkg/tradeapi` | 已完成 |
| 本地依赖编排 | `deploy/docker-compose.yml`、`Dockerfile` | 已完成 |
| P0 验收 | 登录成功、无 token 被拒、`go test ./...` 通过 | 已完成 |

### 2.2 M2–M4 交付记录

| 模块 | 交付物 |
|---|---|
| 撮合引擎 | `internal/matching/`：价格优先/时间优先、GTC/IOC/FOK、市价单、撤单 |
| OMS | `internal/oms/`：下单幂等、冻结/结算、成交落库、查询、对账 |
| 订阅 | `internal/eventbus/`、`internal/gateway/stream_service.go`：seq、回放、慢消费者处理 |
| SDK | `pkg/tradeapi/`：登录/刷新/登出、交易查询、三类订阅重连与 `from_seq` |
| CLI | `internal/cli/`、`cmd/forge/`：login/order/query/sub/stress |
| 行情与页面 | `internal/marketdata/`、`internal/web/` |
| 部署 | `Dockerfile`、`deploy/docker-compose.yml` |

### 2.3 M0 验收记录

| 验收项 | 结果 |
|---|---|
| 命名体系统一并写入文档 | 通过 |
| 设计文档 v0.2 | 通过 |
| README 与台账建立 | 通过 |
| 清理无关 C++ 配置，更新 Go `.gitignore` | 通过 |

---

## 3. 计划与验收

### 3.1 下一步执行顺序（M5）

1. ~~补充 `docs/benchmarks.md`~~（已完成：登录/挂单/成交三路径基线，见 docs/benchmarks.md）。
2. 视需要按 symbol 分片锁，提升多标的并发度。
3. 建立 CI：`make proto` 后 diff 检查、`go vet`、单元与集成测试。
4. 评估市价单资金校验、盘口持久化与更细粒度限流。

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
| R-001 | Go 官方代理与部分外网在开发机不可达 | 依赖下载/代码生成受阻 | 使用 `GOPROXY=https://goproxy.cn,direct`；buf 及 BSR 已验证可用 | 已解决 |
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
| 2026-11-05 | buf lint 通过并生成 `gen/`（gRPC、gateway、OpenAPI）；固定插件版本；Go 基线调整为 1.26；`go build ./...` 通过；R-001 关闭 |
| 2026-11-05 | 完成 M1–M4 MVP：撮合/OMS、冻结结算、订阅与回放、SDK/CLI、行情与 Console、Docker 部署 |
| 2026-11-05 | ADR-003：撮合结果串行落库；启动恢复活动订单；新增单元测试与 PostgreSQL 集成测试 |
| 2026-11-05 | 修复重复刷新 Token 竞态（原子消费 refresh token）；健康检查与 REST 联调通过 |
| 2026-11-05 | 新增 `docs/benchmarks.md` P4 基线：登录 0 err，挂单 753 rps p50=10.6ms，成交 472 rps p50=34.2ms；串行锁为主要瓶颈 |
