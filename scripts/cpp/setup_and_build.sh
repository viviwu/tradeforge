#!/usr/bin/env bash
# WSL2 (x86_64 Linux) 一站式构建 C++ BrokerGateway。
# 用法：在 WSL2 中 bash scripts/cpp/setup_and_build.sh [--with-atp]
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
PREFIX=$ROOT/build/deps/grpc

WITH_ATP=0
[ "${1:-}" = "--with-atp" ] && WITH_ATP=1

echo "=== [1/4] gRPC C++ 依赖（已安装则跳过）==="
if [ ! -x "$PREFIX/bin/grpc_cpp_plugin" ]; then
  SRC=/tmp/tradeforge-grpc/grpc-src
  BLD=/tmp/tradeforge-grpc/grpc-build
  mkdir -p /tmp/tradeforge-grpc
  if [ ! -d "$SRC/.git" ]; then
    git clone --depth 1 --branch v1.48.2 https://github.com/grpc/grpc.git "$SRC"
  fi
  for m in third_party/abseil-cpp third_party/cares/cares third_party/protobuf \
           third_party/re2 third_party/zlib third_party/boringssl-with-bazel; do
    (cd "$SRC" && for i in 1 2 3; do git submodule update --init --depth 1 "$m" && break; done)
  done
  cmake -S "$SRC" -B "$BLD" \
    -DCMAKE_BUILD_TYPE=Release -DgRPC_INSTALL=ON -DgRPC_BUILD_TESTS=OFF \
    -DgRPC_BUILD_CSHARP_EXT=OFF -DCMAKE_INSTALL_PREFIX="$PREFIX"
  cmake --build "$BLD" -j"$(nproc)"
  cmake --install "$BLD"
fi

echo "=== [2/4] 生成 C++ stub（gateways/cpp/gen/，契约零改动）==="
mkdir -p "$ROOT/gateways/cpp/gen"
"$PREFIX/bin/protoc" -I "$ROOT/api/proto" \
  --cpp_out="$ROOT/gateways/cpp/gen" --grpc_out="$ROOT/gateways/cpp/gen" \
  --plugin=protoc-gen-grpc="$PREFIX/bin/grpc_cpp_plugin" \
  "$ROOT/api/proto/brokergateway/v1/brokergateway.proto"

echo "=== [3/4] 构建 C++ 网关 + 核心单测 ==="
CMAKE_ARGS="-DCMAKE_BUILD_TYPE=Release -DBROKERGW_WITH_GRPC=ON -DCMAKE_PREFIX_PATH=$PREFIX"
TARGET=cpp
if [ "$WITH_ATP" -eq 1 ]; then
  CMAKE_ARGS="$CMAKE_ARGS -DBROKERGW_WITH_ATP=ON -DBROKERGW_WITH_AMA=ON"
  TARGET=cpp-atp
fi
cmake -S "$ROOT/gateways/cpp" -B "$ROOT/build/$TARGET" $CMAKE_ARGS
cmake --build "$ROOT/build/$TARGET" -j"$(nproc)"
ctest --test-dir "$ROOT/build/$TARGET" --output-on-failure

echo "=== [4/4] 构建 CLI / 探针 ==="
GO=${GO:-/tmp/tradeforge-go126/go/bin/go}
export GOTOOLCHAIN=local GOPROXY=${GOPROXY:-https://goproxy.cn,direct}
export GOMODCACHE=/tmp/tf-gomod GOPATH=/tmp/tf-gopath GOCACHE=/tmp/tf-gocache
cd "$ROOT"
"$GO" build -o "$ROOT/build/bgw-cli" ./cmd/bgw-cli
"$GO" build -o "$ROOT/build/bgw-probe" ./cmd/bgw-probe
echo "DONE: $ROOT/build/$TARGET/brokergw_server + build/bgw-cli + build/bgw-probe"
