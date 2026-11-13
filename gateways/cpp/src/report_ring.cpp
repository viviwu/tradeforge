#include "brokergw/report_ring.h"

namespace brokergw {

ReportRing::ReportRing(size_t capacity) : capacity_(capacity == 0 ? 4096 : capacity) {}

int64_t ReportRing::Push(ReportEvent ev) {
  ++seq_;
  ev.seq = seq_;
  buf_.push_back(ev);
  while (buf_.size() > capacity_) buf_.pop_front();
  return seq_;
}

int64_t ReportRing::Cursor() const { return seq_; }

bool ReportRing::Snapshot(int64_t from, std::vector<ReportEvent>* out,
                          std::string* error) const {
  if (out == nullptr) return false;
  out->clear();
  if (from > seq_) return true;  // nothing published after `from` yet
  if (buf_.empty() || from < buf_.front().seq) {
    if (error != nullptr) {
      *error = "seq window expired: earliest buffered event seq is " +
               (buf_.empty() ? std::string("0")
                             : std::to_string(buf_.front().seq));
    }
    return false;
  }
  for (const auto& ev : buf_) {
    if (ev.seq >= from) out->push_back(ev);
  }
  return true;
}

}  // namespace brokergw
