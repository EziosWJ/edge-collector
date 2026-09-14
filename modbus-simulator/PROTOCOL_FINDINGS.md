# 编码前协议核对

核对日期：2026-09-14。以实际代码为依据；不改变后端现有开发映射。

## 检查范围与文件

`task/`、`experience/` 不存在。检查了 docs Markdown、根目录摘要，并在仓库常规可见文件中定向搜索协议、Modbus、RTU、UDP、TCP、串口、寄存器和 csv/xlsx/xls/pdf/doc/docx/txt/ods 资料。未发现厂家协议原文、Excel/CSV 转换表或已确认设备原始报文；不声称仓库外或被忽略的文件也不存在。

| 文件 | 核对内容 |
|---|---|
| `docs/requirements/edge-collector-requirements.md` | 全产品协议需求、匿名告警地址示例 |
| `docs/requirements/delivery-phases.md` | 分阶段范围 |
| `docs/design/phase-1-acquisition-technical-design.md` | 串口、Go 库、内置适配与状态模型 |
| `docs/design/phase-1-decisions-summary.md`、`phase-1-ticket-breakdown.md` | 第一阶段实施范围 |
| `docs/adr/0011-phase-one-rs485-modbus-rtu-acquisition.md` | 馈电 RTU 决策、后续协议边界 |
| 根目录 `README.md`、`CONTEXT.md` | 项目上下文 |
| `edge-collector-api/internal/acquisition/protocol.go`、`protocol_test.go` | FC03 地址 0–6、倍率、uint32 高字前、分组读取 |
| 同模块 `transport.go` | `rtu://` 串口会话，Unit 切换、timeout |
| 同模块 `model.go`、`service.go`、`repository.go`、`handler.go` | 唯一设备类型、通道/设备配置与 REST |
| 同模块 `runtime.go`、`runtime_test.go`、`state.go` | 实时采集、部分失败、离线恢复 |
| `edge-collector-api/cmd/api/main.go` | 启动时载入配置并运行采集 |
| `edge-collector-api/configs/config.dev.yaml` | 应用配置；设备运行配置实际存 DB |
| `edge-collector-api/migrations/schema/00008_acquisition_schema.sql` 与 `migrations/sqlite/schema/00008_acquisition_schema.sql` | 通道及设备表 |
| `edge-collector-api/go.mod` | simonvetter/modbus v1.6.4 |

## 识别结果及冲突

1. 唯一实际设备：`FEED_PROTECTOR` 馈电保护器，RS485 Modbus RTU，Slave ID 可配置。
2. 唯一实际采集功能码：03；读取 holding 地址 `0,count=6`，再读 `6,count=1`。字段及 raw 映射详见 README。
3. 代码无厂家表，`ParseFeedProtectorRegisters` 注释明确该映射只是开发闭环替代实现。ADR/技术设计声称协议资料齐备，与当前可见资料和代码注释冲突。本模拟器优先匹配代码，并在设备 YAML 标明来源。
4. 高开保护器只在需求中，没有可确认寄存器和类型，未生成高开配置；不存在已证实的 BMS/电池/PowerBox 设备。
5. status 只透传 uint16，无 bit、枚举或故障码语义；测试样本中出现数值不代表正常/运行状态。配置保留空 bits，不编造含义。
6. 全产品需求中告警示例涉及 `812`、`8166`、`8167–8180`，缺少具体设备身份、功能码、完整类型和位定义，当前代码也不采集。因此没有把它们当作馈电寄存器实现。
7. 后端业务仅串口 RTU；库支持 `tcp://`、`udp://`（MBAP over UDP）和 `rtuoverudp://`。模拟器分别提供网络通道，用相同开发映射做传输验证，不宣称存在已确认厂家的 UDP 设备协议。
8. 代码明确 uint16、大端解码后 uint32 高 word 在前以及各字段比例；未发现 BCD/ASCII/String 或 float 型厂家字段。本工具提供用户要求的通用数值类型，但默认设备只使用 uint16/uint32。

## 实现前确定的设备配置

- `config/devices/feeder_protector_01.yaml`：Slave 1。
- `config/devices/feeder_protector_02.yaml`：Slave 2，同类第二台，电压区别为 310。
- RTU0 挂载两台，RTU1 挂载 Slave 1；TCP、两种 UDP 复用两台配置，各通道独立存储。
- 实际默认设备不提供 FC04/coil 寄存器；通用协议能力由独立测试夹具覆盖。

## PyModbus API 核对

实现前安装并检查 PyModbus 3.15.0 的实际签名、server/requesthandler、SimDevice/SimData/SimCore、PDU decoder 和 RTU/Socket Framer 源码。使用新版 `SimDevice(id, simdata)`、`SimData(address, values, datatype)`，没有旧版 `ModbusSlaveContext` 写法。

参考官方 [Server 文档](https://pymodbus.readthedocs.io/en/latest/source/server.html)；在线 latest 可能指向开发版，因此实现以 uv.lock 锁定的实际安装版本源码及真实集成测试为准。
