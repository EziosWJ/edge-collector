# MQTT 上下行、可靠消息与 Starlark 远程控制 Implementation Spec

状态：accepted

适用范围：`edge-collector-api/`、`react-admin/`、`modbus-simulator/`、测试基础设施

关联决策：[ADR-0017](../adr/0017-mqtt-uplink-downlink-reliable-control.md)

## 1. 目标

在现有 raw acquisition、多 transport Modbus 和 Starlark dynamic transaction 基础上，引入单 Broker MQTT Client。第一版完成 raw/status/event 上行和 command/command-result 下行闭环，同时保证 Broker 断线不会阻塞采集，高频 raw 不逐条持久化，关键 event / command result 可通过 SQLite outbox 可靠补发，控制命令通过既有 channelRunner safe boundary 执行 Starlark `command(ctx, name, args)`。

第一版是 MQTT 基础设施和 raw/control contract，不建立正式业务 telemetry / alarm domain。

## 2. 总体数据流

### 2.1 上行

```text
CurrentStateStore
  ├─ device completed poll snapshot
  │    → RawProjector
  │    → per-device latest/coalesce
  │    → MQTT raw publisher
  │
  ├─ device communication state
  │    → latest device status
  │    → retained MQTT status
  │
Script runtime committed event
  → reliable event projection
  → mqtt_outbox
  → QoS1 publish
  → PUBACK
  → delete outbox row
```

### 2.2 下行

```text
MQTT command subscription
  → parse + validate
  → command journal dedupe
  → device/script/queue validation
  → persist ACCEPTED / rejection
  → reliable command-result outbox
  → bounded device/channel command queue
  → current device cycle ends
  → channel safe boundary
  → capture current published ScriptVersion
  → command(ctx, name, args)
  → persist FINAL journal result
  → reliable command-result outbox
```

MQTT callback 不直接获得或调用 Modbus session。

## 3. 持久化模型

PostgreSQL 与 SQLite schema / repository contract 必须一致。

### 3.1 `mqtt_config`

第一版只有一个逻辑配置实例。建议字段：

- `id`
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
- create/update audit fields

约束：

- 第一版只能有一个 active config；
- `edge_id` / `client_id` 非空；
- broker URL 必须是支持的 TCP/TLS MQTT scheme；
- reconnect min <= max；
- 所有容量/时间参数做明确范围校验；
- API 回读不得返回 password/private-key 明文或可逆 ciphertext。

默认值：

- protocol: MQTT 5
- keepAliveSeconds: 30
- connectTimeoutMs: 10000
- reconnectMinMs: 1000
- reconnectMaxMs: 60000
- topicPrefix: `edge`
- rawPublishIntervalMs: 1000
- outboxMaxRows: 10000
- outboxMaxBytes: 67108864
- outboxRetentionDays: 7
- commandJournalRetentionDays: 7

### 3.2 Secret encryption

Broker password 和 TLS client private key 使用部署级 master secret 对称加密保存。

要求：

- master secret 从环境变量或只读文件加载；
- 数据库只保存 ciphertext / nonce / version 等必要材料；
- master secret 不通过 API、日志或审计 detail 返回；
- 未配置 master secret 时，如果配置包含需要加密的 secret，保存/启动必须明确失败；
- 更新 API 中空 secret 表示“保持现有 secret”，显式 clear 使用独立布尔字段或明确契约，禁止通过回读 masked value 再写回。

### 3.3 `mqtt_outbox`

建议字段：

- `id`
- `message_id` unique
- `message_type`
- `topic`
- `qos`
- `retain`
- `payload` JSON/text/blob
- `priority`
- `created_at`
- `expires_at` nullable
- `attempt_count`
- `last_attempt_at` nullable
- `last_error` nullable

第一版可直接删除已 PUBACK row，不长期保存 sent history。

容量计算同时受 row 和 payload byte 上限约束。达到硬上限时不得删除尚未确认的 command final result 为新低优先级消息腾空间。

建议 priority：

1. command FINAL
2. reliable alarm/script event
3. command ACCEPTED / rejection
4. 其他 reliable event

### 3.4 `mqtt_command_journal`

建议字段：

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

允许状态：

- `ACCEPTED`
- `REJECTED`
- `EXPIRED`
- `DUPLICATE`
- `RUNNING`
- `SUCCEEDED`
- `FAILED`

journal 是控制幂等事实源，不是 publish queue。

清理任务只删除超过 retention 且不处于 `ACCEPTED/RUNNING` 的记录；不得删除仍可能执行的命令。

## 4. 管理 API 与页面

建议 REST：

- `GET /api/v1/mqtt/config`
- `PUT /api/v1/mqtt/config`
- `POST /api/v1/mqtt/test-connection`
- `GET /api/v1/mqtt/state`
- `GET /api/v1/mqtt/outbox/stats`
- `GET /api/v1/mqtt/commands`
- `GET /api/v1/mqtt/commands/{commandId}`

运行状态至少返回：

- MQTT state
- connectedAt / disconnectedAt
- lastError
- reconnect attempt/backoff
- broker endpoint（不含 secret）
- subscribed command topic filter
- outbox rows / bytes / oldest age
- raw latest pending count

React 新增 MQTT 管理页：

- enable
- edgeId / broker / protocol / clientId
- credentials
- TLS
- reconnect / raw publish / outbox 参数
- test connection
- runtime state
- outbox health
- 最近 command journal

页面不得显示已保存 password/private key。

## 5. MQTT Client Runtime

### 5.1 状态机

```text
DISABLED
CONNECTING
CONNECTED
RECONNECTING
ERROR
```

`enabled=false` 时不连接 broker，不启动 publisher/subscription worker。

配置 commit 后通知 MQTT runtime refresh；若连接参数变化：

1. 停止旧 subscription/publisher；
2. 关闭旧 client；
3. 使用最新 committed config 建新 client；
4. 不影响 acquisition runtime。

多次快速配置更新遵循 latest committed config wins。

### 5.2 Connect / reconnect

默认自动指数/有界 backoff，在 `reconnectMinMs..reconnectMaxMs` 范围内。

Broker 连接失败不能 busy loop。

连接恢复后：

1. subscribe command filter；
2. publish retained edge online；
3. publish latest retained device status；
4. 恢复 raw latest stream；
5. drain reliable outbox。

### 5.3 LWT

Topic：`{prefix}/{edgeId}/status`

QoS 1，retain=true。

LWT payload 使用 `edge-status/v1`，`online=false`。正常连接后立即以相同 topic 发布 `online=true` retained message。

## 6. Topic Contract

通过统一 builder 生成，不允许业务模块自行字符串拼接。

```text
{prefix}/{edgeId}/status
{prefix}/{edgeId}/device/{deviceId}/raw
{prefix}/{edgeId}/device/{deviceId}/event
{prefix}/{edgeId}/device/{deviceId}/status
{prefix}/{edgeId}/device/{deviceId}/command
{prefix}/{edgeId}/device/{deviceId}/command-result
```

对 prefix、edgeId、deviceId 中 MQTT wildcard / separator 做明确校验或 escape 约束。第一版建议直接禁止 `+`, `#`, `/` 出现在 identity segment。

Command subscription 可以使用：

```text
{prefix}/{edgeId}/device/+/command
```

收到消息后从 topic 提取 deviceId，并与 body deviceId 严格比较。

## 7. Message ID 与时间

上行每条消息生成稳定唯一 `messageId`。实现可使用 UUID/ULID，contract 不依赖具体算法，但必须：

- 进程内不会碰撞；
- reliable outbox restart 后不会与历史记录冲突。

所有 wire timestamp 使用 RFC3339/RFC3339Nano，明确包含 offset 或 `Z`。

## 8. Payload v1

### 8.1 Edge status

```json
{
  "schema": "edge-status/v1",
  "messageId": "...",
  "edgeId": "edge-01",
  "timestamp": "2026-09-17T14:40:00+08:00",
  "data": {
    "online": true
  }
}
```

### 8.2 Device status

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

设备状态值沿用 acquisition 公开契约，不为 MQTT 发明第二套状态机。

### 8.3 Raw register snapshot

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

- uint16 value 使用 JSON integer；
- 首次成功前 value 可使用 `null`；
- 块失败时保留最后成功值但 `valid=false`；
- block 顺序与 register 地址顺序保持 deterministic；
- raw projection 不进行 signed/float/倍率/单位/bit 解释。

### 8.4 Script event

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

这是通用 dynamic event，不称为 alarm。

### 8.5 Command input

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

约束：

- commandId / deviceId / name 非空；
- args 必须为 JSON object；
- expiresAt 必须晚于 issuedAt；
- 当前 host time 已超过 expiresAt 时不入执行队列；
- payload 有服务端大小上限；
- 未知额外字段第一版明确选择拒绝或忽略之一，建议拒绝以暴露接口错误。

### 8.6 Command result

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

失败：

```json
{
  "error": {
    "type": "MODBUS_EXCEPTION",
    "message": "..."
  }
}
```

Wire payload 不包含 Go stack trace、DB error detail、secret 或 raw credentials。

## 9. Raw Publisher

### 9.1 触发点

设备一个完整 static + `after_poll` cycle 完成、CurrentState 已更新后，通知 raw projector。

Raw MQTT 失败不反馈成 acquisition failure。

### 9.2 Coalesce

内存按 deviceId 维护最多一份 pending latest snapshot。

如果 publish 尚未完成又有新 snapshot，则覆盖旧 pending latest。

不得创建与 poll 次数同比增长的无界 channel/queue。

### 9.3 Publish interval

每设备最多以 `rawPublishIntervalMs` 发送一次最新 snapshot。

该 interval 是 MQTT projection 限速，不改变设备 `pollIntervalMs`。

## 10. Device Status Publisher

Device status 使用 retained current-state 语义。

至少在：

- MQTT 连接建立；
- device status 改变；
- device identity/config 出现需要重新声明的变化；

发布当前状态。

第一版不要求把每一次 ONLINE↔DEGRADED transition 全部持久化为可靠历史事件；未来如上级平台要求完整状态历史，再升级为 reliable transition event。

## 11. Reliable Outbox Worker

要求：

- 单独 goroutine/worker，不阻塞 acquisition；
- 只在 MQTT CONNECTED 时 drain；
- FIFO 结合 priority，保证高优先级不会长期排在大量低优先级之后；
- 单条 publish 使用有限 timeout；
- 失败更新 attempt/error 后按 reconnect/backoff 等待，不 busy loop；
- PUBACK 后删除；
- process restart 后继续 drain；
- 清理 expired rows 时不能误删 command final result 等未确认关键消息，除非显式 retention 策略已经定义且产生可观察告警。

## 12. Command Intake 与幂等

处理顺序：

1. topic parse；
2. payload size limit；
3. strict JSON decode / schema；
4. topic deviceId == payload deviceId；
5. expires check；
6. payload canonical hash；
7. journal lookup；
8. device existence/enabled；
9. script binding + published version availability；
10. command handler availability；
11. queue capacity；
12. transactionally persist ACCEPTED journal + accepted result outbox；
13. enqueue command execution token。

如果 12 成功而进程在 13 前崩溃，重启恢复策略必须能识别 `ACCEPTED` 未完成命令并重新入队，或者将其明确转为 FAILED；不能永久卡在 ACCEPTED。第一版建议启动时重新 enqueue 尚未过期的 ACCEPTED command。

重复 command：

- hash 相同且已有 FINAL：不执行，重新把已有 final result 放入 outbox（若尚无 pending）；
- hash 相同且 ACCEPTED/RUNNING：不创建第二次执行；可重新发布当前状态；
- hash 不同：`COMMAND_ID_CONFLICT`。

## 13. Command Queue 与公平性

每个 channel 或 device 使用有界 pending command 结构；具体内部结构可实现选择，但必须满足：

- MQTT callback 非阻塞或有短有限 enqueue 时间；
- command 不打断 in-flight device cycle；
- command 在完整 device cycle safe boundary 执行；
- 同 channel command transaction 与 poll transaction 不并发；
- 不同 channel 可并行；
- 普通 poll 不能永久饥饿；
- command queue 满时返回 `QUEUE_FULL`；
- disabled/deleted/moved device 的 pending command 需要得到明确失败结果，不可静默消失。

建议公平策略：一次 safe boundary 最多连续执行固定数量 command，然后让已到期 poll 有机会执行；参数可内部常量或服务配置，但必须有自动测试。

## 14. Starlark `command(ctx, name, args)`

### 14.1 Compile/Validate

Published script 可以只定义 `after_poll(ctx)`，也可以额外定义：

```python
def command(ctx, name, args):
    pass
```

现有 Validate 继续要求 `after_poll(ctx)`；`command` 存在时必须 callable 且签名符合要求。

### 14.2 Args

MQTT JSON `args` 转为受控 JSON-compatible Starlark dict/list/scalar。不得注入 host object。

### 14.3 Context

复用现有 DeviceContext：

- `raw_register`
- `read_holding`
- `write_registers`
- `write_coil`
- `delay`
- `state_get/state_set`
- `emit_event`
- `host_time`

同样受 execution step/wall/modbus op/delay/state/event/print limits。

### 14.4 State / event commit

command 与 after_poll 共用 `(deviceId, versionId)` state scope。

每次 command invocation 创建 overlay；成功返回后提交 state + buffered events，失败全部丢弃。已经发出的真实 Modbus write 无法回滚。

command committed event 正常进入 reliable MQTT event pipeline。

### 14.5 Return

允许：

- `None` → `result: null`
- JSON-compatible Starlark value → result JSON

不可序列化返回值映射 `SCRIPT_OUTPUT` / `FAILED`。

## 15. Script Version 与配置变化

Command 收到时不固定 ScriptVersion。

真正从 queue 开始执行时，在 channel safe boundary 捕获 runtime 当前已生效 published version。

若执行前：

- script unbind；
- device disabled/deleted；
- published pointer 不可用；

则 command 明确 FAILED/REJECTED，并 journal + result outbox，不静默 drop。

Version 变化继续沿用 ADR-0016 state reset 规则。

## 16. Error Contract

平台错误：

- `INVALID_MESSAGE`
- `DEVICE_NOT_FOUND`
- `DEVICE_DISABLED`
- `NO_SCRIPT_BOUND`
- `NO_COMMAND_HANDLER`
- `EXPIRED`
- `COMMAND_ID_CONFLICT`
- `QUEUE_FULL`
- `MQTT_UNAVAILABLE`（仅管理/test-connection 等需要时）

执行错误复用：

- `SCRIPT_COMPILE`
- `SCRIPT_RUNTIME`
- `SCRIPT_LIMIT`
- `SCRIPT_OUTPUT`
- `MODBUS_TRANSPORT`
- `MODBUS_EXCEPTION`
- `CANCELED`

错误字符串必须 bounded，不能将 secret、SQL、stack trace 暴露到 MQTT wire。

## 17. Security

- command topic 只订阅当前 edgeId namespace；
- TLS/credentials 支持配置；
- 管理 API 继续使用现有权限/审计体系；
- MQTT config 修改、enable/disable、credential change 写操作审计；
- command 原始 payload 可按 bounded/sanitized 形式进入 journal，不把敏感配置拼入日志；
- Published Starlark 仍不能访问任意网络/MQTT/DB/filesystem；
- `command()` 能写真实设备，页面必须明确风险。

本阶段不新增地址级 Starlark write ACL；沿用 ADR-0016 信任边界。

## 18. 测试

### 18.1 Persistence

PostgreSQL / SQLite：

- mqtt config create/update/read；
- secret ciphertext round-trip，不回传 plaintext；
- outbox insert/order/delete/size limit；
- command journal unique/hash/status/retention；
- migration 保持现有 acquisition/script 数据。

### 18.2 MQTT fake/integration broker

至少覆盖：

- connect / reconnect / backoff；
- MQTT 5 默认；
- 3.1.1 compatibility；
- LWT / retained edge status；
- retained device status；
- raw QoS0；
- event/result QoS1 PUBACK 后 outbox 删除；
- broker offline 时 raw coalesce、outbox 保留；
- restart 后 outbox drain。

CI 可启动 Mosquitto/EMQX 等测试 broker，但产品实现不得依赖某个 broker 私有 extension。

### 18.3 Raw tests

- complete device cycle 后投影；
- block valid=false 时仍保留 last value + invalid marker；
- publish interval；
- slow publisher 覆盖旧 pending snapshot；
- 不产生无界 goroutine/channel。

### 18.4 Command tests

- valid ACCEPTED→SUCCEEDED；
- invalid JSON/schema；
- topic/body mismatch；
- expired；
- device missing/disabled；
- no script/no handler；
- duplicate same payload 不重执行；
- same commandId different payload conflict；
- process restart 后 ACCEPTED recovery；
- queue full；
- safe boundary，不打断 device cycle；
- fairness，poll 不饥饿；
- command 开始执行时捕获最新 published version；
- state/event success commit / failure rollback；
- Modbus write 已发生但后续 script failure 的不可回滚边界；
- command return serialization；
- final result outbox 可靠重发。

### 18.5 UI/API

- config secret masking；
- test connection；
- runtime state；
- outbox health；
- command journal；
- frontend lint/build。

## 19. 验收场景

至少建立一个基于已有 RTC/ZNCK 模拟设备的真实闭环：

1. 启动测试 broker；
2. Edge Collector 连接并发布 retained edge online；
3. RTU RTC Unit 3 持续采集；
4. raw topic 收到 `100..102` snapshot；
5. Broker 断线数个 poll，raw 不逐条写 outbox；
6. Broker 恢复后收到最新 raw；
7. 给绑定脚本设备发布 MQTT command；
8. command 在 safe boundary 调用 `command()`；
9. simulator 观察到一次预期 FC05/FC16；
10. command-result SUCCEEDED 经 QoS1 收到；
11. 重发同 commandId 不再次产生 Modbus write；
12. Broker 在 command final 后、PUBACK 前断开，outbox 在恢复后补发 final result。

## 20. 第一版验收口径

全部满足后才能关闭本阶段 Spec：

1. 单 Broker MQTT config 在 PostgreSQL/SQLite 一致；
2. password/private key 不明文落库或通过 API 回传；
3. MQTT runtime 自动连接、重连且不阻塞 acquisition；
4. edge/device retained status 可观察；
5. raw snapshot 按 poll cycle latest-state 发布，支持 interval/coalesce；
6. raw 断线不逐帧持久化；
7. event/result 进入 bounded SQLite outbox 并在 PUBACK 后删除；
8. outbox 不会静默淘汰 command final result；
9. command journal 防止 QoS1/restart 重复控制；
10. MQTT callback 不直接操作 Modbus；
11. command 在完整 device cycle safe boundary 执行，不破坏 channel 串行；
12. command queue bounded 且 poll 无永久饥饿；
13. Starlark `command(ctx,name,args)` 复用现有 DeviceContext/state/event/resource limits；
14. queued command 在真正执行时捕获当前 published script version；
15. command accepted/final result wire contract 完整；
16. MQTT 5 默认且应用 contract 保留 3.1.1 兼容；
17. 管理页面可以配置、测试和观察 MQTT runtime/outbox/commands；
18. broker offline/restart/duplicate command 的自动测试通过；
19. `task backend:check`、PostgreSQL/SQLite integration、MQTT integration、`task frontend:lint`、`task frontend:build` 和端到端验收通过；
20. 文档明确本阶段只有 raw/event/status/control，没有正式 telemetry/alarm。

## 21. 非目标

- 多 Broker；
- QoS 2；
- 逐帧 raw offline history；
- 正式 telemetry/alarm domain；
- 业务字段解析；
- transaction-level command preemption；
- MQTT 直接 Modbus session；
- Starlark MQTT/network access；
- per-device broker / route rule；
- command 优先级由上级任意指定；
- MQTT 历史消息查询系统。

## 22. 后续演进

后续独立阶段再决定：

- protocol/business field → telemetry；
- Alarm lifecycle → alarm MQTT；
- 正式状态 transition 历史可靠上报；
- 多 Broker / 项目协议适配；
- 更强 control authorization / approval；
- command-specific priority；
- persisted Starlark state；
- Modbus Server 与 MQTT 共享统一业务模型。
