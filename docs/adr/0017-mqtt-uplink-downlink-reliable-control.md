# ADR-0017：MQTT 上下行、可靠消息与 Starlark 远程控制

状态：Accepted

日期：2026-09-17

## 背景

Edge Collector 已完成 ADR-0014 原始寄存器采集、ADR-0015 四种 Modbus transport，以及 ADR-0016 用户可配置 Starlark 动态事务。现有 runtime 已能维护设备 raw `registerBlocks`、通信状态，并在同一 channel/session/pacing 边界内执行 `after_poll(ctx)`、FC03/FC16/FC05、state/event overlay。

完整产品还要求 Edge Collector 作为 MQTT Client 连接上级 Broker，承担 raw/status/event 上报、远程 command 订阅和 command result 反馈。当前没有既定上级平台 MQTT Topic/Payload 协议，因此本阶段定义稳定 v1 contract。

MQTT 必须与现场采集解耦：Broker 慢、断线、重连不得阻塞 Modbus poll；高频 raw 不得逐帧离线落库；MQTT callback 不得直接操作 Modbus session；QoS1 重投与进程重启不得导致真实设备控制重复执行。

## 决策

### 1. 第一版范围

第一版同时实现：

- 单逻辑 Broker MQTT Client；
- raw register snapshot 上报；
- edge/device current status 上报；
- Starlark dynamic event 上报；
- MQTT command 下行；
- 可选 Starlark `command(ctx, name, args)`；
- command ACCEPTED / FINAL result；
- 自动重连；
- bounded reliable outbox；
- command journal；
- MQTT 配置、TLS/认证和运行状态管理。

本阶段不建立正式业务 `telemetry` / `alarm` domain。当前原始寄存器只能以 `raw-register-snapshot/v1` 上报。

### 2. MQTT 与 acquisition 解耦

数据路径：

```text
Modbus channelRunner
  → CurrentStateStore / committed ScriptEvent
  → MQTT projection / dispatcher
  → MQTT client
  → Broker
```

Acquisition 不同步等待 MQTT publish/PUBACK。MQTT runtime 状态不改变设备 `ONLINE/DEGRADED/OFFLINE`。

Broker 不可用时：

- raw：内存 latest/coalesce，不逐帧持久化；
- device current status：保留最新状态，重连后重新发布；
- event / command result：进入可靠 outbox。

### 3. Broker 与协议版本

第一版只支持一个逻辑 Broker，不做 multi-broker、per-device broker、route rules 或 broker pool。

默认 MQTT 5，但 Topic/Payload 不依赖 MQTT 5 专属 property；应用层保留 MQTT 3.1.1 兼容空间。`commandId`、expiry、schema 等全部由 payload 表达。

### 4. Topic v1

```text
{prefix}/{edgeId}/status
{prefix}/{edgeId}/device/{deviceId}/raw
{prefix}/{edgeId}/device/{deviceId}/event
{prefix}/{edgeId}/device/{deviceId}/status
{prefix}/{edgeId}/device/{deviceId}/command
{prefix}/{edgeId}/device/{deviceId}/command-result
```

`edgeId` 和 `deviceId` 是稳定业务 identity；不使用数据库自增 ID、channel ID、endpoint 或 Modbus Unit ID 代替外部 identity。

Identity segment 第一版禁止 `/`、`+`、`#`。

### 5. QoS 与 retain

默认：

- edge status：QoS1，retain=true，配置 LWT；
- device current status：QoS1，retain=true；
- raw：QoS0，retain=false；
- event：QoS1，retain=false；
- command：QoS1，retain=false；
- command-result：QoS1，retain=false。

第一版不用 QoS2。

### 6. LWT 时间语义

MQTT Last Will payload 在 CONNECT 时预先注册，异常断线时 Broker 只能发布已注册 payload，不能把 payload 中的时间字段动态改成实际断线时间。

因此 `edge-status/v1` 必须区分：

- Will 生成/会话建立时间；
- Broker 实际观察并转发 offline Will 的时间。

LWT offline payload 必须标记 `reason = "last_will"`。Payload 中的 `timestamp`/`generatedAt` 只代表 Will 生成时间，不得解释为 `offlineAt`。若上级需要离线发生时间，以 Broker 接收/处理 Will 的时间为准。

正常连接成功后发布 retained `online=true`；异常断开由 LWT 发布 retained `online=false`。

### 7. Raw 是 latest-state 流

Raw snapshot 在设备完整 poll cycle 结束、CurrentState 提交后投影，不与单个 Modbus request 一一对应。

同一设备最多保留一份 pending latest raw；新快照可以覆盖尚未发送的旧快照。提供独立 `rawPublishIntervalMs`，只限制 MQTT publish 频率，不改变 `pollIntervalMs`。

Payload 必须保留 block validity、last-success/attempt 和旧值语义，避免失败块的 last-known value 被误认为当前有效值。

### 8. Reliable outbox

只有离散可靠消息进入 `mqtt_outbox`：

- committed script / 后续 alarm event；
- command ACCEPTED / rejection / FINAL result；
- 后续明确要求可靠的状态 transition。

Raw 不进入 outbox。

默认：

- maxRows = 10000
- maxBytes = 64 MiB
- retention = 7 days

这些是可配置默认值。

QoS1 PUBACK 后删除 row。Outbox 达到容量边界时不得为了低优先级消息静默删除未确认的 command FINAL。

### 9. Command journal 与幂等

`mqtt_command_journal` 是命令幂等和最终结果事实源，不是发送队列。至少保存 commandId、deviceId、name、payloadHash、received/issued/expires/started/completed 时间、status、result 和 error。

默认保留 7 天并设置 row 上限；不得清理仍处于 ACCEPTED/RUNNING 的记录。

重复规则：

- 相同 commandId + 相同 canonical payload：不再执行，返回已有状态/结果；
- 相同 commandId + 不同 payload：`COMMAND_ID_CONFLICT`。

### 10. Final-result 容量准入

真实设备控制一旦发生，FINAL result 必须具有可持久化、可补发路径。

因此 command 在进入 `ACCEPTED` 并 enqueue 前，平台必须确认：

- command journal 可以持久化；
- 后续 FINAL result 的 reliable outbox 容量可以得到保证。

实现可以使用 reservation、关键消息专用保留容量或等价机制，但不能只在控制执行完成后才发现 outbox 已满。

若无法保证 FINAL result 的可靠持久化路径，必须在任何真实 Modbus 控制动作发生前拒绝该命令，错误类型为 `RELIABLE_RESULT_CAPACITY_EXHAUSTED`。

禁止出现“设备已动作，但 FINAL result 因 outbox 容量耗尽无法持久化”的状态。

### 11. Command 进入 channelRunner

MQTT callback 只做 parse/validate/journal/admission/enqueue，不得直接调用 Modbus transport。

Command 在当前完整 device cycle 完成后的 channel safe boundary 执行，不做 transaction 级抢占，不破坏 ADR-0016 的设备 cycle 原子边界。

Pending command queue 必须 bounded，并保证普通到期 poll 不被 command 永久饿死。

排队 command 在真正开始执行的 safe boundary 捕获当时已生效的 published Starlark version；收到 MQTT 时不冻结旧版本。

### 12. Starlark command 入口

Published script 可选定义：

```python
def command(ctx, name, args):
    ...
```

`command()` 与 `after_poll()`：

- 使用同一 `(deviceId, publishedScriptVersion)` state scope；
- 不并发；
- 复用 `raw_register/read_holding/write_registers/write_coil/delay/state/event/host_time`；
- 复用 resource limits 和错误分类；
- state/event 成功提交、失败回滚；
- 已发送 Modbus write 不可 rollback；
- command 中允许 `emit_event()`。

`raw_register()` 在 command 中只表示最近 committed snapshot；涉及控制安全前置条件时脚本应主动 `read_holding()`。

正常 return `None` 或 JSON-compatible value → `SUCCEEDED`；runtime/Modbus/limit/output/cancel error → `FAILED`。

### 13. Command 平台校验

进入 Starlark 前由 Go 平台层完成：

- strict JSON/schema validation；
- topic/body deviceId 一致；
- commandId/name/args/issuedAt/expiresAt 校验；
- expiry；
- canonical payload hash + journal dedupe；
- device exists/enabled；
- published script bound；
- `command()` handler available；
- pending queue capacity；
- reliable final-result capacity admission。

平台错误不交给 Starlark 解释。

错误至少包括：

- `INVALID_MESSAGE`
- `DEVICE_NOT_FOUND`
- `DEVICE_DISABLED`
- `NO_SCRIPT_BOUND`
- `NO_COMMAND_HANDLER`
- `EXPIRED`
- `COMMAND_ID_CONFLICT`
- `QUEUE_FULL`
- `RELIABLE_RESULT_CAPACITY_EXHAUSTED`
- `SCRIPT_COMPILE`
- `SCRIPT_RUNTIME`
- `SCRIPT_LIMIT`
- `SCRIPT_OUTPUT`
- `MODBUS_TRANSPORT`
- `MODBUS_EXCEPTION`
- `CANCELED`

### 14. MQTT runtime

独立状态：

```text
DISABLED
CONNECTING
CONNECTED
RECONNECTING
ERROR
```

配置 commit 后安全重建 MQTT client。重连采用有界 backoff，不占 acquisition channel，不 busy-loop。

连接恢复后：

1. 恢复 command subscription；
2. 发布 retained edge online；
3. 重新发布最新 device status；
4. 恢复 raw latest publisher；
5. drain reliable outbox。

### 15. Secret 存储

Username、CA、公钥证书可存数据库。Broker password 和 TLS private key 必须使用部署级 master secret 加密后落库；master secret 来自环境变量或只读文件，不写数据库、不进入 API、日志或审计。

API 必须支持 keep/set/clear secret 语义，不把 masked value/ciphertext 当明文回写。

### 16. Wire schema

统一上行 envelope：

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

Edge status 可省略 deviceId。LWT 的 timestamp 遵循第 6 节特殊语义。

Command input：

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

完整字段和 REST/DB contract 见 Implementation Spec。

## 后果

正面：MQTT 与 acquisition 解耦；raw 不会断线无界积累；关键 event/result 可补发；commandId+journal 防止重复控制；控制复用现有 Modbus/Starlark runtime；MQTT5 默认但业务协议可兼容 3.1.1。

代价：新增 MQTT runtime、secret encryption、outbox、journal、command admission/queue；command 延迟受完整 device cycle safe boundary 影响；script event 仍不是正式 Alarm domain；第一版只支持单 Broker。

## 明确不做

- 多 Broker / route rules；
- QoS2；
- raw 逐帧离线历史补发；
- MQTT callback 直接发 Modbus；
- transaction 级 command 抢占；
- 正式 telemetry/alarm 解析；
- per-device Broker；
- Starlark 任意 MQTT/network access；
- Starlark 管理 commandId/expiry/journal；
- 依赖 MQTT5 专属 property 才成立的业务协议。

## 关联

- `docs/requirements/edge-collector-requirements.md`
- `docs/requirements/delivery-phases.md`
- ADR-0014：raw validity/current-state
- ADR-0015：多 transport channel/session/pacing
- ADR-0016：Starlark 动态事务
- `docs/specs/mqtt-uplink-downlink-reliable-control.md`
- GitHub Spec #39
