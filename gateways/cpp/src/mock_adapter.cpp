#include "brokergw/mock_adapter.h"

#include <chrono>
#include <sstream>

namespace brokergw {
namespace {

int64_t NowUnixNano() {
  using namespace std::chrono;
  return duration_cast<nanoseconds>(system_clock::now().time_since_epoch()).count();
}

BrokerOrder ToOrder(const SubmitRequest& req, const std::string& exchange_id) {
  BrokerOrder o;
  o.client_order_id = req.client_order_id;
  o.exchange_order_id = exchange_id;
  o.fund_account = req.fund_account;
  o.trade_account = req.trade_account;
  o.market = req.market;
  o.symbol = req.symbol;
  o.side = req.side;
  o.order_type = req.order_type;
  o.tif = req.tif;
  o.price_u = req.price_u;
  o.qty = req.qty;
  o.status = OrderStatus::kPending;
  o.created_unix_nano = NowUnixNano();
  return o;
}

}  // namespace

MockCounterAdapter::MockCounterAdapter() = default;
MockCounterAdapter::~MockCounterAdapter() { Stop(); }

std::string MockCounterAdapter::ServerVersion() const {
  return "brokergw-cpp-mock/0.1";
}

std::vector<GatewayAccount> MockCounterAdapter::Accounts() const {
  GatewayAccount a;
  a.fund_account = "F001";
  for (const char* m : {"SH", "SZ"}) {
    GatewayTradeAccount ta;
    ta.trade_account = "A0001";
    ta.market = m;
    a.trade_accounts.push_back(ta);
  }
  return {a};
}

void MockCounterAdapter::SetSink(CounterEventSink* sink) { sink_ = sink; }

void MockCounterAdapter::Start() {
  std::lock_guard<std::mutex> lk(mu_);
  running_ = true;
}

void MockCounterAdapter::Stop() {
  {
    std::lock_guard<std::mutex> lk(mu_);
    running_ = false;
  }
  for (auto& t : workers_) {
    if (t.joinable()) t.join();
  }
  workers_.clear();
}

void MockCounterAdapter::Later(int delay_ms, std::function<void()> fn) {
  std::lock_guard<std::mutex> lk(mu_);
  if (!running_) return;
  workers_.emplace_back([this, delay_ms, fn]() {
    std::this_thread::sleep_for(std::chrono::milliseconds(delay_ms));
    bool go = false;
    {
      std::lock_guard<std::mutex> lk(mu_);
      go = running_;
    }
    if (go) fn();
  });
}

void MockCounterAdapter::EmitOrder(const BrokerOrder& order,
                                   const std::string& raw_status) {
  if (sink_ == nullptr) return;
  CounterEvent ev;
  ev.kind = CounterEvent::Kind::kOrder;
  ev.order = order;
  ev.raw_status = raw_status;
  sink_->OnCounterEvent(ev);
}

void MockCounterAdapter::EmitTrade(const BrokerTrade& trade) {
  if (sink_ == nullptr) return;
  CounterEvent ev;
  ev.kind = CounterEvent::Kind::kTrade;
  ev.trade = trade;
  sink_->OnCounterEvent(ev);
}

SubmitAck MockCounterAdapter::SubmitOrder(const SubmitRequest& req) {
  BrokerOrder accepted;
  {
    std::lock_guard<std::mutex> lk(mu_);
    std::ostringstream os;
    os << "MOCK-" << next_exchange_id_++;

    Entry e;
    e.req = req;
    e.order = ToOrder(req, os.str());
    orders_[req.client_order_id] = e;
    accepted = e.order;
  }

  SubmitAck ack;
  ack.request_id = req.request_id;
  ack.client_order_id = req.client_order_id;
  ack.accepted = true;
  ack.status = OrderStatus::kPending;

  const std::string cid = req.client_order_id;
  Later(20, [this, cid, accepted]() {
    {
      std::lock_guard<std::mutex> lk(mu_);
      auto it = orders_.find(cid);
      if (it != orders_.end() && it->second.order.status == OrderStatus::kPending) {
        it->second.order.status = OrderStatus::kAccepted;
        it->second.order.updated_unix_nano = NowUnixNano();
      }
    }
    BrokerOrder report = accepted;
    report.status = OrderStatus::kAccepted;
    report.updated_unix_nano = NowUnixNano();
    EmitOrder(report);

    if (accepted.side == Side::kBuy) {
      // Look for a resting sell that can be crossed at the buy price.
      std::string sell_cid;
      int64_t fill_qty = 0;
      {
        std::lock_guard<std::mutex> lk(mu_);
        for (const auto& kv : orders_) {
          const BrokerOrder& s = kv.second.order;
          if (s.side == Side::kSell && s.status == OrderStatus::kAccepted &&
              s.symbol == accepted.symbol && s.qty > 0 &&
              s.price_u <= accepted.price_u) {
            sell_cid = kv.first;
            fill_qty = s.qty < accepted.qty ? s.qty : accepted.qty;
            break;
          }
        }
      }
      if (!sell_cid.empty()) {
        Cross(cid, sell_cid, accepted.price_u, fill_qty);
      }
    }
  });
  return ack;
}

void MockCounterAdapter::Cross(const std::string& buy_cid,
                               const std::string& sell_cid,
                               int64_t price_u, int64_t qty) {
  BrokerOrder buy_report;
  BrokerOrder sell_report;
  BrokerTrade bt;
  BrokerTrade st;
  {
    std::lock_guard<std::mutex> lk(mu_);
    auto bi = orders_.find(buy_cid);
    auto si = orders_.find(sell_cid);
    if (bi == orders_.end() || si == orders_.end()) return;

    bi->second.order.status = OrderStatus::kFilled;
    si->second.order.status = OrderStatus::kFilled;
    bi->second.order.filled_qty = qty;
    si->second.order.filled_qty = qty;
    bi->second.order.avg_fill_price_u = price_u;
    si->second.order.avg_fill_price_u = price_u;
    bi->second.order.updated_unix_nano = NowUnixNano();
    si->second.order.updated_unix_nano = NowUnixNano();
    buy_report = bi->second.order;
    sell_report = si->second.order;

    const int64_t ts = NowUnixNano();
    bt.trade_id = "MOCK-T-" + std::to_string(next_trade_id_++);
    bt.client_order_id = buy_cid;
    bt.exchange_order_id = buy_report.exchange_order_id;
    bt.fund_account = buy_report.fund_account;
    bt.trade_account = buy_report.trade_account;
    bt.market = buy_report.market;
    bt.symbol = buy_report.symbol;
    bt.side = Side::kBuy;
    bt.price_u = price_u;
    bt.qty = qty;
    bt.amount_u = price_u * qty;
    bt.ts_unix_nano = ts;

    st = bt;
    st.trade_id = "MOCK-T-" + std::to_string(next_trade_id_++);
    st.client_order_id = sell_cid;
    st.exchange_order_id = sell_report.exchange_order_id;
    st.side = Side::kSell;

    trades_.push_back(bt);
    trades_.push_back(st);
  }
  EmitOrder(buy_report);
  EmitOrder(sell_report);
  EmitTrade(bt);
  EmitTrade(st);
}

bool MockCounterAdapter::CancelOrder(const std::string& cid,
                                     std::string* reject_reason) {
  BrokerOrder canceled;
  {
    std::lock_guard<std::mutex> lk(mu_);
    auto it = orders_.find(cid);
    if (it == orders_.end()) {
      if (reject_reason != nullptr) *reject_reason = "unknown client_order_id";
      return false;
    }
    if (it->second.order.status == OrderStatus::kFilled ||
        it->second.order.status == OrderStatus::kCanceled) {
      if (reject_reason != nullptr) *reject_reason = "order already terminal";
      return false;
    }
    it->second.order.status = OrderStatus::kWaitCancel;
    it->second.order.updated_unix_nano = NowUnixNano();
    canceled = it->second.order;
  }
  Later(20, [this, cid, canceled]() {
    BrokerOrder report = canceled;
    {
      std::lock_guard<std::mutex> lk(mu_);
      auto it = orders_.find(cid);
      if (it != orders_.end()) {
        it->second.order.status = OrderStatus::kCanceled;
        it->second.order.updated_unix_nano = NowUnixNano();
        report = it->second.order;
      }
    }
    report.status = OrderStatus::kCanceled;
    EmitOrder(report);
  });
  return true;
}

std::vector<BrokerOrder> MockCounterAdapter::QueryOrders(const std::string& cid) {
  std::lock_guard<std::mutex> lk(mu_);
  std::vector<BrokerOrder> out;
  for (const auto& kv : orders_) {
    if (cid.empty() || kv.first == cid) out.push_back(kv.second.order);
  }
  return out;
}

std::vector<BrokerTrade> MockCounterAdapter::QueryTrades(const std::string& cid) {
  std::lock_guard<std::mutex> lk(mu_);
  std::vector<BrokerTrade> out;
  for (const auto& t : trades_) {
    if (cid.empty() || t.client_order_id == cid) out.push_back(t);
  }
  return out;
}

AccountSnapshot MockCounterAdapter::QueryAccount(const std::string& fund_account) {
  std::lock_guard<std::mutex> lk(mu_);
  AccountSnapshot snap;
  snap.fund_account = fund_account.empty() ? "F001" : fund_account;
  snap.available_cash_u = 10000000000;
  snap.ts_unix_nano = NowUnixNano();
  std::map<std::string, BrokerPosition> by_symbol;
  for (const auto& kv : orders_) {
    const BrokerOrder& o = kv.second.order;
    if (o.symbol.empty()) continue;
    BrokerPosition& p = by_symbol[o.symbol];
    p.symbol = o.symbol;
    p.market = o.market;
    p.trade_account = o.trade_account;
    p.qty += o.qty;
    p.available_qty += o.qty;
  }
  for (auto& kv : by_symbol) snap.positions.push_back(kv.second);
  return snap;
}

SecurityInfo MockCounterAdapter::QuerySecurityInfo(const std::string& market,
                                                   const std::string& symbol) {
  SecurityInfo info;
  info.market = market;
  info.symbol = symbol;
  info.trading_status = TradingStatus::kNormal;
  info.price_tick_u = 100;  // 0.01 yuan = 100 units
  info.lot_size = 100;
  info.limit_up_u = 200000;
  info.limit_down_u = 50000;
  info.max_buy_qty = 1000000;
  info.max_sell_qty = 1000000;
  return info;
}

}  // namespace brokergw
