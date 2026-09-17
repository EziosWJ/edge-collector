# RTU 时间寄存器 + Starlark 条件写示例

默认模拟器 `rtu0` 已增加一个独立测试设备：

- RTU alias：`/tmp/modbus-rtu0`
- Unit ID：`3`
- FC03/FC16 holding register：
  - `100`：hour
  - `101`：minute
  - `102`：second
- 初始值：`0, 0, 0`

设备定义：`config/devices/time_registers_03.yaml`。

## Edge Collector 设备配置

在“设备管理”中把 Unit 3 配到 `/tmp/modbus-rtu0` 所在通信通道，并配置一个寄存器读取块：

```text
functionCode = 3
startAddress = 100
quantity = 3
```

这样每个 poll cycle 会先得到 hour/minute/second 的 raw snapshot。

## Starlark

示例脚本：`examples/time-register-sync.star`。

脚本目标值默认是 `12:34:56`。执行逻辑：

1. 通过 `ctx.raw_register(3, 100..102)` 读取本轮静态采集结果，不额外发 FC03。
2. 如果任一 raw 值无效，直接返回。
3. 如果三个值已经等于目标值，直接返回，不发送写请求。
4. 只有不一致时才调用一次：

```python
ctx.write_registers(100, [12, 34, 56])
```

因此模拟器初始为 `00:00:00` 时，首次有效轮询会产生一次 FC16；下一轮静态 FC03 读回 `12:34:56` 后，脚本不再写。若之后通过其他客户端修改任一寄存器，脚本会在下一轮检测到差异并重新写入。

当前 Starlark DeviceContext 没有系统时间/本地时间 API，所以示例使用固定目标常量。若要实现“把设备时间同步到 edge-collector 主机当前时分秒”，需要后续给 DeviceContext 增加受控时间源，例如 `ctx.now()`，不应让脚本直接访问 OS/time API。
