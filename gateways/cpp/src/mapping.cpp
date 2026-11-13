#include "brokergw/mapping.h"

#include <cmath>
#include <limits>
#include <map>

namespace brokergw {
namespace {

constexpr int64_t kMicroPerYuan = 10000;

std::map<char, OrderStatus>& KdvipMap() {
  static std::map<char, OrderStatus> m;
  return m;
}

}  // namespace

bool DecimalYuanToMicro(const std::string& text, int64_t* out) {
  if (out == nullptr || text.empty()) return false;
  size_t i = 0;
  if (text[0] == '+') i = 1;
  if (i >= text.size()) return false;

  int64_t whole = 0;
  size_t whole_digits = 0;
  while (i < text.size() && text[i] >= '0' && text[i] <= '9') {
    const int digit = text[i] - '0';
    if (whole > (std::numeric_limits<int64_t>::max() - digit) / 10) return false;
    whole = whole * 10 + digit;
    ++i;
    ++whole_digits;
  }
  if (whole_digits == 0) return false;

  int64_t frac = 0;
  int frac_digits = 0;
  if (i < text.size() && text[i] == '.') {
    ++i;
    while (i < text.size() && text[i] >= '0' && text[i] <= '9') {
      if (frac_digits == 4) return false;  // finer than 0.0001 yuan
      frac = frac * 10 + (text[i] - '0');
      ++frac_digits;
      ++i;
    }
  }
  if (i != text.size() || frac_digits == 0 && text.back() == '.') return false;
  for (int n = frac_digits; n < 4; ++n) frac *= 10;
  if (whole > (std::numeric_limits<int64_t>::max() - frac) / kMicroPerYuan) return false;
  *out = whole * kMicroPerYuan + frac;
  return true;
}

bool DoubleYuanToMicro(double price, int64_t* out) {
  if (out == nullptr || !std::isfinite(price) || price < 0.0) return false;
  const long double scaled = static_cast<long double>(price) * kMicroPerYuan;
  if (scaled > static_cast<long double>(std::numeric_limits<int64_t>::max())) return false;
  const long double rounded = std::round(scaled);
  // ATP declares N13(4): reject anything that is not representable in 1e-4.
  if (std::fabs(scaled - rounded) > 1e-6L) return false;
  *out = static_cast<int64_t>(rounded);
  return true;
}

int64_t Price6ToMicro(int64_t price6) { return price6 / 100; }

OrderStatus MapAtpStatus(int code) {
  switch (code) {
    case 9:  return OrderStatus::kPending;           // kUnSend 未申报
    case 10: return OrderStatus::kUnsend;            // kSended 正报
    case 0:  return OrderStatus::kPendingNew;        // kNew 已申报
    case 1:  return OrderStatus::kPartiallyFilled;
    case 2:  return OrderStatus::kFilled;
    case 11:
    case 12: return OrderStatus::kWaitCancel;
    case 4:
    case 92: return OrderStatus::kCanceled;
    case 3:
    case 5:  return OrderStatus::kCanceled;          // 原值进 ext_json
    case 8:
    case 90: return OrderStatus::kRejected;
    default: return OrderStatus::kUnspecified;
  }
}

void SetKdvipStatusMap(const std::vector<std::pair<char, OrderStatus>>& entries) {
  KdvipMap().clear();
  for (const auto& e : entries) KdvipMap()[e.first] = e.second;
}

OrderStatus MapKdvipStatus(char code) {
  const auto it = KdvipMap().find(code);
  return it == KdvipMap().end() ? OrderStatus::kUnspecified : it->second;
}

void ClearKdvipStatusMap() { KdvipMap().clear(); }

const char* StatusName(OrderStatus status) {
  switch (status) {
    case OrderStatus::kUnspecified:      return "UNSPECIFIED";
    case OrderStatus::kPending:          return "PENDING";
    case OrderStatus::kUnsend:           return "UNSEND";
    case OrderStatus::kPendingNew:       return "PENDING_NEW";
    case OrderStatus::kAccepted:         return "ACCEPTED";
    case OrderStatus::kPartiallyFilled:  return "PARTIALLY_FILLED";
    case OrderStatus::kFilled:           return "FILLED";
    case OrderStatus::kWaitCancel:       return "WAIT_CANCEL";
    case OrderStatus::kCanceled:         return "CANCELED";
    case OrderStatus::kRejected:         return "REJECTED";
  }
  return "UNSPECIFIED";
}

}  // namespace brokergw
