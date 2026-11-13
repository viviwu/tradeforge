#include "brokergw/gateway.h"

#include <chrono>
#include <utility>

namespace brokergw {
namespace {

int64_t NowUnixNano() {
  using namespace std::chrono;
  return duration_cast<nanoseconds>(system_clock::now().time_since_epoch()).count();
}

void MergeExt(std::string* target, const std::string& raw_status,
              const std::string& raw_ext) {
  if (raw_status.empty() && raw_ext.empty()) return;
  // Skeleton: keep raw counter values as a compact, parseable JSON object.
  // The real adapter layer uses the same escape hatch (contract ext_json).
  std::string json = "{";
  if (!raw_status.empty()) json += "\"raw_status\":\"" + raw_status + "\"";
  if (!raw_ext.empty()) {
    if (!raw_status.empty()) json += ",";
    json += "\"raw_ext\":" + raw_ext;
  }
  json += "}";
  *target = json;
}

}  // namespace

// Private helpers assume mu_ is held by the public entry points.

void Gateway::Publish(ReportEvent ev) {
  ring_.Push(ev);  // assigns seq; gRPC layer fans out from the same cursor
}

void Gateway::ResolveStaticFields(BrokerOrder* order) const {
  const auto it = details_.find(order->client_order_id);
  if (it != details_.end()) {
    const BrokerOrder& d = it->second;
    if (order->price_u == 0) order->price_u = d.price_u;
    if (order->qty == 0) order->qty = d.qty;
    if (order->tif == TimeInForce::kUnspecified) order->tif = d.tif;
    if (order->created_unix_nano == 0) order->created_unix_nano = d.created_unix_nano;
  }
}

void Gateway::EmitTradesForFill(const CounterEvent& ev) {
  const std::string& cid = ev.order.client_order_id;
  FillBook& book = fills_[cid];
  if (ev.order.filled_qty <= book.reported_filled) return;
  // A failed follow-up query must NOT advance the watermark: the next order
  // event retries, otherwise execution reports would be silently lost.
  if (!ev.query_ok || adapter_ == nullptr) return;

  const std::vector<BrokerTrade> trades = adapter_->QueryTrades(cid);
  for (const auto& t : trades) {
    if (t.trade_id.empty() || !book.emitted.insert(t.trade_id).second) continue;
    ReportEvent r;
    r.kind = ReportKind::kTrade;
    r.client_order_id = t.client_order_id.empty() ? cid : t.client_order_id;
    r.exchange_order_id = t.exchange_order_id;
    r.trade = t;
    MergeExt(&r.ext_json, ev.raw_status, ev.raw_ext_json);
    Publish(r);
  }
  book.reported_filled = ev.order.filled_qty;
}

Gateway::Gateway(ICounterAdapter* adapter, size_t ring_capacity)
    : adapter_(adapter), ring_(ring_capacity) {}

void Gateway::Remember(const SubmitRequest& req) {
  std::lock_guard<std::mutex> lk(mu_);
  BrokerOrder d;
  d.client_order_id = req.client_order_id;
  d.fund_account = req.fund_account;
  d.trade_account = req.trade_account;
  d.market = req.market;
  d.symbol = req.symbol;
  d.side = req.side;
  d.order_type = req.order_type;
  d.tif = req.tif;
  d.price_u = req.price_u;
  d.qty = req.qty;
  d.created_unix_nano = NowUnixNano();
  details_[req.client_order_id] = d;
}

void Gateway::OnCounterEvent(const CounterEvent& ev) {
  std::lock_guard<std::mutex> lk(mu_);
  if (ev.kind == CounterEvent::Kind::kTrade) {
    // kdvip OnRtnOrderFill-style direct execution callback.
    FillBook& book = fills_[ev.trade.client_order_id];
    if (!ev.trade.trade_id.empty() &&
        !book.emitted.insert(ev.trade.trade_id).second) {
      return;  // duplicate execution report
    }
    ReportEvent r;
    r.kind = ReportKind::kTrade;
    r.client_order_id = ev.trade.client_order_id;
    r.exchange_order_id = ev.trade.exchange_order_id;
    r.trade = ev.trade;
    MergeExt(&r.ext_json, ev.raw_status, ev.raw_ext_json);
    Publish(r);
    return;
  }

  if (ev.order.filled_qty > 0) EmitTradesForFill(ev);
  BrokerOrder order = ev.order;
  ResolveStaticFields(&order);
  MergeExt(&order.ext_json, ev.raw_status, ev.raw_ext_json);

  ReportEvent r;
  r.kind = ReportKind::kOrder;
  r.client_order_id = order.client_order_id;
  r.exchange_order_id = order.exchange_order_id;
  r.order = order;
  MergeExt(&r.ext_json, ev.raw_status, ev.raw_ext_json);
  Publish(r);
}

bool Gateway::Replay(int64_t from_seq, std::vector<ReportEvent>* replay,
                     std::string* error) {
  std::lock_guard<std::mutex> lk(mu_);
  if (from_seq > ring_.Cursor()) {
    if (error != nullptr) *error = "from_seq is ahead of the gateway cursor";
    return false;
  }
  return ring_.Snapshot(from_seq + 1, replay, error);
}

bool Gateway::Subscribe(int64_t from_seq, std::vector<ReportEvent>* replay,
                        std::string* error) {
  std::lock_guard<std::mutex> lk(mu_);
  const int64_t cursor = ring_.Cursor();
  if (from_seq > cursor) {
    if (error != nullptr) *error = "from_seq is ahead of the gateway cursor";
    return false;
  }
  if (from_seq == 0) {
    if (replay != nullptr) replay->clear();  // live only, from current cursor
    return true;
  }
  return ring_.Snapshot(from_seq + 1, replay, error);
}

int64_t Gateway::Cursor() const {
  std::lock_guard<std::mutex> lk(mu_);
  return ring_.Cursor();
}

}  // namespace brokergw
