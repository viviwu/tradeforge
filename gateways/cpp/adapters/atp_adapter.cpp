#include "adapters/atp_adapter.hpp"

#include <chrono>
#include <cstdlib>
#include <fstream>
#include <sstream>
#include <thread>

#include "brokergw/mapping.h"

namespace brokergw {
namespace {

namespace aq = atp::quant_api;

std::string S(const char* p) { return p == nullptr ? std::string() : std::string(p); }

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

int64_t ToUnixNano(int64_t t) {
  if (t == 0) return 0;
  if (t < 100000000000LL) return t * 1000000000LL;   // seconds
  if (t < 100000000000000LL) return t * 1000000000LL / 1000;  // milliseconds
  return t;  // already nanoseconds
}

std::string MarketName(uint16_t id) {
  switch (id) {
    case aq::ATPMarketIDConst::kShangHai: return "SH";
    case aq::ATPMarketIDConst::kShenZhen: return "SZ";
    default: return "MKT-" + std::to_string(id);
  }
}

uint16_t MarketId(const std::string& market) {
  if (market == "SH") return aq::ATPMarketIDConst::kShangHai;
  return aq::ATPMarketIDConst::kShenZhen;
}

Side MapSide(char c) {
  return c == aq::ATPSideConst::kSell ? Side::kSell : Side::kBuy;
}

void MapOrderType(char c, OrderType* type, TimeInForce* tif) {
  *tif = TimeInForce::kDay;
  switch (c) {
    case 'b':  // best side: treat as market, differences go to ext_json
      *type = OrderType::kMarket;
      break;
    case 'd':  // IOC carried by order_type
      *type = OrderType::kMarket;
      *tif = TimeInForce::kIoc;
      break;
    case 'e':  // FOK carried by order_type
      *type = OrderType::kMarket;
      *tif = TimeInForce::kFok;
      break;
    default:
      *type = OrderType::kLimit;
      break;
  }
}

std::string ErrorText(const aq::ATPRspErrorInfo& info) {
  if (info.error_id == 0) return "";
  std::ostringstream os;
  os << "atp_error=" << info.error_id;
  const char* msg = info.GetErrorMsg();
  if (msg != nullptr && *msg != '\0') os << ":" << msg;
  return os.str();
}

}  // namespace

AtpAdapter::AtpAdapter(const std::string& properties_path)
    : properties_path_(properties_path) {}

AtpAdapter::~AtpAdapter() { Stop(); }

std::string AtpAdapter::ServerVersion() const { return "brokergw-cpp-atp/0.1"; }

void AtpAdapter::SetSink(CounterEventSink* sink) { sink_ = sink; }

int64_t AtpAdapter::NextRequestId() {
  std::lock_guard<std::mutex> lk(mu_);
  return next_request_id_++;
}

std::shared_ptr<AtpPending> AtpAdapter::Register(int64_t request_id) {
  auto p = std::make_shared<AtpPending>();
  std::lock_guard<std::mutex> lk(mu_);
  pending_[request_id] = p;
  return p;
}

AtpCallResult AtpAdapter::Wait(int64_t request_id, int timeout_ms) {
  std::shared_ptr<AtpPending> p;
  {
    std::lock_guard<std::mutex> lk(mu_);
    auto it = pending_.find(request_id);
    if (it == pending_.end()) {
      AtpCallResult r;
      r.error = "unregistered request_id";
      return r;
    }
    p = it->second;
  }
  std::future<AtpCallResult> f = p->promise.get_future();
  if (f.wait_for(std::chrono::milliseconds(timeout_ms)) != std::future_status::ready) {
    AtpCallResult r;
    r.error = "counter query timeout";
    return r;
  }
  return f.get();
}

void AtpAdapter::Resolve(int64_t request_id, AtpCallResult result) {
  std::shared_ptr<AtpPending> p;
  {
    std::lock_guard<std::mutex> lk(mu_);
    auto it = pending_.find(request_id);
    if (it == pending_.end()) return;
    p = it->second;
    pending_.erase(it);
  }
  try {
    p->promise.set_value(std::move(result));
  } catch (const std::future_error&) {
    // already satisfied; ignore
  }
}

std::string AtpAdapter::ClientOrderIdFrom(uint64_t batch_cl_ord_no,
                                          int64_t cl_ord_no) const {
  std::lock_guard<std::mutex> lk(mu_);
  auto it = cid_by_request_.find(static_cast<int64_t>(batch_cl_ord_no));
  if (it != cid_by_request_.end()) return it->second;
  it = cid_by_request_.find(cl_ord_no);
  if (it != cid_by_request_.end()) return it->second;
  auto jt = cid_by_cl_ord_no_.find(cl_ord_no);
  if (jt != cid_by_cl_ord_no_.end()) return jt->second;
  return "";
}

void AtpAdapter::EnqueueOrder(const BrokerOrder& order, const std::string& raw_status,
                              const std::string& raw_ext_json) {
  if (sink_ == nullptr) return;
  CounterEvent ev;
  ev.kind = CounterEvent::Kind::kOrder;
  ev.order = order;
  ev.raw_status = raw_status;
  ev.raw_ext_json = raw_ext_json;
  sink_->OnCounterEvent(ev);
}

void AtpAdapter::EnqueueTrade(const BrokerTrade& trade, const std::string& raw_ext_json) {
  if (sink_ == nullptr) return;
  CounterEvent ev;
  ev.kind = CounterEvent::Kind::kTrade;
  ev.trade = trade;
  ev.raw_ext_json = raw_ext_json;
  sink_->OnCounterEvent(ev);
}

void AtpAdapter::Start() {
  const auto cfg = ReadConfig(properties_path_);
  cust_id_ = Get(cfg, "cust_id");
  fund_account_ = Get(cfg, "fund_account", cust_id_);
  password_ = Get(cfg, "password");
  branch_id_ = Get(cfg, "branch_id");
  account_id_sh_ = Get(cfg, "account_id_sh");
  account_id_sz_ = Get(cfg, "account_id_sz");

  props_ = new aq::ATPProperties();
  props_->SetValue(aq::prop::kLocations, Get(cfg, "locations").c_str());
  props_->SetValue(aq::prop::kAgwUser, Get(cfg, "agw_user").c_str());
  props_->SetValue(aq::prop::kAgwPassword, Get(cfg, "agw_password").c_str());
  props_->SetValue(aq::prop::kOrderWay, Get(cfg, "order_way", "4").c_str());
  props_->SetValue(aq::prop::kClientFeatureCode, Get(cfg, "client_feature_code").c_str());
  props_->SetValue(aq::prop::kCallbackResourceMode, aq::ATPCallbackResourceModeConst::kLowLatencyMode);
  props_->SetValue(aq::prop::kRetransmitMode, aq::ATPRetransmitModeConst::kQuickMode);
  // The demo connects with encrypt mode; keep it as the default for仿真 parity.
  props_->SetValue(aq::prop::kEncryptSchema, aq::ATPEncryptModeConst::kEncryptMode);

  const aq::ATPErrorCodeType rc = aq::ATPQuantAPI::Init(props_);
  if (rc != aq::ATPErrorCode::kSuccess) {
    delete props_;
    props_ = nullptr;
    return;
  }
  api_ = new aq::ATPQuantAPI(this, props_);

#ifdef BROKERGW_WITH_AMA
  const std::string ama_cfg = Get(cfg, "ama_config");
  if (!ama_cfg.empty()) {
    quotes_.reset(new AmaQuoteSource(ama_cfg));
    quotes_->Start();
  }
#endif

  aq::ATPLoginProperty* login = aq::ATPLoginProperty::NewMessage();
  login->SetUserId(cust_id_.c_str());
  login->SetPassword(password_.c_str());
  login->SetLoginMode(aq::ATPLoginModeConst::kCustIDMode);
  login->SetConnectChannel(aq::ATPConnectChannelConst::kSoftChannel);
  api_->Login(login);
  aq::ATPLoginProperty::DeleteMessage(login);

  for (int i = 0; i < 50 && !logged_in_; ++i) {
    std::this_thread::sleep_for(std::chrono::milliseconds(100));
  }
}

void AtpAdapter::Stop() {
#ifdef BROKERGW_WITH_AMA
  quotes_.reset();
#endif
  if (api_ != nullptr) {
    api_->Logout();
    api_->Release();
    delete api_;
    api_ = nullptr;
  }
  aq::ATPQuantAPI::Stop();
  if (props_ != nullptr) {
    delete props_;
    props_ = nullptr;
  }
  logged_in_ = false;
}

std::vector<GatewayAccount> AtpAdapter::Accounts() const {
  std::lock_guard<std::mutex> lk(mu_);
  return accounts_;
}

SubmitAck AtpAdapter::SubmitOrder(const SubmitRequest& req) {
  SubmitAck ack;
  ack.request_id = req.request_id;
  ack.client_order_id = req.client_order_id;

  const int64_t price_u = req.price_u;  // already in 1e-4 yuan units
  const int64_t qty_u = req.qty;
  if (qty_u <= 0 || (req.order_type == OrderType::kLimit && price_u <= 0)) {
    ack.reject_reason = "invalid price or qty";
    return ack;
  }
  const uint16_t market = MarketId(req.market);
  const std::string account_id = req.market == "SH" ? account_id_sh_ : account_id_sz_;

  const int64_t rid = NextRequestId();
  Register(rid);
  {
    std::lock_guard<std::mutex> lk(mu_);
    cid_by_request_[rid] = req.client_order_id;
  }

  aq::ATPReqCashAuctionOrderMsg* msg =
      aq::ATPReqCashAuctionOrderMsg::NewMessage(aq::ATPBusinessTypeConst::kCashAuction);
  msg->SetCustId(cust_id_.c_str());
  msg->SetFundAccountId(req.fund_account.c_str());
  msg->SetBranchId(branch_id_.c_str());
  msg->SetSecurityId(req.symbol.c_str());
  msg->SetMarketId(market);
  msg->SetAccountId(account_id.c_str());
  msg->SetSide(req.side == Side::kSell ? aq::ATPSideConst::kSell : aq::ATPSideConst::kBuy);
  msg->SetOrderQty(static_cast<double>(qty_u));
  msg->SetPrice(static_cast<double>(price_u) / 10000.0);
  msg->SetOrderType(aq::ATPOrderTypeConst::kFixedNew);
  msg->SetPassword(password_.c_str());
  msg->SetBatchClOrdNo(static_cast<uint64_t>(rid));

  const aq::ATPErrorCodeType rc = api_->ReqCashAuctionOrder(msg, rid);
  aq::ATPReqCashAuctionOrderMsg::DeleteMessage(msg);
  if (rc != aq::ATPErrorCode::kSuccess) {
    AtpCallResult failure;
    failure.error = "counter rejected submit";
    Resolve(rid, std::move(failure));
    ack.reject_reason = "counter rejected submit";
    return ack;
  }

  AtpCallResult result = Wait(rid, 5000);
  ack.accepted = result.ok;
  ack.reject_reason = result.error;
  ack.status = result.ok ? OrderStatus::kPendingNew : OrderStatus::kRejected;
  return ack;
}

bool AtpAdapter::CancelOrder(const std::string& cid, std::string* reject_reason) {
  int64_t cl_ord_no = 0;
  {
    std::lock_guard<std::mutex> lk(mu_);
    for (const auto& kv : cid_by_cl_ord_no_) {
      if (kv.second == cid) {
        cl_ord_no = kv.first;
        break;
      }
    }
  }
  if (cl_ord_no == 0) {
    if (reject_reason != nullptr) *reject_reason = "unknown cl_ord_no";
    return false;
  }
  const int64_t rid = NextRequestId();
  aq::ATPReqCashCancelOrderMsg* msg = aq::ATPReqCashCancelOrderMsg::NewMessage();
  msg->SetCustId(cust_id_.c_str());
  msg->SetFundAccountId(fund_account_.c_str());
  msg->SetOrigClOrdNo(cl_ord_no);
  msg->SetPassword(password_.c_str());
  // demo 撤单固定填 1（原委托批号语义），不要用本次 request_id。
  msg->SetBatchClOrdNo(1);
  const aq::ATPErrorCodeType rc = api_->ReqCashCancelOrder(msg, rid);
  aq::ATPReqCashCancelOrderMsg::DeleteMessage(msg);
  if (rc != aq::ATPErrorCode::kSuccess) {
    if (reject_reason != nullptr) *reject_reason = "counter rejected cancel";
    return false;
  }
  // 契约语义：本调用只返回受理 ack；柜台终态（CANCELED/REJECTED）走回报流。
  return true;
}

std::vector<BrokerOrder> AtpAdapter::QueryOrders(const std::string& cid) {
  std::vector<BrokerOrder> all;
  // 沪深股东账户分别查询后合并（ATP 按市场分账户）。
  for (int m = 0; m < 2; ++m) {
    const bool sh = m == 1;
    const std::string account = sh ? account_id_sh_ : account_id_sz_;
    if (account.empty()) continue;
    const int64_t rid = NextRequestId();
    Register(rid);
    {
      std::lock_guard<std::mutex> lk(mu_);
      cid_by_request_[rid] = cid;
    }
    aq::ATPReqCashOrderQueryMsg* msg = aq::ATPReqCashOrderQueryMsg::NewMessage();
    msg->SetCustId(cust_id_.c_str());
    msg->SetFundAccountId(fund_account_.c_str());
    msg->SetBranchId(branch_id_.c_str());
    msg->SetPassword(password_.c_str());
    msg->SetReturnNum(0);
    msg->SetClOrdNo(0);
    msg->SetBatchClOrdNo(static_cast<uint64_t>(rid));  // echo back for cid mapping
    msg->SetMarketId(sh ? aq::ATPMarketIDConst::kShangHai
                        : aq::ATPMarketIDConst::kShenZhen);
    msg->SetAccountId(account.c_str());
    const aq::ATPErrorCodeType rc = api_->ReqCashOrderQuery(msg, rid);
    aq::ATPReqCashOrderQueryMsg::DeleteMessage(msg);
    if (rc != aq::ATPErrorCode::kSuccess) {
      AtpCallResult failure;
      failure.error = "counter rejected order query";
      Resolve(rid, std::move(failure));
    }
    AtpCallResult result = Wait(rid, 5000);
    for (auto& o : result.orders) {
      if (cid.empty() || o.client_order_id == cid) all.push_back(o);
    }
  }
  return all;
}

std::vector<BrokerTrade> AtpAdapter::QueryTrades(const std::string& cid) {
  std::vector<BrokerTrade> all;
  for (int m = 0; m < 2; ++m) {
    const bool sh = m == 1;
    const std::string account = sh ? account_id_sh_ : account_id_sz_;
    if (account.empty()) continue;
    const int64_t rid = NextRequestId();
    Register(rid);
    {
      std::lock_guard<std::mutex> lk(mu_);
      cid_by_request_[rid] = cid;
    }
    aq::ATPReqCashTradeOrderQueryMsg* msg = aq::ATPReqCashTradeOrderQueryMsg::NewMessage();
    msg->SetCustId(cust_id_.c_str());
    msg->SetFundAccountId(fund_account_.c_str());
    msg->SetPassword(password_.c_str());
    msg->SetReturnNum(0);
    msg->SetBatchClOrdNo(static_cast<uint64_t>(rid));  // echo back for cid mapping
    msg->SetMarketId(sh ? aq::ATPMarketIDConst::kShangHai
                        : aq::ATPMarketIDConst::kShenZhen);
    msg->SetAccountId(account.c_str());
    const aq::ATPErrorCodeType rc = api_->ReqCashTradeOrderQuery(msg, rid);
    aq::ATPReqCashTradeOrderQueryMsg::DeleteMessage(msg);
    if (rc != aq::ATPErrorCode::kSuccess) {
      AtpCallResult failure;
      failure.error = "counter rejected trade query";
      Resolve(rid, std::move(failure));
    }
    AtpCallResult result = Wait(rid, 5000);
    for (auto& t : result.trades) {
      if (cid.empty() || t.client_order_id == cid) all.push_back(t);
    }
  }
  return all;
}

AccountSnapshot AtpAdapter::QueryAccount(const std::string& fund_account) {
  const int64_t rid = NextRequestId();
  Register(rid);
  aq::ATPReqCashFundQueryMsg* msg = aq::ATPReqCashFundQueryMsg::NewMessage();
  msg->SetCustId(cust_id_.c_str());
  msg->SetFundAccountId(fund_account.empty() ? fund_account_.c_str() : fund_account.c_str());
  msg->SetBranchId(branch_id_.c_str());
  msg->SetPassword(password_.c_str());
  msg->SetMarketId(aq::ATPMarketIDConst::kShenZhen);
  msg->SetAccountId(account_id_sz_.c_str());
  const aq::ATPErrorCodeType rc = api_->ReqCashFundQuery(msg, rid);
  aq::ATPReqCashFundQueryMsg::DeleteMessage(msg);
  if (rc != aq::ATPErrorCode::kSuccess) {
    AtpCallResult failure;
    failure.error = "counter rejected fund query";
    Resolve(rid, failure);
  }
  AtpCallResult result = Wait(rid, 5000);
  result.snapshot.fund_account = fund_account.empty() ? fund_account_ : fund_account;
  if (result.snapshot.positions.empty()) {
    std::lock_guard<std::mutex> lk(mu_);
    result.snapshot.positions = positions_;
  }
  return result.snapshot;
}

SecurityInfo AtpAdapter::QuerySecurityInfo(const std::string& market,
                                           const std::string& symbol) {
#ifdef BROKERGW_WITH_AMA
  // AMA 行情只作为 QuerySecurityInfo 旁路数据源，不进回报流。
  if (quotes_ != nullptr) {
    SecurityInfo cached = quotes_->Lookup(market, symbol);
    if (cached.price_tick_u > 0) return cached;
  }
#endif
  const int64_t rid = NextRequestId();
  Register(rid);
  aq::ATPReqCashExtQuerySecurityInfoMsg* msg =
      aq::ATPReqCashExtQuerySecurityInfoMsg::NewMessage();
  msg->SetCustId(cust_id_.c_str());
  msg->SetFundAccountId(fund_account_.c_str());
  msg->SetPassword(password_.c_str());
  msg->SetSecurityId(symbol.c_str());
  msg->SetMarketId(MarketId(market));
  msg->SetAccountId(market == "SH" ? account_id_sh_.c_str() : account_id_sz_.c_str());
  const aq::ATPErrorCodeType rc = api_->ReqCashExtQuerySecurityInfo(msg, rid);
  aq::ATPReqCashExtQuerySecurityInfoMsg::DeleteMessage(msg);
  if (rc != aq::ATPErrorCode::kSuccess) {
    AtpCallResult failure;
    failure.error = "counter rejected security query";
    Resolve(rid, failure);
  }
  AtpCallResult result = Wait(rid, 5000);
  result.security.market = market;
  result.security.symbol = symbol;
  return result.security;
}

void AtpAdapter::OnLogin(const aq::ATPCustomerInfo& msg) {
  std::vector<GatewayAccount> accounts;
  const std::string cust = S(msg.GetCustId());
  for (uint32_t i = 0; i < msg.FundAccountArraySize(); ++i) {
    GatewayAccount ga;
    ga.fund_account = S(msg.FundAccountArray_GetFundAccountId(i));
    for (uint32_t j = 0; j < msg.FundAccountArray_AccountArraySize(i); ++j) {
      GatewayTradeAccount ta;
      ta.trade_account = S(msg.FundAccountArray_AccountArray_GetAccountId(i, j));
      ta.market = j == 0 ? "SZ" : "SH";
      ga.trade_accounts.push_back(ta);
    }
    accounts.push_back(ga);
  }
  {
    std::lock_guard<std::mutex> lk(mu_);
    accounts_ = accounts;
    cust_id_ = cust;
    logged_in_ = true;
  }
}

void AtpAdapter::OnRspCashAuctionOrder(const aq::ATPRspCashAuctionOrderMsg& msg,
                                       const aq::ATPRspErrorInfo& error_info,
                                       int64_t request_id) {
  const std::string cid = ClientOrderIdFrom(msg.GetBatchClOrdNo(), msg.GetClOrdNo());
  const std::string err = ErrorText(error_info);

  BrokerOrder order;
  order.client_order_id = cid;
  order.exchange_order_id = "";
  order.fund_account = S(msg.GetFundAccountId());
  order.trade_account = S(msg.GetAccountId());
  order.market = MarketName(msg.GetMarketId());
  order.symbol = S(msg.GetSecurityId());
  MapSide(msg.GetSide()) == Side::kSell ? order.side = Side::kSell : order.side = Side::kBuy;
  MapOrderType(msg.GetOrderType(), &order.order_type, &order.tif);
  int64_t price_u = 0;
  DoubleYuanToMicro(msg.GetPrice(), &price_u);
  order.price_u = price_u;
  order.qty = static_cast<int64_t>(msg.GetOrderQty());
  order.status = error_info.error_id == 0 ? OrderStatus::kPendingNew : OrderStatus::kRejected;
  order.reject_reason = err;
  order.created_unix_nano = ToUnixNano(msg.GetTransactTime());

  std::ostringstream ext;
  ext << "{\"cl_ord_no\":" << msg.GetClOrdNo();
  if (!err.empty()) ext << ",\"error\":\"" << err << "\"";
  ext << "}";

  {
    std::lock_guard<std::mutex> lk(mu_);
    if (!cid.empty()) {
      orders_[cid] = order;
      cid_by_cl_ord_no_[msg.GetClOrdNo()] = cid;
    }
  }
  AtpCallResult result;
  result.ok = error_info.error_id == 0;
  result.error = err;
  result.ack.request_id = request_id;
  result.ack.client_order_id = cid;
  result.ack.accepted = result.ok;
  result.ack.reject_reason = err;
  result.ack.status = order.status;
  Resolve(request_id, std::move(result));
  if (!cid.empty()) EnqueueOrder(order, "", ext.str());
}

void AtpAdapter::OnRtnCashAuctionOrder(const aq::ATPRtnCashAuctionOrderMsg& msg) {
  const std::string cid = ClientOrderIdFrom(msg.GetBatchClOrdNo(), msg.GetClOrdNo());
  if (cid.empty()) return;

  BrokerOrder order;
  order.client_order_id = cid;
  order.exchange_order_id = S(msg.GetOrderId());
  order.fund_account = S(msg.GetFundAccountId());
  order.trade_account = S(msg.GetAccountId());
  order.market = MarketName(msg.GetMarketId());
  order.symbol = S(msg.GetSecurityId());
  order.side = MapSide(msg.GetSide());
  order.status = MapAtpStatus(msg.GetOrdStatus());
  order.order_type = OrderType::kLimit;
  order.tif = TimeInForce::kDay;
  order.qty = static_cast<int64_t>(msg.GetOrderQty());
  order.filled_qty = static_cast<int64_t>(msg.GetCumQty());
  int64_t price_u = 0;
  DoubleYuanToMicro(msg.GetPrice(), &price_u);
  order.price_u = price_u;
  int64_t avg_u = 0;
  if (order.filled_qty > 0 && DoubleYuanToMicro(msg.GetLastPx(), &avg_u)) {
    order.avg_fill_price_u = avg_u;
  }
  order.updated_unix_nano = ToUnixNano(msg.GetTransactTime());
  order.reject_reason = S(msg.GetOrdRejReason());

  std::ostringstream ext;
  ext << "{\"cl_ord_no\":" << msg.GetClOrdNo()
      << ",\"cl_ord_id\":\"" << S(msg.GetClOrdId())
      << "\",\"exec_id\":\"" << S(msg.GetExecId())
      << "\",\"ord_sign\":" << static_cast<int>(msg.GetOrdSign());
  if (msg.GetRejectReasonCode() != 0) {
    ext << ",\"reject_code\":" << msg.GetRejectReasonCode();
  }
  ext << "}";

  {
    std::lock_guard<std::mutex> lk(mu_);
    orders_[cid] = order;
    cid_by_cl_ord_no_[msg.GetClOrdNo()] = cid;
  }
  EnqueueOrder(order, std::to_string(msg.GetOrdStatus()), ext.str());

  // ATP has no independent execution callback: derive a trade from the push.
  const std::string exec_id = S(msg.GetExecId());
  if (!exec_id.empty() && msg.GetLastQty() > 0) {
    BrokerTrade t;
    t.trade_id = exec_id;
    t.client_order_id = cid;
    t.exchange_order_id = order.exchange_order_id;
    t.fund_account = order.fund_account;
    t.trade_account = order.trade_account;
    t.market = order.market;
    t.symbol = order.symbol;
    t.side = order.side;
    int64_t last_px = 0;
    DoubleYuanToMicro(msg.GetLastPx(), &last_px);
    t.price_u = last_px;
    t.qty = static_cast<int64_t>(msg.GetLastQty());
    int64_t amount = 0;
    DoubleYuanToMicro(msg.GetTotalValueTraded(), &amount);
    t.amount_u = amount;
    t.ts_unix_nano = ToUnixNano(msg.GetTransactTime());
    EnqueueTrade(t, ext.str());
  }
}

void AtpAdapter::OnRspCashCancelOrder(const aq::ATPRspCashCancelOrderMsg& msg,
                                      const aq::ATPRspErrorInfo& error_info,
                                      int64_t request_id) {
  const std::string cid = ClientOrderIdFrom(msg.GetBatchClOrdNo(), msg.GetOrigClOrdNo());
  AtpCallResult result;
  result.ok = error_info.error_id == 0;
  result.error = ErrorText(error_info);
  result.cancel_ok = result.ok;
  // 回调回传的 request_id 可能因柜台而异；优先按 cid 反查本次等待的 pending。
  int64_t target = request_id;
  if (!cid.empty()) {
    std::lock_guard<std::mutex> lk(mu_);
    for (const auto& kv : cid_by_request_) {
      if (kv.second == cid && pending_.count(kv.first) > 0) {
        target = kv.first;
        break;
      }
    }
  }
  Resolve(target, std::move(result));
}

void AtpAdapter::OnRspCashOrderQueryResult(const aq::ATPRspCashOrderQueryResultMsg& msg,
                                           int64_t request_id,
                                           const aq::ATPRspErrorInfo& error_info,
                                           bool is_last) {
  const std::string cid = ClientOrderIdFrom(msg.GetBatchClOrdNo(), msg.GetClOrdNo());
  BrokerOrder o;
  o.client_order_id = cid;
  o.exchange_order_id = S(msg.GetOrderId());
  o.fund_account = S(msg.GetFundAccountId());
  o.trade_account = S(msg.GetAccountId());
  o.market = MarketName(msg.GetMarketId());
  o.symbol = S(msg.GetSecurityId());
  o.side = MapSide(msg.GetSide());
  MapOrderType(msg.GetOrdType(), &o.order_type, &o.tif);
  int64_t price = 0;
  DoubleYuanToMicro(msg.GetOrderPrice(), &price);
  o.price_u = price;
  o.qty = static_cast<int64_t>(msg.GetOrderQty());
  o.filled_qty = static_cast<int64_t>(msg.GetCumQty());
  int64_t avg = 0;
  DoubleYuanToMicro(msg.GetExecPrice(), &avg);
  o.avg_fill_price_u = avg;
  o.status = MapAtpStatus(msg.GetOrdStatus());
  o.reject_reason = S(msg.GetOrderStatusInfo());
  o.created_unix_nano = ToUnixNano(msg.GetTransactTime());
  std::ostringstream qext;
  qext << "{\"cl_ord_id\":\"" << S(msg.GetClOrdId())
       << "\",\"cl_ord_no\":" << msg.GetClOrdNo()
       << ",\"batch_cl_ord_no\":" << msg.GetBatchClOrdNo() << "}";
  o.ext_json = qext.str();

  AtpCallResult result;
  bool ready = false;
  {
    std::lock_guard<std::mutex> lk(mu_);
    if (!cid.empty()) {
      orders_[cid] = o;
      cid_by_cl_ord_no_[msg.GetClOrdNo()] = cid;
    }
    AtpCallResult& acc = accum_[request_id];
    acc.ok = error_info.error_id == 0;
    acc.error = ErrorText(error_info);
    acc.orders.push_back(o);
    if (is_last) {
      result = std::move(acc);
      accum_.erase(request_id);
      ready = true;
    }
  }
  if (ready) Resolve(request_id, std::move(result));
}

void AtpAdapter::OnRspCashTradeOrderQueryResult(
    const aq::ATPRspCashTradeOrderQueryResultMsg& msg, int64_t request_id,
    const aq::ATPRspErrorInfo& error_info, bool is_last) {
  const std::string cid = ClientOrderIdFrom(msg.GetBatchClOrdNo(), msg.GetClOrdNo());
  BrokerTrade t;
  t.trade_id = S(msg.GetExecId());
  t.client_order_id = cid;
  t.exchange_order_id = S(msg.GetOrderId());
  t.fund_account = S(msg.GetFundAccountId());
  t.trade_account = S(msg.GetAccountId());
  t.market = MarketName(msg.GetMarketId());
  t.symbol = S(msg.GetSecurityId());
  t.side = MapSide(msg.GetSide());
  int64_t px = 0;
  DoubleYuanToMicro(msg.GetLastPx(), &px);
  t.price_u = px;
  t.qty = static_cast<int64_t>(msg.GetLastQty());
  int64_t amount = 0;
  DoubleYuanToMicro(msg.GetTotalValueTraded(), &amount);
  t.amount_u = amount;
  t.ts_unix_nano = ToUnixNano(msg.GetTransactTime());

  AtpCallResult result;
  bool ready = false;
  {
    std::lock_guard<std::mutex> lk(mu_);
    if (!t.trade_id.empty()) trades_.push_back(t);
    AtpCallResult& acc = accum_[request_id];
    acc.ok = error_info.error_id == 0;
    acc.error = ErrorText(error_info);
    acc.trades.push_back(t);
    if (is_last) {
      result = std::move(acc);
      accum_.erase(request_id);
      ready = true;
    }
  }
  if (ready) Resolve(request_id, std::move(result));
}

void AtpAdapter::OnRspCashFundQueryResult(const aq::ATPRspCashFundQueryResultMsg& msg,
                                          int64_t request_id,
                                          const aq::ATPRspErrorInfo& error_info,
                                          bool is_last) {
  AtpCallResult result;
  bool ready = false;
  int64_t cash = 0;
  DoubleYuanToMicro(msg.GetAvailableT1(), &cash);
  int64_t frozen = 0;
  DoubleYuanToMicro(msg.GetFrozenAll(), &frozen);
  {
    std::lock_guard<std::mutex> lk(mu_);
    AtpCallResult& acc = accum_[request_id];
    acc.ok = error_info.error_id == 0;
    acc.error = ErrorText(error_info);
    // 资金查询按币种分页回调（CNY/USD/HKD），最后一页不是本币；只取 CNY。
    const bool cny = S(msg.GetCurrency()) == "CNY";
    if (cny || acc.snapshot.available_cash_u == 0) {
      if (cny || acc.snapshot.fund_account.empty()) {
        acc.snapshot.available_cash_u = cash;
        acc.snapshot.frozen_cash_u = frozen;
      }
    }
    acc.snapshot.fund_account = S(msg.GetFundAccountId());
    if (is_last) {
      result = std::move(acc);
      accum_.erase(request_id);
      ready = true;
    }
  }
  if (ready) Resolve(request_id, std::move(result));
}

void AtpAdapter::OnRspCashShareQueryResult(const aq::ATPRspCashShareQueryResultMsg& msg,
                                           int64_t request_id,
                                           const aq::ATPRspErrorInfo& error_info,
                                           bool is_last) {
  BrokerPosition p;
  p.symbol = S(msg.GetSecurityId());
  p.market = MarketName(msg.GetMarketId());
  p.trade_account = S(msg.GetAccountId());
  p.qty = static_cast<int64_t>(msg.GetLeavesQty());
  p.available_qty = static_cast<int64_t>(msg.GetAvailableQty());
  int64_t cost = 0;
  DoubleYuanToMicro(msg.GetCostPrice(), &cost);
  p.avg_cost_u = cost;
  AtpCallResult result;
  bool ready = false;
  {
    std::lock_guard<std::mutex> lk(mu_);
    positions_.push_back(p);
    AtpCallResult& acc = accum_[request_id];
    acc.ok = error_info.error_id == 0;
    acc.error = ErrorText(error_info);
    acc.snapshot.positions.push_back(p);
    if (is_last) {
      result = std::move(acc);
      accum_.erase(request_id);
      ready = true;
    }
  }
  if (ready) Resolve(request_id, std::move(result));
}

void AtpAdapter::OnRspCashExtQueryResultSecurityInfo(
    const aq::ATPRspCashExtQueryResultSecurityInfoMsg& msg, int64_t request_id,
    const aq::ATPRspErrorInfo& error_info, bool is_last) {
  SecurityInfo info;
  info.market = MarketName(msg.GetMarketId());
  info.symbol = S(msg.GetSecurityId());
  // 券商未披露 security_status 位义；默认正常，原值进 ext_json 待联调确认。
  info.trading_status = TradingStatus::kNormal;
  int64_t tick = 0;
  DoubleYuanToMicro(msg.GetPriceTick(), &tick);
  info.price_tick_u = tick;
  int64_t up = 0;
  DoubleYuanToMicro(msg.GetPriceUpperLimit(), &up);
  info.limit_up_u = up;
  int64_t down = 0;
  DoubleYuanToMicro(msg.GetPriceLowerLimit(), &down);
  info.limit_down_u = down;
  int64_t lot = static_cast<int64_t>(msg.GetBuyQtyUnit());
  info.lot_size = lot > 0 ? lot : 100;
  info.max_buy_qty = static_cast<int64_t>(msg.GetBuyQtyUpperLimit());
  info.max_sell_qty = static_cast<int64_t>(msg.GetSellQtyUpperLimit());
  std::ostringstream ext;
  ext << "{\"security_type\":" << msg.GetSecurityType()
      << ",\"security_status\":" << msg.GetSecurityStatus()
      << ",\"prev_close\":" << msg.GetPrevClosePrice() << "}";
  info.ext_json = ext.str();

  AtpCallResult result;
  result.ok = error_info.error_id == 0;
  result.error = ErrorText(error_info);
  result.security = info;
  if (is_last) Resolve(request_id, std::move(result));
}

}  // namespace brokergw
