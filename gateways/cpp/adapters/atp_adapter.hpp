// 华锐低延时交易柜台 ATPQuantAPI 适配层。
// 只有定义 BROKERGW_WITH_ATP 且在 Linux x86_64 上才编译。
// 业务范围：仅现货（股票 / ETF / 可转债）；两融接口、kRepo/kAntiRepo 回购方向、
// 港股/京市市场码等其他业务不在适配范围。
#pragma once

#ifdef BROKERGW_WITH_ATP

#ifdef BROKERGW_WITH_AMA
#include "adapters/ama_quote_source.hpp"
#endif

#include <cstdint>
#include <future>
#include <map>
#include <memory>
#include <mutex>
#include <string>
#include <vector>

#include "brokergw/adapter.h"
// CMake 的 target_include_directories 指向：
//   ${BROKERGW_SDK_ROOT}/atp_quant_api/sdk/current/include
#include "atp_quant_api.h"
#include "atp_quant_constant.h"
#include "atp_quant_msg.h"

namespace atp {
namespace quant_api {
class ATPQuantAPI;
class ATPProperties;
}  // namespace quant_api
}  // namespace atp

namespace brokergw {

// SDK 的 Req*/回调是异步的，而 ICounterAdapter 是同步接口：
// 用 request_id -> promise 把应答桥接成同步等待；主推回调只入队到 sink。
struct AtpCallResult {
  bool ok = false;
  std::string error;
  SubmitAck ack;
  bool cancel_ok = false;
  std::vector<BrokerOrder> orders;
  std::vector<BrokerTrade> trades;
  AccountSnapshot snapshot;
  SecurityInfo security;
};

struct AtpPending {
  std::promise<AtpCallResult> promise;
};

// 参考 sdk/v2.7.1.2-gcc4.8.5/include/atp_quant_api.h：
//   ATPQuantAPI(handler, properties) -> Init / Login / Stop
//   ReqCashAuctionOrder / ReqCashCancelOrder / ReqCashOrderQuery /
//   ReqCashTradeOrderQuery / ReqCashFundQuery / ReqCashShareQuery /
//   ReqCashExtQuerySecurityInfo
// 回报模型：登录即推（无 Topic 订阅），继承 ATPQuantHandler：
//   OnRspCashAuctionOrder 为受理 ack，OnRtnCashAuctionOrder 为主推；
//   成交并入主推（exec_id / last_px / last_qty / cum_qty），无独立成交回调。
// 字段映射（详见 docs/m7-cpp-gateway.md §3.2 / D-003）：
//   client_order_id -> batch_cl_ord_no / request_id 映射（下单结构不含 cl_ord_no）
//   exchange_order_id <- order_id；cl_ord_no 原值进 ext_json（语义偏差）
//   trade_id <- exec_id（confirm_exec_id 备用）
//   side <- ATPSideConst；order_type <- ATPOrderTypeConst
//   tif 无独立枚举：DAY 默认，IOC/FOK 由 order_type 承担
//   status <- ATPOrdStatusConst via MapAtpStatus()
//   price_u <- price double via DoubleYuanToMicro；qty 需校验为整数股
class AtpAdapter final : public ICounterAdapter, public atp::quant_api::ATPQuantHandler {
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

  // ATPQuantHandler callbacks: never send gRPC or allocate seq here.
  void OnLogin(const atp::quant_api::ATPCustomerInfo& msg) override;
  void OnRspCashAuctionOrder(const atp::quant_api::ATPRspCashAuctionOrderMsg& msg,
                             const atp::quant_api::ATPRspErrorInfo& error_info,
                             int64_t request_id) override;
  void OnRtnCashAuctionOrder(const atp::quant_api::ATPRtnCashAuctionOrderMsg& msg) override;
  void OnRspCashCancelOrder(const atp::quant_api::ATPRspCashCancelOrderMsg& msg,
                            const atp::quant_api::ATPRspErrorInfo& error_info,
                            int64_t request_id) override;
  void OnRspCashOrderQueryResult(const atp::quant_api::ATPRspCashOrderQueryResultMsg& msg,
                                 int64_t request_id,
                                 const atp::quant_api::ATPRspErrorInfo& error_info,
                                 bool is_last) override;
  void OnRspCashTradeOrderQueryResult(
      const atp::quant_api::ATPRspCashTradeOrderQueryResultMsg& msg, int64_t request_id,
      const atp::quant_api::ATPRspErrorInfo& error_info, bool is_last) override;
  void OnRspCashFundQueryResult(const atp::quant_api::ATPRspCashFundQueryResultMsg& msg,
                                int64_t request_id,
                                const atp::quant_api::ATPRspErrorInfo& error_info,
                                bool is_last) override;
  void OnRspCashShareQueryResult(const atp::quant_api::ATPRspCashShareQueryResultMsg& msg,
                                 int64_t request_id,
                                 const atp::quant_api::ATPRspErrorInfo& error_info,
                                 bool is_last) override;
  void OnRspCashExtQueryResultSecurityInfo(
      const atp::quant_api::ATPRspCashExtQueryResultSecurityInfoMsg& msg,
      int64_t request_id, const atp::quant_api::ATPRspErrorInfo& error_info,
      bool is_last) override;

 private:
  int64_t NextRequestId();
  std::shared_ptr<AtpPending> Register(int64_t request_id);
  AtpCallResult Wait(int64_t request_id, int timeout_ms);
  void Resolve(int64_t request_id, AtpCallResult result);
  void EnqueueOrder(const BrokerOrder& order, const std::string& raw_status,
                    const std::string& raw_ext_json);
  void EnqueueTrade(const BrokerTrade& trade, const std::string& raw_ext_json);
  std::string ClientOrderIdFrom(uint64_t batch_cl_ord_no, int64_t cl_ord_no) const;

  mutable std::mutex mu_;
  CounterEventSink* sink_ = nullptr;
  atp::quant_api::ATPQuantAPI* api_ = nullptr;
  atp::quant_api::ATPProperties* props_ = nullptr;
#ifdef BROKERGW_WITH_AMA
  std::unique_ptr<AmaQuoteSource> quotes_;
#endif

  std::string properties_path_;
  std::string cust_id_;
  std::string fund_account_;
  std::string password_;
  std::string branch_id_;
  std::string account_id_sh_;
  std::string account_id_sz_;
  int64_t next_request_id_ = 1;
  std::map<int64_t, std::shared_ptr<AtpPending>> pending_;
  std::map<int64_t, AtpCallResult> accum_;
  std::map<int64_t, std::string> cid_by_request_;
  std::map<int64_t, std::string> cid_by_cl_ord_no_;
  std::map<std::string, BrokerOrder> orders_;
  std::vector<BrokerTrade> trades_;
  std::vector<BrokerPosition> positions_;
  std::vector<GatewayAccount> accounts_;
  bool logged_in_ = false;
};

}  // namespace brokergw

#else
// 默认构建（含本机 arm）不触碰 SDK：仅保留此适配层的接口约定说明。
#endif  // BROKERGW_WITH_ATP
