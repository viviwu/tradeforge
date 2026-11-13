// Pure C++ unit tests: no SDK, no gRPC, no vendor headers.
#include <cassert>
#include <iostream>
#include <string>
#include <vector>

#include "brokergw/adapter.h"
#include "brokergw/gateway.h"
#include "brokergw/mapping.h"
#include "brokergw/report_ring.h"

namespace {

int g_failures = 0;
#define CHECK(cond)                                                        \
  do {                                                                     \
    if (!(cond)) {                                                          \
      std::cerr << "FAIL line " << __LINE__ << ": " #cond << std::endl;     \
      ++g_failures;                                                         \
    }                                                                       \
  } while (0)

using namespace brokergw;

class FakeAdapter : public ICounterAdapter {
 public:
  std::vector<BrokerTrade> trades;

  std::string ServerVersion() const override { return "fake/1.0"; }
  std::vector<GatewayAccount> Accounts() const override { return {}; }
  SubmitAck SubmitOrder(const SubmitRequest& req) override {
    SubmitAck ack;
    ack.request_id = req.request_id;
    ack.client_order_id = req.client_order_id;
    ack.accepted = true;
    ack.status = OrderStatus::kPendingNew;
    return ack;
  }
  bool CancelOrder(const std::string&, std::string*) override { return true; }
  std::vector<BrokerOrder> QueryOrders(const std::string&) override { return {}; }
  std::vector<BrokerTrade> QueryTrades(const std::string&) override { return trades; }
  AccountSnapshot QueryAccount(const std::string&) override { return AccountSnapshot(); }
  SecurityInfo QuerySecurityInfo(const std::string&, const std::string&) override {
    return SecurityInfo();
  }
  void SetSink(CounterEventSink*) override {}
  void Start() override {}
  void Stop() override {}
};

void TestUnitConversions() {
  int64_t v = 0;
  CHECK(DecimalYuanToMicro("20.8", &v) && v == 208000);
  CHECK(DecimalYuanToMicro("20", &v) && v == 200000);
  CHECK(DecimalYuanToMicro("0.0001", &v) && v == 1);
  CHECK(DecimalYuanToMicro("199.9", &v) && v == 1999000);
  CHECK(!DecimalYuanToMicro("20.00001", &v));  // finer than 1e-4
  CHECK(!DecimalYuanToMicro("abc", &v));
  CHECK(!DecimalYuanToMicro("", &v));
  CHECK(!DecimalYuanToMicro("20.", &v));

  CHECK(DoubleYuanToMicro(199.9, &v) && v == 1999000);
  CHECK(DoubleYuanToMicro(0.0001, &v) && v == 1);
  CHECK(!DoubleYuanToMicro(-1.0, &v));

  CHECK(Price6ToMicro(200000000) == 2000000);  // 200.000000 -> 200.0000
}

void TestStatusMapping() {
  CHECK(MapAtpStatus(9) == OrderStatus::kPending);
  CHECK(MapAtpStatus(10) == OrderStatus::kUnsend);
  CHECK(MapAtpStatus(0) == OrderStatus::kPendingNew);
  CHECK(MapAtpStatus(1) == OrderStatus::kPartiallyFilled);
  CHECK(MapAtpStatus(2) == OrderStatus::kFilled);
  CHECK(MapAtpStatus(11) == OrderStatus::kWaitCancel);
  CHECK(MapAtpStatus(4) == OrderStatus::kCanceled);
  CHECK(MapAtpStatus(8) == OrderStatus::kRejected);
  CHECK(MapAtpStatus(999) == OrderStatus::kUnspecified);

  ClearKdvipStatusMap();
  CHECK(MapKdvipStatus('6') == OrderStatus::kUnspecified);  // no guessed code table
  SetKdvipStatusMap({{'6', OrderStatus::kFilled}});
  CHECK(MapKdvipStatus('6') == OrderStatus::kFilled);
  ClearKdvipStatusMap();
  CHECK(MapKdvipStatus('6') == OrderStatus::kUnspecified);
}

void TestReportRing() {
  ReportRing ring(3);
  for (int i = 0; i < 4; ++i) {
    ReportEvent ev;
    ev.client_order_id = std::to_string(i);
    CHECK(ring.Push(ev) == i + 1);
  }
  CHECK(ring.Cursor() == 4);
  std::vector<ReportEvent> out;
  std::string error;
  CHECK(ring.Snapshot(2, &out, &error));
  CHECK(out.size() == 3 && out.front().seq == 2 && out.back().seq == 4);
  CHECK(!ring.Snapshot(1, &out, &error) && !error.empty());
  CHECK(ring.Snapshot(5, &out, &error) && out.empty());
}

CounterEvent FillEvent(const std::string& cid, int64_t filled, bool query_ok) {
  CounterEvent ev;
  ev.kind = CounterEvent::Kind::kOrder;
  ev.order.client_order_id = cid;
  ev.order.exchange_order_id = "o1";
  ev.order.status = OrderStatus::kFilled;
  ev.order.filled_qty = filled;
  ev.query_ok = query_ok;
  ev.raw_status = "2";
  return ev;
}

void TestGatewayFlow() {
  FakeAdapter adapter;
  BrokerTrade t;
  t.trade_id = "7";
  t.client_order_id = "c1";
  t.exchange_order_id = "o1";
  t.price_u = 1999000;
  t.qty = 100;
  adapter.trades.push_back(t);

  Gateway gw(&adapter, 16);
  SubmitRequest req;
  req.client_order_id = "c1";
  req.price_u = 1999000;
  req.qty = 100;
  req.tif = TimeInForce::kDay;
  gw.Remember(req);

  const int64_t before = gw.Cursor();
  CHECK(before == 0);
  gw.OnCounterEvent(FillEvent("c1", 100, true));
  CHECK(gw.Cursor() == 2);  // trade report first, then order report

  std::vector<ReportEvent> replay;
  std::string error;
  CHECK(gw.Replay(before, &replay, &error));
  CHECK(replay.size() == 2);
  CHECK(replay[0].seq == 1 && replay[0].kind == ReportKind::kTrade);
  CHECK(replay[1].seq == 2 && replay[1].kind == ReportKind::kOrder);
  CHECK(replay[1].order.price_u == 1999000 && replay[1].order.qty == 100);
  CHECK(replay[1].order.tif == TimeInForce::kDay);

  // Repeated cumulative fill: no second trade report, one more order report.
  gw.OnCounterEvent(FillEvent("c1", 100, true));
  CHECK(gw.Cursor() == 3);
  CHECK(gw.Replay(2, &replay, &error) && replay.size() == 1);
  CHECK(replay[0].kind == ReportKind::kOrder);

  // Failed trade query must not advance the watermark.
  gw.OnCounterEvent(FillEvent("c1", 200, false));
  CHECK(gw.Cursor() == 4);
  CHECK(gw.Replay(3, &replay, &error) && replay.size() == 1 &&
        replay[0].kind == ReportKind::kOrder);

  // Next event retries and publishes the new execution once.
  BrokerTrade t2 = t;
  t2.trade_id = "8";
  adapter.trades.push_back(t2);
  gw.OnCounterEvent(FillEvent("c1", 200, true));
  CHECK(gw.Cursor() == 6);
  CHECK(gw.Replay(4, &replay, &error) && replay.size() == 2);
  CHECK(replay[0].kind == ReportKind::kTrade && replay[0].trade.trade_id == "8");

  // Direct duplicate execution callback is deduplicated by trade_id.
  CounterEvent direct;
  direct.kind = CounterEvent::Kind::kTrade;
  direct.trade = t;  // trade_id "7" already emitted
  gw.OnCounterEvent(direct);
  CHECK(gw.Cursor() == 6);

  CHECK(!gw.Subscribe(999, &replay, &error));  // from_seq ahead of cursor
  CHECK(gw.Subscribe(0, &replay, &error) && replay.empty());  // 0 = live only
}

}  // namespace

int main() {
  TestUnitConversions();
  TestStatusMapping();
  TestReportRing();
  TestGatewayFlow();
  if (g_failures != 0) {
    std::cerr << g_failures << " check(s) failed" << std::endl;
    return 1;
  }
  std::cout << "ALL PASS: brokergw core (mapping / status / seq ring / gateway)" << std::endl;
  return 0;
}
