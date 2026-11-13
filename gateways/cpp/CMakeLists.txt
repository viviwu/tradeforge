cmake_minimum_required(VERSION 3.15)
project(brokergw_cpp LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 11)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# Default build is pure standard C++: no SDK, no gRPC, no protobuf.
# It compiles and runs on any platform (including arm/macOS dev machines).
option(BROKERGW_WITH_GRPC "Build the gRPC server layer (needs generated C++ stubs)" OFF)
option(BROKERGW_WITH_KDVIP "Build the kdvip_ap adapter (Linux x86_64 SDK)" OFF)
option(BROKERGW_WITH_ATP "Build the ATPQuantAPI adapter (Linux x86_64 SDK)" OFF)
option(BROKERGW_WITH_AMA "Build the AMA quote source (Linux x86_64 SDK)" OFF)

add_library(brokergw_core STATIC
  src/mapping.cpp
  src/report_ring.cpp
  src/gateway.cpp
)
target_include_directories(brokergw_core PUBLIC ${CMAKE_CURRENT_SOURCE_DIR}/include)

add_executable(brokergw_core_test tests/core_test.cpp)
target_link_libraries(brokergw_core_test PRIVATE brokergw_core)

enable_testing()
add_test(NAME brokergw_core_test COMMAND brokergw_core_test)

set(BROKERGW_SDK_ROOT "${CMAKE_CURRENT_SOURCE_DIR}/../../refs/broker-sdks"
    CACHE PATH "Local directory holding the three vendor SDK symlinks")

if(BROKERGW_WITH_KDVIP OR BROKERGW_WITH_ATP OR BROKERGW_WITH_AMA OR BROKERGW_WITH_GRPC)
  if(NOT CMAKE_SYSTEM_NAME STREQUAL "Linux" OR NOT CMAKE_SYSTEM_PROCESSOR MATCHES "x86_64|amd64")
    message(FATAL_ERROR
      "Vendor SDKs and the gRPC server layer require Linux x86_64. "
      "On arm/macOS keep all BROKERGW_WITH_* options OFF and run brokergw_core_test only.")
  endif()
endif()

foreach(vendor KDVIP ATP AMA)
  if(BROKERGW_WITH_${vendor})
    target_compile_definitions(brokergw_core PUBLIC BROKERGW_WITH_${vendor})
    if(NOT EXISTS "${BROKERGW_SDK_ROOT}")
      message(FATAL_ERROR "BROKERGW_SDK_ROOT does not exist: ${BROKERGW_SDK_ROOT}")
    endif()
  endif()
endforeach()

if(BROKERGW_WITH_GRPC)
  find_package(Protobuf REQUIRED)
  find_package(gRPC CONFIG REQUIRED)
  # The generated C++ stubs are produced on the Linux build host from
  # api/proto/brokergateway/v1/brokergateway.proto (contract stays frozen).
  message(STATUS "gRPC layer enabled: supply generated .pb.cc/.pb.h via BROKERGW_PROTO_GEN_DIR")
  set(BROKERGW_PROTO_GEN_DIR "" CACHE PATH "Directory with generated C++ protobuf/gRPC stubs")
  if(BROKERGW_PROTO_GEN_DIR STREQUAL "")
    message(FATAL_ERROR "BROKERGW_WITH_GRPC=ON requires BROKERGW_PROTO_GEN_DIR")
  endif()
endif()
