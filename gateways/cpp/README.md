# C++ BrokerGateway 骨架（M7）

真实券商柜台适配进程的起点。上游是冻结的 `broker-gateway.v1` 契约，下游是券商 C++ SDK；设计与字段映射见 [`docs/m7-cpp-gateway.md`](../../docs/m7-cpp-gateway.md)。

## 边界（重要）

- 本目录**只包含我们自己写的代码**：纯 C++ 核心、适配层接口骨架、单测。不复制任何 SDK 头文件、`.so`、授权/配置文件入库。
- 三套 SDK 通过本地软链接 `refs/broker-sdks/{kdvip_api,atp_quant_api,amd_ama}` 提供，已被 `.gitignore` 排除；参考范围**只以 C++ demo 里 `#include` 的头文件为入口**（必要时看其传递依赖的结构定义）+ demo 源码，其余 SDK 头文件一律不看。
- 本机为 arm/macOS，SDK 都是 Linux x86_64 预编译库（gcc 4.8.5 / 11.2.1、C++11）。**本机不编译、不运行 SDK demo，也不链接任何 `.so`**。默认构建只编译我们自己的纯 C++ 核心与单测。

## 本机跑通

```bash
cmake -S gateways/cpp -B /tmp/tf-cpp-build
cmake --build /tmp/tf-cpp-build
ctest --test-dir /tmp/tf-cpp-build --output-on-failure
```

`brokergw_core_test` 覆盖：金证十进制价格字符串 → 1e-4 定点、ATP double → 1e-4、AMA Price6 → 1e-4、ATP 状态码映射、金证码表缺省与注入、seq ring 补发/窗口过期、成交回报去重、查询失败不推进累计成交量水位并重试。

## Linux x86_64 构建机接 SDK

```bash
cmake -S gateways/cpp -B build-cpp \
  -DBROKERGW_WITH_KDVIP=ON \
  -DBROKERGW_WITH_ATP=ON \
  -DBROKERGW_WITH_AMA=ON \
  -DBROKERGW_SDK_ROOT=/path/to/tradeforge/refs/broker-sdks
```

- 打开任一 `BROKERGW_WITH_*` 会检查平台为 Linux x86_64；`BROKERGW_SDK_ROOT` 指向三个软链接目录。
- 金证：`include/c++` + `lib/Lnx64` 下的 `libmaCliApi/libmaTradeApi/...`，demo 用 `-DOS_IS_LINUX -fpermissive`、C++11。
- ATP：`sdk/current/include` + `lib/libatpquantapi.so`（rudplib 另带），`-std=c++11`。
- AMA：`c++/include` + `c++/lib/Lnx64/libama.so`，CMake≥3.15 / C++11。
- gRPC server 层（`-DBROKERGW_WITH_GRPC=ON`）额外需要 Protobuf/gRPC C++ 与在 Linux 上从冻结 proto 生成的 `.pb.cc/.pb.h`，通过 `BROKERGW_PROTO_GEN_DIR` 指定；契约本身不改。

`adapters/*.hpp` 里对 SDK 的 `#include` 全部包在条件编译内，未打开对应开关时不会被编译。

## WSL2 联调机（AlmaLinux 8 x86_64）

联调机上三套 demo 位于 `/home/wuxw/workspace/{kdvip_demo,archforce_quantapi_demo,archforce_ama_demo}`，
只读原位引用、不复制入库。gRPC C++ 依赖源码构建（Alma8 源无 grpc 包）：

```bash
# 1) 构建 gRPC C++（含 protoc / grpc_cpp_plugin），产物在 build/deps/grpc
bash build/deps/build_grpc_min.sh

# 2) 从冻结 proto 生成 C++ stub，提交到 gateways/cpp/gen/
bash build/deps/gen_cpp_stubs.sh

# 3) 构建 C++ 网关（mock 契约层）+ 跑核心单测
bash build/deps/build_cpp_gateway.sh

# 4) 契约层端到端自检（启动 C++ 网关 + Go bgw-probe）
bash scripts/e2e-cpp.sh
```

ATP 真实柜台适配（可选）：

```bash
cmake -S gateways/cpp -B build/cpp-atp -DCMAKE_BUILD_TYPE=Release \
  -DBROKERGW_WITH_GRPC=ON -DBROKERGW_WITH_ATP=ON \
  -DgRPC_DIR=build/deps/grpc/lib/cmake/grpc \
  -DCMAKE_PREFIX_PATH=build/deps/grpc \
  -DBROKERGW_ATP_SDK=/home/wuxw/workspace/archforce_quantapi_demo/sdk/v2.7.1.2-gcc4.8.5
cmake --build build/cpp-atp -j4

# 仿真配置（key=value，真实值不入库）。ATP demo 中实测可用的仿真值：
#   locations=172.16.109.17:32001
#   agw_user=user5 / agw_password=password5
#   order_way=4 / client_feature_code=dev-0001
#   cust_id=10014939 / password=123123 / fund_account=10014939
#   branch_id=2250 / account_id_sz=0117920347 / account_id_sh=A461951056
#   ama_config=/path/to/ama.conf  （可选行情旁路）
build/cpp-atp/brokergw_server --adapter atp --atp-config /path/to/atp.conf \
  --addr 0.0.0.0:50053

# 菜单式客户端：1 attach → 2 sub → 7 account → 4 security → 8 submit → 9 cancel → 10 hb
build/bgw-cli --addr 127.0.0.1:50053

# AMA 行情旁路配置（ama.conf）：
#   username=amatest / password=amatest
#   ums_server_1=172.16.109.14:8200 / ums_server_2=172.16.109.15:8200
#   symbols=SH:600000,SZ:000001
```

