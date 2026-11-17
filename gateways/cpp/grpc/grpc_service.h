// gRPC server layer: frozen broker-gateway.v1 contract <-> SDK-independent
// Gateway core. All vendor adapters plug in through ICounterAdapter.
#pragma once

#include <string>

#include <grpcpp/grpcpp.h>

#include "brokergateway/v1/brokergateway.grpc.pb.h"
#include "brokergateway/v1/brokergateway.pb.h"
#include "brokergw/adapter.h"
#include "brokergw/gateway.h"

namespace brokergw {

namespace bg = ::brokergateway::v1;

class GatewayServiceImpl final : public bg::BrokerGatewayService::Service {
 public:
  GatewayServiceImpl(Gateway* gateway, ICounterAdapter* adapter,
                     std::string server_version);

  grpc::Status Attach(grpc::ServerContext* context, const bg::AttachRequest* request,
                      bg::AttachResponse* response) override;
  grpc::Status Heartbeat(grpc::ServerContext* context, const bg::HeartbeatRequest* request,
                         bg::HeartbeatResponse* response) override;
  grpc::Status SubmitOrder(grpc::ServerContext* context,
                           const bg::SubmitOrderRequest* request,
                           bg::SubmitOrderResponse* response) override;
  grpc::Status CancelOrder(grpc::ServerContext* context,
                           const bg::CancelOrderRequest* request,
                           bg::CancelOrderResponse* response) override;
  grpc::Status QueryOrders(grpc::ServerContext* context,
                           const bg::QueryOrdersRequest* request,
                           bg::QueryOrdersResponse* response) override;
  grpc::Status QueryTrades(grpc::ServerContext* context,
                           const bg::QueryTradesRequest* request,
                           bg::QueryTradesResponse* response) override;
  grpc::Status QueryAccount(grpc::ServerContext* context,
                            const bg::QueryAccountRequest* request,
                            bg::QueryAccountResponse* response) override;
  grpc::Status QuerySecurityInfo(grpc::ServerContext* context,
                                 const bg::QuerySecurityInfoRequest* request,
                                 bg::QuerySecurityInfoResponse* response) override;
  grpc::Status SubscribeReports(grpc::ServerContext* context,
                                const bg::SubscribeReportsRequest* request,
                                grpc::ServerWriter<bg::ReportEvent>* writer) override;

 private:
  Gateway* gateway_;
  ICounterAdapter* adapter_;
  std::string server_version_;
};

// Conversion helpers (also used by tests).
bg::ReportEvent ToProtoReportEvent(const ReportEvent& ev);
bg::BrokerOrder ToProtoOrder(const BrokerOrder& o);
bg::BrokerTrade ToProtoTrade(const BrokerTrade& t);
bg::AccountSnapshot ToProtoSnapshot(const AccountSnapshot& s);

}  // namespace brokergw
