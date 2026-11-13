// Unit conversion and counter status mapping. Pure C++ / no SDK dependency.
#pragma once

#include <cstdint>
#include <string>
#include <utility>
#include <vector>

#include "brokergw/types.h"

namespace brokergw {

// kdvip: prices are char[21] decimal strings in yuan ("20.8" -> 208000).
// Fixed-point parsing, never atof:金额的浮点误差会破坏契约的 int64 口径。
bool DecimalYuanToMicro(const std::string& text, int64_t* out);

// ATP: price is a double in yuan with N13(4); reject non-finite / out-of-range.
bool DoubleYuanToMicro(double price, int64_t* out);

// AMA: Price6 = yuan * 1e6, contract unit = yuan * 1e4 -> divide by 100.
int64_t Price6ToMicro(int64_t price6);

// ATP status constants from atp_quant_constant.h:1772-1804.
OrderStatus MapAtpStatus(int code);

// kdvip status dictionary is NOT present in the SDK headers/demo: by default
// this returns kUnspecified and leaves the raw code for ext_json. Inject the
// confirmed mapping at integration time instead of guessing code values.
void SetKdvipStatusMap(const std::vector<std::pair<char, OrderStatus>>& entries);
OrderStatus MapKdvipStatus(char code);
void ClearKdvipStatusMap();

const char* StatusName(OrderStatus status);

}  // namespace brokergw
