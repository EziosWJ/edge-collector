# Starlark Host Time 与 RTC 条件校时 Extension Spec

状态：accepted

适用范围：`edge-collector-api/`、`modbus-simulator/`

关联基线：

- [ADR-0016](../adr/0016-user-configurable-starlark-modbus-dynamic-transactions.md)
- [Starlark Modbus 动态事务平台 Implementation Spec](starlark-modbus-dynamic-transactions.md)

## 1. 目的

在 ADR-0016 已验收的 `after_poll(ctx)` 动态事务平台上增加一个受控主机时间 API，用于设备 RTC 校时场景。该扩展不改变固定 `registerBlocks` 作为周期采集主干的设计，也不允许脚本直接访问 Go `time`、OS、环境变量或任意系统 API。

目标流程：

```text
FC03 固定读取设备 RTC raw register
→ after_poll(ctx)
→ ctx.host_time() 获取本轮稳定的 edge-controller 主机本地时间
→ 比较设备 RTC 与主机时间偏差
→ 仅当偏差超过阈值时 FC16 校时
```

## 2. DeviceContext API

新增：

```python
now = ctx.host_time()
```

返回只读语义的普通 Starlark dict：

```python
{
    "year": 2026,
    "month": 9,
    "day": 17,
    "hour": 12,
    "minute": 34,
    "second": 56,
    "unix": 1789619696,
    "timezone": "JST",
    "utc_offset_seconds": 32400,
}
```

约束：

- 默认时间源为 edge-controller 进程的本地 wall clock；
- Runtime `Options.HostTime` 可注入测试时钟；
- 每次 `after_poll` invocation 开始时只捕获一次时间；
- 同一次 invocation 内多次调用 `ctx.host_time()` 必须返回同一时刻的字段；
- `ctx.host_time()` 不计入 Modbus operation budget；
- 不提供 sleep、timer、时区切换、OS time 修改等能力；
- 事件时间 `Options.Now` 保持原有 UTC/事件语义，与 `HostTime` 分离。

生产部署必须显式保证 edge-controller 进程/容器的本地时区与现场设备 RTC 采用的时区一致；若现场统一 UTC，则进程时区也应明确配置为 UTC。

## 3. RTC 模拟设备

默认 simulator 增加独立 RTU 通道：

```text
channel: rtc0
alias: /tmp/modbus-rtc0
Unit ID: 3
```

holding registers：

- `100`: hour，0..23
- `101`: minute，0..59
- `102`: second，0..59

固定读取使用 FC03 `startAddress=100, quantity=3`。

RTC fixture 行为：

- 启动时以配置值建立 RTC 基准；
- 后续读取按 monotonic elapsed time 自行走时；
- 跨午夜自动回到 `00:00:00`；
- 仅接受 FC16 从地址 `100` 一次写入三个值 `[hour, minute, second]` 作为校时；
- 成功校时后重设 RTC 基准并继续走时；
- 部分写、FC06 校时、非法时分秒返回 Modbus `ILLEGAL_VALUE`。

该 fixture 只用于验证动态脚本校时，不代表厂家真实 RTC 协议。

## 4. 条件校时策略

示例脚本位于：

`modbus-simulator/examples/time-register-sync.star`

默认容差：

```python
MAX_DRIFT_SECONDS = 2
```

脚本必须：

1. 使用 `ctx.raw_register(3, 100..102)` 读取本轮固定采集 snapshot，不额外发送 FC03；
2. snapshot 无效时不写；
3. 调用 `ctx.host_time()` 取得本轮稳定的主机时间；
4. 设备 RTC 值越界时立即执行一次 FC16 校时；
5. 正常值按一天内秒数比较，并正确处理 `23:59:59 ↔ 00:00:01` 等跨午夜差值；
6. 偏差 `<= 2s` 时不写；
7. 只有偏差 `> 2s` 时执行一次：

```python
ctx.write_registers(
    100,
    [now["hour"], now["minute"], now["second"]],
)
```

这样避免每个 poll cycle 都发送 FC16。

由于当前模拟设备和目标设备只暴露时/分/秒三个寄存器，本策略只能判断 time-of-day 偏差，不能判断设备日期是否错误；如真实设备提供年月日寄存器，应由对应设备脚本扩展完整日期比较。

## 5. 自动测试

Go Starlark runtime 测试至少覆盖：

- `ctx.host_time()` 返回 year/month/day/hour/minute/second/unix/timezone/offset；
- 注入 `Options.HostTime` 后结果可确定；
- 同一次 invocation 时间稳定；
- 偏差在阈值内不产生 FC16；
- 偏差超过阈值只产生一次 FC16；
- 跨午夜容差正确；
- 主机时间 API 不增加 Modbus operation count。

Simulator 测试至少覆盖：

- RTC 随 monotonic elapsed time 前进；
- 跨午夜；
- FC16 `[hour, minute, second]` 成功重设基准；
- 校时后继续走时；
- 非法值、部分写、FC06 返回 `ILLEGAL_VALUE`；
- 默认 `rtc0` 配置可通过 config validation。

CI：

- backend/SQLite/PostgreSQL gate 必须继续通过；
- `Modbus simulator tests` workflow 必须通过。

## 6. 非目标

本扩展不引入：

- 通用日期时间解析框架；
- NTP/PTP 客户端；
- 自动修改 edge-controller 系统时间；
- per-device timezone 配置；
- 后台独立校时 schedule；
- 绕过 `registerBlocks` 的脚本主轮询；
- 非 Modbus RTC 协议。
