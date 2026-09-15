# Modbus Simulator Spec

状态：accepted

适用范围：`modbus-simulator/` 开发测试工具

关联决策：[ADR-0012](../adr/0012-independent-modbus-simulator.md)

## 1. 目的

在没有真实 Modbus 硬件时，为当前 Edge Collector 提供可重复启动、可配置、可观察的纯软件设备。模拟器用于协议联调、数据读取、异常处理和采集状态测试，不承担生产设备管理、历史存储或用户管理职责。

成功标准是：Go 采集代码可以通过 `/tmp/modbus-rtu0` 读取两个不同 Slave 的原始寄存器值，并验证 FC03 / FC04 读取块；同一模拟器也能提供 TCP、MBAP over UDP 和 RTU over UDP 的独立传输测试端点。

## 2. 事实边界

当前仓库能被源码确认的设备只有 `FEED_PROTECTOR`（馈电保护器），实际业务采集只使用 RS485 Modbus RTU。`edge-collector-api/internal/acquisition/protocol.go` 中既有固定映射已被 ADR-0014 排除出第一阶段目标；厂家寄存器表、工程值、告警寄存器和状态 bit 语义不在本 Spec 中臆定。

高开保护器、BMS、电池及匿名告警示例地址没有足够的设备身份、类型或采集代码依据，因此不生成对应设备 YAML。网络模式是模拟器和底层 Go 库的传输联调能力，当前 Go 业务 API 尚未提供网络采集配置。

## 3. 设备数据契约

### 3.1 默认设备

| 文件 | 名称 | Slave ID | 所属默认通道 | 地址 0 raw |
| --- | --- | ---: | --- | ---: |
| `config/devices/feeder_protector_01.yaml` | 馈电保护器-01 | 1 | `rtu0`、`rtu1` | 3000 |
| `config/devices/feeder_protector_02.yaml` | 馈电保护器-02 | 2 | `rtu0`、网络测试通道 | 3100 |

同一通道内 Slave / Unit ID 必须唯一；不同通道的设备状态和动态值相互独立。

### 3.2 寄存器映射

地址为零基 Modbus 地址。默认 fixture 暴露 FC03 地址 0～6；Go 采集目标按设备读取块配置决定请求范围，不再固定拆成电气量和状态两次读取。

| 地址 | 字段 | 原始类型 | 缩放 | 设备 1 默认 raw | 设备 1 工程值 |
| ---: | --- | --- | ---: | ---: | ---: |
| 0 | `voltage` | `uint16` | `/ 10` | 3000 | 300 |
| 1 | `current` | `uint16` | `/ 10` | 125 | 12.5 |
| 2–3 | `activePower` | `uint32`，高字在前 | `/ 10` | 0, 1234 | 123.4 |
| 4 | `frequency` | `uint16` | `/ 100` | 5000 | 50 |
| 5 | `powerFactor` | `uint16` | `/ 1000` | 980 | 0.98 |
| 6 | `status` | `uint16` 原值 | `/ 1` | 0 | 0 |

表中的字段名、组合方式、缩放和工程值只服务于模拟器生成可重复 raw 数据，不进入 Go 第一阶段 API，也不代表厂家协议事实。设备 YAML 的 `bits` 保持为空。

### 3.3 配置类型

配置支持 `bool`、`uint16`、`int16`、`uint32`、`int32`、`float32`、`float64`。区域使用 `coil`、`discrete_input`、`holding_register`、`input_register`。默认馈电设备只配置 holding registers；FC04、coil 和 discrete input 由独立测试夹具覆盖，不作为馈电设备事实。

多字节字段支持：

| `byte_order` | `word_order` | 排列 |
| --- | --- | --- |
| `big` | `big` | ABCD |
| `little` | `big` | BADC |
| `big` | `little` | CDAB |
| `little` | `little` | DCBA |

配置中的 `value`、`simulation.min/max/step` 是工程值，编码规则为 `raw = value / scale`。`length` 必须与类型占用的寄存器数一致；地址不能重叠或超出 0–65535；未配置的地址不自动填零。

## 4. 通道与帧契约

### 4.1 Modbus RTU

`pty.openpty()` 创建 master/slave；模拟器持有 master，Go 客户端打开固定 alias，例如 `/tmp/modbus-rtu0`。alias 原子替换为当前 `/dev/pts/N`，旁边的 `.lock` 文件防止两个进程抢占同一路。PTY 使用 raw 模式，保留 baudrate、bytesize、parity、stopbits 配置供客户端使用，但不模拟真实物理波特率、线路碰撞或校验位时序。

一个 RTU 通道允许多个设备，使用 Slave ID 分发。未知 Slave、广播和错误 CRC 不响应；请求错误记录后继续服务。`rtu` 是按 RTU 帧处理的独立模式，不能当作 UDP。

### 4.2 Modbus TCP

使用 PyModbus 原生 `ModbusTcpServer`，默认 `127.0.0.1:1502`。MBAP Transaction ID 和 Unit ID 保留；默认 TCP 通道挂 Unit 1、2。非法 Unit 返回网关无响应异常，非法功能码、地址或值按 Modbus 异常响应处理。

### 4.3 Modbus UDP

每个 UDP Datagram 都是 `MBAP Header + Modbus PDU`，没有 CRC，默认 `127.0.0.1:1600`。这是 MBAP over UDP，不能与 RTU over UDP 共用解析器或端口。

### 4.4 Modbus RTU over UDP

每个 UDP Datagram 都是完整 `Slave + Function Code + Data + CRC16`，默认 `127.0.0.1:1700`，没有 MBAP。CRC 错误、短包、超长包和不完整帧被丢弃并记录；有效请求使用同一设备模型生成带正确 CRC 的响应。

## 5. 支持的功能码

第一阶段支持：

| 功能码 | 含义 | 读写约束 |
| ---: | --- | --- |
| 01 | Read Coils | 只访问已配置 coil |
| 02 | Read Discrete Inputs | 只访问已配置 discrete input |
| 03 | Read Holding Registers | 默认馈电设备实际使用 |
| 04 | Read Input Registers | 通用测试夹具使用 |
| 05 | Write Single Coil | 只写已配置 coil |
| 06 | Write Single Register | 只写已配置 holding register |
| 15 | Write Multiple Coils | 数据长度和地址必须合法 |
| 16 | Write Multiple Registers | 数据长度和地址必须合法 |

异常输入必须满足：服务不中止；记录协议、通道、设备、Slave / Unit、功能码和地址信息；能返回异常响应时返回 `01`（非法功能码）、`02`（非法地址）、`03`（非法值/长度）或 `0B`（未知网络 Unit）。RTU 未知 Slave 不响应以保留真实总线超时语义。

## 6. 动态模拟与故障行为

寄存器可使用 `fixed`、`increment`、`decrement`、`random`。模拟值按访问时的 monotonic 时间推进，不建立每个寄存器的后台任务。设备可设置 `behavior.response_delay_ms` 模拟慢设备；默认值为 0。当前不支持 `drop_rate`、脚本、表达式、广播写和配置热加载。

YAML 修改后重启模拟器生效。写功能码可以修改运行时数据；重启恢复 YAML 初值。动态字段在下一次模拟时间点按其规则更新。

## 7. 配置文件契约

```yaml
logging:
  level: INFO
  hex: true
channels:
  - name: rtu0
    protocol: rtu
    alias: /tmp/modbus-rtu0
    baudrate: 9600
    bytesize: 8
    parity: N
    stopbits: 1
    devices:
      - devices/feeder_protector_01.yaml
```

网络通道使用 `host` 和 `port`；设备文件使用 `name`、`slave_id`、`registers`、可选 `behavior`。配置加载在打开任何端口前完成，重复 channel 名称、端点、alias、Slave / Unit、寄存器地址或不支持字段都必须直接报错退出。

## 8. 日志要求

每个请求至少记录时间、协议、通道、设备（未知设备可标记 unknown）、Slave / Unit、功能码、起始地址、数量和结果。`logging.hex: true` 时增加 RX/TX 原始十六进制报文；`false` 时不输出模拟器自己的 HEX 报文，也不应让 PyModbus 的原始帧转储污染普通错误日志。

启动日志必须能看出每个 RTU alias 与实际 PTY、串口参数、网络监听地址和设备 ID；停止或启动失败必须释放已创建资源。

## 9. 验收矩阵

实现必须通过以下可重复验收：

1. `uv sync --locked` 和 `uv run modbus-simulator --check-config` 成功。
2. Python 测试使用真实临时 socket 和 PTY 验证 TCP、UDP、RTU、RTU over UDP；覆盖 FC01/02/03/04/05/06/15/16、写后读回、多个 ID、CRC、非法包、地址、数量、功能码、延迟、动态值、PTY alias 锁和启动失败清理。
3. Go `NewModbusSessionFactory → 读取块轮询 → CurrentStateStore` 能从 `/tmp/modbus-rtu0` 读到两个 Slave 的 FC03 / FC04 原始寄存器值、地址和读取块有效性，不经过业务解析。
4. 真实 Go 采集循环中，未知 Slave 连续失败 3 次进入 `OFFLINE`，同一 PTY 上另一 Slave 继续 `ONLINE`，恢复后回到 `ONLINE`。
5. 项目现用 Go Modbus 客户端能通过 `tcp://127.0.0.1:1502`、`udp://127.0.0.1:1600`、`rtuoverudp://127.0.0.1:1700` 读取 Unit 1、2。
6. 停止模拟器后 `/tmp/modbus-rtu0`、`/tmp/modbus-rtu1` alias 被清理；下次启动得到新的 `/dev/pts/N` 指向。

已执行结果记录在 [`modbus-simulator/TEST_RESULTS.md`](../../modbus-simulator/TEST_RESULTS.md)。

## 10. 明确不属于本 Spec

完整 Go API / 数据库 / React 页面端到端验收、真实厂家硬件验收、厂家寄存器表校正、高开保护器、告警详情和 status bit 语义、MQTT、数据库存储、Web UI、插件系统、真实 RS485 电气特性与生产部署均不属于本 Spec。它们需要各自的协议事实或产品决策后再形成新的 Spec / ADR。
