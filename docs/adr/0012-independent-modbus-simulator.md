# ADR-0012: 以独立 Python 工具提供 Modbus 设备模拟器

## Status

Accepted

为支持没有真实硬件时的协议联调，仓库在根目录维护独立的 `modbus-simulator/` Python 项目。模拟器使用 uv 管理依赖，使用 PyModbus 处理 Modbus PDU、功能码、异常响应、Framer 和 CRC；设备模型和寄存器数据由 YAML 配置，通信传输层分别实现 Modbus RTU、Modbus TCP、MBAP over UDP 和 RTU over UDP。

## Context

当前 Edge Collector 的实际采集代码只实现了馈电保护器的 RS485 Modbus RTU，开发机没有真实设备时无法验证 PTY 串口、多 Slave、异常响应和采集状态恢复。完整需求还包含 TCP、UDP 与 RTU over UDP，但这些模式尚未接入 Go 业务采集层，仍需要独立的传输验证工具。

仓库中的 ADR / 需求文档声称已有完整厂家协议资料，实际可见源码却明确把 `protocol.go` 中的馈电保护器寄存器映射标注为开发闭环替代映射。模拟器必须优先匹配当前实际采集代码，不能把未确认的告警地址、设备类型或状态位语义当成事实。

## Decision

### 工具边界

- 模拟器是开发测试辅助工具，不是生产 IoT 平台。
- 模拟器独立于 Go API、React、数据库和后端运行配置；新增或修改模拟器不改变现有 Go 业务协议。
- 使用 `uv sync` 和 `uv run modbus-simulator`；不使用 `pip + requirements.txt` 作为主依赖方案。
- 依赖锁定为 PyModbus 3.15.0 和 PyYAML 6.0.3；升级 PyModbus 必须重新执行集成测试。

### 统一模型与传输边界

设备、寄存器和动态值只维护一份模型。Transport 只负责收发字节，协议边界负责解码和异常响应，Datastore 负责按 Slave / Unit 访问统一设备数据。

第一阶段分别处理以下模式：

| 模式 | 帧格式 | 默认端点 |
| --- | --- | --- |
| Modbus RTU | `Slave + PDU + CRC16`，通过原生 PTY | `/tmp/modbus-rtu0`、`/tmp/modbus-rtu1` |
| Modbus TCP | MBAP + PDU over TCP | `127.0.0.1:1502` |
| Modbus UDP | MBAP + PDU over UDP | `127.0.0.1:1600` |
| Modbus RTU over UDP | 完整 RTU 帧作为 UDP Payload | `127.0.0.1:1700` |

Modbus TCP 直接使用 PyModbus Server；RTU 使用 Python 标准库 `pty.openpty()` 创建并持有 master FD，自动维护固定 alias；两种 UDP 使用独立 Datagram Transport。一个 RTU channel 可以挂载多个不重复的 Slave ID，模拟共享 RS485 总线。

### 配置和协议来源

总配置为 `config/simulator.yaml`，设备配置位于 `config/devices/`。默认提供两台 `FEED_PROTECTOR` 开发设备：Slave 1 和 Slave 2。已有地址 0～6、字段名、类型和工程值只用于构造可重复的模拟器寄存器内容，不代表厂家协议事实；Go 第一阶段验收只读取其原始 16-bit 寄存器值，并可另用 FC04 测试夹具验证输入寄存器读取。

配置在启动时一次性加载并校验；第一阶段修改 YAML 后重启生效，不承诺热加载。允许配置 fixed、increment、decrement、random 和响应延迟，但不引入脚本执行、数据库、Web UI、MQ 或复杂故障注入。

### 验证边界

验收必须包含真实 PTY / socket 收发、FC03 / FC04 读取、写入功能码、CRC 和非法请求处理、多个 Slave、单个 Slave 超时后其他设备继续运行，以及 Go 采集代码的成功、离线和恢复状态。网络模式在 Go 业务采集未接入前，只作为底层 Go Modbus 客户端和传输联调端点验收。

## Considered Options

- **在 Go API 内增加模拟设备分支**：会把测试设备生命周期、PTY 和协议异常注入到生产进程，增加数据库与启动耦合，因此不选。
- **每台设备单独启动一个进程或 PTY**：不能自然表达一个 RS485 总线上的多个 Slave，也使端口、关闭和配置管理更复杂，因此不选。
- **依赖 socat 创建虚拟串口**：增加开发环境外部依赖和额外进程，不能满足模拟器自包含，因此不选。
- **自行实现 Modbus PDU、CRC 和 Server**：重复成熟协议库能力，增加与 PyModbus / Go 客户端不一致的风险，因此不选。

## Consequences

- 无硬件时可以直接使用固定串口 alias、网络端口和 YAML 设备值完成联调。
- 模拟器能提供稳定的 FC03 / FC04 原始寄存器测试数据，但不能证明厂家真实寄存器表、工程值、告警地址或状态位语义。
- PyModbus 版本与其 simulator API 成为工具内部升级约束；Transport 和 Datastore 边界保留后续替换空间。
- Go 业务 API 当前仍只支持 RTU，TCP、UDP 和 RTU over UDP 端点不能通过现有设备配置页面直接启用。

## References

- [Modbus Simulator Spec](../specs/modbus-simulator.md)
- [协议核对记录](../../modbus-simulator/PROTOCOL_FINDINGS.md)
- [模拟器 README](../../modbus-simulator/README.md)
- [ADR-0011](0011-phase-one-rs485-modbus-rtu-acquisition.md)
- [ADR-0014](0014-raw-register-acquisition-before-protocol-parsing.md)
