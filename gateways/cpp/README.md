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

`adapters/*.hpp` 里对 SDK 的 `#include` 全部包在条件编译内，未打开对应开关时不会被编译；真正的柜台联调需要账号、授权与仿真环境，不在本机范围内。
