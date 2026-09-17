# RTC 条件校时示例：只在设备时间与 edge-controller 主机时间偏差超过阈值时写 FC16。
# 前提：设备管理为 Unit 3 配置 FC03 registerBlock：startAddress=100, quantity=3。
# ctx.host_time() 由 edge-controller runtime 提供，使用进程本地时间，并在单次 after_poll
# 开始时捕获一次，因此 year/month/day/hour/minute/second 在本次执行内保持一致。

MAX_DRIFT_SECONDS = 2


def seconds_of_day(hour, minute, second):
    return hour * 3600 + minute * 60 + second


def after_poll(ctx):
    device_hour = ctx.raw_register(3, 100)
    device_minute = ctx.raw_register(3, 101)
    device_second = ctx.raw_register(3, 102)

    # 静态读取块缺失或本轮无有效值时，不执行写入。
    if device_hour == None or device_minute == None or device_second == None:
        return

    now = ctx.host_time()

    # 设备返回非法 RTC 值时直接校准，不参与差值计算。
    if device_hour > 23 or device_minute > 59 or device_second > 59:
        ctx.write_registers(
            100,
            [now["hour"], now["minute"], now["second"]],
        )
        return

    device_time = seconds_of_day(device_hour, device_minute, device_second)
    host_time = seconds_of_day(now["hour"], now["minute"], now["second"])
    diff = abs(device_time - host_time)

    # 处理 23:59:59 与 00:00:01 这类跨午夜情况。
    if diff > 43200:
        diff = 86400 - diff

    # 允许少量轮询/串口传输造成的秒级偏差，避免每轮都写。
    if diff <= MAX_DRIFT_SECONDS:
        return

    # 只有真正漂移时才一次 FC16 写入三个连续 holding register。
    ctx.write_registers(
        100,
        [now["hour"], now["minute"], now["second"]],
    )
