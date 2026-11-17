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

# Pure-C++ mock counter: validates the contract layer without any vendor SDK.
add_library(brokergw_mock STATIC src/mock_adapter.cpp)
target_include_directories(brokergw_mock PUBLIC ${CMAKE_CURRENT_SOURCE_DIR}/include)
target_link_libraries(brokergw_mock PUBLIC brokergw_core)

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
    if(NOT ${vendor} STREQUAL "ATP" AND NOT ${vendor} STREQUAL "AMA"
       AND NOT EXISTS "${BROKERGW_SDK_ROOT}")
      message(FATAL_ERROR "BROKERGW_SDK_ROOT does not exist: ${BROKERGW_SDK_ROOT}")
    endif()
  endif()
endforeach()

# ATP (华锐) adapter: real SDK headers/libs live outside the repo and are never copied in.
if(BROKERGW_WITH_ATP)
  set(BROKERGW_ATP_SDK "/home/wuxw/workspace/archforce_quantapi_demo/sdk/v2.7.1.2-gcc4.8.5"
      CACHE PATH "ATP SDK root in WSL2 (include/ + lib/libatpquantapi.so)")
  if(NOT EXISTS "${BROKERGW_ATP_SDK}/lib/libatpquantapi.so")
    message(FATAL_ERROR "ATP SDK not found at ${BROKERGW_ATP_SDK}")
  endif()
  add_library(brokergw_atp STATIC adapters/atp_adapter.cpp)
  target_include_directories(brokergw_atp PUBLIC
    ${CMAKE_CURRENT_SOURCE_DIR} ${CMAKE_CURRENT_SOURCE_DIR}/include
    "${BROKERGW_ATP_SDK}/include")
  target_link_libraries(brokergw_atp PUBLIC brokergw_core
    "${BROKERGW_ATP_SDK}/lib/libatpquantapi.so")
endif()

# AMA (华锐行情) quote source: bypass data source for QuerySecurityInfo only.
if(BROKERGW_WITH_AMA)
  set(BROKERGW_AMA_SDK "/home/wuxw/workspace/archforce_ama_demo/c++"
      CACHE PATH "AMA C++ SDK root in WSL2")
  if(NOT EXISTS "${BROKERGW_AMA_SDK}/lib/Lnx64/libama.so")
    message(FATAL_ERROR "AMA SDK not found at ${BROKERGW_AMA_SDK}")
  endif()
  add_library(brokergw_ama STATIC adapters/ama_quote_source.cpp)
  target_include_directories(brokergw_ama PUBLIC
    ${CMAKE_CURRENT_SOURCE_DIR} ${CMAKE_CURRENT_SOURCE_DIR}/include
    "${BROKERGW_AMA_SDK}/include")
  target_link_libraries(brokergw_ama PUBLIC brokergw_core
    "${BROKERGW_AMA_SDK}/lib/Lnx64/libama.so"
    "${BROKERGW_AMA_SDK}/lib/Lnx64/libfmdutil.so"
    "${BROKERGW_AMA_SDK}/lib/Lnx64/libllmi.so"
    "${BROKERGW_AMA_SDK}/lib/Lnx64/librmm.so"
    "${BROKERGW_AMA_SDK}/lib/Lnx64/librum.so")
  if(TARGET brokergw_atp)
    target_link_libraries(brokergw_atp PUBLIC brokergw_ama)
  endif()
endif()

if(BROKERGW_WITH_GRPC)
  find_package(Protobuf CONFIG REQUIRED)
  find_package(gRPC CONFIG REQUIRED)
  # Generated C++ stubs are produced from the frozen proto on the Linux host
  # (see build/deps/gen_cpp_stubs.sh) and committed under gateways/cpp/gen/.
  set(BROKERGW_PROTO_GEN_DIR "${CMAKE_CURRENT_SOURCE_DIR}/gen"
      CACHE PATH "Directory with generated C++ protobuf/gRPC stubs")
  if(NOT EXISTS "${BROKERGW_PROTO_GEN_DIR}/brokergateway/v1/brokergateway.grpc.pb.cc")
    message(FATAL_ERROR
      "BROKERGW_WITH_GRPC=ON but no stubs in ${BROKERGW_PROTO_GEN_DIR}; run build/deps/gen_cpp_stubs.sh first")
  endif()

  set(BROKERGW_PROTO_SRCS
    "${BROKERGW_PROTO_GEN_DIR}/brokergateway/v1/brokergateway.pb.cc"
    "${BROKERGW_PROTO_GEN_DIR}/brokergateway/v1/brokergateway.grpc.pb.cc")

  add_library(brokergw_grpc STATIC grpc/grpc_service.cpp ${BROKERGW_PROTO_SRCS})
  target_include_directories(brokergw_grpc PUBLIC
    ${CMAKE_CURRENT_SOURCE_DIR}
    ${BROKERGW_PROTO_GEN_DIR})
  target_link_libraries(brokergw_grpc PUBLIC
    brokergw_core gRPC::grpc++ protobuf::libprotobuf)

  add_executable(brokergw_server main.cpp)
  set(BROKERGW_SERVER_LIBS brokergw_grpc brokergw_mock)
  if(BROKERGW_WITH_ATP)
    list(APPEND BROKERGW_SERVER_LIBS brokergw_atp)
    target_compile_definitions(brokergw_server PRIVATE BROKERGW_WITH_ATP)
  endif()
  target_link_libraries(brokergw_server PRIVATE ${BROKERGW_SERVER_LIBS})
endif()
