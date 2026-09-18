# MQTT 上下行、可靠消息与 Starlark 远程控制 Implementation Spec

状态：accepted

适用范围：`edge-collector-api/`、`react-admin/`、`modbus-simulator/`、测试基础设施

关联决策：[ADR-0017](../adr/0017-mqtt-uplink-downlink-reliable-control.md)

## 1. 目标

在现有 raw acquisition、多 transport Modbus 和 Starlark dynamic transaction 基础上，引入单 Broker MQTT Client。第一版完成 raw/status/event 上行和 command/command-result 下行闭环，同时保证：

- Broker 断线不阻塞采集；
- 高频 raw 不逐条持久化；
- 关键 event / command result 可可靠补发；
- QoS1 重投/进程重启不重复执行真实控制；
- command 在既有 channelRunner safe boundary 执行；
- 真实控制开始前已保证 FINAL result 具有可持久化路径。

第一版不建立正式业务 telemetry / alarm domain。

## 2. 总体数据流

### 2.1 上行

```text
CurrentStateStore
  ├─ completed device poll
  │    → RawProjector
  │    → per-device latest/coalesce
  │    → MQTT raw publisher
  │
  ├─ device current communication state
  │    → retained status publisher
  │
committed ScriptEvent
  → reliable event projection
  → mqtt_outbox
  → QoS1 publish
  → PUBACK
  → delete row
```

### 2.2 下行

```text
MQTT command subscription
  → strict parse/validate
  → command journal dedupe
  → device/script/queue validation
  → reliable FINAL-capacity admission
  → persist ACCEPTED/rejection
  → enqueue bounded command
  → current device cycle completes
  → channel safe boundary
  → capture current published ScriptVersion
  → command(ctx,name,args)
  → persist FINAL journal result + reliable outbox
```

MQTT callback 不直接获得 Modbus session。

## 3. 持久化模型

PostgreSQL 与 SQLite contract 必须一致。

### 3.1 `mqtt_config`

第一版只有一个逻辑配置实例。字段至少包括：

- `enabled`
- `edge_id`
- `broker_url`
- `protocol_version`: `MQTT_5` / `MQTT_3_1_1`
- `client_id`
- `username` nullable
- `password_ciphertext` nullable
- `tls_enabled`
- `ca_certificate` nullable
- `client_certificate` nullable
- `client_private_key_ciphertext` nullable
- `keep_alive_seconds`
- `connect_timeout_ms`
- `reconnect_min_ms`
- `reconnect_max_ms`
- `topic_prefix`
- `raw_publish_interval_ms`
- `outbox_max_rows`
- `outbox_max_bytes`
- `outbox_retention_days`
- `command_journal_retention_days`
- `command_journal_max_rows`
- command queue capacity / fairness 参数（若实现为配置）
- create/update audit fields

默认值：

```text
protocolVersion = MQTT_5
keepAliveSeconds = 30
connectTimeoutMs = 10000
reconnectMinMs = 1000
reconnectMaxMs = 60000
topicPrefix = edge
rawPublishIntervalMs = 1000
outboxMaxRows = 10000
outboxMaxBytes = 67108864
outboxRetentionDays = 7
commandJournalRetentionDays = 7
```

所有数值参数有服务端范围校验；`reconnectMinMs <= reconnectMaxMs`。

### 3.2 Secret encryption

Password 与 TLS client private key 使用部署级 master secret 对称加密保存。

要求：

- master secret 来自环境变量或只读文件；
- 数据库只存 ciphertext/nonce/version 等必要材料；
- master secret/password/private key 不进入 API、日志、审计或 MQTT payload；
- 未配置 master secret 时，保存需要加密 secret 的配置明确失败；
- update API 明确支持 keep / set / clear，不把 masked value/ciphertext 回写为明文。

### 3.3 `mqtt_outbox`

字段至少包括：

- `id`
- `message_id` unique
- `message_type`
- `topic`
- `qos`
- `retain`
- `payload`
- `priority`
- `created_at`
- `expires_at` nullable
- `attempt_count`
- `last_attempt_at` nullable
- `last_error` nullable

建议 priority：

1. command FINAL
2. reliable event
3. command ACCEPTED/rejection
4. 其他 reliable event

PUBACK 后删除 row。第一版不保留 sent history。

容量同时受 rows 与 bytes 约束。达到硬上限时不得静默删除未确认 command FINAL。

### 3.4 Reliable result capacity admission

控制命令在 ACCEPTED/enqueue 之前必须保证后续 FINAL result 有可靠持久化空间。

允许实现方式：

- 对每个已 ACCEPTED command 预留一份 FINAL outbox 容量；或
- 为 command FINAL 保留独立的 row/byte 配额；或
- 其他能够证明“控制执行后 FINAL 必可持久化”的等价机制。

不允许：执行真实 Modbus 后才尝试插入 final outbox，并在容量不足时失败。

准入失败：

```text
RELIABLE_RESULT_CAPACITY_EXHAUSTED
```

且不得执行真实控制。

Reservation 若存在，必须在 FINAL durable commit、命令终止或明确 rejection 时正确释放，不能泄漏容量。

### 3.5 `mqtt_command_journal`

字段至少包括：

- `command_id` PK
- `device_id`
- `command_name`
- `payload_hash`
- `received_at`
- `issued_at`
- `expires_at`
- `status`
- `started_at` nullable
- `completed_at` nullable
- `result_payload` nullable
- `error_type` nullable
- `error_message` nullable

状态至少：

- `ACCEPTED`
- `REJECTED`
- `EXPIRED`
- `RUNNING`
- `SUCCEEDED`
- `FAILED`

`DUPLICATE` 是 intake 判定，不要求覆盖原 journal 的事实状态；重复 command 应返回已有状态/结果。

Retention 只删除已完成且超过窗口的记录；不得清理 ACCEPTED/RUNNING。

## 4. Topic contract

统一 builder 生成：

```text
{prefix}/{edgeId}/status
{prefix}/{edgeId}/device/{deviceId}/raw
{prefix}/{edgeId}/device/{deviceId}/event
{prefix}/{edgeId}/device/{deviceId}/status
{prefix}/{edgeId}/device/{deviceId}/command
{prefix}/{edgeId}/device/{deviceId}/command-result
```

第一版 identity segment 禁止 `/`、`+`、`#`。

Command subscription：

```text
{prefix}/{edgeId}/device/+/command
```

从 topic 提取 deviceId，并与 body deviceId 严格一致。

## 5. MQTT runtime

状态：

```text
DISABLED
CONNECTING
CONNECTED
RECONNECTING
ERROR
```

要求：

- `enabled=false` 不建立连接；
- config commit 后使用最新 committed config refresh；
- 连接参数变化时关闭旧 client，再建新 client；
- 多次快速修改 latest committed config wins；
- reconnect 有界 backoff，不 busy-loop；
- MQTT worker 不占 acquisition channel。

重连成功：

1. 恢复 command subscription；
2. 发布 retained edge online；
3. 重新发布最新 device status；
4. 恢复 raw latest；
5. drain outbox。

## 6. LWT 与 edge status

Topic：`{prefix}/{edgeId}/status`，QoS1，retain=true。

正常 online payload 示例：

```json
{
  "schema": "edge-status/v1",
  "messageId": "...",
  "edgeId": "edge-01",
  "timestamp": "2026-09-17T15:00:00+08:00",
  "data": {
    "online": true,
    "reason": "connected"
  }
}
```

LWT 在 CONNECT 时注册，例如：

```json
{
  "schema": "edge-status/v1",
  "messageId": "...",
  "edgeId": "edge-01",
  "timestamp": "2026-09-17T15:00:00+08:00",
  "data": {
    "online": false,
    "reason": "last_will"
  }
}
```

关键语义：LWT 中 `timestamp` 是 Will 生成/CONNECT 时刻，**不是实际断线时刻**。Broker 不能在异常断线发生时动态修改预注册 payload。上级平台如需 offline observed time，应使用 Broker 收到/处理 LWT 的时间。

测试必须覆盖长连接一段时间后异常断开，证明 payload timestamp 不被误命名/解释为 `offlineAt`。

## 7. Payload v1

### 7.1 通用 envelope

```json
{
  "schema": ".../v1",
  "messageId": "...",
  "edgeId": "edge-01",
  "deviceId": "device-01",
  "timestamp": "RFC3339/RFC3339Nano",
  "data": {}
}
```

Edge status 无 deviceId。所有上行 messageId 必须唯一；可靠消息 restart 后不得碰撞历史未确认 row。

### 7.2 Device status

```json
{
  "schema": "device-status/v1",
  "messageId": "...",
  "edgeId": "edge-01",
  "deviceId": "device-01",
  "timestamp": "...",
  "data": {
    "status": "ONLINE",
    "lastSuccessAt": "...",
    "lastAttemptAt": "...",
    "error": null
  }
}
```

状态值沿用 acquisition 当前公开语义。

### 7.3 Raw snapshot

```json
{
  "schema": "raw-register-snapshot/v1",
  "messageId": "...",
  "edgeId": "edge-01",
  "deviceId": "device-01",
  "timestamp": "...",
  "data": {
    "communicationStatus": "ONLINE",
    "blocks": [
      {
        "name": "rtc",
        "functionCode": 3,
        "valid": true,
        "lastSuccessAt": "...",
        "lastAttemptAt": "...",
        "error": null,
        "registers": [
          {"address": 100, "value": 14},
          {"address": 101, "value": 40},
          {"address": 102, "value": 12}
        ]
      }
    ]
  }
}
```

规则：

- raw uint16 → JSON integer；
- 首次成功前 value 可 null；
- 失败块保留 last-known values 但 `valid=false`；
- blocks/registers 顺序 deterministic；
- 不做 signed/float/word-order/倍率/单位/bit 解释。

### 7.4 Dynamic event

```json
{
  "schema": "device-event/v1",
  "messageId": "...",
  "edgeId": "edge-01",
  "deviceId": "device-01",
  "timestamp": "...",
  "data": {
    "kind": "fault_detail",
    "key": "...",
    "scriptId": 12,
    "scriptVersionId": 34,
    "payload": {}
  }
}
```

这是 generic event，不称为业务 Alarm。

### 7.5 Command input

```json
{
  "schema": "device-command/v1",
  "commandId": "01K...",
  "deviceId": "device-01",
  "name": "close",
  "args": {},
  "issuedAt": "...",
  "expiresAt": "..."
}
```

规则：

- strict schema；第一版未知字段拒绝；
- commandId/deviceId/name 非空；
- args 必须 object；
- expiresAt > issuedAt；
- host time 已超过 expiresAt → EXPIRED；
- payload 有服务端大小限制。

Canonical payload hash 必须确定性，不受 JSON object key 顺序影响。

### 7.6 Command result

```json
{
  "schema": "device-command-result/v1",
  "messageId": "...",
  "edgeId": "edge-01",
  "deviceId": "device-01",
  "timestamp": "...",
  "data": {
    "commandId": "01K...",
    "name": "close",
    "status": "SUCCEEDED",
    "receivedAt": "...",
    "startedAt": "...",
    "completedAt": "...",
    "result": {},
    "error": null
  }
}
```

Wire error 只暴露稳定 type/message，不含 Go stack、SQL detail、secret。

## 8. Raw publisher

触发点：设备完整 static + `after_poll` cycle 完成、CurrentState 已提交之后。

规则：

- per-device 最多一份 pending latest；
- 新 snapshot 覆盖旧 pending；
- `rawPublishIntervalMs` 限 MQTT，不改 `pollIntervalMs`；
- offline/slow broker 不创建无界 queue/goroutine；
- QoS0 retain=false；
- publish failure 不改变 acquisition state。

## 9. Device status publisher

QoS1 retain=true。至少在以下时机发布当前状态：

- MQTT connect/reconnect；
- device current communication status 改变；
- device identity/config 发生需要重新声明的变化。

第一版 current status 不要求逐 transition 持久化历史。

## 10. Reliable outbox worker

要求：

- 独立 worker；
- MQTT CONNECTED 才 drain；
- priority + createdAt 顺序；
- 单条 publish 有 timeout；
- 失败更新 attempts/lastError；
- PUBACK 后删除；
- restart 自动继续；
- 不 busy-loop；
- event 只在 Starlark execution 成功 commit 后入 outbox。

Retention 与 capacity 不能破坏第 3.4 节 FINAL guarantee。

## 11. Starlark command runtime

Compiler 支持可选：

```python
def command(ctx, name, args):
    ...
```

存在时必须 callable 且签名合法；旧脚本只含 `after_poll(ctx)` 继续可用。

Command 复用：

- `raw_register`
- `read_holding`
- `write_registers`
- `write_coil`
- `delay`
- `state_get/state_set`
- `emit_event`
- `host_time`

并复用 wall/step/modbus-op/delay/state/event/print 限制。

Command 与 after_poll 共用 `(deviceId, versionId)` state scope，不能并发。

Execution overlay：成功提交 state/events，失败回滚；真实 Modbus write 不可回滚。

正常 return `None`/JSON-compatible value；不可序列化 return → `SCRIPT_OUTPUT`。

## 12. Command scheduling

Command 不在 block/after_poll 中间抢占。

执行边界：

```text
complete current device cycle
→ channel safe boundary
→ optionally execute command
→ continue normal scheduling
```

同 channel 严格串行，不同 channel 可并行。

Queue bounded。必须有 poll fairness；自动测试证明 command burst 下到期 poll 不会永久饥饿。

Command 真正开始时捕获当前 runtime 已生效 published ScriptVersion。排队期间 Publish/Rollback 后使用新版本。

## 13. Command intake 与幂等

顺序建议：

1. parse strict JSON；
2. validate schema/topic/body；
3. check expiry；
4. canonical hash + journal lookup；
5. validate device enabled/script/handler；
6. validate queue capacity；
7. reliable final-result capacity admission；
8. durable persist ACCEPTED + accepted result outbox；
9. enqueue；
10. executor start → journal RUNNING；
11. completion → atomic durable FINAL journal + final outbox；
12. release any reservation。

重复：

- same ID + same hash + FINAL → 不执行，确保已有 final result 可重新补发；
- same ID + same hash + ACCEPTED/RUNNING → 不创建第二执行 token；
- same ID + different hash → `COMMAND_ID_CONFLICT`。

启动恢复：扫描未完成 ACCEPTED。未过期且仍可执行则重新 enqueue；无法执行/已过期则形成明确 FINAL。不得永久挂起。

## 14. Error types

平台错误至少：

```text
INVALID_MESSAGE
DEVICE_NOT_FOUND
DEVICE_DISABLED
NO_SCRIPT_BOUND
NO_COMMAND_HANDLER
EXPIRED
COMMAND_ID_CONFLICT
QUEUE_FULL
RELIABLE_RESULT_CAPACITY_EXHAUSTED
```

脚本/设备错误复用 ADR-0016：

```text
SCRIPT_COMPILE
SCRIPT_RUNTIME
SCRIPT_LIMIT
SCRIPT_OUTPUT
MODBUS_TRANSPORT
MODBUS_EXCEPTION
CANCELED
```

## 15. 管理 API

至少：

```text
GET  /api/v1/mqtt/config
PUT  /api/v1/mqtt/config
POST /api/v1/mqtt/test-connection
GET  /api/v1/mqtt/state
GET  /api/v1/mqtt/outbox/stats
GET  /api/v1/mqtt/commands
GET  /api/v1/mqtt/commands/{commandId}
```

Config 回读不返回 secret/ciphertext。Test connection 有有限 timeout，不改变正式 runtime state。

State 至少返回 MQTT state、connected/disconnected、lastError、reconnect metadata、subscription filter、outbox rows/bytes/oldest age、pending raw count。

## 16. React 管理页

MQTT 管理拆为三个独立页面：

- `/mqtt/overview`：运行总览，观察 MQTT runtime、Reliable Outbox、Raw pending latest 和最近错误。
- `/mqtt/config`：连接配置，管理现有 Broker、TLS、重连、Topic、Outbox、Journal 和 Command 调度配置。
- `/mqtt/commands`：Command Journal，只读查询控制指令的持久化生命周期事实。

`/mqtt` 仅作为兼容入口，跳转到当前用户可访问的默认页面，优先进入运行总览。三个页面独立加载、独立鉴权并独立处理 loading/error/empty 状态；Reliable Outbox 不单独成页。

页面权限独立使用 `mqtt:overview:list`、`mqtt:config:list`、`mqtt:config:edit`、`mqtt:config:test`、`mqtt:command:list` 和 `mqtt:command:detail`。导航只展示用户可访问的子页面，Journal 权限不依赖连接配置权限。

运行总览可见时每 10 秒刷新，运行状态和 Outbox 独立加载；刷新失败保留上次成功数据并标记过期。连接配置支持草稿测试、保存后的异步重连、未保存变更保护和 `keep/set/clear` Secret 语义；Secret 不回显，错误、日志、审计和响应不得泄露 Secret 或 ciphertext。Journal 支持状态、设备 ID、Command ID、命令名称筛选和服务端分页，存在 `ACCEPTED` 或 `RUNNING` 记录时每 5 秒刷新。

Journal 详情只展示安全元数据和脱敏稳定错误，不返回 command payload、result payload、payload hash、ciphertext 或 credential；不增加重试、取消、重放、导出或手动下发命令。页面时间继续使用 API 的 RFC3339 UTC instant，并按 `Asia/Shanghai` 展示。

配置写入 operation audit，但 audit 不记录 secret/ciphertext。

## 17. 测试要求

### Persistence

PostgreSQL + SQLite：

- migration；
- config validation；
- secret encryption；
- outbox unique/order/delete/limits；
- journal dedupe/retention/restart；
- final capacity reservation/admission；
- 并发 command intake 下容量不超卖。

### MQTT integration

标准测试 Broker 覆盖：

- MQTT5 默认；
- MQTT3.1.1 compatibility；
- connect/reconnect/backoff；
- command subscription restore；
- retained edge/device status；
- LWT；
- LWT timestamp 不是 offlineAt；
- PUBACK 前断开与 outbox restart drain。

### Raw/status

- complete-cycle projection；
- block validity/old values；
- first-read null；
- deterministic order；
- interval；
- coalesce；
- slow/offline publisher bounded memory。

### Command

- strict schema；
- expiry；
- duplicate/conflict；
- queue full；
- reliable capacity exhausted before real write；
- safe boundary；
- fairness；
- latest script version capture；
- state/event commit/rollback；
- Modbus side-effect nonrollback；
- crash after ACCEPTED before enqueue；
- final durable but PUBACK missing → resend only, no re-execution；
- concurrent duplicate QoS1 → one real control only。

### E2E

真实测试 Broker + `modbus-simulator` 至少证明：

1. raw/status 正常上报；
2. Broker offline 时 acquisition 持续；
3. raw 不逐帧写 outbox，恢复后只发 latest；
4. committed event 可补发；
5. MQTT command 经 safe boundary 产生确定 simulator write；
6. duplicate commandId 不产生第二次真实写；
7. final ACK 前断线/重启只补 result，不重执行；
8. outbox 容量不足时 command 在 simulator write 之前被拒绝；
9. LWT 时间语义正确；
10. MQTT5/3.1.1 都通过应用 contract。

最终继续执行 backend check、PostgreSQL/SQLite integration、MQTT integration、simulator tests、frontend lint/build 和 browser/API E2E。

## 18. Out of Scope

- multi-broker / broker route rules；
- QoS2；
- raw 逐帧离线历史；
- 正式 telemetry/alarm domain；
- 业务寄存器解析；
- MQTT callback 直接操作 Modbus；
- transaction-level command preemption；
- Starlark 任意 MQTT/network access；
- per-device broker；
- 外部 command 自定义调度 priority。
