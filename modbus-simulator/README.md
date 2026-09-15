# Edge Collector Modbus Simulator

当前 Go 采集服务的纯软件设备模拟器，用于开发联调、读取测试和异常测试。独立 Python 项目，不修改 Go 配置、数据库或依赖，不需要硬件、socat、Web UI 或其他辅助服务。

**协议依据：本工具提供原始寄存器联调夹具，不包含厂家协议解析。** `FEED_PROTECTOR` 只保留为现有设备身份示例；不要把模拟器标签、数值或状态字解释为真实硬件语义。完整核对记录见 [PROTOCOL_FINDINGS.md](PROTOCOL_FINDINGS.md)。

## 环境与启动

Linux、Python 3.12+、uv；Go 联调脚本另需项目所用 Go 工具链。PTY 使用 Python 标准库；不需要 pyserial 或 socat。

```bash
cd modbus-simulator
uv sync
uv run modbus-simulator
```

唯一推荐启动入口为 `uv run modbus-simulator`。可选参数：

```bash
uv run modbus-simulator --config config/simulator.yaml
uv run modbus-simulator --check-config
```

配置相对路径基于所指定的总配置文件目录解析。上述命令在 `modbus-simulator/` 内执行。Ctrl+C / SIGTERM 停止服务并关闭端口、PTY 和自己创建的软链接。若任一通道启动失败，已经启动的通道一并关闭，退出码非零。

运行依赖锁定于 `uv.lock`：PyModbus **3.15.0**、PyYAML **6.0.3**；构建依赖 uv_build。测试使用标准库 unittest，不引入额外运行依赖。

## 默认通道

| 通道 | 模式 | 连接地址 | Slave / Unit |
|---|---|---|---|
| rtu0 | Modbus RTU | `/tmp/modbus-rtu0` | 1、2 |
| rtu1 | Modbus RTU | `/tmp/modbus-rtu1` | 1 |
| tcp0 | Modbus TCP | `127.0.0.1:1502` | 1、2 |
| udp0 | MBAP + PDU over UDP | `127.0.0.1:1600` | 1、2 |
| rtu_udp0 | RTU over UDP | `127.0.0.1:1700` | 1、2 |

网络默认仅监听本机。需要其他机器连接时，在 `config/simulator.yaml` 把对应 `host` 改为 `0.0.0.0`，客户端使用模拟器所在机器实际 IP。

`rtu0` 的两台设备共用一条总线，通过 Slave ID 区分；`rtu1` 是另一条独立总线。不同通道的数据存储相互独立，复用同一设备 YAML 不会共享写入或动态状态。可复制通道配置扩展到 8 路；每路 alias 必须唯一，每个通道内 Slave / Unit 不得重复。

## 配置与设备映射

- `config/simulator.yaml`：日志、通道、串口参数和设备文件列表。
- `config/devices/feeder_protector_01.yaml`：Slave 1，holding 原始值从 3000 开始，input 样例值 42。
- `config/devices/feeder_protector_02.yaml`：Slave 2，holding 原始值从 3100 开始，input 样例值 43。

所有地址为线上 **零基地址**，不是 40001 风格的显示编号。以下是 raw 联调数据；holding 和 input 是两个独立寄存器空间。

| 区域 | 地址 | 测试标签 | 类型 | Slave 1 raw | Slave 2 raw |
|---|---|---|---|---|---|
| holding | 0 | sample-0 | uint16 | 3000 | 3100 |
| holding | 1 | sample-1 | uint16 | 125 | 125 |
| holding | 2–3 | sample-2/3 | uint16 | 0 / 1234 | 0 / 1234 |
| holding | 4 | sample-4 | uint16 | 5000 | 5000 |
| holding | 5 | sample-5 | uint16 | 980 | 980 |
| holding | 6 | sample-6 | uint16 | 0 | 0 |
| input | 0 | input_sample | uint16 | 42 | 43 |

这些值仅用于验证 FC03/FC04、地址、数量和显示进制；系统不把它们转换为电压、电流、功率、单位或状态语义。

`value`、动态模拟的 min/max/step 均使用**工程值**：`raw = value / scale`。整数寄存器必须可以精确表示该值；越界、重叠、非法字节序、重复 ID 等会在启动前拒绝。`length` 可省略；填写时必须匹配类型。

通用类型：bool、uint16、int16、uint32、int32、float32、float64。区域：coil、discrete_input、holding_register、input_register。bool 仅用于前两个区域，其余类型用于寄存器。

字节序配置默认为 `byte_order: big`、`word_order: big`，即当前 Go 代码需要的网络字节序和高字在前：

| byte_order | word_order | uint32 字节排列 |
|---|---|---|
| big | big | ABCD |
| little | big | BADC |
| big | little | CDAB |
| little | little | DCBA |

`bits` 可保留位号到语义字符串的映射，值仍由整个数值寄存器控制。当前馈电协议未确认 bit 含义，因此默认 `bits: {}`；不要把其他设备的位定义填进来。仓库无 BCD、ASCII、String 的实际协议依据，本阶段不实现这些类型。

## 修改数值、状态与动态模拟

**本阶段修改 YAML 后重启生效，无热加载。** 例如把设备 1 的 `voltage.value` 从 300 改为 280，重新启动后 Go 读到 280。修改 `status.value` 为 4，Go 下一轮采集得到原始状态值 4；这不意味着已模拟某种确认的告警。当前 Go 无告警详情采集代码。

数值寄存器可添加：

```yaml
simulation:
  type: increment
  interval: 1
  step: 0.1
  min: 280
  max: 320
```

`fixed` 保持初值；`increment` / `decrement` 每 interval 秒加减 step，并在边界停住；`random` 在 min/max 范围内取随机值，整数类型按 scale 量化。没有脚本或表达式引擎。更新在访问设备时按 monotonic 时间补算，无人读取时不启动后台寄存器任务。

通用写功能码可直接修改已配置的 holding / coil 数据，fixed 不会每次读取覆盖写入；重启恢复 YAML 初值。动态字段到下一次模拟时间点会按模拟规则覆盖客户端写值。

设备级慢响应：

```yaml
behavior:
  response_delay_ms: 2000
```

读取将延迟 2 秒，可与 Go 通道 `timeoutMs: 300` 配合测试超时。延迟在每次 datastore 访问前应用；单写功能码内部含写后回读时，可能应用两次延迟。同一 RTU 总线按请求串行处理，慢设备会延迟本总线后续请求，其他 PTY 不受影响。`drop_rate` 暂未实现，填写会明确报错。

## RTU 与原生 PTY

模拟器调用 `pty.openpty()`，持有 master FD，从 master 收请求并写响应；Go 打开 slave 对应的固定 alias。slave 使用 raw 模式，关闭回显，生命周期内保留 slave FD，避免客户端尚未打开时 master 出现 EIO。

启动日志示例：

```text
[rtu] channel=rtu0 alias=/tmp/modbus-rtu0 pty=/dev/pts/12 baudrate=9600 bytesize=8 parity=N stopbits=1 devices=1:馈电保护器-01, 2:馈电保护器-02
```

每次启动自动更新软链接，实际 `/dev/pts/N` 以日志为准。alias 旁保留 `.lock` 小文件用于防止两个模拟器抢占同一路串口；退出时释放锁但保留锁文件 inode。不会覆盖同名普通文件。强制终止留下的旧软链接会在下次启动替换。

PTY 不模拟物理 RS485 波特率耗时、碰撞、电气噪声或真实校验位。baudrate/bytesize/parity/stopbits 保留在配置和日志中供 Go 使用；实际 PTY 流不强制物理参数。标准已知长度请求收到完整帧即可处理；未知功能码/残帧使用 30 ms 软件空闲间隔划界。每路只应有一个串行事务的 master。

## UDP 与 RTU over UDP

这两者独立配置、不同端口、不能混用：

- `protocol: udp`：一个 Datagram 为 `MBAP Header + PDU`，保留 Transaction ID、Unit ID，无 CRC。
- `protocol: rtu_over_udp`：一个 Datagram 为完整 `Slave + Function + Data + CRC16`，没有 MBAP；CRC 由 PyModbus 校验和生成。

设备 1 读取全部寄存器的真实测试报文：

```text
UDP RX: 00 01 00 00 00 06 01 03 00 00 00 07
UDP TX: 00 01 00 00 00 11 01 03 0E 0B B8 00 7D 00 00 04 D2 13 88 03 D4 00 00

RTU/UDP RX: 01 03 00 00 00 07 04 08
RTU/UDP TX: 01 03 0E 0B B8 00 7D 00 00 04 D2 13 88 03 D4 00 00 84 F4
```

每个 Datagram 要求恰好一个完整 ADU。错误 MBAP、CRC、超长/短包丢弃并记录；RTU/RTU over UDP 的未知 Slave 不响应；TCP/MBAP UDP 未知 Unit 返回网关无响应异常 `0B`。非法功能码 `01`、非法地址 `02`、非法值/长度 `03` 使用 Modbus 异常码响应。广播（Slave 0）本阶段不执行，不响应。

## Go 项目如何连接

### 当前实际采集服务：RTU

采集配置存数据库，通过管理页面“通信通道 / 设备管理”或已有 REST API 创建，不是在 `config.dev.yaml` 写设备寄存器。创建通道 `POST /api/v1/acquisition/channels` 的请求体：

```json
{
  "name": "Simulator RS485-1",
  "port": "/tmp/modbus-rtu0",
  "baudRate": 9600,
  "dataBits": 8,
  "parity": "N",
  "stopBits": 1,
  "timeoutMs": 500,
  "enabled": 1
}
```

设备 `POST /api/v1/acquisition/devices`（channelId 使用创建结果）：

```json
{
  "name": "馈电保护器-01",
  "deviceType": "FEED_PROTECTOR",
  "channelId": 1,
  "slaveId": 1,
  "pollIntervalMs": 1000,
  "failureThreshold": 3,
  "enabled": 1,
  "registerBlocks": [
    {"name": "holding-sample", "functionCode": 3, "startAddress": 0, "quantity": 2, "sortOrder": 0},
    {"name": "input-sample", "functionCode": 4, "startAddress": 0, "quantity": 1, "sortOrder": 1}
  ]
}
```

再添加 Slave 2 即可在同一总线采集第二台。第二路用 `/tmp/modbus-rtu1`，Slave 1。REST API 沿用项目现有登录认证。

配置保存后由运行中的 Go API 在当前采集周期结束后热刷新；按项目 Taskfile 启动：根目录 `task api`（SQLite profile 使用 `task api:sqlite`）；新库先运行对应 `task db:migrate` / `task db:migrate:sqlite`。API 存活用 `/health`、数据库就绪用 `/ready`；当前数据为 `/api/v1/acquisition/states`。

实际 Go 每轮按数据库中的读取块读取 FC03 和 FC04，`/api/v1/acquisition/states` 返回 `registerBlocks` 及其 raw `uint16` 值；页面负责 HEX/DEC/BIN 显示，不做业务解析。

### TCP / UDP / RTU over UDP

**当前业务采集服务只支持 RTU，不能通过填写 host/port 就启用网络模式。** 模拟器的网络模式已通过项目现用 `github.com/simonvetter/modbus v1.6.4` 实际客户端测试；后续采集 transport 接入时可用：

| 模式 | Go 库 `ClientConfiguration.URL` | ID |
|---|---|---|
| TCP | `tcp://127.0.0.1:1502` | SetUnitId(1) / (2) |
| MBAP UDP | `udp://127.0.0.1:1600` | SetUnitId(1) / (2) |
| RTU over UDP | `rtuoverudp://127.0.0.1:1700` | SetUnitId(1) / (2) |

`ReadRegisters(0, 7, modbus.HOLDING_REGISTER)` 读取开发映射。完整可运行示例在 `scripts/go_smoke.go`。

## 日志

```yaml
logging:
  level: INFO
  hex: true
```

包含时间、模式、通道、设备、Slave/Unit、功能码、地址、数量、结果；HEX 开关显示原始收发字节。TCP 原生 trace 的 RX/TX 为网络片段，有可能包含分片或多个 ADU；PDU 日志给出解析后的请求信息。`hex: false` 关闭本工具 HEX 日志，并过滤 PyModbus 错误日志自动附带的原始帧转储。

## 验证

无需预先启动模拟器的自动测试（临时端口、临时 PTY，自动清理）：

```bash
uv run python -m unittest discover -s tests -v
```

覆盖 01/02/03/04/05/06/15/16；四种模式真实 socket/PTY 收发；FC03/04、多个 ID；CRC、未知 ID、非法地址/数量/功能码/长度；异常后的恢复；PTY 分片；数据类型、字节/字序、倍率；动态值与慢响应；配置校验、PTY 别名锁和失败清理。FC04/coil 等使用明确的通用测试夹具，未把它们冒充馈电设备协议。

真实 Go 联调：在一个终端用推荐命令启动默认模拟器，另一个终端在本目录执行：

```bash
uv run python scripts/go_smoke.py
```

需保持默认 YAML 数值和端口。脚本把 Go harness 放在 `/tmp` 临时 module 中，通过本地 replace 引用真实后端模块，不修改后端 go.mod、go.sum 或数据库；默认 GOCACHE 为 `/tmp/modbus-go-build`。首次编译需现有依赖缓存或可访问 Go 模块源。

脚本直接执行真实 `NewModbusSessionFactory → PollChannelOnce → CurrentStateStore`，按配置读取 FC03/FC04 原始寄存器并检查 `registerBlocks`、ONLINE 状态和 raw 值；再执行三次未知 Slave 超时，检查 OFFLINE、同总线另一 Slave 继续 ONLINE，以及恢复成功。随后对三个网络端点执行真实 Go 库读取。整体进程有 180 秒编译/运行上限，Go 采集检查有 15 秒上下文期限。

本次实际验收结果见 [TEST_RESULTS.md](TEST_RESULTS.md)。没有启动完整 API/数据库/UI 链路，不能将模块级真实串口联调等同于完整页面验收。

## 实现边界

- `transports/` 仅负责通信；`protocol.py` / `decoder.py` 复用 PyModbus PDU、异常和 Framer/CRC；TCP 使用原生 ModbusTcpServer。
- `device.py` / `datastore.py` 统一四种模式的数据，使用新版 SimData / SimDevice；SimCore 内部 API 被隔离在 datastore，版本精确锁定，升级需复跑集成测试。
- `decoder.py` 修补 3.15 原生 TCP 对无效 PDU 返回 FC00 的行为，保留原功能码并返回合法异常；不重写功能码业务。
- 配置无热加载；扩展可从重新加载已验证配置、重建设备存储入手，通道和模型已经分离。
- 未实现厂家真实寄存器表、高开协议、告警详情、BCD/String、广播写入、drop_rate、物理 RS485 时序与噪声。
- 只保证列出的 8 个功能码，其余返回非法功能码；未配置地址返回异常，绝不自动填满所有寄存器为零。
