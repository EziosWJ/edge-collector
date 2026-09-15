# ADR-0015: 多传输 Modbus 通道与网络设备寻址

## Status

Accepted

## Context

Edge Collector 已通过 ADR-0011、ADR-0013 和 ADR-0014 建立 RS485 Modbus RTU 的持续轮询、通道级串行请求、设备级采集周期、运行时热刷新和原始寄存器读取块模型。下一阶段需要继续支持 Modbus TCP、Modbus UDP 和 Modbus RTU over UDP，并保持现有 `/api/v1/acquisition/states`、`registerBlocks` 与实时寄存器页面的数据语义。

现有 `acquisition_channel` 直接保存串口参数，隐含“通道等于一条 RS485 物理总线”；`acquisition_device` 使用 `slave_id`，并以 `(channel_id, slave_id)` 约束唯一性。该模型无法表达实际网络现场：同一个轮询组可能包含 `192.168.1.10:502` 与 `192.168.1.11:502` 两个独立设备，而它们都可能使用 Unit ID `1`。

本 ADR 将“通信通道”明确为**使用同一种 Modbus 传输协议、共享串行轮询调度的一组设备**。通道不是网络 endpoint，也不要求等同于一条物理介质。即使 Modbus TCP 技术上允许并发，本阶段也主动保持同一通道内一次只执行一个 Modbus transaction，以复用既有调度、报文间隔和故障隔离语义。

本 ADR supersede ADR-0011 中“采集传输范围仅为 RS485 Modbus RTU”的阶段限制，并把 ADR-0013 的通道串行请求与 `interRequestDelayMs` 语义推广到四种 Modbus 传输。ADR-0014 的原始寄存器模型继续有效：本阶段仍只采集 FC03 / FC04，不引入业务协议解析、告警、控制写入或 MQTT。

当前交付计划曾把 TCP / UDP 放在更后阶段；本 ADR 记录的新决策是将多传输 Modbus 接入提前为下一实现阶段。后续交付计划应以本 ADR 为准调整顺序，但不因此扩大本 ADR 的功能范围。

## Decision

### 1. 通道定义为同协议的串行轮询设备组

通信通道使用以下协议枚举：

- `MODBUS_RTU`
- `MODBUS_TCP`
- `MODBUS_UDP`
- `MODBUS_RTU_OVER_UDP`

其中 `MODBUS_UDP` 明确定义为 **MBAP Header + Modbus PDU over UDP**，不包含 CRC；`MODBUS_RTU_OVER_UDP` 为完整 `Slave + Function Code + Data + CRC16` RTU 帧作为单个 UDP Datagram。两者不得共用 framing 解释。

一个通道只能使用一种协议。协议在通道创建后不可修改；需要更换协议时新建目标通道并重新配置设备，避免同时切换持久化扩展表、会话类型和运行时语义。

同一通道内所有 Modbus transaction 串行执行，不为 Modbus TCP 引入 pipeline 或并发请求。多个通道仍可以由各自的 runner 并行运行。

### 2. 通道保存调度属性，设备保存网络 endpoint

持久化模型拆分为公共通道、串口通道扩展和网络设备扩展：

- `acquisition_channel`：保存 `id`、`name`、`protocol`、`timeout_ms`、`inter_request_delay_ms`、`enabled` 及审计时间等通用字段；
- `acquisition_serial_channel`：与 `acquisition_channel` 1:1，仅 `MODBUS_RTU` 使用，保存 `port`、`baud_rate`、`data_bits`、`stop_bits`、`parity`；
- `acquisition_device`：继续保存设备身份、`channel_id`、`unit_id`、`poll_interval_ms`、`failure_threshold`、启用状态与读取块关系；
- `acquisition_network_device`：与 `acquisition_device` 1:1，仅三种网络协议使用，保存 `host`、`port`。

网络 endpoint 属于设备而不是通道。一个 `MODBUS_TCP` 通道可以同时轮询 `192.168.1.10:502` 与 `192.168.1.11:502`；两个设备仍受同一个通道 runner 的串行调度约束。

`host` 允许 IPv4、IPv6 或 hostname，数据库保存用户配置的原始字符串，不持久化 DNS 解析结果。网络 `port` 必须显式持久化；前端创建 Modbus TCP 设备时可以预填 `502`，后端不根据协议隐式补端口。

### 3. `slaveId` 统一升级为 `unitId`

数据库列、Go model、API DTO 和前端字段统一使用 `unit_id` / `unitId`。其协议语义为：

- `MODBUS_RTU`：RTU Slave Address，范围 `1～247`；
- `MODBUS_RTU_OVER_UDP`：RTU Slave Address，范围 `1～247`；
- `MODBUS_TCP`：MBAP Unit Identifier，范围 `0～255`；
- `MODBUS_UDP`：MBAP Unit Identifier，范围 `0～255`。

唯一性按实际寻址范围约束：

- `MODBUS_RTU`：同一通道内 `(channel_id, unit_id)` 唯一；
- 三种网络协议：同一通道内 `(channel_id, host, port, unit_id)` 唯一。

因此同一 TCP 通道内不同 IP 的设备都使用 Unit ID `1` 是合法且预期的配置。

### 4. 保持通道级串行调度与通用请求节流

`pollIntervalMs` 继续属于设备，表达同一设备两次完整采集之间的目标等待时间。

`interRequestDelayMs` 继续属于通道，并推广到四种协议：同一通道上一条 Modbus 请求完成后、下一条请求发送前统一增加业务等待。它既作用于同一设备的连续读取块，也作用于不同设备之间的请求。`0` 表示不额外增加业务等待。

只有 Modbus RTU 还同时受底层协议帧间静默约束；`interRequestDelayMs` 不替代 RTU 的 `t3.5`。

同一通道不因上一请求超时而追赶周期，不在调度器内部补发历史周期，也不因 TCP 支持 transaction ID 而并行请求。

### 5. TCP 使用设备级长连接，连接错误结束该设备本轮

TCP endpoint 属于设备，因此 TCP session 也属于设备运行上下文，而不是通道唯一 session。

生命周期规则：

- 第一次实际轮询该设备时按需 `Open()`；
- 成功建立后跨采集周期保持连接；
- 通道切换到下一设备时不关闭前一设备的正常 TCP 连接；
- 同一通道有 N 个 TCP 设备时允许最多维持 N 条空闲长连接，但请求仍严格串行；
- 不引入连接池、idle timeout、后台 reconnect scheduler 或隐藏的多次请求重试；
- 发生明确的连接级错误时立即关闭该设备 session，并结束该设备本轮剩余读取块；
- 下一个设备采集周期到来时再重新 `Open()`，由现有采集循环自然承担重试驱动。

普通 Modbus 异常响应或单个读取块业务失败仍按 ADR-0014 的读取块语义处理；只有被判定为 transport / connection 级错误时才提前结束该设备本轮。

### 6. UDP 与 RTU over UDP 使用设备级 endpoint 上下文

`MODBUS_UDP` 与 `MODBUS_RTU_OVER_UDP` 不定义“长连接”。运行时可以为设备保留 client/session 与 UDP socket 作为 endpoint 上下文，但每个 Datagram 对应一个独立 Modbus transaction。

网络通道默认由 OS 分配本地 UDP 端口，本阶段不增加固定本地端口配置。响应必须与当前设备 endpoint 和当前 transaction 的协议身份匹配；不属于当前 transaction 的 Datagram 不得当作当前响应消费。

UDP 超时或协议错误结束当前设备本轮；下一个采集周期重新尝试，不增加额外后台重连机制。

### 7. `timeoutMs` 继续表示单次通信事务超时

本阶段不拆分 connect timeout、request timeout 和 reconnect backoff 配置。

`timeoutMs` 统一表示单次 Modbus transaction 的通信时间边界；TCP 建连也使用该边界。失败后的下一次尝试由设备下一采集周期触发，不新增可配置 reconnect interval。

### 8. 配置热更新遵守设备与通道边界

ADR-0013 的“数据库先提交、运行时随后刷新、当前正在执行的采集不被中断”继续有效。

新增规则：

- 网络设备 `host` / `port` 允许修改；
- 网络 endpoint 修改必须等待该设备当前采集结束，再关闭旧 session；
- endpoint 修改后保留最近一次成功寄存器值，但立即将对应快照标记为无效，直到新 endpoint 首次成功采集；
- 设备允许移动到**相同协议**的其他通道，并采用同样的快照失效语义；
- 设备禁止直接移动到不同协议的通道；跨协议迁移通过新建或重新配置明确完成，不把旧 transport 状态继承为新协议当前状态；
- 通道协议不可修改；
- `MODBUS_RTU` 的串口参数修改继续按 ADR-0013 在通道边界关闭旧 session 并重建。

### 9. 通道运行状态由设备状态聚合

本阶段增加进程内“通道运行状态”，用于表达轮询组整体健康，不另设 `channelFailureThreshold`。

状态为：

- `IDLE`：通道没有已启用并加载到运行时的设备；
- `STARTING`：存在尚未完成首次通信结果的设备，且当前没有可用的 `ONLINE` / `DEGRADED` 设备结果；
- `ONLINE`：通道内所有已产生结果的启用设备均为 `ONLINE`，且没有仍处于首次等待的设备；
- `DEGRADED`：存在 `DEGRADED` 设备，或通道内同时存在可用设备与异常 / 首次等待设备，或多个已产生结果的设备状态不一致；
- `OFFLINE`：所有已启用设备均已进入 `OFFLINE`，且没有仍处于首次等待的设备。

通道状态记录 `lastAttemptAt`、`lastSuccessAt` 和 `lastError` 等运行时观察字段；其失败判定来自设备状态聚合，不维护独立连续失败计数。

### 10. 本阶段继续只采集原始 FC03 / FC04

多传输协议只扩展“如何到达设备”，不改变 ADR-0014 的数据边界。

生产采集继续仅支持：

- FC03 Read Holding Registers；
- FC04 Read Input Registers；
- 原始 16-bit 无符号寄存器快照；
- 读取块级有效性、时间与错误；
- 设备 `ONLINE / DEGRADED / OFFLINE` 与新增通道运行状态。

FC01 / FC02、FC05 / FC06 / FC15 / FC16、厂家业务协议解析、告警、控制、MQTT 和 Modbus Server 均不进入本 ADR。

### 11. 管理页面使用协议驱动配置

通信通道页面先选择协议，再显示协议相关配置：

- `MODBUS_RTU`：显示串口参数；
- 三种网络协议：通道本身不显示远端 endpoint，网络设备配置中填写 `host` / `port` / `unitId`。

通道列表显示协议与轮询组信息；协议字段创建后只读。网络设备列表或详情显示 endpoint，例如 `192.168.1.10:502 · Unit 1`。

前端显示名称使用“Modbus UDP (MBAP)”与“Modbus RTU over UDP”，避免把两种 UDP framing 混淆。

### 12. `modbus-simulator` 作为本阶段正式开发验收基线

在真实厂家网络设备进入现场验收前，`modbus-simulator` 是正式开发验收基线。

至少验证：

1. `MODBUS_RTU`、`MODBUS_TCP`、`MODBUS_UDP`、`MODBUS_RTU_OVER_UDP` 都通过同一套 `registerBlocks → CurrentStateStore → /api/v1/acquisition/states → 实时寄存器页面` 主链路；
2. 每种协议至少覆盖两个设备 / Unit；TCP 场景必须覆盖同一通道两个不同 `host:port` 且可使用相同 Unit ID；
3. 四种协议都验证 FC03 / FC04；
4. 网络超时、TCP 断连和恢复后设备状态可以恢复；TCP 连接级错误会结束该设备本轮而不阻塞通道后续设备；
5. 多个不同协议通道同时运行互不阻塞；
6. `interRequestDelayMs` 对四种协议保持一致的通道级串行节流语义；
7. host / port 热更新、同协议通道迁移、协议不可修改和跨协议设备迁移拒绝均有契约测试；
8. PostgreSQL 与 SQLite migration、Repository/API 契约保持一致；
9. React 通道与设备表单、实时寄存器页面完成 lint/build 与对应回归验收。

## Considered Options

- **一个网络通道固定一个 `host:port`**：无法表达一个轮询组包含多台独立 IP 设备的真实现场，因此不选。
- **一个网络设备就是一个通道**：可以工作，但会把“轮询分组”退化为纯 endpoint 容器，无法统一表达用户需要的分组串行调度，因此不选。
- **Modbus TCP 在同一通道并发请求**：能够提高吞吐，但会引入 pipeline、连接并发、响应关联和更复杂的节流 / 故障语义；当前设备规模不要求该复杂度，因此不选。
- **每次 TCP 请求重新建连**：资源行为简单但增加连接开销，并且无法复用稳定设备连接，因此不选。
- **后台持续重连 TCP**：会形成第二套 reconnect scheduler，并在没有到期采集任务时制造无意义连接活动，因此不选。
- **通道表平铺串口和网络字段**：会产生大量协议无关字段与条件校验；已确认采用公共表加协议相关扩展，因此不选。
- **把网络 endpoint 放在通道扩展表**：与“同一轮询组包含多个 IP”冲突，因此不选。
- **允许修改通道协议**：会同时改变持久化扩展、会话类型和设备寻址约束，运行时切换边界不清晰，因此不选。
- **所有协议统一限制 Unit ID `1～247`**：会无必要收窄 MBAP Unit Identifier；因此按 RTU 与 MBAP 语义分别校验。
- **借多传输扩展同时增加写功能码和业务解析**：会把 transport 验证与上层语义混在同一阶段，违背 ADR-0014 的原始寄存器边界，因此不选。

## Consequences

- `acquisition_channel` 从“RS485 串口配置”提升为协议同质的串行轮询组；现有串口字段需要迁移到 `acquisition_serial_channel`。
- `slave_id` 需要迁移为 `unit_id`，现有 `(channel_id, slave_id)` 唯一约束需要按协议重构。
- 网络 endpoint 随设备持久化，三种网络协议共享 `acquisition_network_device`，避免为 TCP、UDP、RTU over UDP 建三套结构相同的表。
- 运行时从“每通道一个 RTU session”演进为“每通道一个串行 runner；session 生命周期由协议和设备寻址决定”。TCP 通道可能维持多条设备级空闲连接，但同时只执行一个 transaction。
- 现有 `RegisterReader` / 原始读取块接口可以继续作为上层协议无关边界；transport session factory 需要根据通道协议和设备 endpoint 创建或复用底层客户端。
- 通道状态成为设备状态的聚合视图，不增加第二套失败阈值。
- PostgreSQL、SQLite、Go model / DTO / Repository、Swagger、React 表单与类型都需要同步迁移。
- 交付顺序相对旧计划发生变化：多传输 Modbus 接入提前，但告警、MQTT、控制、Modbus Server 和业务协议解析仍保持后续独立设计。

## References

- [ADR-0011: 第一阶段采用 RS485 Modbus RTU 采集闭环与内存当前状态](0011-phase-one-rs485-modbus-rtu-acquisition.md)
- [ADR-0013: RS485 通道请求节流与采集配置运行时刷新](0013-rs485-request-pacing-and-runtime-reconfiguration.md)
- [ADR-0014: 第一阶段先采集原始寄存器，暂缓设备协议解析](0014-raw-register-acquisition-before-protocol-parsing.md)
- [Modbus Simulator Spec](../specs/modbus-simulator.md)
- [Edge Collector 分阶段交付计划](../requirements/delivery-phases.md)
