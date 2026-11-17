// Executable entry for the C++ BrokerGateway adapter process.
// Adapter is selected at runtime; only the built-in mock is available in the
// default build, real broker adapters require BROKERGW_WITH_*=ON on Linux x86_64.
#include <cstdlib>
#include <iostream>
#include <memory>
#include <string>

#include <grpcpp/server_builder.h>

#include "brokergw/gateway.h"
#include "brokergw/mock_adapter.h"
#include "grpc/grpc_service.h"

#ifdef BROKERGW_WITH_ATP
#include "adapters/atp_adapter.hpp"
#endif

namespace {

std::string Arg(int argc, char** argv, const std::string& key,
                const std::string& fallback) {
  for (int i = 1; i + 1 < argc; ++i) {
    if (key == argv[i]) return argv[i + 1];
  }
  return fallback;
}

}  // namespace

int main(int argc, char** argv) {
  const std::string addr = Arg(argc, argv, "--addr", "0.0.0.0:50053");
  const std::string adapter_name = Arg(argc, argv, "--adapter", "mock");
  const std::string config_path = Arg(argc, argv, "--atp-config", "");
  const std::string ring_arg = Arg(argc, argv, "--ring", "4096");
  const size_t ring_capacity = static_cast<size_t>(std::strtoull(ring_arg.c_str(), nullptr, 10));

  std::unique_ptr<brokergw::ICounterAdapter> adapter;
  if (adapter_name == "mock") {
    adapter.reset(new brokergw::MockCounterAdapter());
#ifdef BROKERGW_WITH_ATP
  } else if (adapter_name == "atp") {
    if (config_path.empty()) {
      std::cerr << "--atp-config is required for the atp adapter\n";
      return 2;
    }
    adapter.reset(new brokergw::AtpAdapter(config_path));
#endif
  } else {
    std::cerr << "unsupported adapter: " << adapter_name << "\n";
    return 2;
  }

  const std::string version = adapter->ServerVersion();
  adapter->Start();
  brokergw::Gateway gateway(adapter.get(), ring_capacity);
  adapter->SetSink(&gateway);
  brokergw::GatewayServiceImpl service(&gateway, adapter.get(), version);

  grpc::ServerBuilder builder;
  builder.AddListeningPort(addr, grpc::InsecureServerCredentials());
  builder.RegisterService(&service);
  std::unique_ptr<grpc::Server> server(builder.BuildAndStart());
  if (!server) {
    std::cerr << "failed to listen on " << addr << "\n";
    adapter->Stop();
    return 1;
  }
  std::cout << "brokergw server listening on " << addr
            << " adapter=" << adapter_name << " ring=" << ring_capacity << std::endl;
  server->Wait();
  adapter->Stop();
  return 0;
}
