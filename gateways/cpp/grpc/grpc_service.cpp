#include "grpc/grpc_service.h"

#include <chrono>
#include <thread>

namespace brokergw {
namespace {

int64_t NowUnixNano() {
  using namespace std::chrono;
  return duration_cast<nanoseconds>(system_clock::now().time_since_epoch()).count();
}

bg::Side ToProtoSide(Side s) { return static_cast<bg::Side>(s); }
bg::OrderType ToProtoOrderType(OrderType t) { return static_cast<bg::OrderType>(t); }
bg::TimeInForce ToProtoTif(TimeInForce t) { return static_cast<bg::TimeInForce>(t); }
bg::OrderStatus ToProtoStatus(OrderStatus s) { return static_cast<bg::OrderStatus>(s); }
bg::TradingStatus ToProtoTrading(TradingStatus s) {
  return static_cast<bg::TradingStatus>(s);
}

}  // namespace

bg::BrokerOrder ToProtoOrder(const BrokerOrder& o) {
  bg::BrokerOrder out;
  out.set_client_order_id(o.client_order_id);
  out.set_exchange_order_id(o.exchange_order_id);
  out.set_fund_account(o.fund_account);
  out.set_trade_account(o.trade_account);
  out.set_market(o.market);
  out.set_symbol(o.symbol);
  out.set_side(ToProtoSide(o.side));
  out.set_order_type(ToProtoOrderType(o.order_type));
  out.set_tif(ToProtoTif(o.tif));
  out.set_price_u(o.price_u);
  out.set_qty(o.qty);
  out.set_filled_qty(o.filled_qty);
  out.set_avg_fill_price_u(o.avg_fill_price_u);
  out.set_status(ToProtoStatus(o.status));
  out.set_reject_reason(o.reject_reason);
  out.set_created_unix_nano(o.created_unix_nano);
  out.set_updated_unix_nano(o.updated_unix_nano);
  out.set_ext_json(o.ext_json);
  return out;
}

bg::BrokerTrade ToProtoTrade(const BrokerTrade& t) {
  bg::BrokerTrade out;
  out.set_trade_id(t.trade_id);
  out.set_client_order_id(t.client_order_id);
  out.set_exchange_order_id(t.exchange_order_id);
  out.set_fund_account(t.fund_account);
  out.set_trade_account(t.trade_account);
  out.set_market(t.market);
  out.set_symbol(t.symbol);
  out.set_side(ToProtoSide(t.side));
  out.set_price_u(t.price_u);
  out.set_qty(t.qty);
  out.set_amount_u(t.amount_u);
  out.set_ts_unix_nano(t.ts_unix_nano);
  out.set_ext_json(t.ext_json);
  return out;
}

bg::AccountSnapshot ToProtoSnapshot(const AccountSnapshot& s) {
  bg::AccountSnapshot out;
  out.set_fund_account(s.fund_account);
  out.set_available_cash_u(s.available_cash_u);
  out.set_frozen_cash_u(s.frozen_cash_u);
  out.set_ts_unix_nano(s.ts_unix_nano);
  out.set_ext_json(s.ext_json);
  for (const auto& p : s.positions) {
    auto* bp = out.add_positions();
    bp->set_trade_account(p.trade_account);
    bp->set_market(p.market);
    bp->set_symbol(p.symbol);
    bp->set_qty(p.qty);
    bp->set_available_qty(p.available_qty);
    bp->set_avg_cost_u(p.avg_cost_u);
    bp->set_ext_json(p.ext_json);
  }
  return out;
}

bg::ReportEvent ToProtoReportEvent(const ReportEvent& ev) {
  bg::ReportEvent out;
  out.set_seq(ev.seq);
  out.set_client_order_id(ev.client_order_id);
  out.set_exchange_order_id(ev.exchange_order_id);
  switch (ev.kind) {
    case ReportKind::kOrder:
      out.mutable_order()->CopyFrom(ToProtoOrder(ev.order));
      break;
    case ReportKind::kTrade:
      out.mutable_trade()->CopyFrom(ToProtoTrade(ev.trade));
      break;
    case ReportKind::kAccount:
      out.mutable_account()->CopyFrom(ToProtoSnapshot(ev.account));
      break;
  }
  out.set_ext_json(ev.ext_json);
  return out;
}

GatewayServiceImpl::GatewayServiceImpl(Gateway* gateway, ICounterAdapter* adapter,
                                       std::string server_version)
    : gateway_(gateway),
      adapter_(adapter),
      server_version_(std::move(server_version)) {}

grpc::Status GatewayServiceImpl::Attach(grpc::ServerContext*,
                                        const bg::AttachRequest*,
                                        bg::AttachResponse* response) {
  response->set_server_version(server_version_);
  response->set_seq_cursor(gateway_->Cursor());
  for (const auto& a : adapter_->Accounts()) {
    auto* pa = response->add_accounts();
    pa->set_fund_account(a.fund_account);
    for (const auto& ta : a.trade_accounts) {
      auto* pta = pa->add_trade_accounts();
      pta->set_trade_account(ta.trade_account);
      pta->set_market(ta.market);
    }
  }
  return grpc::Status::OK;
}

grpc::Status GatewayServiceImpl::Heartbeat(grpc::ServerContext*,
                                           const bg::HeartbeatRequest* request,
                                           bg::HeartbeatResponse* response) {
  response->set_request_id(request->request_id());
  response->set_seq_cursor(gateway_->Cursor());
  response->set_server_time_unix_nano(NowUnixNano());
  return grpc::Status::OK;
}

grpc::Status GatewayServiceImpl::SubmitOrder(grpc::ServerContext*,
                                             const bg::SubmitOrderRequest* request,
                                             bg::SubmitOrderResponse* response) {
  SubmitRequest req;
  req.request_id = request->request_id();
  req.client_order_id = request->client_order_id();
  req.fund_account = request->fund_account();
  req.trade_account = request->trade_account();
  req.market = request->market();
  req.symbol = request->symbol();
  req.side = static_cast<Side>(request->side());
  req.order_type = static_cast<OrderType>(request->order_type());
  req.tif = static_cast<TimeInForce>(request->tif());
  req.price_u = request->price_u();
  req.qty = request->qty();
  req.ext_json = request->ext_json();

  gateway_->Remember(req);
  SubmitAck ack = adapter_->SubmitOrder(req);
  response->set_request_id(ack.request_id);
  response->set_client_order_id(ack.client_order_id);
  response->set_accepted(ack.accepted);
  response->set_reject_reason(ack.reject_reason);
  response->set_status(ToProtoStatus(ack.status));
  return grpc::Status::OK;
}

grpc::Status GatewayServiceImpl::CancelOrder(grpc::ServerContext*,
                                             const bg::CancelOrderRequest* request,
                                             bg::CancelOrderResponse* response) {
  std::string reason;
  bool accepted = adapter_->CancelOrder(request->client_order_id(), &reason);
  response->set_request_id(request->request_id());
  response->set_client_order_id(request->client_order_id());
  response->set_accepted(accepted);
  response->set_reject_reason(reason);
  response->set_status(accepted ? bg::ORDER_STATUS_WAIT_CANCEL
                                : bg::ORDER_STATUS_UNSPECIFIED);
  return grpc::Status::OK;
}

grpc::Status GatewayServiceImpl::QueryOrders(grpc::ServerContext*,
                                             const bg::QueryOrdersRequest* request,
                                             bg::QueryOrdersResponse* response) {
  const bg::QueryFilter& f = request->filter();
  const std::string cid = f.client_order_id();
  for (const auto& o : adapter_->QueryOrders(cid)) {
    if (!f.fund_account().empty() && o.fund_account != f.fund_account()) continue;
    if (!f.market().empty() && o.market != f.market()) continue;
    if (!f.symbol().empty() && o.symbol != f.symbol()) continue;
    response->add_orders()->CopyFrom(ToProtoOrder(o));
  }
  response->set_is_last(true);
  return grpc::Status::OK;
}

grpc::Status GatewayServiceImpl::QueryTrades(grpc::ServerContext*,
                                             const bg::QueryTradesRequest* request,
                                             bg::QueryTradesResponse* response) {
  const bg::QueryFilter& f = request->filter();
  const std::string cid = f.client_order_id();
  for (const auto& t : adapter_->QueryTrades(cid)) {
    if (!f.fund_account().empty() && t.fund_account != f.fund_account()) continue;
    if (!f.market().empty() && t.market != f.market()) continue;
    if (!f.symbol().empty() && t.symbol != f.symbol()) continue;
    response->add_trades()->CopyFrom(ToProtoTrade(t));
  }
  response->set_is_last(true);
  return grpc::Status::OK;
}

grpc::Status GatewayServiceImpl::QueryAccount(grpc::ServerContext*,
                                              const bg::QueryAccountRequest* request,
                                              bg::QueryAccountResponse* response) {
  response->mutable_snapshot()->CopyFrom(
      ToProtoSnapshot(adapter_->QueryAccount(request->fund_account())));
  return grpc::Status::OK;
}

grpc::Status GatewayServiceImpl::QuerySecurityInfo(
    grpc::ServerContext*, const bg::QuerySecurityInfoRequest* request,
    bg::QuerySecurityInfoResponse* response) {
  SecurityInfo info = adapter_->QuerySecurityInfo(request->market(), request->symbol());
  auto* pinfo = response->mutable_info();
  pinfo->set_market(info.market);
  pinfo->set_symbol(info.symbol);
  pinfo->set_trading_status(ToProtoTrading(info.trading_status));
  pinfo->set_price_tick_u(info.price_tick_u);
  pinfo->set_lot_size(info.lot_size);
  pinfo->set_limit_up_u(info.limit_up_u);
  pinfo->set_limit_down_u(info.limit_down_u);
  pinfo->set_max_buy_qty(info.max_buy_qty);
  pinfo->set_max_sell_qty(info.max_sell_qty);
  pinfo->set_ext_json(info.ext_json);
  return grpc::Status::OK;
}

grpc::Status GatewayServiceImpl::SubscribeReports(
    grpc::ServerContext* context, const bg::SubscribeReportsRequest* request,
    grpc::ServerWriter<bg::ReportEvent>* writer) {
  int64_t from = request->from_seq();
  if (from == 0) from = gateway_->Cursor();
  while (!context->IsCancelled()) {
    std::vector<ReportEvent> replay;
    std::string error;
    if (!gateway_->Replay(from, &replay, &error)) {
      return grpc::Status(grpc::StatusCode::RESOURCE_EXHAUSTED, error);
    }
    for (const auto& ev : replay) {
      if (!writer->Write(ToProtoReportEvent(ev))) return grpc::Status::OK;
      from = ev.seq;
    }
    std::this_thread::sleep_for(std::chrono::milliseconds(10));
  }
  return grpc::Status::OK;
}

}  // namespace brokergw
