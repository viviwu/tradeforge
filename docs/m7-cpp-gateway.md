# M7：真实券商 C++ 网关适配设计

> 状态：**设计已定，骨架已落地**；契约 `broker-gateway.v1` 保持冻结，不新增 RPC、不改 oneof。
> 材料范围：**只以 C++ demo 中实际 `#include` 的头文件为入口**（必要时看这些头的传递依赖里的结构定义），加 demo 源码与 CMake/Makefile；其余 SDK 头文件（其他市场 API、其他版本目录、python/java、底层 C API 等）一律不看。不读取 `.ini` 登录配置、`.lic` 授权、账号/密钥文件；材料通过本地软链接提供，不入库。
>
> demo 实际 include：`kdvip_api/demo/trade_demo/stk` → `maCliStkTradeApi.h`、`maCliStkTradeApiStruct.h`、`maCliTradeApi.h`、`maCliMktData.h`（+ demo 自有 `iniFileRW.h` / `vc610IO.hpp`）；`atp_quant_api/demo/*` → `atp_quant_api.h`、`atp_quant_constant.h`、`atp_quant_msg.h`（+ demo 自有 `trade_app.h` / `uni_trade_app.h` / `utils.h`）；`amd_ama/c++/demo_src` → `ama.h`、`ama_property.h`、`ama_tools.h`、`ama_serialize.h`、`query/ama_query_spi.h`（`ama_struct.h` / `ama_datatype.h` 为 `ama.h` 传递依赖中的结构定义）。
> **业务范围：仅现货（股票 / ETF / 可转债）的委托与成交回报**。金证 demo 虽 include `maCliMktData.h`，本项目行情统一由华锐 AMA 提供；期权 / 期货 / 两融 / 回购 / 港股通等其他市场 API 与头文件（`maCliOpt*`、`maCliFuture*`、`maCliFisl*`、`maCliAllTradeApi.h`、ATP 两融协议与 `kRepo`/`kAntiRepo`、AMA 的 usa/hkex/cfets 头）一律不看、不映射。
> 本机为 arm/macOS，SDK 均为 Linux x86_64 预编译库（gcc 4.8.5 / 11.2.1、C++11），**本机不编译、不运行 SDK demo**，只编译我们自己的纯 C++ 核心单测。

## 1. 三套通道总览（仅 demo `#include` 的头文件 + demo 源码）

| 通道 | 材料路径 | demo 实际 include 的头 / demo 源码 | 回报模型 | 柜台原生键 | 价格单位 |
|---|---|---|---|---|---|
| 金证极速交易柜台 `kdvip_ap` | `refs/broker-sdks/kdvip_api` | `maCliStkTradeApi.h`、`maCliStkTradeApiStruct.h`、`maCliTradeApi.h`、`maCliMktData.h`；`demo/trade_demo/stk/maCliStkTradeApiTest.cpp` | **Topic 订阅制**：`SubTopic(topic, filter=股东账号, chDataSet='1')`，SPI `OnRtnOrderConfirm` / `OnRtnOrderFill` | `szOrderId` 合同序号、`iOrderBsn` 委托批号（客户端可填、非唯一）、`szMatchedSn` 成交编号 | 价格/金额为 `char[21]` 十进制字符串（元） |
| 华锐低延时交易柜台 `atp_quant_api` | `refs/broker-sdks/atp_quant_api` | `atp_quant_api.h`、`atp_quant_constant.h`、`atp_quant_msg.h`；`demo/trade_app/trade_app.cpp`、`simple_demo/demo.cpp`、`all_message_demo/demo.cpp` | **登录即推**：继承 `ATPQuantHandler`，`OnRspCashAuctionOrder` 为 ack、`OnRtnCashAuctionOrder` 为主推；成交并入主推（`exec_id/last_px/last_qty/cum_qty`），无独立成交回调 | `cl_ord_no`（客户订单号，**下单不入参**，柜台在应答返回）、`order_id` / `cl_ord_id`、`exec_id` | `price double` 元（N13(4)）、`order_qty double` 股（N15(2)） |
| 华锐低延时行情 `amd_ama`（C++） | `refs/broker-sdks/amd_ama/c++` | `ama.h`、`ama_property.h`、`ama_tools.h`、`ama_serialize.h`、`query/ama_query_spi.h`；`demo_src/ama_demo_tcp.cpp`（结构定义见 `ama.h` 传递依赖 `ama_struct.h` / `ama_datatype.h`） | **行情旁路**：`IAMDSpi::OnMDSnapshot/OnMDTickOrder/OnMDTickExecution`，`SubscribeData` 按 market+flag+code 订阅，TCP / FPGA 两种通道 | `appl_seq_num`、`channel_no`（仅行情内部序号） | `Price6 = ×10⁶`、`Qty2 = ×10²`、`Amt5 = ×10⁵`，`int64` |

SDK 版本与平台（`version.txt` / `BuildInfo.txt` / README）：ATP `v2.7.1.2-gcc4.8.5`（`current`、`current_atp` 均指向它）与 `v2.6.3.5-gcc11.2.1`，rhel7.4 x64；AMA `V4.5.8.250331-rc1.4`，RedHat/CentOS7.x + gcc4.8.5，CMake≥3.15 / C++11；金证 `lib/Lnx64` 为 x86_64 `.so`，demo CMake 用 `-DOS_IS_LINUX -fpermissive`、C++11。

## 2. 目标架构

```
渠道核心（Go，BrokerGateway v1 client）
   │ gRPC（Attach / Submit / Cancel / Query* / SubscribeReports）
C++ 网关进程（每柜台一个：kdvip / atp）
   ├─ gRPC server 层：契约 ↔ 内部结构（不随柜台变化）
   ├─ Gateway core：ack/终态分离、本地 seq、内存 ring 补发、成交去重、查询补齐
   ├─ ICounterAdapter：柜台无关接口（Submit/Cancel/Query*/Attach/Start/Stop）
   └─ SDK adapter：金证 / ATP，封装 SDK 头文件、回调、字段映射
          └─ 行情旁路：AMA quote source，只喂 QuerySecurityInfo，不进回报流
```

线程模型统一为「**SDK 回调线程只入队，单线程消费**」：

- 金证底层有 AR/PS 两条回调通道并带 `AcceptSn`/`RestorePsList`；ATP 文档与 demo 明确回调线程禁阻塞、勿多线程并发调同一实例；AMA 回调要求 `FreeMemory` 并尽快返回。三者都不能在回调里做 gRPC 发送或分配 seq。
- 因此所有回调把 `RawCounterEvent` 推入一个队列，由 gateway 单线程按序消费：补齐静态字段 → 分配 `seq`（从 1 起严格递增）→ 写入内存 ring（默认 4096）→ 扇出到订阅者。与 Go 版 `internal/brokergw` 语义一致。
- 成交回报：金证有 `OnRtnOrderFill`；ATP 无独立回调，须在累计成交量增长时按 `exec_id` 去重，并从主推字段（`last_px/last_qty/cum_qty`）或成交查询增量补齐。查询失败不推进水位，下个事件重试。
- 会话内补发窗口过期 / 慢消费者 → `ResourceExhausted`，渠道核心重新 `Attach` 并用查询补齐（v1 已约定；跨进程持久化补发仍不做）。
- 网关重启后 seq 清零，静态字段从「下单注册表 + 柜台查询」补齐（Go 版 `lookupOrderDetails` 同构）。

## 3. 字段映射

### 3.1 金证 kdvip → 契约

| 契约 | 金证 | 说明 |
|---|---|---|
| `client_order_id` | 无原生幂等键 → `iOrderBsn` / `szOrderText` 承载 + 网关映射表 | `iOrderBsn` 客户端可填、贯穿响应/回报/查询，但 demo 注明非唯一；网关必须自己建唯一映射 |
| `exchange_order_id` | `szOrderId`（char[11] 合同序号） | 只读透传 |
| `trade_id` | `szMatchedSn` | 成交流水 |
| `side` | `iStkBiz` + `iStkBizAction`（demo 买 100 / 卖 101） | 需字典确认后映射 |
| `order_type` / `tif` | **现货委托结构未找到独立字段**（仅 `chMpOrdertype` 市价保护价） | 默认 LIMIT/DAY，差异登记 |
| `price_u` / `amount_u` | `szOrderPrice` 等 `char[21]` 十进制元字符串 | 定点解析 ×10000，禁止浮点 |
| `qty` | `llOrderQty` / `llMatchedQty`（LONGLONG 股） | 直用 |
| `status` | `chOrderStatus` + `chIsWithdraw` + `chCancelStatus` + `szOfferRetMsg` | **码表在头文件/demo 中未找到**，见 §5 |
| `reject_reason` | `iMsgCode` / `szMsgText` / `szOfferRetMsg` | 结构化拒单码缺失，原值进 `ext_json` |
| 时间 | `iOrderDate` + `szOrderTime` / `szMatchedTime` | 秒级字符串，转 unix_nano（精度差登记） |
| 账号 | `llCuacctCode` 资产账户、`llCustCode` 客户代码、`szTrdacct` 股东账户、`szStkbd` 板块、`chStkex` 市场 | Attach 的 accounts 由登录响应 + `ReqQryStkholderAcct` 拼出 |
| `QuerySecurityInfo` | `CRspStkQryStkInfoField`：`szStkSpread`（tick）、`llStkLotSize`/`iBuyUnit`/`iSaleUnit`（lot）、`szStkUplmtPrice`/`szStkLwlmtPrice`（涨跌停）、`chStkSuspended`/`chStkStatus`（停牌）、`llStkUplmtQty`（上限） | 均可由查询供给 |
| 资金持仓快照 | 无资金/持仓 `OnRtn` 推送 | 靠 `ReqQryFundBala` / `ReqQryExpendableFund` / `ReqQryExpendableShares` 拼装，或成交回报里的 `szFundAvl`/`llStkAvl` |

### 3.2 华锐 ATP → 契约

| 契约 | ATP | 说明 |
|---|---|---|
| `client_order_id` | 下单结构**不含** `cl_ord_no`；只有 `batch_cl_ord_no` | 网关把 `client_order_id` 编入 `batch_cl_ord_no` 或维护 request_id 映射，`OnRspCashAuctionOrder` 返回 `cl_ord_no` 后回填 |
| `exchange_order_id` | `order_id`（交易所订单编号）；`cl_ord_id` 申报合同号 | **契约注释把 `cl_ord_no` 视作柜台原生键，但它其实是客户订单号**，此处语义偏差必须登记；原值进 `ext_json` |
| `trade_id` | `exec_id`（`confirm_exec_id` 深交所确认编号备用） | 仅成交有效 |
| `side` | `ATPSideConst`：kBuy `'1'` / kSell `'2'` | 另有 kRepo/kAntiRepo（回购），现货网关不映射 |
| `order_type` | `ATPOrderTypeConst`：`'a'` 限价、`'b'` 本方最优、`'d'` IOC 类、`'e'` FOK 类 | `b` 需登记差异；IOC/FOK 同时承担 TIF 语义 |
| `tif` | **独立 TIF 字段/枚举未找到**，语义隐含在 `order_type` | DAY 默认，GTC 不支持，登记 |
| `price_u` | `price double`（元） | ×10000，边界与四舍五入策略需固定（建议定点 decimal 转换） |
| `qty` | `order_qty double`（股，N15(2)） | 校验为整数股后再用，非整数进 `ext_json` 并拒单 |
| `market` | `ATPMarketIDConst` 沪 101 / 深 102 / 港 103 / 京 104 | 映射为 `SH`/`SZ`/… |
| 账号 | `cust_id` / `fund_account_id` / `branch_id` / `account_id`（按市场分） | Attach accounts 两层结构由登录模式 `kCustIDMode`/`kFundAccountIDMode` 决定 |
| `QuerySecurityInfo` | `ATPRspCashExtQueryResultSecurityInfoMsg`：`price_tick`、`price_upper/lower_limit`、`buy/sell_qty_unit`、数量上下限、`security_status`/`has_price_limit` | `prev_close_price`/`security_type` 等进 `ext_json`；`lot_size` 手/股需换算 |
| 回报 | `OnRspCashAuctionOrder`（ack）+ `OnRtnCashAuctionOrder`（主推） | ack/终态分离与契约一致；成交并入主推 |

### 3.3 华锐 AMA 行情（旁路，不进回报流）

契约只有 `QuerySecurityInfo`，没有行情 RPC，`ReportEvent` oneof 也没有行情载荷。因此 AMA 只作为**网关内部旁路数据源**：

- `price_tick_u ← price_tick`、`limit_up_u/limit_down_u ← high/low_limited`、`trading_status ← security_status(1=停牌)/instrument_status/kHalt(10)`、参考价 ← `last_price`/`pre_close_price`。
- 单位换算：`Price6(×10⁶) → 契约(×10⁴)` 需 **÷100**；`Qty2`/`Amt5` 同理按需缩放。
- 时间戳 `orig_time`（`YYYYMMDDHHMMSSsss`，毫秒）→ `unix_nano` 需 ×10⁶。
- 市场码 kSSE 101 / kSZSE 102 → `SH`/`SZ`；`trading_phase_code` 沪深位义不同，需按市场解释。
- 10 档盘口、`appl_seq_num`/`channel_no` 无契约位置：不进回报流，最多进 `ext_json`；`QueryMDTick(begin/end_appl_seq_num)` 是行情侧补发，与网关本地 seq 无关。
- TCP 通道上游过滤、断线自动重连、独占 2 线程；FPGA 通道下游过滤、部分字段不披露、1 线程。回调禁业务处理、须 `FreeMemory`。

## 4. 单位换算（统一到 1 unit = 0.0001 元）

| 来源 | 原始 | 换算 | 实现要求 |
|---|---|---|---|
| 金证 | `char[21]` 十进制字符串（元） | ×10000 | 定点字符串解析，避免 `atof` 浮点误差；`gateways/cpp/src/mapping.cpp::DecimalYuanToMicro` |
| ATP | `double` 元（4 位小数） | ×10000 | 先按 N13(4) 约束校验，再定点化；`DoubleYuanToMicro` |
| AMA | `int64` Price6（×10⁶） | ÷100 | 整除校验；`Price6ToMicro` |
| 模拟所（M6 stub） | 0.01 元整数 | ×10 | 已在 Go 版实现，作为换算口径对照 |

## 5. 状态映射

ATP 状态码（`atp_quant_constant.h:1772-1804`）→ 契约：

| ATP | 契约 | ATP | 契约 |
|---|---|---|---|
| 9 kUnSend 未申报 | `PENDING` | 10 kSended 正报 | `UNSEND` |
| 0 kNew 已申报 | `PENDING_NEW`（交易所确认后 `ACCEPTED`） | 1 部成 | `PARTIALLY_FILLED` |
| 2 全成 | `FILLED` | 11/12 待撤 / 部成待撤 | `WAIT_CANCEL` |
| 4 已撤 / 92 撤成 | `CANCELED` | 8 拒绝 / 90 内部拒 | `REJECTED` |
| 3 部成部撤 / 5 部撤 | `CANCELED`（原值进 `ext_json`） | 其他 | `UNSPECIFIED` + `ext_json` |

金证 `chOrderStatus` 的**字典取值表在头文件与 demo 中未找到**。骨架不猜码值：`MapKdvipStatus` 默认返回 `UNSPECIFIED` 并把原码写入 `ext_json`，留 `SetKdvipStatusMap()` 在拿到码表/联调时注入。这是明确的待办，不是已完成项。

## 6. 券商差异登记表（承接 `docs/broker-gateway.md` §9）

| 编号 | 柜台 | 差异点 | 契约侧处置 |
|---|---|---|---|
| D-002 | 金证 kdvip | Topic 订阅制（`filter`=股东账号、`chDataSet='1'`）、无原生幂等键、价格字符串、无 OrderType/TIF、无资金持仓推送、状态码表缺失、秒级时间 | 网关维护 `client_order_id↔iOrderBsn/szOrderId` 映射；订阅内部消化；缺省 LIMIT/DAY；查询拼快照；原码/原值进 `ext_json`；码表待补 |
| D-003 | 华锐 ATP | 登录即推（非订阅）、`cl_ord_no` 语义偏差（客户订单号而非交易所编号）、下单不含 `cl_ord_no`、成交并入主推、无独立 TIF、FPGA/软报盘双通道、双 SDK 版本消息结构差异、双中心/多通道推送 | 网关内部消化主推并按 `exec_id` 去重；`exchange_order_id` 取 `order_id`，`cl_ord_no` 进 `ext_json`；IOC/FOK 由 order_type 承担；通道/版本/重连参数进 `ext_json` |
| D-004 | 华锐 AMA 行情 | 契约无行情 RPC / 无行情载荷；Price6 单位差 100 倍；市场码/停牌字段多位义；TCP/FPGA 字段差异；行情侧自动重连与补发 | 仅作 `QuerySecurityInfo` 旁路数据源，不进 `ReportEvent`；多余字段进 `ext_json`；`appl_seq_num` 与网关 seq 隔离 |

## 7. 骨架交付与构建约束

`gateways/cpp/` 交付的是**可编译的纯 C++ 核心 + 适配层接口骨架**：

- `include/brokergw/{types,adapter,mapping,report_ring}.h`、`src/{mapping,report_ring,mock_adapter,gateway}.cpp`、`tests/core_test.cpp`：不依赖任何 SDK / gRPC / protobuf，本机 clang（arm）可直接 `cmake` 编译并 `ctest` 跑单测，覆盖单位换算、ATP 状态映射、金证码表缺省、seq ring 补发/窗口过期、成交去重与查询失败重试。
- `gateways/cpp/adapters/{kdvip,atp,ama}.hpp`：三个适配层的接口骨架与字段映射清单，SDK `#include` 一律包在 `BROKERGW_WITH_*` 条件编译里，默认关闭。
- `CMakeLists.txt`：`BROKERGW_WITH_GRPC/KDVIP/ATP/AMA` 默认 OFF；打开时才要求 `BROKERGW_SDK_ROOT` 指向 `refs/broker-sdks/` 并在 **Linux x86_64** 上链接对应 `.so`（金证 6 个库、ATP `libatpquantapi.so`+rudplib、AMA `libama.so`）。
- 不复制任何 SDK 头文件/库/授权文件入库；`refs/broker-sdks/` 由 `.gitignore` 排除。

## 8. 验收路径与待确认事项

1. 本机：`cmake -S gateways/cpp -B /tmp/tf-cpp-build && ctest` 全绿（纯核心，不碰 SDK）。
2. Linux x86_64 构建机：打开 `BROKERGW_WITH_*`，用各 SDK 自带的 CMake/Makefile 确认头文件与库可链接（只编译，不连真实柜台）。
3. 柜台仿真环境：用 `mock_adapter` → `bgw-probe` 先验证契约语义不变；再逐柜台联调 Topic 订阅 / 登录即推、字段映射与回报去重。
4. 待材料/联调确认：金证 `ORDER_STATUS`/`STK_BIZ` 码表、ATP `cl_ord_no` 与 `order_id` 的实际取值关系、TIF/GTC 支持范围、纳秒级时间戳字段、AMA 组播/Topic 配置与 `trading_phase_code` 位义。

**契约零改动**：以上全部通过 `ext_json`、网关内部映射与旁路数据源消化，不改 `api/proto/brokergateway/v1/brokergateway.proto`；若未来 `ext_json` 中某字段稳定复用，再走新版本 proto + buf breaking 检查。
