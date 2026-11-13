// Counter-independent adapter interface. One adapter per broker SDK process.
// SDK callback threads MUST only enqueue CounterEvent into the sink; seq
// assignment, ring replay and gRPC sends happen on the gateway's single
// consumer thread (see docs/m7-cpp-gateway.md §2).
#pragma once

#include <string>
#include <vector>

#include "brokergw/types.h"

namespace brokergw {

// Normalized counter event produced by an SDK callback.
struct CounterEvent {
  enum class Kind { kOrder = 0, kTrade = 1 };
  Kind kind = Kind::kOrder;
  BrokerOrder order;
  BrokerTrade trade;
  std::string raw_status;   // original counter status code, goes to ext_json
  std::string raw_ext_json; // counter-specific extras
  bool query_ok = true;     // false when a follow-up trade query failed
};

class CounterEventSink {
 public:
  virtual ~CounterEventSink() {}
  virtual void OnCounterEvent(const CounterEvent& ev) = 0;
};

class ICounterAdapter {
 public:
  virtual ~ICounterAdapter() {}

  virtual std::string ServerVersion() const = 0;
  virtual std::vector<GatewayAccount> Accounts() const = 0;

  virtual SubmitAck SubmitOrder(const SubmitRequest& req) = 0;
  virtual bool CancelOrder(const std::string& client_order_id, std::string* reject_reason) = 0;
  virtual std::vector<BrokerOrder> QueryOrders(const std::string& client_order_id) = 0;
  virtual std::vector<BrokerTrade> QueryTrades(const std::string& client_order_id) = 0;
  virtual AccountSnapshot QueryAccount(const std::string& fund_account) = 0;
  virtual SecurityInfo QuerySecurityInfo(const std::string& market,
                                         const std::string& symbol) = 0;

  virtual void SetSink(CounterEventSink* sink) = 0;
  virtual void Start() = 0;
  virtual void Stop() = 0;
};

}  // namespace brokergw
