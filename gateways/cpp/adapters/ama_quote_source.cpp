#include "adapters/ama_quote_source.hpp"

#include <cstring>
#include <fstream>
#include <map>
#include <mutex>
#include <sstream>
#include <vector>

#include "brokergw/mapping.h"

namespace brokergw {
namespace {

namespace am = amd::ama;

std::map<std::string, std::string> ReadConfig(const std::string& path) {
  std::map<std::string, std::string> cfg;
  std::ifstream in(path.c_str());
  std::string line;
  while (std::getline(in, line)) {
    if (line.empty() || line[0] == '#') continue;
    const size_t eq = line.find('=');
    if (eq == std::string::npos) continue;
    std::string key = line.substr(0, eq);
    std::string val = line.substr(eq + 1);
    while (!key.empty() && (key.back() == ' ' || key.back() == '\r')) key.pop_back();
    while (!val.empty() && (val.front() == ' ' || val.front() == '\t')) val.erase(val.begin());
    while (!val.empty() && (val.back() == ' ' || val.back() == '\r')) val.pop_back();
    cfg[key] = val;
  }
  return cfg;
}

std::string Get(const std::map<std::string, std::string>& cfg, const char* key,
                const std::string& fallback = "") {
  auto it = cfg.find(key);
  return it == cfg.end() ? fallback : it->second;
}

std::string MarketName(int32_t market_type) {
  if (market_type == am::MarketType::kSSE) return "SH";
  if (market_type == am::MarketType::kSZSE) return "SZ";
  return "MKT-" + std::to_string(market_type);
}

std::string Key(const std::string& market, const std::string& symbol) {
  return market + "/" + symbol;
}

void SplitSymbols(const std::string& text,
                  std::vector<std::pair<int32_t, std::string>>* out) {
  size_t start = 0;
  while (start < text.size()) {
    const size_t comma = text.find(',', start);
    const std::string token = text.substr(start, comma == std::string::npos
                                                     ? std::string::npos
                                                     : comma - start);
    const size_t colon = token.find(':');
    if (colon != std::string::npos) {
      const std::string m = token.substr(0, colon);
      const std::string code = token.substr(colon + 1);
      const int32_t market = m == "SH" || m == "SSE" ? am::MarketType::kSSE
                                                     : am::MarketType::kSZSE;
      if (!code.empty()) out->push_back(std::make_pair(market, code));
    } else if (!token.empty()) {
      out->push_back(std::make_pair(am::MarketType::kSSE, token));
    }
    if (comma == std::string::npos) break;
    start = comma + 1;
  }
}

}  // namespace

AmaQuoteSource::AmaQuoteSource(const std::string& cfg_path) : cfg_path_(cfg_path) {}
AmaQuoteSource::~AmaQuoteSource() { Stop(); }

bool AmaQuoteSource::Start() {
  const auto cfg = ReadConfig(cfg_path_);
  am::AMDProperty props;
  // 与 demo 一致：TCP 关键线程绑核、启用 dedup；不设置时部分环境数据不下发。
  const std::string affinity = Get(cfg, "tcp_thread_affinity", "0,1");
  props.SetValue(am::property::kTCPThreadAffinity, affinity.c_str());
  am::IAMDApi::SetProperty(&props);

  am::Cfg c;
  c.channel_mode = am::ChannelMode::kTCP;
  c.ha_mode = am::HighAvailableMode::kMasterSlaveA;
  c.min_log_level = am::LogLevel::kInfo;
  c.is_thread_safe = true;
  c.is_subscribe_full = false;
  c.enable_order_book = am::OrderBookType::kNone;
  const std::string user = Get(cfg, "username", "amatest");
  const std::string pass = Get(cfg, "password", "amatest");
  std::strncpy(c.username, user.c_str(), sizeof(c.username) - 1);
  std::strncpy(c.password, pass.c_str(), sizeof(c.password) - 1);

  int idx = 0;
  for (int i = 1; i <= 8; ++i) {
    const std::string key = "ums_server_" + std::to_string(i);
    const std::string endpoint = Get(cfg, key.c_str());
    if (endpoint.empty()) continue;
    const size_t colon = endpoint.rfind(':');
    if (colon == std::string::npos) continue;
    std::strncpy(c.ums_servers[idx].local_ip, "0.0.0.0",
                 sizeof(c.ums_servers[idx].local_ip) - 1);
    const std::string ip = endpoint.substr(0, colon);
    std::strncpy(c.ums_servers[idx].server_ip, ip.c_str(),
                 sizeof(c.ums_servers[idx].server_ip) - 1);
    c.ums_servers[idx].server_port =
        static_cast<uint16_t>(std::strtoul(endpoint.substr(colon + 1).c_str(), nullptr, 10));
    ++idx;
  }
  if (idx == 0) {
    // Demo defaults for the broker test environment.
    std::strncpy(c.ums_servers[0].local_ip, "0.0.0.0", sizeof(c.ums_servers[0].local_ip) - 1);
    std::strncpy(c.ums_servers[0].server_ip, "172.16.109.14",
                 sizeof(c.ums_servers[0].server_ip) - 1);
    c.ums_servers[0].server_port = 8200;
    std::strncpy(c.ums_servers[1].local_ip, "0.0.0.0", sizeof(c.ums_servers[1].local_ip) - 1);
    std::strncpy(c.ums_servers[1].server_ip, "172.16.109.15",
                 sizeof(c.ums_servers[1].server_ip) - 1);
    c.ums_servers[1].server_port = 8200;
    idx = 2;
  }
  c.ums_server_cnt = static_cast<uint32_t>(idx);

  if (am::IAMDApi::Init(this, c) != 0) return false;
  started_ = true;

  std::vector<std::pair<int32_t, std::string>> symbols;
  SplitSymbols(Get(cfg, "symbols", "SH:600000,SZ:000001"), &symbols);
  if (!symbols.empty()) {
    // 券商联调环境的 amatest 账号只有「按品种订阅」权限（厂商 demo 用
    // SubscribeCategoryItem），按权限订阅 SubscribeItem 拿不到快照。
    const uint64_t data_type = am::SubscribeSecuDataType::kSnapshot;
    const uint64_t category = am::SubscribeCategoryType::kStock |
                              am::SubscribeCategoryType::kFund |
                              am::SubscribeCategoryType::kBond |
                              am::SubscribeCategoryType::kIndex;
    std::vector<am::SubscribeCategoryItem> items(symbols.size());
    for (size_t i = 0; i < symbols.size(); ++i) {
      std::memset(&items[i], 0, sizeof(items[i]));
      items[i].market = symbols[i].first;
      items[i].data_type = data_type;
      items[i].category_type = category;
      std::strncpy(items[i].security_code, symbols[i].second.c_str(),
                   sizeof(items[i].security_code) - 1);
    }
    am::IAMDApi::SubscribeData(am::SubscribeType::kAdd, items.data(),
                               static_cast<uint32_t>(items.size()));
  }

  // Code table supplies price_tick; configure via symbols= list.
  am::CodeTableRecordList list;
  list.list_nums = 0;
  list.records = nullptr;
  if (am::IAMDApi::GetCodeTableList(list) && list.records != nullptr) {
    std::lock_guard<std::mutex> lk(mu_);
    for (uint32_t i = 0; i < list.list_nums; ++i) {
      const am::CodeTableRecord& r = list.records[i];
      SecurityInfo& info = cache_[Key(MarketName(r.market_type), r.security_code)];
      info.market = MarketName(r.market_type);
      info.symbol = r.security_code;
      info.price_tick_u = Price6ToMicro(r.price_tick);
    }
    am::IAMDApi::FreeMemory(list.records);
  }
  return true;
}

void AmaQuoteSource::Stop() {
  if (started_) {
    am::IAMDApi::Join();
    am::IAMDApi::Release();
    started_ = false;
  }
}

void AmaQuoteSource::OnMDSnapshot(am::MDSnapshot* snapshots, uint32_t cnt) {
  if (snapshots == nullptr) return;
  {
    std::lock_guard<std::mutex> lk(mu_);
    for (uint32_t i = 0; i < cnt; ++i) {
      const am::MDSnapshot& s = snapshots[i];
      const std::string market = MarketName(s.market_type);
      const std::string symbol = s.security_code;
      SecurityInfo& info = cache_[Key(market, symbol)];
      info.market = market;
      info.symbol = symbol;
      info.limit_up_u = Price6ToMicro(s.high_limited);
      info.limit_down_u = Price6ToMicro(s.low_limited);
      // 沪市第0位 P / 深市第0位 H 表示停牌；instrument_status 也做兜底。
      const char phase = s.trading_phase_code[0];
      const char inst = s.instrument_status[0];
      info.trading_status = (phase == 'P' || phase == 'H' || inst == '1' ||
                             std::strstr(s.instrument_status, "HALT") != nullptr)
                                ? TradingStatus::kSuspended
                                : TradingStatus::kNormal;
      if (info.lot_size == 0) info.lot_size = 100;
      std::ostringstream ext;
      ext << "{\"orig_time\":\"" << s.orig_time << "\",\"phase\":\""
          << s.trading_phase_code << "\",\"last_price_u\":"
          << Price6ToMicro(s.last_price) << "}";
      info.ext_json = ext.str();
    }
  }
  am::IAMDApi::FreeMemory(snapshots);
}

SecurityInfo AmaQuoteSource::Lookup(const std::string& market,
                                    const std::string& symbol) {
  std::lock_guard<std::mutex> lk(mu_);
  auto it = cache_.find(Key(market, symbol));
  if (it == cache_.end()) {
    SecurityInfo empty;
    empty.market = market;
    empty.symbol = symbol;
    empty.trading_status = TradingStatus::kUnspecified;
    empty.ext_json = "{\"ama\":\"no snapshot yet\"}";
    return empty;
  }
  return it->second;
}

}  // namespace brokergw
