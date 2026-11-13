// 华锐 archforce 低延时交易柜台 ATPQuantAPI 适配层骨架。
// 只有定义 BROKERGW_WITH_ATP 且在 Linux x86_64 上才编译。
// 业务范围：仅现货（股票 / ETF / 可转债）；两融接口、kRepo/kAntiRepo 回购方向、
// 港股/京市市场码等其他业务不在适配范围。
#pragma once

#ifdef BROKERGW_WITH_ATP

#include <string>
#include <vector>

#include "brokergw/adapter.h"
// CMake 的 target_include_directories 指向：
//   ${BROKERGW_SDK_ROOT}/atp_quant_api/sdk/current/include
#include "atp_quant_api.h"
#include "atp_quant_constant.h"
#include "atp_quant_msg.h"

namespace brokergw {

// 参考：sdk/current/include/atp_quant_api.h
//   ATPQuantAPI(handler, properties) -> Init(314) / Login(329) / Stop(321)
//   ReqCashAuctionOrder(411) / ReqCashCancelOrder(415)
//   ReqCashOrderQuery(419) / ReqCashTradeOrderQuery(439)
//   ReqCashFundQuery(447) / ReqCashShareQuery(427)
//   ReqCashExtQuerySecurityInfo(443)
// 回报模型：登录即推（无 Topic 订阅），继承 ATPQuantHandler：
//   OnRspCashAuctionOrder(72) 为受理 ack，OnRtnCashAuctionOrder(82) 为主推；
//   成交并入主推（exec_id / last_px / last_qty / cum_qty），无独立成交回调，
//   网关在累计成交量增长时查询成交并按 exec_id 去重。
// 字段映射（详见 docs/m7-cpp-gateway.md §3.2 / D-003）：
//   client_order_id -> batch_cl_ord_no / request_id 映射（下单结构不含 cl_ord_no）
//   exchange_order_id <- order_id；cl_ord_no 原值进 ext_json（语义偏差）
//   trade_id <- exec_id（confirm_exec_id 备用）
//   side <- ATPSideConst；order_type <- ATPOrderTypeConst('a'/'b'/'d'/'e')
//   tif 无独立枚举：DAY 默认，IOC/FOK 由 order_type 'd'/'e' 承担
//   status <- ATPOrdStatusConst via MapAtpStatus()
//   price_u <- price double via DoubleYuanToMicro；qty 需校验为整数股
//   market <- ATPMarketIDConst 101/102/103/104 -> SH/SZ/...
// 回调线程约束：回调只入队、勿多线程并发调同一实例；
//   retransmit_mode / multi_channel_flag / FPGA 通道参数进 ext_json。
class AtpAdapter final : public ICounterAdapter, public ATPQuantHandler {
 public:
  explicit AtpAdapter(const std::string& properties_path);
  ~AtpAdapter() override;

  std::string ServerVersion() const override;
  std::vector<GatewayAccount> Accounts() const override;
  SubmitAck SubmitOrder(const SubmitRequest& req) override;
  bool CancelOrder(const std::string& client_order_id, std::string* reject_reason) override;
  std::vector<BrokerOrder> QueryOrders(const std::string& client_order_id) override;
  std::vector<BrokerTrade> QueryTrades(const std::string& client_order_id) override;
  AccountSnapshot QueryAccount(const std::string& fund_account) override;
  SecurityInfo QuerySecurityInfo(const std::string& market,
                                 const std::string& symbol) override;
  void SetSink(CounterEventSink* sink) override;
  void Start() override;
  void Stop() override;

  void OnRspCashAuctionOrder(const ATPRspCashAuctionOrderMsg* msg) override;
  void OnRtnCashAuctionOrder(const ATPRtnCashAuctionOrderMsg* msg) override;

 private:
  // TODO(Linux 仿真环境)：ATPQuantAPI 实例、request_id 映射、cl_ord_no 回填、
  // 成交增量查询与去重队列、软/硬通道配置。
  CounterEventSink* sink_ = nullptr;
};

}  // namespace brokergw

#else
// 默认构建（含本机 arm）不触碰 SDK：仅保留此适配层的接口约定说明。
#endif  // BROKERGW_WITH_ATP
