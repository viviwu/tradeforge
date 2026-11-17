// In-process counter simulator used to validate the gRPC contract layer and
// Gateway core without any vendor SDK. It mimics the bgw-probe self-cross
// flow: ACCEPTED ack/terminal separation, self-cross fill with trade reports
// on both sides, query consistency and cancel.
#pragma once

#include <functional>
#include <map>
#include <mutex>
#include <string>
#include <thread>
#include <vector>

#include "brokergw/adapter.h"

namespace brokergw {

class MockCounterAdapter final : public ICounterAdapter {
 public:
  MockCounterAdapter();
  ~MockCounterAdapter() override;

  std::string ServerVersion() const override;
  std::vector<GatewayAccount> Accounts() const override;

  SubmitAck SubmitOrder(const SubmitRequest& req) override;
  bool CancelOrder(const std::string& client_order_id,
                   std::string* reject_reason) override;
  std::vector<BrokerOrder> QueryOrders(const std::string& client_order_id) override;
  std::vector<BrokerTrade> QueryTrades(const std::string& client_order_id) override;
  AccountSnapshot QueryAccount(const std::string& fund_account) override;
  SecurityInfo QuerySecurityInfo(const std::string& market,
                                 const std::string& symbol) override;

  void SetSink(CounterEventSink* sink) override;
  void Start() override;
  void Stop() override;

 private:
  struct Entry {
    SubmitRequest req;
    BrokerOrder order;
  };

  void EmitOrder(const BrokerOrder& order, const std::string& raw_status = "");
  void EmitTrade(const BrokerTrade& trade);
  void Cross(const std::string& buy_cid, const std::string& sell_cid,
             int64_t price_u, int64_t qty);
  void Later(int delay_ms, std::function<void()> fn);

  mutable std::mutex mu_;
  CounterEventSink* sink_ = nullptr;
  bool running_ = false;
  int64_t next_exchange_id_ = 1;
  int64_t next_trade_id_ = 1;
  std::map<std::string, Entry> orders_;
  std::vector<BrokerTrade> trades_;
  std::vector<std::thread> workers_;
};

}  // namespace brokergw
