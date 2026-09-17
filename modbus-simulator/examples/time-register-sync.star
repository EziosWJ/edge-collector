# 条件写示例：只有本轮 raw snapshot 与目标值不一致时才写 FC16。
# 前提：设备管理为 Unit 3 配置 FC03 registerBlock：startAddress=100, quantity=3。
# 当前 Starlark Host API 不提供系统时间，因此这里用固定目标值演示条件写。

TARGET_HOUR = 12
TARGET_MINUTE = 34
TARGET_SECOND = 56


def after_poll(ctx):
    hour = ctx.raw_register(3, 100)
    minute = ctx.raw_register(3, 101)
    second = ctx.raw_register(3, 102)

    # 静态读取块缺失或本轮无有效值时，不执行写入。
    if hour == None or minute == None or second == None:
        return

    # 已经一致则不写，避免每个 poll cycle 都发送 FC16。
    if hour == TARGET_HOUR and minute == TARGET_MINUTE and second == TARGET_SECOND:
        return

    # 任一字段不一致时一次写入三个连续 holding register。
    ctx.write_registers(100, [TARGET_HOUR, TARGET_MINUTE, TARGET_SECOND])
