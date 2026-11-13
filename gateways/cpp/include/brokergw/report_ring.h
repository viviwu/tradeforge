// In-memory session replay window for ReportEvent seq, mirroring the Go stub.
#pragma once

#include <cstddef>
#include <cstdint>
#include <deque>
#include <string>
#include <vector>

#include "brokergw/types.h"

namespace brokergw {

class ReportRing {
 public:
  explicit ReportRing(size_t capacity);

  // Assigns the next seq (1-based) and keeps the newest `capacity` events.
  int64_t Push(ReportEvent ev);
  int64_t Cursor() const;

  // Replays events with seq >= from. Returns false when `from` is older than
  // the buffered window (ResourceExhausted in gRPC terms); callers re-Attach.
  bool Snapshot(int64_t from, std::vector<ReportEvent>* out, std::string* error) const;

 private:
  size_t capacity_;
  int64_t seq_ = 0;
  std::deque<ReportEvent> buf_;
};

}  // namespace brokergw
