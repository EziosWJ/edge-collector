# ADR-0016: 用户可配置 Starlark Modbus 动态事务平台

## Status

Accepted

## Context

ADR-0014 将当前采集边界收敛为配置驱动的 FC03 / FC04 原始寄存器读取块，ADR-0015 又把同一套原始寄存器模型扩展到 `MODBUS_RTU`、`MODBUS_TCP`、`MODBUS_UDP` 与 `MODBUS_RTU_OVER_UDP`，并保持同一通信通道内所有 Modbus transaction 严格串行。

固定寄存器读取块适合持续轮询已知地址范围，但真实设备协议已经出现静态读取块无法优雅表达的多步骤行为。用户提供的《ZNCK-I 低压侧双回路开关程序通讯协议》给出了一个明确例子：持续读取的当前故障代号位于寄存器 `8166`；首次检测到非零故障代号后，需要先使用 FC16 向寄存器 `8120` 写入查询索引 `0`，设备随后刷新 `8121~8137`，再使用 FC03 读取故障详情。协议没有规定刷新耗时，因此本项目首个验收脚本采用 `50ms` 等待作为运行策略，而不是把它声明为厂家协议保证。

同一协议还包含 FC16 参数写入以及 FC05 瞬时线圈控制。由此可见，后续现场协议不仅需要“寄存器值解析”，还需要条件判断、状态记忆、延迟和多条 Modbus 请求组成的设备级动态事务。

本项目不把这种差异继续硬编码成大量 `if deviceType == ...` 的 Go 分支。用户确认 Starlark 的设计目标是**用户可配置动态脚本平台**，而不是仅由开发者提交到仓库的设备协议代码。脚本需要通过管理面配置、版本化、校验、发布和绑定到设备，并在现有 channel runner 的串行调度边界内执行。

本阶段仍不做业务工程量解析。固定读取块和动态脚本读取得到的值继续以原始 Modbus 地址、16-bit 寄存器数组及用户脚本自定义的 JSON-compatible payload 表达，不把电压、电流、倍率、单位、bit 位等业务解释引入平台契约。

## Decision

### 1. Starlark 是正式的用户可配置动态协议平台

Edge Collector 嵌入 Go Starlark 解释器作为设备协议动态编排层。用户可以通过管理接口维护 Starlark 源码；源码持久化到数据库，不要求部署时存在外部 `.star` 文件。

脚本平台的职责是表达静态 `registerBlocks` 无法表达的条件和多步骤 Modbus 行为，例如：

```text
读取固定 raw registerBlocks
  ↓
脚本检查某个 raw 寄存器
  ↓
条件满足
  ↓
FC16 写查询索引
  ↓
等待 50ms
  ↓
FC03 读取动态结果
  ↓
提交脚本状态 / emit_event
```

Starlark 不成为 transport 或调度器。脚本不得自行创建 socket、串口、client、goroutine 或连接生命周期。

### 2. 固定采集继续由 `registerBlocks` 负责

ADR-0014 的寄存器读取块继续是持续 raw 采集的主模型：

- FC03 / FC04 固定轮询仍由 Go runtime 按设备采集周期执行；
- `registerBlocks` 继续更新 `CurrentStateStore`；
- 脚本不取代固定读取块，也不重新实现设备轮询调度；
- 本阶段不从 raw 寄存器解析业务工程量。

脚本的首个入口为 `after_poll(ctx)`。它在当前设备的静态读取块采集完成后、channel runner 调度下一设备之前执行。

如果当前设备因明确的 transport / connection 级失败提前终止本轮，`after_poll` 不再执行，避免在已经失效的设备会话上继续动态事务。静态采集正常完成或仅出现可继续处理的块级 / Modbus exception 时，脚本可以执行，并通过 raw 有效性判断是否需要动作。

### 3. 动态脚本事务占用现有 channel 串行边界

一次 `after_poll(ctx)` 从开始到结束都属于当前设备本轮采集的一部分。

- 同一脚本执行期间，不允许 channel runner 插入其他设备请求；
- 脚本发起的 Modbus 请求使用当前设备已经解析好的 transport、endpoint、Unit ID、session 与 channel request pacer；
- RTU 继续使用通道共享 session；网络协议继续使用 ADR-0015 的设备级 session / endpoint 上下文；
- 脚本生成的 Modbus 请求同样受 `interRequestDelayMs` 约束；
- `ctx.delay(ms)` 表示显式协议等待，request pacer 仍只保证两条请求之间的最小业务间隔，因此实际下一请求间隔至少满足显式 delay 与 pacing 中较大的约束，而不是无条件把二者相加。

脚本不能绕过 channel runner 直接访问 `ModbusSession`。

### 4. 第一版 DeviceContext 只暴露受控能力

第一版公开以下 Starlark host API：

- `ctx.raw_register(function_code, address)`：读取本轮 / 当前静态 raw 快照中的单个寄存器；对应块无效或不存在时返回 `None`；
- `ctx.read_holding(address, quantity)`：执行 FC03，返回 `list[int]`；
- `ctx.write_registers(address, values)`：执行 FC16；
- `ctx.write_coil(address, on)`：执行 FC05；
- `ctx.delay(milliseconds)`：在当前原子动态事务内等待；
- `ctx.state_get(key, default=None)` / `ctx.state_set(key, value)`：访问脚本的设备级运行状态；
- `ctx.emit_event(kind, key, payload)`：产生通用动态脚本事件；
- 只读的设备 / 脚本执行元数据可以作为 `ctx` 属性暴露，例如 `device_id`、`unit_id`、`protocol`、`script_version`。

本阶段不开放：

- 文件系统；
- 任意 HTTP / TCP / UDP；
- 数据库；
- MQTT；
- goroutine / thread；
- OS 环境变量；
- Go reflection；
- raw Modbus frame；
- 用户自定义 module loader / `load()`。

单个脚本是一个独立源码单元，不在第一版建设包管理、依赖解析或脚本模块系统。

### 5. 已发布脚本拥有开放的 Modbus 写能力

本阶段不为脚本建立 `READ_ONLY`、`QUERY_WRITE`、`PARAMETER_WRITE`、`CONTROL_WRITE` 等地址或功能码 capability 分级。

只要脚本已经发布并绑定到设备，它就可以调用平台公开的 FC16 和 FC05 API。平台不会根据寄存器 / coil 地址判断“这是查询写、参数写还是控制写”。例如设备协议中的 `8120` 查询索引、`8255` 参数保存以及 `618/619` 合分闸地址，从脚本执行器视角都是已开放 Modbus 写原语的调用。

因此安全边界前移到**谁能够编辑、发布和绑定脚本**。本阶段至少要求这些管理操作进入既有认证与操作审计链路，但不引入 Modbus 地址级 ACL。

脚本执行中的实际 Modbus 写请求必须进入结构化运行日志，记录设备、脚本版本、功能码、地址、数量与成功 / 失败结果；本阶段不要求把每一条自动轮询产生的写请求持久化为业务审计表，避免脚本轮询造成无界数据库写放大。未来远程控制阶段可以对人工 / MQTT 控制建立独立的持久控制审计。

### 6. 脚本采用 Draft → Validate → Publish 的不可变版本模型

脚本包含稳定身份和可编辑 draft。保存 draft 不影响运行设备。

发布流程：

1. 用户编辑并保存 draft；
2. Validate 对源码进行 parse / compile、入口函数和 host API 契约检查；
3. Publish 从当前通过校验的 draft 创建不可变 `ScriptVersion`；
4. 脚本身份保存当前 published version 指针；
5. 绑定该脚本的设备在安全边界切换到新的 published version。

每个 published version 至少保存：

- 版本号；
- 完整源码；
- SHA-256 checksum；
- 发布者；
- 发布时间。

历史 published version 不允许原地修改。Rollback 通过把当前 published version 指针切回该脚本自己的历史版本实现，不复制或改写旧版本内容。

设备绑定脚本身份而不是直接保存可变源码。一个脚本可以复用于多个设备；未发布的脚本不能绑定到运行设备。

### 7. 一次设备采集固定使用一个脚本版本

设备本轮开始后，其 `after_poll` 必须使用同一个不可变 published version 直到执行结束。

用户在中途 Publish / Rollback / 修改绑定时：

- 数据库先成功提交；
- 当前 in-flight 设备周期不被中断；
- 新版本 / 新绑定在下一安全设备边界生效。

脚本版本编译结果可以按 version ID + checksum 缓存，但每次调用必须使用新的执行 Thread / Globals，不允许利用可变 Starlark global 在设备、周期或并发通道之间隐式共享状态。

### 8. 脚本状态是显式、受限且本阶段只保存在内存

跨周期状态只能通过 `ctx.state_get/state_set` 表达，不依赖 Starlark global。

脚本状态作用域为当前设备 + 当前 published script version。设备寻址身份发生变化（Unit ID、network endpoint、同协议通道移动）、脚本解绑或 published version 切换时，旧脚本状态不继续继承到新的执行身份。

本阶段脚本状态保存在进程内存，不写数据库；服务重启后允许丢失。因此“同一故障码持续时不重复查询”的保证只覆盖同一运行进程和未发生脚本 / 设备身份切换的期间。重启后仍存在的非零故障允许重新查询一次。

可持久化脚本状态如未来确有需求，必须另行定义数据一致性、写放大和版本迁移语义。

### 9. state / event 使用执行级提交语义，但外部 Modbus I/O 不可回滚

一次脚本调用创建临时 state overlay 和 event buffer：

- `state_get` 读取已提交状态叠加当前调用中的修改；
- `state_set` 只修改当前 overlay；
- `emit_event` 先写入当前调用的 buffer；
- `after_poll` 成功返回后，overlay 和 events 一次提交到内存 runtime store；
- 发生 Starlark runtime error、资源限制、Modbus 请求错误或 context cancellation 时，本次 overlay 与 events 全部丢弃。

这保证例如故障详情读取失败时，不会提前把 `last_fault_code` 标记为“已处理”，下一轮仍可重试。

但已经发送给真实设备的 FC16 / FC05 请求属于外部 I/O，不能由 Starlark runtime 回滚。脚本作者必须理解：`state/event` 提交具有调用级原子性，Modbus 设备副作用没有数据库事务式回滚能力。

第一版 Modbus host builtin 采用 fail-fast 语义：请求失败直接终止当前脚本调用，不提供 `try/catch` 或错误值分支协议。需要复杂恢复策略时在后续版本单独设计。

### 10. 通用动态事件不是本阶段的业务告警系统

`ctx.emit_event(kind, key, payload)` 用于让脚本向运行时输出一条通用事件：

- `kind` 和 `key` 是字符串；
- `payload` 限制为可安全序列化的 JSON-compatible Starlark 值；
- runtime 按 `(device, script version, kind, key)` 在当前进程内去重；
- 每设备只保留有上限的最近事件，避免内存无界增长；
- 事件通过管理 API / 页面用于调试与验收；
- 本阶段不做数据库告警历史、不做 MQTT 上报、不定义确认 / 恢复 / 消警生命周期。

业务告警模型将在后续阶段建立在已经验证的动态查询能力之上。

### 11. 脚本错误不污染静态寄存器通信状态

脚本执行拥有独立的运行观察状态，至少包括：

- 当前绑定 script / version；
- `lastAttemptAt`；
- `lastSuccessAt`；
- `lastError`；
- 最近 resource-limit / runtime / Modbus error 分类；
- 最近动态事件。

Starlark compile/runtime error 或动态查询失败不会把已经成功的 `registerBlocks` 改成无效，也不会单独把设备 `ONLINE` 改为 `DEGRADED/OFFLINE`。ADR-0014 的设备通信状态仍由静态读取块结果决定。

如果脚本中的真实 Modbus 请求暴露出 transport failure，底层 session 仍按 ADR-0015 的 transport 生命周期处理，例如网络 session 可被关闭并在下一设备周期重建；但该次脚本失败仍记录在独立 script execution state 中，不追溯修改本轮已经成功的静态 raw snapshot。

### 12. 必须设置硬资源边界

因为脚本由用户配置，Starlark 的语言沙箱本身不能替代运行资源控制。服务端必须提供不可由单个脚本突破的全局限制，至少覆盖：

- 单次执行 wall-clock timeout；
- Starlark execution step / instruction budget；
- 单次脚本最大 Modbus operation 数；
- 单次 `delay` 和单次执行累计 delay 上限；
- 单次执行最多 emit event 数；
- 单个 payload / 状态总大小；
- print / script log 数量。

超出限制时立即终止本次脚本调用，按 `SCRIPT_LIMIT` 记录，不阻塞 channel runner 后续设备。

这些上限属于服务运行配置 / 内部安全默认值，不由每个用户脚本自行扩大。Implementation Spec 给出第一版配置项和默认值。

### 13. 首个正式验收协议只验证 raw 动态事务

首个真实协议验收使用用户提供的 ZNCK-I 低压侧双回路开关 Modbus 行为，但不把该厂家协议硬编码进 Go runtime。

验收流程：

1. 静态 `registerBlocks` 中包含当前故障代号寄存器 `8166`；
2. `after_poll` 读取 `ctx.raw_register(3, 8166)`；
3. 故障代号从 `0` 变为非零，或从故障码 A 变为不同的非零故障码 B 时，触发一次动态查询；
4. 相同非零故障码持续期间不重复查询；
5. 脚本执行 FC16：`8120 = 0`；
6. 脚本显式 `delay(50)`；
7. 脚本执行 FC03 读取 `8121~8137` 共 17 个寄存器；
8. 以详情中的故障代号、故障回路、年/月/日/时/分/秒组合构造 event key；
9. `emit_event` payload 保存原始查询索引和 `8121~8137` raw register 数组，不在本阶段换算电压、电流、漏电电阻等业务含义；
10. 详情查询成功后才通过 `state_set` 记住当前 fault code。

故障历史索引 `1..25` 的新旧排序未由现有文档充分定义，本阶段只查询索引 `0`。

同码故障在两个静态采集周期之间发生“恢复后快速再次发生”，且中间 `8166=0` 没有被采到时，可能被视为同一次持续故障。本阶段接受该 edge-triggered optimization 限制；若现场要求严格事件完整性，需要后续增加周期重查或历史索引扫描策略。

## Consequences

### Positive

- 固定高频 raw 轮询继续使用已经验证的 Go runtime，不因脚本平台退回到全脚本调度。
- 不需要为每个厂家动态查询流程修改并重新编译 Go 服务。
- 用户可以版本化、发布和回滚现场协议脚本，运行设备不会直接执行未发布 draft。
- 所有脚本 Modbus I/O 仍经过同一 channel 串行调度、transport session 和 pacing，ADR-0013 / ADR-0015 的并发边界不会被绕过。
- 显式脚本 state 可以表达“首次触发、同值不重复”等跨周期逻辑，而不用依赖不可控的全局变量。
- 资源限制、版本快照和独立错误状态使脚本错误可以被观察并隔离。

### Negative

- 发布脚本拥有开放 FC16 / FC05 能力，错误或恶意脚本可以对现场设备产生真实写副作用；平台安全依赖脚本管理权限、审核习惯和版本审计，而不是地址 ACL。
- Starlark runtime、版本管理、热更新和运行状态增加新的持久化与运行时复杂度。
- 内存脚本 state 在进程重启后丢失，重启后可能重新执行一次仍处于活动状态的动态查询。
- `state/event` 可以回滚，但已经发出的 Modbus 写操作无法回滚，需要脚本作者自行设计设备侧操作顺序。
- 第一版单文件、无 `load()`、fail-fast error 模型限制了一部分高级脚本复用和错误恢复能力。

## Alternatives Considered

### 在 Go 中为每个设备型号硬编码状态机

短期实现最直接，但厂家协议变化或新增设备时需要频繁修改、编译和发布主程序，并会把具体设备状态机持续扩散到采集 runtime，拒绝。

### 所有设备轮询都交给 Starlark

可以获得最大动态性，但会重复实现已经稳定的寄存器读取块、设备周期、状态聚合和 transport 故障处理，并增加脚本性能与可靠性风险，拒绝。

### Lua / JavaScript / Yaegi

这些方案都能提供动态逻辑，但本阶段更重视可控宿主 API、确定性语言子集、无默认系统 I/O 能力以及与 Go 嵌入场景的简单边界，因此选择 Starlark。该选择不意味着未来需要建设通用多语言脚本平台。

### 仅把脚本作为 Git 仓库内代码资产

安全和发布流程更简单，但不满足已确认的“用户配置动态脚本平台”目标，拒绝。

## Related Decisions

- ADR-0013：通道请求节流与配置运行时刷新。
- ADR-0014：协议解析前先采集原始寄存器。
- ADR-0015：多传输 Modbus 通道、设备 endpoint 与 session 生命周期。
- Implementation Spec：`docs/specs/starlark-modbus-dynamic-transactions.md`。
