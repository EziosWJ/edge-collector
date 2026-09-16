# 实际测试记录

> 本文件前半部分记录 2026-09-14 的模拟器历史验收。ADR-0014 后，Go 采集链路改为读取配置驱动的原始 FC03/FC04 寄存器；以下“真实 Go 采集链路”说明已同步为当前 smoke 契约。

## ADR-0015 实现阶段验证（2026-09-16）

- `task backend:check`：通过。
- `task db:integration:sqlite`：通过，包含旧 RTU 数据迁移保留、四协议 API 模型、endpoint 唯一性、Unit ID 范围和跨协议迁移拒绝。
- `task frontend:lint` / `task frontend:build`：通过。
- `uv run python -m unittest discover -s tests -v`：通过。
- `uv run python scripts/go_smoke.py`：通过；真实 acquisition session 与 Runtime 已验证 RTU、Modbus TCP、MBAP over UDP、RTU over UDP，四协议 Runtime 并行运行互不阻塞，TCP 不可达 endpoint 可隔离并在热更新 endpoint 后恢复，网络同通道不同 endpoint 可使用相同 Unit ID。
- `task db:integration:postgres`：通过，包含 PostgreSQL 全量迁移、Repository/API 及 ADR-0015 transport migration 契约。

## ADR-0015 DB → cmd/api → browser 验收（2026-09-16）

- `UV_CACHE_DIR=/tmp/modbus-uv-cache npm run test:acquisition-e2e`（在 `react-admin/`）：通过，进程退出码 0。
- 测试临时执行 SQLite 全量 migration，启动 `modbus-simulator/config/adr0015-e2e.yaml`、真实 Go `cmd/api`、Vite 和 Playwright；未使用现有开发数据库。
- 四协议共 4 个 API 通道、8 台设备：RTU 同通道 Unit 1/2；TCP、MBAP UDP、RTU over UDP 各在同一 API 通道配置 A/B 两个不同端口，两个 endpoint 均使用 Unit ID 1；C 端口用于热更新。
- API `states` 验证 8 台设备均为 `ONLINE`，每台均有有效 FC03/FC04 `registerBlocks`；四个通道聚合状态均为 `ONLINE`。
- browser 设备管理页验证不同 `host:port` endpoint 展示；实时寄存器页验证 8 台设备在线和四协议通道状态；RTU over UDP Unit ID 248 被前端拒绝，范围为 1～247。
- 通过 API 将 TCP、MBAP UDP、RTU over UDP 的 A endpoint 分别热更新到 C 端口；无 API 重启，状态恢复 `ONLINE`，browser 重新加载后显示新 endpoint。
- fixture 使用 TCP 2502～2504、MBAP UDP 2600～2602、RTU over UDP 2700～2702，避免依赖默认 simulator 端口；测试结束已清理临时进程和数据库。

以上是模块、SQLite/API 和 simulator 验证；没有把它们表述为已完成的真实厂家设备验收。

日期：2026-09-14。环境：Linux，uv 0.11.7，uv 项目 Python 3.12.13，PyModbus 3.15.0，PyYAML 6.0.3。

## 依赖与配置

- `uv sync --locked`：通过，3 个项目/运行包已解析并检查。
- `uv run modbus-simulator --check-config`：`Configuration OK`。
- Python 模块 compileall：通过。
- Git 状态：以上记录生成时仅新增 `modbus-simulator/`；不是当前仓库工作区状态。

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

使用后端实际导出的串口 session、`PollChannelOnce` 和 raw 状态存储，不是另写一个假采集器，不要求数据库。

结果：

- RTU0 Slave 1：`ONLINE`，holding raw 首值 3000、input raw 样例值 42。
- RTU0 Slave 2：`ONLINE`，holding raw 首值 3100、input raw 样例值 43。
- 两个读取块有效，`LastError` 为空，存在成功时间。
- 将第一台客户端 Slave 改为不存在的 99，连续 3 次真实超时后为 `OFFLINE`，失败计数=3。
- 同总线第二台继续 `ONLINE`；恢复第一台 Slave=1 后回到 `ONLINE`。
- RTU1 Slave 1 同样完整读取成功。

当前 Go smoke 按读取块配置同时使用 FC03 和 FC04；TCP、MBAP over UDP、RTU over UDP 也通过真实 Runtime 使用同一读取块和状态聚合链路。

## 真实 Go 网络客户端

使用项目现有 `simonvetter/modbus v1.6.4`，每种模式读取两个不同 endpoint 的 Unit 1，并通过 Runtime 验证通道状态：

```text
PASS Go library tcp://127.0.0.1:1502 unit=1 registers=[3000 125 0 1234 5000 980 0]
PASS Go library tcp://127.0.0.1:1502 unit=2 registers=[3100 125 0 1234 5000 980 0]
PASS Go library udp://127.0.0.1:1600 unit=1 registers=[3000 125 0 1234 5000 980 0]
PASS Go library udp://127.0.0.1:1600 unit=2 registers=[3100 125 0 1234 5000 980 0]
PASS Go library rtuoverudp://127.0.0.1:1700 unit=1 registers=[3000 125 0 1234 5000 980 0]
PASS Go library rtuoverudp://127.0.0.1:1700 unit=2 registers=[3100 125 0 1234 5000 980 0]
ALL GO SMOKE CHECKS PASSED
```

实际 RTU over UDP 请求 `01 03 00 00 00 07 04 08`，响应末尾 CRC `84 F4`，完整帧记录在 README；Go acquisition session、Runtime 及 Python socket 测试均验证了 CRC 和读取结果。

## 没有完成或不能确认的内容

- 2026-09-14 历史记录未启动完整 `cmd/api`、数据库和浏览器端到端链路；本次 2026-09-16 已补充独立临时 SQLite、`cmd/api` 和 browser 验收，且没有修改用户现有持久设备配置。
- 仍未完成真实厂家设备验收；网络 smoke 通过实际 acquisition session 和 simulator 端点验证，不替代现场设备测试。
- 无厂家寄存器表，无法验证真实馈电硬件、高开保护器、告警地址、status bit 的厂家语义。
- 无热加载、drop_rate、广播写、BCD/String 或物理 RS485 电气/时序模拟；详见 README。
- 以上历史记录生成时未改后端实现；ADR-0014 后端/前端检查由仓库根目录 Taskfile 单独执行。
