# RTU RTC + Starlark 主机时间条件校时

默认模拟器提供一个独立、会自行走时的 RTC 测试设备：

- RTU alias：`/tmp/modbus-rtc0`
- Unit ID：`3`
- FC03 holding register：
  - `100`：hour，0..23
  - `101`：minute，0..59
  - `102`：second，0..59
- 校时写：仅接受 FC16 从地址 `100` 一次写入 `[hour, minute, second]`
- 初始基准：`00:00:00`，模拟器启动后会继续走时

设备定义：`config/devices/time_registers_03.yaml`。RTC 行为由 `RtcClockFixture` 提供：FC16 校时只重设时钟基准，后续 FC03 读取会按经过的单调时间返回新的时分秒；跨午夜会自动回到 `00:00:00`。部分写、FC06 校时或非法时分秒返回 Modbus `ILLEGAL_VALUE`。

## Edge Collector 配置

在“通信通道”创建 RTU 通道：

```text
port = /tmp/modbus-rtc0
baudRate = 9600
dataBits = 8
parity = N
stopBits = 1
```

在“设备管理”创建 Unit 3，并配置一个固定读取块：

```text
functionCode = 3
startAddress = 100
quantity = 3
```

这样每个 poll cycle 会先得到设备 RTC 的 raw snapshot，再进入绑定脚本的 `after_poll(ctx)`。

## `ctx.host_time()`

Starlark DeviceContext 提供受控主机时间：

```python
now = ctx.host_time()
```

返回字典：

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

实际值以 edge-controller 进程所在主机/容器的本地时区为准。Runtime 在一次 `after_poll` 开始时只捕获一次主机时间，所以一次脚本执行中多次调用 `ctx.host_time()` 得到相同字段，不会出现读取 hour 后刚好跨秒导致 minute/second 来自不同时刻的问题。

部署时应保证 edge-controller 容器/进程的本地时区就是现场设备所使用的时区；如果现场统一使用 UTC，也应让进程时区明确配置为 UTC。

## 条件校时脚本

示例：`examples/time-register-sync.star`。

核心策略不是“字段不同就写”，而是比较一天内的秒数，并允许默认 `2s` 偏差：

```python
MAX_DRIFT_SECONDS = 2
```

执行逻辑：

1. `ctx.raw_register(3, 100..102)` 读取本轮静态 snapshot，不额外发送 FC03。
2. 任一 raw 值无效则直接返回。
3. 调用 `ctx.host_time()` 得到本轮固定主机时间。
4. 设备时分秒越界时立即执行一次校时。
5. 正常值计算设备与主机的秒差，并处理跨午夜情况。
6. 偏差 `<= 2s` 时不写。
7. 只有偏差 `> 2s` 时发送一次：

```python
ctx.write_registers(
    100,
    [now["hour"], now["minute"], now["second"]],
)
```

因此正常运行时不会每个 poll cycle 都 FC16。比如设备 `12:34:55`、主机 `12:34:56` 时不写；设备 `12:34:40`、主机 `12:34:56` 时校时一次，之后模拟 RTC 会继续走时，后续轮询在容差内不再写。
