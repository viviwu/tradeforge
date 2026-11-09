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
| ADR-004 | 券商渠道网关化（线路 1）与 BrokerGateway 统一契约 | 已接受 | 2026-11-09 |

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

### ADR-004 券商渠道网关化（线路 1）与 BrokerGateway 统一契约

- **背景**：真实券商交易渠道 API/SDK 均为 C++（Linux），渠道核心主体语言沿用 Go；线路 3（cgo 同进程混编）会把 Go runtime 与 C++ 回调线程、构建链绑死，线路 2（全 C++）则推翻主体语言选型。撮合系统定位为「委托→订单回报」链路验证托底件，不应因券商差异改核心。
- **决策**：
  - 每个券商 SDK 封装为独立薄适配进程，作为 `BrokerGatewayService` 的 **server**；渠道核心按配置列表作为 client 接入，**不设中央网关聚合服务**，跨券商路由属渠道核心职责（将来可选演进）。
  - 第一个模拟实现 `broker-stub` 与真网关同构：下游用 `pkg/tradeapi` 顶着 TradeForge server，**不直连进程内 OMS**（撮合簿是 server 内存态）；现有 server / CLI / Forge Console 零改动。
  - 契约通用化：`client_order_id` 幂等主键、受理 ack 与终态回报分离、网关本地 seq + 会话内内存补发、渠道核心重启靠查询补齐状态；单券商怪癖进 `ext_json` 并登记差异表。
  - 跨进程持久化补发、多客户号映射、C++ 网关模板列为将来可选；第一个真实券商接入时再启动（M7 之后，不阻塞 M6）。
- **影响**：契约冻结后接入真实券商只需新增适配进程；渠道核心与 TradeForge server 无需改动。完整语义与差异登记表见 `docs/broker-gateway.md`。
- **状态**：已接受；M6 全链路 `scripts/e2e-broker.sh` 验收通过，见 §2.4。选型对比与语义决策详见 §3.1。

---

## 2. 里程碑与状态

| 编号 | 名称 | 主要内容 | 完成标准 | 估计工期 | 状态 |
|---|---|---|---|---|---|
| M0 | 仓库基线 | 命名体系、设计文档、工程配置、Go module | 文档与配置入库 | - | 已完成 |
| M1 | P0 骨架 | proto、gRPC server、Auth、PostgreSQL 迁移、docker-compose | `forge login` 成功；无 token 被拒 | 1 周 | 已完成 |
| M2 | P1 交易主链路 | 撮合引擎、OMS 订单状态、下单/撤单、订单查询 | 下单→成交→FILLED；幂等不重复成交 | 2 周 | 已完成 |
| M3 | P2 持仓与订阅 | 持仓投影、事件订阅、TradeApi 重连 | 订阅实时；断线恢复；对账一致 | 2 周 | 已完成 |
| M4 | P3 行情与 Forge Console | 行情 Provider、行情订阅、REST、页面 | 行情订阅可用；Console 可查可撤 | 2 周 | 已完成 |
| M5 | P4 压测与加固 | 限流、背压、metrics、对账、故障恢复 | 达到 NFR 目标；重启后数据一致 | 1-2 周 | 已完成（快照决策不做） |
| M6 | 渠道对接（线路1：网关化） | `broker-gateway.v1` 契约 + broker-stub（模拟券商通道）+ 渠道视角 e2e | `scripts/e2e-broker.sh` 全链路 PASS；契约冻结 | 1 周 | 已完成（2026-11-09） |
| M7 | P5 可选扩展（搁置） | 拆分服务、NATS、L2 行情、策略沙箱 | 与"链路验证托底件"定位不符，按需重启 | - | 搁置 |

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

### 2.4 M6 验收记录（2026-11-09）

| 验收项 | 结果 |
|---|---|
| M6.1 契约冻结 | 通过：`api/proto/brokergateway/v1/brokergateway.proto`（提交 a523c77），buf lint/generate 绿，`gen/` 无漂移 |
| M6.2 broker-stub 网关 | 通过：`internal/brokergw`（BrokerGatewayService server + seq ring 补发 + 账号/单位映射 + 成交回报增量翻译）、`cmd/broker-stub`、Dockerfile/compose 新容器（50053/50054）|
| M6.3 渠道视角链路 | 通过：`cmd/bgw-probe` + `scripts/e2e-broker.sh` 三进程编排；Attach→证券档案→挂单 ACCEPTED→对冲 FILLED（双方成交回报）→查询一致→心跳→撤单 CANCELED→seq 连续→旧游标重连补发；独立库多次重复 PASS，CI go job 接入独立 step |
| M6.4 文档与收尾 | 通过：`docs/broker-gateway.md`（契约语义、seq/重连、账号映射、券商差异登记表 D-001）、README 状态/快速开始/配置/路线、本台账与 ADR-004 |
| 门禁：`go test ./... -race` | 通过（含 brokergw 15 个用例：换算/账户校验/seq 补发/窗口过期/慢消费者/成交去重/失败重试/外部订单回查）|
| 门禁：`integration -race` | 通过（oms 6 个用例，本地 PostgreSQL 55432）|
| 门禁：关键路径 metrics + 结构化日志 | 通过：`internal/brokergw/metrics.go` 暴露 `brokergw_*` Prometheus 端点（默认 :50054）与 /healthz + JSON 结构化日志；e2e 脚本校验指标确实有回报计数 |
| 门禁：主流程集成测试 | 通过：网关单测 + 渠道 e2e（与 CLI 链路正交）|
| 门禁：README / API 文档 / 台账 | 通过 |
| 原有 CLI 链路回归 | 通过：`scripts/e2e.sh` 全 PASS（未被 M6 改动破坏）|

---

## 3. 计划与验收

### 3.1 下一步执行顺序（M6：渠道对接）

**背景与定位**（2026-11-06 用户拍板）：
- 撮合系统仅为验证「委托→订单回报」链路的托底件，交易所侧不再深化（快照决策不做）。
- 用户手上的券商交易渠道 API/SDK 均为 C++（Linux）。架构选定**线路 1：网关化**——每个券商 SDK 封装为独立进程（薄网关），对上游以统一网络契约（gRPC）对接；渠道核心主体语言沿用 Go。
- 方向澄清：**券商适配进程是 BrokerGateway 契约的 server**（对上游渠道核心），对下游（券商柜台）才是 SDK client。无中央网关聚合服务；"跨券商统一接入点"列为将来可选演进。
- 线路 2（全 C++/进程内桥接）、线路 3（cgo 同进程混编）已评估放弃：后者将 Go runtime 与 C++ 回调线程、构建链绑死，维护代价远超收益。

**架构图**：

```
渠道核心（用户主项目，未来）
   │ broker-gateway.v1（gRPC）
broker-stub（模拟券商通道 = 翻译进程）
   │ TradeApi SDK（pkg/tradeapi：登录/刷新/重连/补发全自动）
TradeForge server（现状完全不动）
```

- **关键架构修正**：broker-stub **不直连进程内 OMS**（撮合簿是 server 内存态），而是与真网关同构——上游实现 BrokerGateway server，下游通过 TradeApi SDK 顶着 TradeForge。现有 server/CLI/Console 零改动。

**任务拆解**：

| 步骤 | 内容 | 交付物/验收 |
|---|---|---|
| M6.1 | 契约定义 | `api/proto/brokergateway/v1/brokergateway.proto`：Attach（版本协商+seq 游标）、Heartbeat、SubmitOrder/CancelOrder（幂等 RequestId）、QueryOrders/QueryTrades/QueryAccount、SubscribeReports（stream：ReportEvent{seq, oneof 订单/成交/资金快照, ext_json}）；buf lint/generate 绿 |
| M6.2 | Stub 网关 | `internal/brokergw/service.go`（BrokerGatewayServer 实现，内部持 TradeApi.Client；网关本地 seq + 会话内存 ring 补发）+ `cmd/broker-stub`（env：监听地址/上游地址/上游账号）+ compose 新容器 + 单测（seq 分配/补发/幂等映射）|
| M6.3 | 渠道视角验收链路 | `cmd/bgw-probe`（最小渠道核心客户端：Attach→排单→对冲成交→收 FILLED 订单回报+成交回报推送→撤单）+ `scripts/e2e-broker.sh`（server+stub+probe 三进程编排）+ CI 新增一步（默认接入，本地跑绿后挂钩）；**不并入 scripts/e2e.sh**（两链路正交）|
| M6.4 | 文档收尾 | `docs/broker-gateway.md`（契约语义、seq/重连规则、账号映射说明、券商差异登记表首条）+ README 快速开始 + 本台账验收记录 |

**已确认的语义决策**（用户 2026-11-06 未提异议，按默认执行）：
1. **seq 语义**：网关本地 seq + 会话内内存补发；渠道核心重启后靠查询补状态。跨进程重启持久化补发列为将来可选项。
2. **账号映射**：stub 以单一配置账号（默认 alice）连上游，所有委托归属该柜台账号；多客户号映射等真实券商阶段再做。
3. **差异逃生舱**：`ext`（JSON 字符串）吸收单券商怪癖；差异登记表随真券商接入扩展。
4. C++ 生成与 CMake 网关模板在第一个真实券商接入时启动（M7 之后，不阻塞 M6）。

**执行结果（2026-11-09）**：M6.1–M6.4 全部完成，`broker-gateway.v1` 契约冻结，验收记录见 §2.4，线路 1 决策由 ADR-004 固化。契约语义、seq/重连规则、账号映射与差异登记表以 `docs/broker-gateway.md` 为准。

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
| R-002 | 生成代码入库后与 proto 漂移 | 编译结果与契约不一致 | CI proto job 执行 `buf lint/generate` 后 `git diff --exit-code gen/` | 已解决 |
| R-003 | 事件与 DB 不一致 | 订单/持仓状态错误 | 幂等消费 + 序列号 + 对账 | 设计已覆盖 |
| R-004 | 慢消费者拖垮 Gateway | 推送延迟升高 | 独立缓冲 + 行情丢弃 + 背压指标 | 设计已覆盖 |
| R-005 | 金额浮点误差 | 成交金额错误 | 全部使用 int64 最小单位 | 已约定 |
| R-006 | 券商 SDK 全为 C++（Linux），渠道核心沿用 Go | 跨语言/跨进程对接复杂度 | 线路 1 网关化：每个券商一个薄适配进程做 BrokerGateway 契约的 server，上游对接面全部同一契约；TradeForge 以 broker-stub 扮演第一个模拟券商 | 已缓解：线路 1 落地、M6 验收通过；真券商 C++ 网关模板待首个柜台接入 |
| R-007 | 各券商回报语义/字段参差 | 契约被单券商怪癖污染 | 主契约保持通用，`ext`（JSON）逃生舱 + 差异登记表管控 | 设计已覆盖；差异登记表首条 D-001 已建立（docs/broker-gateway.md） |

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
| 2026-11-06 | 建立 GitHub Actions CI（proto 漂移检测 + build/vet/race 单测/集成测试），R-002 关闭；漂移检测经故意污染验证有效 |
| 2026-11-06 | OMS 锁按 symbol 分片（ADR-003 预留演进）：4 标的成交 1172 rps（单标的 ×2.25）；kill -9 演练通过，重启 Recover + 挂单簿重建验证 |
| 2026-11-06 | 市价买预算冻结：按最优卖价估算冻结额（存于订单 price），settle/release 与限价单统一口径，防止透支；空盘口直接拒绝 |
| 2026-11-06 | CLI 新增 `forge stress order`；修复 CLI 枚举显示（int32 enum 误用 string() 转换，改用 .String()） |
| 2026-11-06 | 按账号限流上线：下单/撤单（默认 50 rps/burst 100）、查询（200/400）令牌桶 + 每账号订阅并发上限（16），超配 429 并计入 `tradeforge_rate_limited_total`；验证 300 单并发压得 193 拒绝，压测配置可放宽 |
| 2026-11-06 | 联调便利性：`scripts/e2e.sh` 一条命令全链路自检（登录→挂单→self-cross 成交→订单/成交/订阅回报→撤单），并接入 CI go job；项目定位明确为"渠道侧链路验证托底" |
| 2026-11-06 | 种子账号初始库存 `TRADEFORGE_INITIAL_POSITION`（默认 10000 股/标的），迁移 0002 `positions.seed_qty` 作为对账基线（实际=基线+成交净额），否则 seeded 库存会被对账误报漂移 |
| 环境备注 | 本机 shell 环境变量不跨命令保留：每次 bash 需显式 `export GOPROXY=https://goproxy.cn,direct GOMODCACHE=/tmp/tf-gomod GOPATH=/tmp/tf-gopath GOCACHE=/tmp/tf-gocache`，否则 go 命令走 proxy.golang.org 超时 |
| 2026-11-06 | M5 收官：P4 状态改为"已完成（快照决策不做）"；快照列入决策不做 |
| 2026-11-06 | 渠道对接选型拍板：线路 1 网关化（券商 C++ SDK 各封装为独立 BrokerGateway server 进程，渠道核心 Go，上游统一契约）；broker-stub 与真网关同构（报 TradeApi SDK 连 TradeForge，不直连 OMS）；三方案对比与语义决策记入 §3.1；新增 R-006/R-007；M6 启动，M7（P5 可选扩展）搁置 |
| 2026-11-09 | M6.1 契约入库（补记提交 a523c77）：新增 `api/proto/brokergateway/v1/brokergateway.proto` 及生成代码，Attach/Heartbeat、下单/撤单、三类查询、证券档案与回报流齐备，buf lint/generate 绿 |
| 2026-11-09 | M6.2 落地 broker-stub：`internal/brokergw`（BrokerGatewayService server、会话内 seq ring 补发、账号/价格单位映射、成交回报增量翻译）、`cmd/broker-stub`、Dockerfile/compose 新容器（50053 gRPC / 50054 metrics）|
| 2026-11-09 | 修复半成品缺陷：回报流单测数据竞争（fakeStream 加锁）、未初始化 `lookd` 导致非本进程订单回报 panic、成交去重集合 nil map、查询失败仍推进累计成交量水位导致漏发成交；新增外部订单回查与失败重试用例，全量 `go test -race` 转绿 |
| 2026-11-09 | M6.3 渠道视角验收：`cmd/bgw-probe` 严格断言 ack/终态回报分离、双方成交回报、seq 连续与旧游标重连补发；`scripts/e2e-broker.sh` 三进程编排（独立库/端口）本地多次 PASS；CI go job 新增独立 step；原 CLI 链路 `scripts/e2e.sh` 回归仍 PASS |
| 2026-11-09 | 网关可观测性补齐：`internal/brokergw/metrics.go` 暴露 `brokergw_*` Prometheus 指标与 /healthz，e2e 校验回报计数存在，满足阶段验收门禁 |
| 2026-11-09 | M6.4 文档收尾：新增 `docs/broker-gateway.md`（契约语义、seq/重连规则、账号映射、券商差异登记表 D-001/D-002 模板），README 状态/快速开始/配置/路线同步；ADR-004 固化线路 1；M6 标记已完成、契约冻结，R-006/R-007 状态更新 |
