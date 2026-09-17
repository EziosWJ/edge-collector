-- +goose Up
-- 开发 SQLite 的 Modbus RTC 校时联调夹具。
-- 仅在 APP_ENV=dev 且 SQLite profile 下执行；标准 seed stream 不包含模拟设备。

INSERT INTO acquisition_channel (name, protocol, timeout_ms, enabled, inter_request_delay_ms)
SELECT '模拟器 RTC 时间同步 RTU', 'MODBUS_RTU', 500, 1, 5
WHERE NOT EXISTS (
    SELECT 1
    FROM acquisition_channel
    WHERE name = '模拟器 RTC 时间同步 RTU' AND deleted = 0
);

INSERT INTO acquisition_serial_channel (channel_id, port, baud_rate, data_bits, stop_bits, parity)
SELECT channel.id, '/tmp/modbus-rtc0', 9600, 8, 1, 'N'
FROM acquisition_channel AS channel
WHERE channel.name = '模拟器 RTC 时间同步 RTU'
  AND channel.deleted = 0
  AND NOT EXISTS (
      SELECT 1
      FROM acquisition_serial_channel AS serial
      WHERE serial.channel_id = channel.id
  );

INSERT INTO acquisition_script (name, description, draft_source, create_by, update_by)
SELECT
    'RTC 时间寄存器条件校时',
    '读取 Unit 3 的时分秒寄存器，仅在偏差超过 2 秒时执行 FC16 校时。',
    '# RTC 条件校时示例：只在设备时间与 edge-controller 主机时间偏差超过阈值时写 FC16。
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
',
    1,
    1
WHERE NOT EXISTS (
    SELECT 1
    FROM acquisition_script
    WHERE name = 'RTC 时间寄存器条件校时' AND deleted = 0
);

INSERT INTO acquisition_script_version (script_id, version_no, source, checksum, published_by, published_at)
SELECT script.id, 1,
       script.draft_source,
       'c748fad7e56c09caf8157215b20e2e02953f294a1fc2b7db37fc1a1080af13a6',
       1,
       CURRENT_TIMESTAMP
FROM acquisition_script AS script
WHERE script.name = 'RTC 时间寄存器条件校时'
  AND script.deleted = 0
  AND NOT EXISTS (
      SELECT 1
      FROM acquisition_script_version AS version
      WHERE version.script_id = script.id AND version.version_no = 1
  );

UPDATE acquisition_script
SET published_version_id = (
        SELECT version.id
        FROM acquisition_script_version AS version
        WHERE version.script_id = acquisition_script.id AND version.version_no = 1
    ),
    update_by = 1,
    update_time = CURRENT_TIMESTAMP
WHERE name = 'RTC 时间寄存器条件校时'
  AND deleted = 0
  AND published_version_id IS NULL;

INSERT INTO acquisition_device (name, device_type, channel_id, unit_id, poll_interval_ms, failure_threshold, enabled, script_id)
SELECT
    '时间寄存器测试设备-03',
    'FEED_PROTECTOR',
    channel.id,
    3,
    1000,
    3,
    1,
    script.id
FROM acquisition_channel AS channel
CROSS JOIN acquisition_script AS script
WHERE channel.name = '模拟器 RTC 时间同步 RTU'
  AND channel.deleted = 0
  AND script.name = 'RTC 时间寄存器条件校时'
  AND script.deleted = 0
  AND NOT EXISTS (
      SELECT 1
      FROM acquisition_device AS device
      WHERE device.channel_id = channel.id AND device.unit_id = 3 AND device.deleted = 0
  );

INSERT INTO acquisition_register_block (device_id, name, function_code, start_address, quantity, sort_order)
SELECT device.id, 'rtc-time', 3, 100, 3, 0
FROM acquisition_device AS device
JOIN acquisition_channel AS channel ON channel.id = device.channel_id
WHERE channel.name = '模拟器 RTC 时间同步 RTU'
  AND channel.deleted = 0
  AND device.unit_id = 3
  AND device.deleted = 0
  AND NOT EXISTS (
      SELECT 1
      FROM acquisition_register_block AS block
      WHERE block.device_id = device.id AND block.name = 'rtc-time'
  );

-- 处理 migration 执行前已存在同名设备但尚未绑定脚本的开发库。
UPDATE acquisition_device
SET script_id = (
        SELECT script.id
        FROM acquisition_script AS script
        WHERE script.name = 'RTC 时间寄存器条件校时' AND script.deleted = 0
    ),
    update_time = CURRENT_TIMESTAMP
WHERE name = '时间寄存器测试设备-03'
  AND unit_id = 3
  AND deleted = 0
  AND script_id IS NULL;

-- +goose Down
DELETE FROM acquisition_device
WHERE name = '时间寄存器测试设备-03'
  AND unit_id = 3
  AND channel_id IN (
      SELECT id FROM acquisition_channel WHERE name = '模拟器 RTC 时间同步 RTU'
  );
DELETE FROM acquisition_script
WHERE name = 'RTC 时间寄存器条件校时' AND deleted = 0;
DELETE FROM acquisition_serial_channel
WHERE channel_id IN (
    SELECT id FROM acquisition_channel WHERE name = '模拟器 RTC 时间同步 RTU'
);
DELETE FROM acquisition_channel
WHERE name = '模拟器 RTC 时间同步 RTU' AND deleted = 0;

