// 华锐低延时行情 AMA（C++ 版）旁路数据源骨架。
// 只有定义 BROKERGW_WITH_AMA 且在 Linux x86_64 上才编译。
// 注意：契约没有行情 RPC，ReportEvent oneof 也没有行情载荷；
// 本文件只喂 QuerySecurityInfo，绝不进回报流。
// 业务范围：仅沪深现货（股票 / ETF / 可转债）；usa / hkex / cfets 等其他市场
// 头文件一律不看。
#pragma once

#ifdef BROKERGW_WITH_AMA

#include <string>

#include "brokergw/adapter.h"
// CMake 的 target_include_directories 指向：
//   ${BROKERGW_SDK_ROOT}/amd_ama/c++/include
#include "ama.h"
#include "ama_property.h"
#include "ama_serialize.h"
#include "ama_tools.h"
#include "query/ama_query_spi.h"
// MDSnapshot / CodeTableRecord 等结构定义位于 ama.h 的传递依赖
// ama_struct.h / ama_datatype.h，仅作字段参考，不主动扫其他 SDK 头文件。

namespace brokergw {

// 参考：c++/include/ama.h、demo_src/ama_demo_tcp.cpp
//   SetProperty（须先于 Init）-> Init(IAMDSpi*, Cfg) -> Join / Release
//   SubscribeData(SubscribeItem/CategoryItem)，按 market + flag + security_code
//   回调：OnMDSnapshot / OnMDTickOrder / OnMDTickExecution（须 FreeMemory）
// 只用到证券档案与参考价（详见 docs/m7-cpp-gateway.md §3.3 / D-004）：
//   price_tick_u    <- CodeTableRecord.price_tick（Price6 -> 契约 1e-4，÷100）
//   limit_up/down_u <- MDSnapshot.high_limited / low_limited
//   trading_status  <- security_status(1=停牌) / instrument_status / kHalt(10)
//   参考价          <- last_price / pre_close_price
//   时间戳          <- orig_time「YYYYMMDDHHMMSSsss」(ms) -> unix_nano ×1e6
//   市场码          <- kSSE 101 / kSZSE 102 -> SH / SZ
// 行情侧 appl_seq_num / channel_no 与网关本地 seq 完全隔离；
// 10 档盘口、trading_phase_code 位义、FPGA 字段缺失进 ext_json。
class AmaQuoteSource final : public IAMDSpi {
 public:
  explicit AmaQuoteSource(const std::string& cfg_path);
  ~AmaQuoteSource() override;

  bool Start();
  void Stop();

  // 由网关的 QuerySecurityInfo 调用；缓存 + 超时，回调线程不做业务处理。
  SecurityInfo Lookup(const std::string& market, const std::string& symbol);

  void OnMDSnapshot(MDSnapshot* snapshot) override;

 private:
  // TODO(Linux 仿真环境)：Init/Join、订阅维度、代码表缓存、锁与超时。
};

}  // namespace brokergw

#else
// 默认构建（含本机 arm）不触碰 SDK；行情旁路仅在设计文档中约定。
#endif  // BROKERGW_WITH_AMA
