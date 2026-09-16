# Starlark Modbus 动态事务平台 Implementation Spec

状态：accepted

适用范围：`edge-collector-api/`、`react-admin/`、`modbus-simulator/`

关联决策：[ADR-0016](../adr/0016-user-configurable-starlark-modbus-dynamic-transactions.md)

## 1. 目标

在 ADR-0014 原始寄存器采集和 ADR-0015 四协议 Modbus runtime 已稳定的基础上，引入用户可配置的 Starlark 动态脚本平台，用来表达固定 `registerBlocks` 无法描述的条件式、多步骤 Modbus 事务。

第一阶段不把全部采集迁移到脚本，也不做业务工程量解析。固定 FC03 / FC04 仍由现有 `registerBlocks` 持续采集；脚本在设备静态采集完成后以 `after_poll(ctx)` 形式执行，读取 raw snapshot，并在需要时通过受控 DeviceContext 发起 FC03 / FC16 / FC05、显式 delay、脚本状态更新和通用 event 输出。

首个正式验收场景来自 ZNCK-I 低压侧双回路开关协议：静态轮询包含寄存器 `8166`；首次看到非零 fault code 时，脚本执行 `FC16 write 8120=0 → delay 50ms → FC03 read 8121..8137`，相同 fault code 持续时不重复查询，详情仍以 raw register 数组保存。

## 2. 本阶段范围与非目标

### 2.1 范围

- PostgreSQL / SQLite 保存协议脚本、draft、不可变 published versions 和设备脚本绑定。
- 脚本管理 REST API、Swagger、React 管理页面。
- Draft → Validate → Publish → Rollback 生命周期。
- Go 进程内嵌 Starlark runtime。
- `after_poll(ctx)` 单一首版入口。
- 脚本执行固定占用当前设备所在 channel 的串行调度边界。
- 受控 DeviceContext：raw snapshot、FC03、FC16、FC05、delay、state、emit_event。
- 脚本状态与动态事件的进程内 runtime store。
- 脚本版本 / 绑定热更新。
- 硬资源限制和脚本错误隔离。
- `modbus-simulator` + fake transport + browser/API 端到端验收。

### 2.2 非目标

- 电压、电流、功率、倍率、单位、bit field 等业务字段解析。
- 业务告警的确认 / 恢复 / 消警 / 历史持久化。
- MQTT 上报或控制下行。
- Modbus Server。
- FC01 / FC02 / FC04 动态读取、FC06 / FC15 等额外脚本原语。
- raw Modbus frame API。
- Starlark `load()`、多文件 module、包管理或依赖仓库。
- 文件、HTTP、数据库、MQTT、OS、环境变量或任意网络访问。
- Modbus 地址级 capability / ACL。
- 每条自动脚本 Modbus 写操作的数据库审计流水。
- 脚本状态持久化到数据库。
- 在线调试器、断点、单步执行或 IDE 级编辑器。
- 用户上传 Go plugin、Lua、JavaScript 或其他多语言脚本。

## 3. 持久化模型

### 3.1 `acquisition_script`

新增脚本身份表，建议字段：

- `id`
- `name`
- `description`
- `draft_source`
- `published_version_id` nullable
- `create_by`
- `update_by`
- `create_time`
- `update_time`
- `deleted`

约束：

- 未软删脚本名称唯一；
- `draft_source` 是可编辑内容，不直接参与 runtime；
- `published_version_id` 只能指向同一 `script_id` 的 version；
- 删除已被设备绑定的脚本必须拒绝；
- 已发布脚本允许继续编辑 draft，不影响当前 published version。

### 3.2 `acquisition_script_version`

新增不可变版本表：

- `id`
- `script_id`
- `version_no`
- `source`
- `checksum`
- `published_by`
- `published_at`

约束：

- `(script_id, version_no)` 唯一；
- `checksum` 使用完整 UTF-8 source 的 SHA-256；
- 已创建的 version 不允许 update source；
- rollback 只改变 `acquisition_script.published_version_id`，不得改写历史 version；
- 发布事务必须同时插入 version 并更新 published pointer。

### 3.3 设备绑定

`acquisition_device` 增加 nullable `script_id`。

- 一个设备最多绑定一个动态脚本；
- 一个脚本可以绑定多个设备；
- 只能绑定已有 published version 的脚本；
- `script_id = null` 表示设备只运行固定 `registerBlocks`；
- 脚本绑定不改变设备 transport、Unit ID、endpoint、读取块、采集周期和失败阈值语义。

PostgreSQL 与 SQLite migration 版本同步，并覆盖外键、软删除、版本完整性和回读一致性。

## 4. 脚本管理 API

REST 路径使用 `/api/v1/acquisition/scripts`。

### 4.1 基础 CRUD

至少提供：

- `GET /api/v1/acquisition/scripts`
- `POST /api/v1/acquisition/scripts`
- `GET /api/v1/acquisition/scripts/{id}`
- `PUT /api/v1/acquisition/scripts/{id}`：只更新 name / description / draft source；
- `DELETE /api/v1/acquisition/scripts/{id}`：仅未绑定脚本可删除；
- `GET /api/v1/acquisition/scripts/{id}/versions`

列表 / 详情至少返回当前 draft 是否与 published checksum 一致、当前 published version、最近发布时间等管理信息。

### 4.2 Validate

`POST /api/v1/acquisition/scripts/{id}/validate`

Validate 只对当前 draft 做静态 / 解释器级校验，不访问真实 Modbus 设备，不产生写请求。

至少验证：

- UTF-8 source 长度在服务限制内；
- Starlark parse / compile 成功；
- 禁止 `load()`；
- `after_poll` 存在并可调用；
- source 只引用允许的 predeclared symbols；
- 可返回包含行列号的 compile error。

Validate 成功不自动发布。

### 4.3 Publish

`POST /api/v1/acquisition/scripts/{id}/publish`

行为：

1. 在事务内重新 Validate 当前 draft，不能依赖客户端上次校验结果；
2. 若源码与当前 published version checksum 相同，可返回当前版本或明确 no-op，不创建重复版本；
3. 创建下一 `version_no` 的不可变 version；
4. 更新 published pointer；
5. 写既有操作审计；
6. 数据库提交后通知 runtime refresh。

### 4.4 Rollback

`POST /api/v1/acquisition/scripts/{id}/rollback`

body 至少包含目标 `versionId`。

- 目标 version 必须属于同一 script；
- 不生成修改版 source；
- 更新 published pointer；
- 写操作审计；
- commit 后通知 runtime refresh。

### 4.5 设备绑定

现有设备 API 增加 `scriptId?: number | null`。

- 绑定未发布脚本返回 400；
- 解绑设置 null；
- 创建 / 更新设备成功提交后沿用现有 runtime refresh；
- 绑定 / 解绑属于设备配置审计的一部分。

## 5. React 管理页面

新增“协议脚本”管理页面。

第一版使用普通文本编辑区域即可，不建设 IDE。

页面至少支持：

- 脚本列表：名称、published version、更新时间、绑定设备数量；
- 新建 / 编辑 name、description、draft source；
- Validate 并显示行列号 / 错误文本；
- Publish；
- 查看 version history；
- Rollback 到历史版本；
- 明确提示“保存草稿不会影响运行设备”；
- 明确提示“已发布脚本可以执行 FC16 / FC05，对真实设备产生写副作用”。

设备表单增加可选“动态协议脚本”下拉：

- 只列出已有 published version 的脚本；
- 展示脚本当前 published version；
- 解绑后设备继续只运行 `registerBlocks`。

本阶段不增加单独的参数设置 / 合闸分闸页面。

## 6. Starlark Runtime

### 6.1 解释器

后端使用 Go Starlark 实现。版本依赖通过 Go module 管理，但文档不锁死具体 module commit。

Published version 首次使用时可以 compile 为缓存 Program，缓存 key 至少包含 version ID + checksum。

每次设备调用：

- 新建独立 Thread；
- 新建独立 Globals；
- 只注入受控 `ctx`；
- 不复用上一次调用的 mutable Starlark globals；
- 不在不同设备之间共享 Starlark object state。

### 6.2 入口

第一版脚本必须定义：

```python
def after_poll(ctx):
    pass
```

不定义其他自动 hook；不支持用户 cron、独立 schedule、before_poll 或后台脚本任务。

脚本执行时机：

1. 当前设备静态 `registerBlocks` 完成；
2. 若本轮已因 transport / connection error 提前终止，不执行脚本；
3. 否则在 channel runner 仍持有当前设备调度权时执行 `after_poll`；
4. 脚本结束后才计算下一设备调度。

脚本耗时属于当前设备完整 cycle 的一部分；设备下一 `pollIntervalMs` 仍从完整设备周期结束后计算，不做追赶。

## 7. DeviceContext Host API

### 7.1 `raw_register`

```python
value = ctx.raw_register(function_code, address)
```

- `function_code` 第一版只允许 3 或 4，因为来源是静态 `registerBlocks`；
- 如果当前 snapshot 没有覆盖该地址、读取块无效或值仍为 null，返回 `None`；
- 不发 Modbus 请求；
- 返回 0..65535 的整数。

### 7.2 `read_holding`

```python
values = ctx.read_holding(address, quantity)
```

- 执行 FC03；
- 使用当前设备 Unit ID / endpoint / session；
- 返回 raw `list[int]`；
- 不自动写入静态 `registerBlocks`；
- 结果只有脚本显式 state / event 输出后才进入脚本运行状态。

### 7.3 `write_registers`

```python
ctx.write_registers(address, values)
```

- 执行 FC16；
- `values` 必须是非空 16-bit unsigned integer list；
- 地址与数量需满足 Modbus / 当前客户端实现限制；
- 不按地址做权限分类；
- 成功返回 `None`，失败终止当前脚本调用。

### 7.4 `write_coil`

```python
ctx.write_coil(address, True)
```

- 执行 FC05；
- `on` 必须为 bool；
- 不按 coil 地址做权限分类；
- 成功返回 `None`，失败终止当前脚本调用。

### 7.5 `delay`

```python
ctx.delay(50)
```

- milliseconds 必须为非负整数；
- 单次和累计 delay 受 resource limits；
- 可响应 runtime context cancellation；
- 不允许脚本通过语言运行库获得其他 sleep / timer；
- request pacer 仍对下一 Modbus request 生效。

### 7.6 state

```python
last = ctx.state_get("fault_code", 0)
ctx.state_set("fault_code", code)
```

状态值只允许：

- `None`
- bool
- integer
- string
- list / tuple of allowed values
- dict with string key and allowed values

拒绝不可序列化 host object、function、set 或超出大小限制的值。

### 7.7 event

```python
ctx.emit_event("fault_detail", key, {
    "index": 0,
    "registers": regs,
})
```

- `kind` / `key` 必须为非空字符串；
- payload 使用和 state 相同的 JSON-compatible 类型限制；
- 当前调用先 buffer，成功结束后提交；
- runtime 对 `(deviceId, scriptVersionId, kind, key)` 去重；
- 每设备只保留固定上限最近事件。

### 7.8 `print`

允许 Starlark `print()`，但重定向到结构化 logger，并受每次执行最大行数和单行长度限制。日志自动附加 device ID、script ID、version、channel ID；不允许脚本控制 logger destination。

## 8. 资源限制

新增服务端脚本运行配置。第一版默认值：

- `maxSourceBytes`: `262144`（256 KiB）
- `maxExecutionMs`: `10000`
- `maxExecutionSteps`: `100000`
- `maxModbusOperations`: `16`
- `maxDelayMs`: `1000`
- `maxTotalDelayMs`: `2000`
- `maxStateBytesPerDevice`: `65536`
- `maxEventsPerExecution`: `16`
- `maxEventsPerDevice`: `100`
- `maxEventPayloadBytes`: `65536`
- `maxPrintLines`: `100`
- `maxPrintLineBytes`: `2048`

这些值由服务配置控制，不由单个脚本覆盖或扩大。配置可以后续根据真实协议调整，但 API 不允许脚本修改限制。

限制触发统一分类为 `SCRIPT_LIMIT`，终止本次调用并释放 channel 调度权。

## 9. Script Runtime State

进程内新增设备脚本状态，不与 `CurrentStateStore.registerBlocks` 混为同一数据结构。

每个已绑定设备至少维护：

- `deviceId`
- `scriptId`
- `scriptVersionId`
- `versionNo`
- `lastAttemptAt`
- `lastSuccessAt`
- `lastError`
- `lastErrorType`
- 当前 committed script state
- 最近 bounded dynamic events

建议错误分类：

- `SCRIPT_COMPILE`
- `SCRIPT_RUNTIME`
- `SCRIPT_LIMIT`
- `MODBUS_TRANSPORT`
- `MODBUS_EXCEPTION`
- `SCRIPT_OUTPUT`
- `CANCELED`

API：

- `GET /api/v1/acquisition/script-states`
- `GET /api/v1/acquisition/script-states/{deviceId}`

页面至少可以查看绑定版本、最近成功 / 失败、错误类型和最近 events，用于现场调试。

脚本错误不会改写静态 `registerBlocks` 的 Valid、LastSuccessAt 或设备 ONLINE / DEGRADED / OFFLINE 状态。

## 10. state / event 提交规则

每次 `after_poll` 创建 execution overlay：

```text
committed state
   + current overlay
   + buffered events
```

成功返回：

- 原子替换该设备 / version 的 committed script state；
- 提交 buffered events；
- 更新 script `lastSuccessAt`；
- 清空 script `lastError`。

失败返回：

- 丢弃 overlay；
- 丢弃 buffered events；
- committed state / events 保持原值；
- 更新 script `lastAttemptAt` / `lastError` / error type。

已经成功发送给设备的 Modbus write 无法回滚。运行时不得通过“脚本失败”伪装成设备写操作没有发生。

## 11. Runtime 集成与 session 行为

### 11.1 channel 原子性

同一 channel 一次只执行一个 Modbus transaction 的 ADR-0015 约束继续成立。

动态脚本执行期间，即使脚本调用 `delay(50)`，channel 也仍由当前设备占用；其他设备不能趁 delay 插入请求。

不同 channel 的脚本仍可以并行。

### 11.2 Session

脚本 I/O 必须使用 channel runner 当前管理的 session：

- RTU：共享通道 session，设置当前 Unit ID；
- TCP：当前设备 persistent session；
- MBAP UDP / RTU over UDP：当前设备 endpoint context。

脚本不缓存 session，不跨 execution 保存 host Modbus object。

动态请求 transport failure 时继续遵守 ADR-0015 session lifecycle，例如 TCP session 关闭、下周期再 Open。

### 11.3 pacing

所有脚本 FC03 / FC16 / FC05 都经过同一个 `requestPacer`。

例如上一请求结束后脚本 `delay(50)`：

- 若 `interRequestDelayMs <= 50`，下一请求可以在 50ms 后发出；
- 若 `interRequestDelayMs > 50`，仍需等满 channel pacing；
- 不做 `50 + interRequestDelayMs` 的强制累加。

## 12. 热更新

### 12.1 Publish / Rollback

Publish / rollback commit 后触发 runtime refresh。`Runtime.Refresh()` 在 configuration snapshot 中加载并固化绑定设备的 immutable published `ScriptVersion`；`channelRunner` 每个 poll cycle 不再查询数据库。

当前正在执行的设备周期继续使用进入周期时捕获的 script version；新 published version 从下一安全设备边界生效。

版本变化时：

- 旧 compiled program 可继续供 in-flight execution 使用；
- 新周期使用新 version；
- 设备 script state / dynamic events 重置，避免旧脚本状态被新代码解释；
- 静态 `registerBlocks` snapshot 不受影响。

### 12.2 Bind / Unbind

- 新绑定：下一设备周期开始执行当前 published version；
- 解绑：当前 in-flight execution 完成，后续不执行脚本并清理 script runtime state；
- 更换 script：旧脚本周期完成后切换，状态 / events 重置。

### 12.3 设备身份变化

Unit ID、network endpoint、同协议 channel move 等 ADR-0015 identity change 除了使静态 snapshot invalid 外，也重置该设备的 script state / events；下一次成功静态采集后重新执行脚本。

## 13. 首个真实协议验收脚本

验收 fixture 使用以下已确认 Modbus 行为：

- 静态 FC03 block 覆盖 `8166` 当前故障代号；
- `8166 = 0`：不动态查询；
- `0 → N (N != 0)`：查询一次；
- `N → N`：不重复查询；
- `N → M (M != 0, M != N)`：查询一次；
- `N → 0`：执行 `state_set("fault_code", 0)`，不查询；
- 下一次 `0 → N`：再次查询。

动态查询顺序：

```text
FC16 write address=8120 values=[0]
→ ctx.delay(50)
→ FC03 read address=8121 quantity=17
```

读取结果保持 raw：

```json
{
  "index": 0,
  "registers": [/* 17 x uint16 */]
}
```

事件 key 使用读取结果中的：

- `8121` 故障代号；
- `8137` 故障回路；
- `8131..8136` 年/月/日/时/分/秒。

该 key 只是首个验收脚本的去重策略，不上升为平台统一 Alarm ID。

非零故障只有动态查询和 `emit_event` 成功后才 `state_set("fault_code", code)`；如果 FC16、delay、FC03、event serialization 任一步失败，state 不提交，下一轮允许重试。恢复到 0 时显式提交 `state_set("fault_code", 0)`，使后续 `0 → N` 能再次触发。

本阶段不查询历史索引 `1..25`，不解析 `8122..8128` 的单位 / 倍率，也不解析 `8160..8161` 四字节电量 word order。

## 14. Simulator 与测试

### 14.1 后端单元测试

必须覆盖：

- compile / validate 成功与语法错误；
- 缺失 `after_poll`；
- 禁止 `load()`；
- source size；
- execution steps / wall timeout；
- operation count；
- delay 单次 / 总量；
- state / event serialization 和 size；
- print limit；
- state overlay 成功 commit / 失败 rollback；
- Modbus write 已发生但 state rollback 的边界；
- script error 不污染 registerBlocks / device communication state；
- fresh globals，不允许跨设备隐式共享；
- compiled program cache 按 version/checksum 隔离。

### 14.2 Runtime fake session

必须证明：

- `registerBlocks → after_poll → next device` 顺序；
- 脚本 transaction 期间不会调度同 channel 其他设备；
- 50ms delay 期间不插入其他设备请求；
- script Modbus ops 继续走 channel pacing；
- 不同 channel 可并行；
- publish / rollback / bind / unbind 等待 in-flight device cycle；
- version / identity change 清空 script state；
- dynamic transport failure 释放 / 重建 session 符合 ADR-0015。

### 14.3 PostgreSQL / SQLite integration

覆盖：

- script CRUD；
- immutable versions；
- monotonic version number；
- checksum；
- publish atomicity；
- rollback ownership；
- device binding；
- bound script delete reject；
- audit；
- 两种数据库行为一致。

### 14.4 `modbus-simulator`

建立可重复验收 fixture：

- static `8166` 可从 0 变为非零 / 不同非零 / 恢复 0；
- FC16 `8120=0` 可成功；
- FC03 `8121..8137` 返回固定 raw detail；
- 请求记录可以验证动态请求只在触发边沿出现；
- 至少覆盖 RTU 和一种网络 transport 的脚本事务；最终 E2E 应证明脚本 runtime 与 transport-neutral DeviceContext 不绑定单一 transport。

### 14.5 前端 / browser E2E

至少覆盖：

- 创建脚本 draft；
- Validate 失败展示行号；
- Publish v1；
- 设备绑定；
- runtime script state 可观察；
- 修改 draft 不影响运行 v1；
- Publish v2 后安全切换；
- Rollback v1；
- script error 页面可见但实时寄存器仍正常；
- 首个动态故障查询只在 fault code 边沿触发。

## 15. 第一版验收口径

全部满足后才能关闭本阶段 Spec：

1. PostgreSQL / SQLite 脚本、版本和设备绑定模型一致；
2. 用户可以通过 React 页面保存 draft、Validate、Publish、查看历史版本并 Rollback；
3. 未发布 draft 绝不会被 runtime 执行；
4. 同一设备周期固定使用一个 immutable version；
5. `after_poll` 在静态 registerBlocks 之后执行，并保持 channel 原子占用；
6. FC03 / FC16 / FC05 全部经过现有 transport session 和 request pacer；
7. 脚本无法访问文件 / 任意网络 / DB / MQTT / raw frame / load；
8. 所有硬资源限制有自动测试；
9. script state / events 成功提交、失败回滚；
10. script error 不污染静态 registerBlocks 和设备通信状态；
11. publish / rollback / bind / unbind / endpoint identity change 按安全边界生效；
12. 首个 ZNCK-I fixture 完成 `8166 edge → 8120 FC16 → 50ms → 8121..8137 FC03`；
13. 同一 fault code 持续不重复查询，恢复 0 后再次出现会重新查询；
14. dynamic result 保持 raw，不新增工程量解析；
15. `task backend:check`、PostgreSQL/SQLite integration、simulator tests、`task frontend:lint`、`task frontend:build` 与 browser E2E 全部通过。

## 16. 后续演进边界

本阶段完成后，再根据真实现场需要决定：

- 业务告警生命周期和持久化；
- MQTT 告警 / 状态上报；
- 参数管理和人工控制 UI；
- 持久 script state；
- FC04 / 其他功能码动态原语；
- 可捕获的 Modbus error value；
- `load()` / module system；
- 更细的发布授权 / 审批；
- 地址级写 capability；
- 历史故障索引扫描。

这些能力不得为了“以后可能需要”提前混入第一版实现。
