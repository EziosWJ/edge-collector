# ADR-0017：MQTT 上下行、可靠消息与 Starlark 远程控制

状态：Accepted

日期：2026-09-17

## 背景

Edge Collector 已完成 ADR-0014 原始寄存器采集、ADR-0015 四种 Modbus transport 和 ADR-0016 用户可配置 Starlark 动态事务。当前运行时已经能够持续维护设备 raw `registerBlocks`、设备通信状态，并在 `after_poll(ctx)` 中执行受控 FC03 / FC16 / FC05 动态事务。

完整产品目标还要求 Edge Collector 作为 MQTT Client 连接上级 Broker，承担实时数据上报、告警/事件上报、设备状态上报、控制结果上报和远程控制指令订阅。当前没有既定上级平台 MQTT Topic / Payload 协议，因此本阶段需要先定义一个稳定的 v1 接口，同时保留未来按项目适配外部平台的空间。

MQTT 不能反向污染已经稳定的采集调度：Broker 慢、断线或重连不得阻塞 Modbus poll；高频 raw 数据不得因为 MQTT 断线而逐帧无限落库；控制命令必须进入既有 channel 调度边界，不能从 MQTT callback 直接操作 Modbus session。

## 决策

### 1. 第一版范围

第一版 MQTT 同时实现上行与下行：

- raw register snapshot 上报；
- Starlark dynamic event 上报；
- edge / device 当前状态上报；
- MQTT command 下行；
- Starlark `command(ctx, name, args)`；
- command accepted / final result 上报；
- MQTT 自动重连；
- SQLite 有界 reliable outbox；
- SQLite command journal，用于命令去重和最终结果恢复；
- MQTT 管理配置和运行状态可观察性。

本阶段不建立正式业务 `telemetry` / `alarm` 模型。当前 raw 数据可以直接 MQTT 上报，但必须明确使用 `raw-register-snapshot/v1`，不得伪装成已经完成业务解析的 telemetry。

### 2. MQTT 与 acquisition 解耦

Modbus acquisition 不同步等待 MQTT publish / PUBACK。

数据路径：

```text
Modbus channelRunner
  → CurrentStateStore / ScriptEvent
  → MQTT projection / dispatcher
  → MQTT client
  → Broker
```

Broker 不可用时：

- raw snapshot 使用内存 latest/coalesce，不逐帧持久化；
- device current status 保持最新值，连接恢复后重新发布；
- reliable event 和 command result 使用 SQLite outbox；
- MQTT 状态变化不改变设备 ONLINE / DEGRADED / OFFLINE。

### 3. 单逻辑 Broker

第一版只支持一个逻辑上级 Broker，不做多 Broker、按设备路由、broker pool 或多云复制。

配置至少包括：

- `enabled`
- `edgeId`
- `brokerUrl`
- `protocolVersion`
- `clientId`
- `username`
- 加密保存的 password
- TLS enable / CA / client certificate / 加密保存的 private key
- keep alive
- connect timeout
- reconnect min / max backoff
- topic prefix
- raw publish interval
- outbox row / byte / retention limits
- command journal retention limit

`edgeId` 是上级平台稳定识别 Edge Collector 的业务身份，不使用数据库自增 ID。

### 4. MQTT 版本

默认使用 MQTT 5，但 Topic / Payload v1 不依赖 MQTT 5 专属属性。

实现保留 MQTT 3.1.1 兼容空间。`commandId`、`expiresAt`、消息 schema 等业务语义全部放在应用层 payload 中，不依赖 Correlation Data、Response Topic 或 Message Expiry 才能成立。

### 5. Topic v1

默认 Topic：

```text
{prefix}/{edgeId}/status
{prefix}/{edgeId}/device/{deviceId}/raw
{prefix}/{edgeId}/device/{deviceId}/event
{prefix}/{edgeId}/device/{deviceId}/status
{prefix}/{edgeId}/device/{deviceId}/command
{prefix}/{edgeId}/device/{deviceId}/command-result
```

`deviceId` 使用 Edge Collector 稳定设备标识，不使用 channel ID、endpoint 或 Modbus Unit ID 作为外部身份。

第一版不提供正式：

```text
.../telemetry
.../alarm
```

待后续业务字段解析与 Alarm domain 建立后再新增。

### 6. QoS 与 retain

默认策略：

- edge status：QoS 1，retain=true，并配置 LWT；
- device current status：QoS 1，retain=true；
- raw snapshot：QoS 0，retain=false；
- event：QoS 1，retain=false；
- command：QoS 1，retain=false；
- command-result：QoS 1，retain=false。

第一版不使用 QoS 2。

### 7. Raw snapshot 是 latest-state 流

Raw snapshot 在一个设备完整 poll cycle 更新 CurrentState 后生成，不与每个 Modbus request 一一对应。

若 MQTT 发送速度落后，同一设备尚未发送的 raw snapshot 只保留最新一份，旧 pending snapshot 可被覆盖。

另提供独立 `rawPublishIntervalMs`，允许设备高频 poll、MQTT 低频发布最新 snapshot。

Payload 必须保留 block validity、last success 等语义，避免失败块保留的旧值被上级误认为本轮有效值。

### 8. Reliable outbox

只有需要可靠补发的离散消息进入 SQLite `mqtt_outbox`，包括：

- script / 后续 alarm event；
- command ACCEPTED / FINAL result；
- 后续明确要求可靠的状态 transition。

高频 raw snapshot 不进入 outbox。

默认边界：

- `maxRows = 10000`
- `maxBytes = 64 MiB`
- `retention = 7 days`

这些是服务端可配置默认值，不是协议常量。

Outbox 达到硬上限时不得静默覆盖尚未确认的 command final result。低优先级可靠事件可以按显式策略拒绝并产生可观察错误，但不能无界增长。

收到 QoS1 PUBACK 后删除对应 outbox row。

### 9. Command journal 独立于 outbox

`mqtt_command_journal` 保存命令幂等和最终结果，不承担发送队列职责。

至少保存：

- `command_id`
- `device_id`
- `command_name`
- payload hash
- received / expires / started / completed 时间
- status
- result payload
- error type / message

默认保留 7 天并设置固定条数上限。

重复命令规则：

- 同 `commandId` + 同 payload：不再次执行，返回已有 accepted / final result；
- 同 `commandId` + 不同 payload：拒绝为 `COMMAND_ID_CONFLICT`。

这样避免 MQTT QoS1 重投或进程重启导致真实设备控制动作重复执行。

### 10. Command 进入 channelRunner，不直接操作 session

MQTT callback 只负责校验、journal、入队，绝不能直接调用 Modbus transport。

控制命令在当前设备完整 cycle 结束后的 channel safe boundary 执行，不做 transaction 级抢占，不破坏 ADR-0016 的设备 cycle 原子边界。

普通 poll 不能被 command 永久饿死。实现必须使用有界 pending command queue，并设置公平性约束。

排队 command 在真正开始执行的 safe boundary 捕获**当时已生效**的 published Starlark version；收到 MQTT 时不冻结旧版本。

### 11. 新增 Starlark `command(ctx, name, args)`

Published script 可选定义：

```python
def command(ctx, name, args):
    ...
```

`after_poll(ctx)` 保持现有语义。

`command()` 与 `after_poll()`：

- 使用同一 `(deviceId, publishedScriptVersion)` state scope；
- 不能并发执行；
- 复用同一 DeviceContext Modbus / delay / state / event 能力；
- `ctx.emit_event()` 在 command 中允许使用；
- state / event 仍使用 execution overlay，成功才提交，失败回滚；
- 已发送到真实设备的 Modbus write 不能 rollback。

`ctx.raw_register()` 在 command 中只表示最近 committed static snapshot，不保证为控制前即时值。涉及控制安全前置条件时，脚本应主动使用 `ctx.read_holding()` 获取设备当前值。

`command()` 正常返回 JSON-compatible value 表示 `SUCCEEDED`，返回值放入 command-result `data`；Starlark / Modbus / limit / cancel error 表示 `FAILED`。

### 12. Command 入口校验

进入 Starlark 前由 Go 平台层完成：

- JSON/schema validation；
- Topic / body `deviceId` 一致性；
- commandId 必填；
- issuedAt / expiresAt 合法性；
- command journal 去重；
- device 存在且 enabled；
- published script 已绑定；
- `command()` handler 存在；
- pending queue 有容量。

平台层错误不交给 Starlark 自己解释。

### 13. Command 状态

至少支持：

- `ACCEPTED`
- `REJECTED`
- `EXPIRED`
- `DUPLICATE`
- `SUCCEEDED`
- `FAILED`

`ACCEPTED` 和最终结果是不同阶段。最终结果必须进入 reliable outbox。

错误类型至少包括：

- `INVALID_MESSAGE`
- `DEVICE_NOT_FOUND`
- `DEVICE_DISABLED`
- `NO_SCRIPT_BOUND`
- `NO_COMMAND_HANDLER`
- `EXPIRED`
- `COMMAND_ID_CONFLICT`
- `QUEUE_FULL`
- `SCRIPT_COMPILE`
- `SCRIPT_RUNTIME`
- `SCRIPT_LIMIT`
- `MODBUS_TRANSPORT`
- `MODBUS_EXCEPTION`
- `CANCELED`

脚本执行错误尽量复用 ADR-0016 已有错误分类。

### 14. MQTT runtime 状态

独立维护：

```text
DISABLED
CONNECTING
CONNECTED
RECONNECTING
ERROR
```

配置提交后安全重建 MQTT client。MQTT reconnect 不占用 acquisition channel，也不改变设备通信状态。

连接成功后：

- 发布 retained edge online；
- 重新发布最新 device status；
- 恢复 raw latest publisher；
- drain reliable outbox；
- 恢复 command subscription。

非正常断开由 LWT 发布 edge offline。

### 15. Secret 存储

MQTT username、CA、公钥证书可以数据库保存。

Broker password 与 TLS private key 不允许数据库明文保存。使用部署级 master secret 加密后落数据库；master secret 从环境变量或只读文件提供，不写入数据库和普通管理 API 响应。

### 16. 消息 envelope

第一版统一应用层字段：

```json
{
  "schema": ".../v1",
  "messageId": "...",
  "edgeId": "edge-01",
  "deviceId": "device-01",
  "timestamp": "RFC3339 timestamp",
  "data": {}
}
```

Edge status 不要求 `deviceId`。

Command 使用专用输入 schema，至少包含：

```json
{
  "schema": "device-command/v1",
  "commandId": "...",
  "deviceId": "device-01",
  "name": "close",
  "args": {},
  "issuedAt": "...",
  "expiresAt": "..."
}
```

完整 Payload contract 在 Implementation Spec 固定。

## 后果

### 正面

- MQTT 失败不会拖慢或阻塞现场 Modbus acquisition。
- 高频 raw 数据不会因为 Broker 长时间离线导致 SQLite 无界增长。
- 告警/事件和控制结果拥有明确可靠补发路径。
- commandId journal 提供进程重启后的控制幂等性。
- MQTT 下行控制复用现有 channel/session/pacing 与 Starlark 协议平台，不在 MQTT 层硬编码厂家寄存器。
- 默认 MQTT 5，同时保留 3.1.1 上级平台兼容空间。
- raw、event、status、command 与未来 telemetry/alarm 的边界清晰。

### 代价

- 新增 MQTT runtime、outbox、command journal 和配置 secret 加密基础设施。
- command 执行延迟受当前完整设备 cycle 边界影响，不提供 Modbus transaction 级抢占。
- 当前 script event 仍不是正式 Alarm domain；未来需要新增 alarm 生命周期时不能简单把 event 表重命名为 alarm。
- 单 Broker 是第一版限制；未来多 Broker 需要新的路由决策。

## 明确不做

- 多 Broker / broker route rules；
- QoS 2；
- raw snapshot 逐帧离线历史补发；
- MQTT callback 直接发 Modbus；
- transaction 级 command 抢占；
- 正式业务 telemetry / alarm 解析模型；
- per-device Broker；
- MQTT 作为脚本可访问的任意网络 API；
- Starlark 直接管理 commandId / expiry / journal；
- 依赖 MQTT 5 专属 property 才能工作的业务协议。

## 关联

- `docs/edge-collector-requirements.md`
- `docs/plans/edge-collector-phased-delivery-plan.md`
- ADR-0014：原始寄存器采集优先于业务解析
- ADR-0015：多传输 Modbus channel / session 边界
- ADR-0016：用户可配置 Starlark Modbus 动态事务
- `docs/specs/starlark-modbus-dynamic-transactions.md`
