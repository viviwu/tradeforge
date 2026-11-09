# BrokerGateway 渠道对接契约（M6）

> 状态：**契约冻结（v1）**，第一条实现为 `broker-stub`。本文是渠道核心与券商适配网关之间的对接说明；契约原文见 [`api/proto/brokergateway/v1/brokergateway.proto`](../api/proto/brokergateway/v1/brokergateway.proto)，决策记录见 [`dev_ledger.md`](../dev_ledger.md) §3.1 / ADR-004。

## 1. 定位与拓扑

用户手上的券商交易渠道 API/SDK 均为 C++（Linux），而渠道核心主体语言沿用 Go。架构选定**线路 1：网关化**——每个券商 SDK 各封装为一个独立薄进程，对上游实现同一个 `BrokerGatewayService` gRPC server；对下游才用券商 SDK 做 client。**没有中央网关聚合服务**，跨券商接入点/路由属于渠道核心职责，列为将来可选演进。

```
渠道核心（Go，未来主项目；M6 内由 cmd/bgw-probe 扮演）
   │ broker-gateway.v1（gRPC，渠道核心是 client）
券商适配网关（每个柜台一个进程；M6 内为 cmd/broker-stub）
   │ 券商柜台 SDK（真实阶段为 C++）；M6 内用 TradeApi SDK 顶着模拟交易所
TradeForge server（模拟交易所，现状零改动）
```

关键约束：**适配进程不直连进程内 OMS**（撮合簿是 server 内存态）。stub 与真网关同构，走 `pkg/tradeapi` 登录上游，因此现有 server / CLI / Forge Console 完全不动。线路 2（全 C++）、线路 3（cgo 同进程）已评估放弃。

## 2. 契约速查

| RPC | 语义 |
|---|---|
| `Attach` | 建立会话：版本协商、返回当前回报游标 `seq_cursor` 与资金账号/股东账号清单 |
| `Heartbeat` | 应用层心跳，返回游标与服务器时间，供渠道核心探测积压 |
| `SubmitOrder` / `CancelOrder` | **受理 ack + 状态演练**：调用只返回受理结果，终态必须通过回报推送传递；以 `client_order_id` 为幂等主键 |
| `QueryOrders` / `QueryTrades` / `QueryAccount` | 当日委托/成交/持仓查询，支持 `client_order_id` 过滤与分页 |
| `QuerySecurityInfo` | 证券档案：交易状态、最小变动价位、手数、涨跌停、单笔上限，供下单前预校验 |
| `SubscribeReports` | 服务端流式推送 `ReportEvent{seq, oneof order/trade/account, ext_json}` |

**单位与标识**

- 价格/金额统一 `int64`，`1 unit = 0.0001 元`；模拟所上游价格单位为 `0.01 元`，网关按 ×10 / ÷10 换算，因此 `price_u` 必须是 10 的倍数（细于 0.01 元网格的委托在受理层直接拒绝）。
- `client_order_id` 由渠道核心生成，是契约订单主键；柜台原生键（`exchange_order_id`）由网关维护映射并只读透传。
- 所有消息带 `ext_json`（JSON 字符串）作为**差异逃生舱**：单券商怪癖不得污染主契约，须登记到第 9 节差异表。

**状态机**：`PENDING / UNSEND / PENDING_NEW / ACCEPTED / PARTIALLY_FILLED / FILLED / WAIT_CANCEL / CANCELED / REJECTED`。柜台原状态码由网关映射，无法精确映射时取语义最近状态并在 `reject_reason` / `ext_json` 保留原始描述。stub 只会产出 `PENDING_NEW / ACCEPTED / PARTIALLY_FILLED / FILLED / CANCELED / REJECTED`。

## 3. 会话、seq 与重连规则（核心）

1. **连接后先 `Attach`**，取得 `seq_cursor`；用该游标调用 `SubscribeReports(from_seq = seq_cursor)`。seq 由网关本地分配，从 1 开始严格单调递增，与上游事件的 seq 无对应关系。
2. **补发窗口**：网关在内存中保留最近 N 条事件（`BROKER_STUB_REPORT_RING`，默认 4096）。断线后用已收到的最后一条 seq 作为 `from_seq` 重连，网关先回放 `from_seq+1 .. cursor` 的缓冲事件，再无缝接实时流（回放与实时在同一把锁下原子衔接，不会重叠或漏发）。`from_seq = 0` 表示从当前游标开始只看实时。
3. **错误语义**：
   - `from_seq > cursor` → `InvalidArgument`（游标超前，客户端应重新 `Attach`）；
   - `from_seq` 早于最早缓冲事件 → `ResourceExhausted`（窗口过期）；
   - 单个订阅邮箱溢出（默认 1024 条）→ 网关主动断开并返回 `ResourceExhausted`（慢消费者）。
   以上两种 `ResourceExhausted` 的统一处置：**重新 `Attach` 取新游标 + 用 `QueryOrders` / `QueryTrades` / `QueryAccount` 补齐状态**，再从新游标订阅。
4. **持久化边界（已确认决策）**：seq 只在会话内内存补发，网关重启后清空，渠道核心重启后靠查询补状态；跨进程持久化补发列为将来可选项，不进入 v1。
5. **客户端校验**：seq 必须逐条连续；缺口按第 3 条处理。上游重连补发可能让网关重复生成同一状态的**订单回报**，渠道核心应按 `client_order_id + status` 幂等处理；**成交回报**由网关按 `trade_id` 去重，不会重复。
6. **心跳**：`Heartbeat.seq_cursor` 应单调不回退，可用 `cursor - 已处理 seq` 度量积压。

## 4. 回报翻译（stub 实现要点）

上游 `SubscribeOrders` 只携带累计状态/成交字段，因此网关做三件事：

- **静态字段**：下单时把 `price_u / qty / tif / 创建时间` 记入会话注册表；若订单不是本进程提交的（网关重启、其他客户端共用上游账号），通过 `QueryOrders` 回查一次并缓存，保证订单回报仍是全量视图。
- **成交回报**：仅当累计 `filled_qty` 增长时，按 `order_id` 分页拉当日成交，按 `trade_id` 去重后逐条先于订单回报推送。查询失败**不推进累计成交量水位**，下个上游事件自动重试，避免静默丢成交。
- **映射表**：`TIF DAY → 上游会话默认（unspecified）`；上游 `unspecified → 契约 DAY`；价格 ×10，成交金额 = `price_u × qty`；`BROKER_STUB_*` 的账号/市场字段统一盖章。

## 5. 账号映射

stub 以**单一配置账号**登录上游（默认 `alice/alice123`），所有委托归属该柜台账号：

| 契约字段 | 默认值 | 环境变量 |
|---|---|---|
| `fund_account` | `F001` | `BROKER_STUB_FUND_ACCOUNT` |
| `trade_account` | `A0001` | `BROKER_STUB_TRADE_ACCOUNT` |
| `market` | `SIM` | `BROKER_STUB_MARKET` |
| 上游账号 | `alice` | `BROKER_STUB_USER` / `BROKER_STUB_PASS` |

`SubmitOrder` 的账号不匹配在受理层拒绝；`SubscribeReports` 的 `fund_account` 过滤只允许该账号。`QueryAccount` 返回持仓（含 `available_qty = qty - frozen_qty`），模拟所**不暴露现金余额**，故 `available_cash_u / frozen_cash_u` 为 0，并在 `AccountSnapshot.ext_json` 标注 `cash_unavailable`。多客户号 / 多资金账号映射在真实券商接入阶段扩展。

## 6. 证券档案与可选模拟规则

`QuerySecurityInfo` 返回 `price_tick_u = 100`（0.01 元）、`lot_size = 100`、交易状态。stub 额外提供两组**默认关闭**的模拟开关，用于让渠道核心演练柜台风控：

| 环境变量 | 作用 |
|---|---|
| `BROKER_STUB_HALTED_SYMBOLS` | 逗号分隔标的，返回 `TRADING_STATUS_SUSPENDED` |
| `BROKER_STUB_LIMIT_PCT` | 1..99，按行情最新价计算 `limit_up_u / limit_down_u`；开启时订阅上游行情维护参考价 |

注意：网关受理层只校验账号、价格网格、必填字段；tick 对齐、停牌、涨跌停、手数等预校验由渠道核心依证券档案执行，网关侧另有柜台校验兜底（真实券商阶段）。

## 7. 运行与验收

```bash
# 方式 A：compose 一键起 PostgreSQL + server + broker-stub（网关端口 50053）
docker compose -f deploy/docker-compose.yml up -d --build

# 方式 B：渠道视角全链路自检（脚本自行拉起 server + stub + probe 三进程）
# 需要 PostgreSQL 可达；有 psql 时自动建库，没有时请预先建库
scripts/e2e-broker.sh
```

`scripts/e2e-broker.sh` 与 `scripts/e2e.sh`（Forge CLI 直连链路）**正交**，使用独立端口（19590/18580/15053/15054）；CI 优先使用独立数据库 `tradeforge_bgw`（有 `psql` 时自动创建）。全部可覆盖变量：`BGW_TEST_DB_DSN`、`BGW_TEST_SERVER_GRPC/HTTP`、`BGW_TEST_STUB_GRPC/METRICS`、`BGW_TEST_*_BIN`、`BGW_TEST_BCRYPT_COST`。

网关自带独立 Prometheus 端点 `/metrics`（默认 `:50054`）与 `/healthz`，指标前缀 `brokergw_`：`grpc_requests_total{method,code}`、`grpc_request_duration_seconds`、`reports_published_total{payload}`、`reports_replayed_total`、`report_consumers`、`slow_consumers_total`、`upstream_errors_total`；关键路径同时输出结构化 JSON 日志。`e2e-broker.sh` 会校验指标端点确实有回报计数，避免验收只覆盖功能、不覆盖可观测性。

`cmd/bgw-probe` 的验收断言：

1. `Attach` 返回账号与游标；`QuerySecurityInfo` 返回有效 tick；
2. SELL 挂单在回报流出现 `ACCEPTED`，BUY 对冲后回报流出现 `FILLED`，**双方各收到成交回报**；
3. 按 `client_order_id` 查询订单均 `FILLED`，成交查询非空，账户快照含该标的持仓；
4. `Heartbeat` 游标不回退；撤单在回报流出现 `CANCELED`；
5. 回报 seq 从 `attach_cursor+1` 起严格连续；用旧游标重连能按序回放窗口内事件。

CI（`.github/workflows/ci.yml`）的 go job 已把该链路作为独立 step 接入；`go test ./... -race` 另覆盖网关单测（seq 分配/补发、窗口过期、慢消费者、字段换算、成交去重、查询失败重试、外部订单回查）。

## 8. 环境变量速查（broker-stub）

| 变量 | 默认值 | 说明 |
|---|---|---|
| `BROKER_STUB_LISTEN_ADDR` | `:50053` | 网关 gRPC 监听 |
| `BROKER_STUB_METRICS_ADDR` | `:50054` | Prometheus `/metrics` 与 `/healthz` 监听 |
| `BROKER_STUB_UPSTREAM_ADDR` | `127.0.0.1:50051` | 模拟交易所 gRPC（compose 内为 `tradeforge:9090`） |
| `BROKER_STUB_USER` / `BROKER_STUB_PASS` | `alice` / `alice123` | 上游登录账号 |
| `BROKER_STUB_SYMBOLS` | `AAPL,MSFT,TSLA,NVDA` | 订阅行情的标的（涨跌停模拟用） |
| `BROKER_STUB_REPORT_RING` | `4096` | 会话内回报补发窗口 |

`Attach.server_version` 固定为 `broker-stub/0.1.0`，用于渠道核心做实现版本协商与日志标记。

## 9. 券商差异登记表

> 规则：主契约保持通用；任何单柜台特有字段/状态/行为先落 `ext_json`，并在本表登记后再决定是否需要升版本。真券商接入时每家新增一行，C++ 网关模板在第一个真实柜台接入时启动（M7 之后）。

| 编号 | 柜台 / 网关 | 差异点 | 契约侧处置 | 状态 |
|---|---|---|---|---|
| D-001 | TradeForge 模拟所（broker-stub） | 价格单位 0.01 元 vs 契约 0.0001 元 | 网关 ×10 / ÷10 换算，细网格委托受理层拒绝 | 已实现 |
| D-001 | 同上 | 无资金余额查询 | `AccountSnapshot` 现金字段置 0，`ext_json.cash_unavailable` 标注 | 已实现 |
| D-001 | 同上 | 状态机子集，不产生 `PENDING/UNSEND/WAIT_CANCEL` | 按语义映射，差异登记不扩展契约 | 已实现 |
| D-001 | 同上 | 无原生 `DAY` 有效期限 | `DAY → 上游会话默认`；查询回报反向补 `DAY` | 已实现 |
| D-001 | 同上 | 订单事件不含成交明细 | 网关按累计成交量增量查询成交并去重推送 | 已实现 |
| D-001 | 同上 | 回报 seq 仅内存、进程重启清零 | 渠道核心重启用查询补齐（v1 约定） | 已实现 |
| D-001 | 同上 | 单一上游账号、无多客户号 | `F001 / A0001@SIM` 固定映射 | 已实现 |
| D-001 | 同上 | 停牌 / 涨跌停为模拟开关 | `ext_json` 不承载业务规则，走 `QuerySecurityInfo` | 已实现 |
| D-002 | 真实券商（待接入） | 柜台原生委托号、成交流水号、Topic 订阅 / 登录即推模型、原始状态码 | 网关内部消化并映射为 `exchange_order_id` / `trade_id` / 统一状态；原值进 `ext_json` | 待接入 |

## 10. 已知限制与演进

- **不做的**：跨进程持久化 seq 补发、中央网关聚合、订单簿快照、拆分服务/NATS/L2 行情（M5/M7 决策）。
- **将来可选**：C++ + CMake 网关模板（第一个真实券商接入时）、多账号映射、按券商扩展差异表、把 `ext_json` 中稳定复用的字段升级为契约字段（需新版本 proto 与 breaking 检查）。
- **当前边界**：stub 的适配器是 Go 进程；换成 C++ 实现时只要满足本文契约与 seq 语义，渠道核心与 TradeForge server 均无需改动。
