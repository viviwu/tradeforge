// Contract-equivalent POD types for the C++ gateway skeleton.
// Field names mirror api/proto/brokergateway/v1/brokergateway.proto; the gRPC
// layer (built only on Linux x86_64 with generated C++ stubs) converts between
// these PODs and protobuf messages. No SDK or protobuf dependency here.
#pragma once

#include <cstdint>
#include <string>
#include <vector>

namespace brokergw {

enum class Side { kUnspecified = 0, kBuy = 1, kSell = 2 };
enum class OrderType { kUnspecified = 0, kLimit = 1, kMarket = 2 };
enum class TimeInForce { kUnspecified = 0, kDay = 1, kGtc = 2, kIoc = 3, kFok = 4 };
enum class OrderStatus {
  kUnspecified = 0,
  kPending = 1,
  kUnsend = 2,
  kPendingNew = 3,
  kAccepted = 4,
  kPartiallyFilled = 5,
  kFilled = 6,
  kWaitCancel = 7,
  kCanceled = 8,
  kRejected = 9,
};
enum class TradingStatus { kUnspecified = 0, kNormal = 1, kSuspended = 2, kDelisted = 3 };

// Price/amount unit: 1 unit = 0.0001 yuan. All values are int64.
struct GatewayTradeAccount {
  std::string trade_account;
  std::string market;
};
struct GatewayAccount {
  std::string fund_account;
  std::vector<GatewayTradeAccount> trade_accounts;
};

struct SubmitRequest {
  int64_t request_id = 0;
  std::string client_order_id;
  std::string fund_account;
  std::string trade_account;
  std::string market;
  std::string symbol;
  Side side = Side::kUnspecified;
  OrderType order_type = OrderType::kUnspecified;
  TimeInForce tif = TimeInForce::kUnspecified;
  int64_t price_u = 0;
  int64_t qty = 0;
  std::string ext_json;
};

struct SubmitAck {
  int64_t request_id = 0;
  std::string client_order_id;
  bool accepted = false;
  std::string reject_reason;
  OrderStatus status = OrderStatus::kUnspecified;
};

struct BrokerOrder {
  std::string client_order_id;
  std::string exchange_order_id;
  std::string fund_account;
  std::string trade_account;
  std::string market;
  std::string symbol;
  Side side = Side::kUnspecified;
  OrderType order_type = OrderType::kUnspecified;
  TimeInForce tif = TimeInForce::kUnspecified;
  int64_t price_u = 0;
  int64_t qty = 0;
  int64_t filled_qty = 0;
  int64_t avg_fill_price_u = 0;
  OrderStatus status = OrderStatus::kUnspecified;
  std::string reject_reason;
  int64_t created_unix_nano = 0;
  int64_t updated_unix_nano = 0;
  std::string ext_json;
};

struct BrokerTrade {
  std::string trade_id;
  std::string client_order_id;
  std::string exchange_order_id;
  std::string fund_account;
  std::string trade_account;
  std::string market;
  std::string symbol;
  Side side = Side::kUnspecified;
  int64_t price_u = 0;
  int64_t qty = 0;
  int64_t amount_u = 0;
  int64_t ts_unix_nano = 0;
  std::string ext_json;
};

struct BrokerPosition {
  std::string trade_account;
  std::string market;
  std::string symbol;
  int64_t qty = 0;
  int64_t available_qty = 0;
  int64_t avg_cost_u = 0;
  std::string ext_json;
};

struct AccountSnapshot {
  std::string fund_account;
  int64_t available_cash_u = 0;
  int64_t frozen_cash_u = 0;
  std::vector<BrokerPosition> positions;
  int64_t ts_unix_nano = 0;
  std::string ext_json;
};

struct SecurityInfo {
  std::string market;
  std::string symbol;
  TradingStatus trading_status = TradingStatus::kUnspecified;
  int64_t price_tick_u = 0;
  int64_t lot_size = 0;
  int64_t limit_up_u = 0;
  int64_t limit_down_u = 0;
  int64_t max_buy_qty = 0;
  int64_t max_sell_qty = 0;
  std::string ext_json;
};

enum class ReportKind { kOrder = 0, kTrade = 1, kAccount = 2 };

struct ReportEvent {
  int64_t seq = 0;  // gateway-local, strictly increasing, starts at 1
  std::string client_order_id;
  std::string exchange_order_id;
  ReportKind kind = ReportKind::kOrder;
  BrokerOrder order;
  BrokerTrade trade;
  AccountSnapshot account;
  std::string ext_json;
};

}  // namespace brokergw
