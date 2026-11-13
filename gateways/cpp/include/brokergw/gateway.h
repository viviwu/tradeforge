// Gateway core: ack/terminal-report separation, gateway-local seq, trade
// dedup and query-based recovery. This mirrors internal/brokergw/service.go
// and contains no SDK / gRPC dependency.
#pragma once

#include <cstddef>
#include <cstdint>
#include <map>
#include <mutex>
#include <set>
#include <string>
#include <vector>

#include "brokergw/adapter.h"
#include "brokergw/report_ring.h"
#include "brokergw/types.h"

namespace brokergw {

struct FillBook {
  int64_t reported_filled = 0;   // cumulative filled qty watermark
  std::set<std::string> emitted; // trade_id already published
};

class Gateway : public CounterEventSink {
 public:
  Gateway(ICounterAdapter* adapter, size_t ring_capacity = 4096);

  // Called by the single consumer thread fed from SDK callback queues.
  void OnCounterEvent(const CounterEvent& ev) override;

  // Replay buffered events with seq > from_seq (0 = from the beginning).
  // Used by the gRPC layer for reconnect replay and by core tests.
  bool Replay(int64_t from_seq, std::vector<ReportEvent>* replay,
              std::string* error);

  // from_seq == 0 means "live from current cursor" (no replay).
  bool Subscribe(int64_t from_seq, std::vector<ReportEvent>* replay,
                 std::string* error);
  int64_t Cursor() const;

  // Submit-time static fields (price/qty/tif/created) for full order reports.
  void Remember(const SubmitRequest& req);

 private:
  void Publish(ReportEvent ev);
  void ResolveStaticFields(BrokerOrder* order) const;
  void EmitTradesForFill(const CounterEvent& ev);

  ICounterAdapter* adapter_;
  ReportRing ring_;
  mutable std::mutex mu_;
  std::map<std::string, BrokerOrder> details_;
  std::map<std::string, FillBook> fills_;
};

}  // namespace brokergw
