# 多传输 Modbus 采集 Implementation Spec

状态：accepted

适用范围：`edge-collector-api/`、`react-admin/`、`modbus-simulator/`

关联决策：[ADR-0015](../adr/0015-multi-transport-modbus-channels-and-network-device-addressing.md)

## 1. 目标

在保持 ADR-0014 原始寄存器采集模型不变的前提下，把当前仅支持 RS485 Modbus RTU 的采集链路扩展为四种 Modbus 传输：

- `MODBUS_RTU`
- `MODBUS_TCP`
- `MODBUS_UDP`（MBAP over UDP）
- `MODBUS_RTU_OVER_UDP`

本阶段必须继续复用现有设备级采集周期、寄存器读取块、当前状态、ONLINE / DEGRADED / OFFLINE 设备状态和“实时寄存器”页面。新增网络传输不能另建一套采集模型，也不能引入业务协议解析。

核心业务语义为：**通信通道是同协议的串行轮询设备组；网络 endpoint 属于设备。** 同一 TCP 通道可以包含多个不同 `host:port` 的独立现场设备，即使这些设备都使用相同 Unit ID；同一通道任一时刻仍只执行一个 Modbus transaction。

## 2. 范围与非目标

### 2.1 本阶段范围

- 通信通道增加不可修改的协议类型。
- 串口物理参数从公共通道拆到串口通道扩展。
- `slaveId` 全链路迁移为 `unitId`。
- 网络设备增加 `host` / `port` endpoint 配置。
- Go 采集运行时支持 RTU、TCP、MBAP over UDP、RTU over UDP。
- TCP 使用设备级长连接，但请求仍按通道串行调度。
- UDP 两种 framing 明确分离，不共用协议解释。
- 四种协议统一使用设备 `pollIntervalMs`、通道 `timeoutMs` 和 `interRequestDelayMs`。
- 网络 endpoint 与同协议通道移动支持运行时热更新。
- 增加通道运行状态聚合和管理页面可观察信息。
- 以 `modbus-simulator` 完成正式开发验收。

### 2.2 非目标

- FC01、FC02、FC05、FC06、FC15、FC16 等新增功能码。
- 写寄存器、远程控制和 MQTT 下行。
- 告警采集与告警详情。
- 厂家协议解析、工程量换算、bit 语义。
- Modbus TCP pipeline、并发 transaction 或连接池。
- 后台独立 TCP 重连调度器、可配置 reconnect interval、idle timeout。
- 网络设备固定本地 UDP 端口配置。
- 真实厂家网络设备作为本阶段完成门槛。
- 跨协议移动现有设备。

## 3. 持久化模型

### 3.1 公共通道

`acquisition_channel` 只保存所有传输共享的属性：

- `id`
- `name`
- `protocol`
- `timeout_ms`
- `inter_request_delay_ms`
- `enabled`
- `create_time`
- `update_time`
- `deleted`

`protocol` 仅允许 `MODBUS_RTU`、`MODBUS_TCP`、`MODBUS_UDP`、`MODBUS_RTU_OVER_UDP`，创建后不可修改。

现有 RS485 通道 migration 后统一得到 `MODBUS_RTU`。

### 3.2 串口通道扩展

新增 `acquisition_serial_channel`，与 `acquisition_channel` 1:1，仅 `MODBUS_RTU` 存在：

- `channel_id`
- `port`
- `baud_rate`
- `data_bits`
- `stop_bits`
- `parity`

现有 `acquisition_channel` 中对应串口字段迁移到该表。PostgreSQL 与 SQLite 逻辑版本锁步。

### 3.3 设备与网络 endpoint

`acquisition_device` 保留设备身份、通道关系、采集周期、失败阈值和启用状态，并将 `slave_id` 迁移为 `unit_id`。

新增 `acquisition_network_device`，与 `acquisition_device` 1:1，仅三种网络协议设备存在：

- `device_id`
- `host`
- `port`

`host` 允许 IPv4、IPv6 和 hostname，保存原始配置字符串；`port` 必须显式保存。前端可为 TCP 默认填入 502，但后端不隐式补默认端口。

### 3.4 Unit ID 与唯一性

- `MODBUS_RTU`：Unit ID `1～247`；同一通道 `(channel_id, unit_id)` 唯一。
- `MODBUS_RTU_OVER_UDP`：Unit ID `1～247`。
- `MODBUS_TCP` / `MODBUS_UDP`：Unit ID `0～255`。
- 三种网络协议按 `(channel_id, host, port, unit_id)` 唯一。

因此同一 TCP 轮询组中的 `192.168.1.10:502 / unit 1` 和 `192.168.1.11:502 / unit 1` 必须合法。

数据库约束能直接表达的规则放入 migration；需要跨表读取协议类型才能判断的规则由 Service 在事务内校验，并由 PostgreSQL / SQLite 集成测试覆盖。

## 4. API 与管理配置契约

### 4.1 通道 API

通道创建和详情返回公共字段与协议对应配置：

- 所有协议：`name`、`protocol`、`timeoutMs`、`interRequestDelayMs`、`enabled`。
- `MODBUS_RTU`：额外提供 `serialConfig`。
- 网络协议：不在通道上提供 `host` / `port`。

编辑通道时 `protocol` 只读；服务端收到变更协议请求必须拒绝，而不是隐式迁移扩展配置。

### 4.2 设备 API

设备统一使用 `unitId`。网络设备额外提供 `networkEndpoint: { host, port }`；RTU 设备不得携带网络 endpoint。

设备允许移动到同协议通道；跨协议通道移动直接拒绝。

修改网络 endpoint 或移动到同协议通道必须走既有配置成功写入后通知运行时刷新的机制。

### 4.3 前端配置

通信通道表单先选择协议，创建后协议不可编辑：

- RTU 展示串口参数；
- 三种网络协议只展示公共调度参数，不展示 endpoint。

设备表单根据所属通道协议展示配置：

- RTU：显示 Unit ID；
- TCP / UDP / RTU over UDP：显示 host、port、Unit ID。

通道列表展示协议；RTU 可展示串口 endpoint，网络通道不伪造单一远端 endpoint，因为一个轮询组可包含多个网络设备。

所有 Slave 文案迁移为 Unit ID 或协议语义明确的“从站地址 / Unit ID”，避免网络模式继续暗示通道内唯一 Slave。

## 5. 运行时模型

### 5.1 调度边界

每个启用且存在启用设备的通信通道维护一个 `channelRunner`。不同通道可以并行；同一通道所有设备和读取块继续严格串行执行。

设备调度规则延续 ADR-0013：

- `pollIntervalMs` 属于设备；
- 到期更早的设备优先，同到期时间按设备 ID；
- 不追赶超时导致错过的周期；
- `interRequestDelayMs` 作用于同一通道所有连续 Modbus 请求；
- 第一条请求不额外等待；
- 失败请求也形成下一请求的 pacing 边界。

### 5.2 Transport / Session 边界

寄存器读取仍通过稳定的 `RegisterReader` 语义进入采集逻辑；transport 差异限制在 session 创建、endpoint、framing 和生命周期管理，不扩散到读取块与当前状态模型。

运行时不得根据设备业务类型选择 Modbus transport。

### 5.3 Modbus RTU

保留现有共享串口 session：一个 RTU 通道一个物理串口会话，通道内通过 Unit ID 切换设备。

串口物理参数修改时等待当前设备采集完成，在通道边界关闭旧 session 并按新参数重建。

### 5.4 Modbus TCP

TCP session 属于设备运行上下文，因为同一轮询组可以包含多个 endpoint。

- 设备第一次实际到期采集时建立连接；
- 成功连接跨采集周期保持；
- 不因通道切换到下一设备而关闭健康连接；
- 不建立后台连接池，不允许同通道并发请求；
- 连接级错误立即关闭该设备 session，并直接结束该设备本轮采集；
- 该设备剩余读取块本轮不再尝试；
- 下一个设备继续调度，不受该连接错误阻断；
- 失败设备到下一个采集周期时再尝试 Open；
- 不做隐藏的 N 次重试，不建立独立 reconnect scheduler。

`timeoutMs` 继续作为单次通信事务的统一超时边界，连接建立不新增独立用户配置参数。

### 5.5 Modbus UDP / RTU over UDP

两种 UDP transport 都按设备 endpoint 创建运行上下文；可以复用 socket/client 对象，但不得将其解释为 TCP 式连接状态。

- `MODBUS_UDP` 使用 MBAP + PDU，无 CRC；
- `MODBUS_RTU_OVER_UDP` 使用完整 RTU frame 和 CRC16；
- 每个 Datagram 对应一个 transaction；
- 超时或协议级失败结束当前设备本轮，剩余块不再读取；
- 下一设备继续调度；
- 不暴露固定本地 UDP port 配置。

响应必须与当前设备 endpoint 和当前 Modbus transaction 语义匹配；不相关 datagram 不得污染当前设备快照。

## 6. 热更新与快照语义

配置热更新继续遵守“当前设备采集完成后再应用”的总原则，不中断 in-flight request。

### 6.1 通道更新

- `timeoutMs`、`interRequestDelayMs`、启用状态：当前请求 / 当前设备边界后应用。
- RTU 串口物理参数：当前设备结束后重建通道 session。
- `protocol`：不可修改。

### 6.2 设备更新

- 网络 `host` / `port` 修改：当前设备结束后关闭旧 session；已有读取块最后成功值保留但立即标记无效；下周期访问新 endpoint。
- 同协议通道移动：采用相同语义，旧运行上下文释放，新通道接管；已有值保留但无效，直到新通道首次成功。
- 跨协议通道移动：配置层拒绝。
- Unit ID 修改属于寻址变化：保留最后成功值但立即无效，直到新地址成功。
- 读取块自身修改继续遵守 ADR-0014 的快照身份规则。

## 7. 设备状态与通道运行状态

设备状态继续由读取块结果决定：

- 完整成功：`ONLINE`；
- 部分成功：`DEGRADED`；
- 全部失败达到设备 `failureThreshold`：`OFFLINE`。

网络 transport 的连接级 / transaction 级失败使该设备本轮剩余读取块不再执行，因此该轮未执行块应被视为本轮无有效更新，且不得保留“本轮有效”标记。

通道运行状态只聚合启用设备状态，不增加通道失败阈值：

- 无启用设备：`IDLE`；
- 尚未有通信结果：`STARTING`；
- 全部启用设备 `ONLINE`：`ONLINE`；
- 存在 `ONLINE` 且同时存在非 `ONLINE`：`DEGRADED`；
- 没有 `ONLINE` 且仍有 `STARTING`：`STARTING`；
- 全部启用设备 `OFFLINE`：`OFFLINE`；
- 其他包含 `DEGRADED` 的组合归为 `DEGRADED`。

通道状态至少提供 `lastAttemptAt`、`lastSuccessAt`、`lastError` 等可观察信息；它是进程内运行状态，不作为历史记录持久化。

## 8. 当前状态与实时寄存器页面

`/api/v1/acquisition/states` 的设备原始寄存器主体继续使用 `registerBlocks`，不按 transport 分裂接口。

网络协议采集成功后与 RTU 使用完全一致的读取块快照结构、数值格式和 ONLINE / DEGRADED / OFFLINE 设备状态。

实时寄存器页面：

- 继续支持 HEX / DEC / BIN；
- 设备导航把旧 Slave 地址文案调整为 Unit ID；
- 网络设备可展示 `host:port` 供现场识别；
- 可展示所属通道协议和通道运行状态，但不得把 TCP “connected” 状态泛化给 UDP；
- 页面不得恢复任何占位业务协议字段。

## 9. `modbus-simulator` 正式开发验收

模拟器继续作为本阶段正式开发验收基线。现有 TCP、MBAP over UDP、RTU over UDP 能力应扩展或配置出可验证“同一轮询组多个 endpoint、重复 Unit ID”的拓扑。

最低验收拓扑：

- 一个 RTU 通道，至少两个 Unit；
- 一个 TCP 轮询组，至少两个不同 endpoint，两个 endpoint 均允许 Unit ID `1`；
- 一个 MBAP-over-UDP 轮询组，至少两个不同 endpoint；
- 一个 RTU-over-UDP 轮询组，至少两个不同 endpoint；
- 四种协议可以同时运行，各通道互不阻塞。

若本机多 IP fixture 不稳定，可使用不同端口表达不同 endpoint；验收目标是证明 endpoint 属于设备且唯一性不是 `(channel_id, unit_id)`。

## 10. 测试与验收矩阵

### 10.1 数据库与 API

PostgreSQL 与 SQLite 均验证：

- 旧 RTU 配置迁移为新模型且数据不丢失；
- protocol 校验与不可修改；
- `slave_id` → `unit_id` 数据迁移；
- RTU / 网络扩展表 1:1 关系；
- RTU 与网络 Unit ID 范围；
- 网络 endpoint 必填和 port 边界；
- RTU `(channel, unit)` 唯一；
- 网络 `(channel, host, port, unit)` 唯一；
- 不同 endpoint 重复 Unit ID 合法；
- 同协议通道移动合法、跨协议移动拒绝。

### 10.2 运行时

可控 fake session 与真实 simulator 分别验证：

- 四协议走相同读取块逻辑；
- 同通道严格串行；
- 不同通道可以独立推进；
- pacing 跨设备和跨读取块生效；
- TCP 设备 session 跨周期复用；
- TCP 连接错误结束当前设备本轮但不阻塞下一设备；
- 下周期重新连接并恢复；
- UDP 超时 / framing 错误不污染其他 endpoint；
- endpoint / Unit ID / 同协议通道移动热更新等待 in-flight 设备完成；
- 旧快照值保留但在寻址变化后立即无效；
- 设备状态和通道聚合状态正确。

### 10.3 前端

验证：

- 通道创建协议选择与协议只读编辑；
- RTU 串口配置条件显示；
- 网络设备 endpoint 条件显示；
- Unit ID 按协议校验；
- TCP 默认端口 502 只属于前端初值；
- 通道 / 设备列表协议与 endpoint 展示；
- 实时寄存器页面在四协议下保持同一快照体验。

### 10.4 完整验收

最终端到端必须证明：

1. PostgreSQL 与 SQLite 契约一致；
2. RTU 既有能力无回归；
3. TCP 同一轮询组两个 endpoint、相同 Unit ID 可持续串行采集；
4. MBAP over UDP 和 RTU over UDP 均能持续采集；
5. 某一个 endpoint 离线不会阻断同组其他设备；
6. TCP 断开后下周期能够重连恢复；
7. endpoint 热修改与同协议通道移动无需重启进程；
8. 四协议同时运行时互不阻塞；
9. `/states` 与实时寄存器页面保持 transport-neutral 的 `registerBlocks` 契约；
10. 后端检查、PostgreSQL/SQLite integration、前端 lint/build 和相关 simulator/E2E 全部通过。

本阶段完整 browser 验收入口为 `cd react-admin && UV_CACHE_DIR=/tmp/modbus-uv-cache npm run test:acquisition-e2e`。该测试使用 `modbus-simulator/config/adr0015-e2e.yaml` 的真实不同网络端口，在临时 SQLite 数据库中经 `cmd/api` 创建四协议设备，再由 Playwright 检查配置列表 endpoint、实时寄存器 `registerBlocks`、四通道 ONLINE、重复 Unit ID 和 endpoint 热更新。

## 11. 交付策略

采用 expand / migrate / contract 的顺序，确保每张 implementation ticket 完成时仓库仍可运行：

1. 先扩展 schema 与配置领域模型，并把既有 RTU 数据迁移到新结构；
2. 泛化通道 / 设备配置 API 与前端配置，不改变现有 RTU 采集结果；
3. 把运行时从“通道唯一 session”抽象为按 transport 管理会话；
4. 依次打通 TCP、MBAP over UDP、RTU over UDP；
5. 完成热更新、通道状态、页面可观察性；
6. 最后执行四协议端到端回归并清理迁移兼容代码。

实现过程中不得为了未来控制指令提前泛化功能码或增加未被本 Spec 使用的 transport abstraction。
