// 金证极速交易柜台 kdvip_ap 适配层骨架。
// 只有定义 BROKERGW_WITH_KDVIP 且在 Linux x86_64 上才编译；本机 arm/macOS
// 默认不编译，也不会 include 任何 SDK 头文件。
// 业务范围：仅现货（股票 / ETF / 可转债）；maCliOpt* / maCliFuture* /
// maCliFisl* 等其他市场头不看，金证行情头也不在适配范围（行情由 AMA 提供）。
#pragma once

#ifdef BROKERGW_WITH_KDVIP

#include <string>
#include <vector>

#include "brokergw/adapter.h"
// CMake 的 target_include_directories 会指向：
//   ${BROKERGW_SDK_ROOT}/kdvip_api/include 与 .../include/c++
#include "maCliStkTradeApi.h"
#include "maCliStkTradeApiStruct.h"
#include "maCliTradeApi.h"

namespace brokergw {

// 参考：demo/trade_demo/stk/maCliStkTradeApiTest.cpp
//   RegisterSpi -> RegisterServer -> Init -> RegisterAcct -> ReqUserLogin
//   -> SubTopic(topic, filter=股东账号, chDataSet='1')
//   -> ReqOrder(856) / ReqCancelOrder(862) / ReqQryCurrDayOrder(898)
//      / ReqQryCurrDayFill(901) / ReqQryFundBala(952)
//      / ReqQryExpendableShares(895) / ReqQryStkInfo(841)
// 字段映射（详见 docs/m7-cpp-gateway.md §3.1 / D-002）：
//   client_order_id -> iOrderBsn / szOrderText + 网关映射表（无原生幂等键）
//   exchange_order_id <- szOrderId; trade_id <- szMatchedSn
//   price_u/amount_u <- char[21] 十进制字符串 via DecimalYuanToMicro
//   status <- chOrderStatus（码表未提供，用 SetKdvipStatusMap 注入，勿猜）
//   QuerySecurityInfo <- CRspStkQryStkInfoField（tick/lot/涨跌停/停牌/上限）
// 回调线程约束：OnRtnOrderConfirm / OnRtnOrderFill 只把 CounterEvent 入队，
//   不做 gRPC 发送、不分配 seq；AR/PS 双通道的 AcceptSn 用于柜台侧补发。
class KdvipAdapter final : public ICounterAdapter, public CCliStkTradeSpi {
 public:
  explicit KdvipAdapter(const std::string& config_path);
  ~KdvipAdapter() override;

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

  // SPI: 只入队，业务全部交给 gateway 单线程。
  void OnRtnOrderConfirm(CRspStkOrderConfirmField* field) override;
  void OnRtnOrderFill(CRspStkOrderFillField* field) override;

 private:
  // TODO(Linux 仿真环境)：CCliStkTradeApi 实例、账号映射表、
  // iOrderBsn <-> client_order_id、szOrderId 反查、队列与生命周期。
  CounterEventSink* sink_ = nullptr;
};

}  // namespace brokergw

#else
// 默认构建（含本机 arm）不触碰 SDK：仅保留此适配层的接口约定说明。
#endif  // BROKERGW_WITH_KDVIP
