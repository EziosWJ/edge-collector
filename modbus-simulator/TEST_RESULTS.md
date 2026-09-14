# 实际测试记录

日期：2026-09-14。环境：Linux，uv 0.11.7，uv 项目 Python 3.12.13，PyModbus 3.15.0，PyYAML 6.0.3。

## 依赖与配置

- `uv sync --locked`：通过，3 个项目/运行包已解析并检查。
- `uv run modbus-simulator --check-config`：`Configuration OK`。
- Python 模块 compileall：通过。
- Git 状态：仅新增 `modbus-simulator/`；现有 Go、React、Taskfile、数据库均未修改。

本环境使用 `UV_CACHE_DIR=/tmp/modbus-uv-cache`，避免向沙箱外默认缓存写入。首次依赖安装和 socket 测试使用环境批准的扩展执行权限。第一次在默认沙箱执行网络测试因 `socket(): Operation not permitted` 失败；允许本机 socket 后才执行成功，不把该环境失败算作通过。

## 自动测试

执行：

```bash
uv run python -m unittest discover -s tests -v
```

最终结果：

```text
Ran 12 tests in 1.630s
OK
```

| 测试 | 实际覆盖 |
|---|---|
| test_default_mapping | 默认五通道、同总线两 Slave |
| test_duplicate_ids_and_bad_yaml | 重复 Unit/Slave、非法 YAML |
| test_invalid_device | 地址、倍率、长度、字节序、模拟参数、寄存器重叠 |
| test_pty_ownership | 不覆盖普通文件、替换旧软链接、独占锁、退出/重启清理 |
| test_all_modes_and_functions | TCP、两种真实 UDP、真实 PTY 上的 01/02/03/04/05/06/15/16，写后读回和多 ID |
| test_bad_datagrams_and_fragmented_rtu | 空/短/超长/非法 Datagram、恢复读取、PTY 请求分片 |
| test_delay_and_dynamic | 四种模式慢响应和按时间更新 input 寄存器 |
| test_exceptions_and_recovery | 非法地址、未配置 coil、数量0、未知功能码、未知 ID、错误 CRC 后恢复 |
| test_invalid_pdu_lengths | 多写 byte count 不符、非法 coil 值，错误后继续读取 |
| test_startup_failure_cleans_pty | TCP 端口占用，先前 PTY 回滚，已有服务仍可读取 |
| test_simulation | fixed/increment/decrement/random、上下界与量化 |
| test_types_and_orders | 所有通用数值类型、倍率、ABCD/BADC/CDAB/DCBA |

测试过程中发现并修正了 PyModbus 3.15 原生 TCP 对数量 0 返回 FC00 异常的问题；共用 RequestDecoder 将其转换成原功能码对应的非法值异常 `83 03`。测试里的非法请求日志、端口占用日志是预期故障输入，不是测试失败。

## 默认配置实际启动

启动命令：`uv run modbus-simulator`。

实际创建：

- `/tmp/modbus-rtu0 -> /dev/pts/12`，Slave 1、2。
- `/tmp/modbus-rtu1 -> /dev/pts/20`，Slave 1。
- TCP `127.0.0.1:1502`。
- MBAP UDP `127.0.0.1:1600`。
- RTU over UDP `127.0.0.1:1700`。

实际 PTY 编号仅代表本次运行，重新启动会变化。验收后已停止进程，确认两个 alias 不再存在；`.lock` 文件按设计保留，锁已释放。

## 真实 Go 采集链路

执行：`uv run python scripts/go_smoke.py`。

使用后端实际导出的串口 session、`PollChannelOnce`、协议解析和状态存储，不是另写一个假采集器，不要求数据库。

结果：

- RTU0 Slave 1：`ONLINE`，voltage=300、current=12.5、activePower=123.4、frequency=50、powerFactor=0.98、status=0。
- RTU0 Slave 2：`ONLINE`，voltage=310，其余字段同上。
- 所有 6 个字段有效，`LastError` 为空，存在成功时间。
- 将第一台客户端 Slave 改为不存在的 99，连续 3 次真实超时后为 `OFFLINE`，失败计数=3。
- 同总线第二台继续 `ONLINE`；恢复第一台 Slave=1 后回到 `ONLINE`。
- RTU1 Slave 1 同样完整读取成功。

Go 真实采集模块只读 FC03。FC04 由上面的真实 PTY 客户端测试覆盖，不声称后端业务已经使用 FC04。

## 真实 Go 网络客户端

使用项目现有 `simonvetter/modbus v1.6.4`，每种模式读取 Unit 1、2，FC03，地址0，数量7：

```text
PASS Go library tcp://127.0.0.1:1502 unit=1 registers=[3000 125 0 1234 5000 980 0]
PASS Go library tcp://127.0.0.1:1502 unit=2 registers=[3100 125 0 1234 5000 980 0]
PASS Go library udp://127.0.0.1:1600 unit=1 registers=[3000 125 0 1234 5000 980 0]
PASS Go library udp://127.0.0.1:1600 unit=2 registers=[3100 125 0 1234 5000 980 0]
PASS Go library rtuoverudp://127.0.0.1:1700 unit=1 registers=[3000 125 0 1234 5000 980 0]
PASS Go library rtuoverudp://127.0.0.1:1700 unit=2 registers=[3100 125 0 1234 5000 980 0]
ALL GO SMOKE CHECKS PASSED
```

实际 RTU over UDP 请求 `01 03 00 00 00 07 04 08`，响应末尾 CRC `84 F4`，完整帧记录在 README；Go 客户端及 Python socket 测试均验证了 CRC。

## 没有完成或不能确认的内容

- 未启动完整 `cmd/api`、数据库和浏览器端到端链路，本次直接执行真实采集模块。没有修改用户现有持久设备配置。
- 后端目前没有 TCP/UDP/RTU over UDP 业务采集接入，因此网络验收是现用 Go 客户端库级别，不是业务 API 配置验证。
- 无厂家寄存器表，无法验证真实馈电硬件、高开保护器、告警地址、status bit 的厂家语义。
- 无热加载、drop_rate、广播写、BCD/String 或物理 RS485 电气/时序模拟；详见 README。
- 未改后端实现，因此未运行与本改动无关的全量后端、前端检查；新增 Go harness 已由真实 `go run` 编译执行。
