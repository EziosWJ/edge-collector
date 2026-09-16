# ADR-0016 实际验收记录

> 验收日期：2026-09-16（Asia/Shanghai）
>
> 本记录只记录本次实现所在环境的实际结果；本地 PostgreSQL 因 WSL 环境限制未运行，但 GitHub Actions PostgreSQL gate 单独列出，其他已执行的浏览器 E2E 按实际结果记录。

## 已覆盖的实现闭环

- `acquisition_script` / `acquisition_script_version` 以及设备 `script_id` 绑定已加入 PostgreSQL、SQLite migration 和 Repository；版本 checksum、同脚本版本归属、不可变约束、Publish/Rollback 与审计路径均有测试。
- 受控 Starlark runtime 已接入 `after_poll(ctx)`，通过 Host seam 复用现有 Modbus session、channel pacing 和设备周期边界；固定 `registerBlocks` 快照与脚本运行状态相互隔离。
- `Runtime.Refresh()` 会在 configuration snapshot 中加载并固化设备对应的 immutable published `ScriptVersion`；channel runner 的 poll cycle 不查询数据库，Publish/Rollback/绑定变更在 commit 后经 refresh 于下一安全边界生效。
- state/event 使用成功提交、失败丢弃的 execution overlay；运行观察暴露脚本版本、最近尝试/成功/错误、state 和动态事件，且服务日志接收带设备/脚本/版本/channel 元数据的 `print()`。脚本硬限制由 `acquisition.script.*` 服务配置注入 runtime，动态 FC03/FC16/FC05 记录带功能码、地址、数量和结果的结构化日志。
- 管理后台支持脚本 draft、Validate（行列号）、Publish、版本历史、Rollback、设备已发布脚本绑定/解绑和 runtime state 查看。
- ZNCK-I fixture 保持 raw 数据语义，覆盖 `8166`、FC16 `8120=0`、50ms 策略、FC03 `8121..8137`、边沿抑制、`7→0→7` 恢复再触发、异常和请求日志。

## 本次执行结果

| 验收项 | 命令 / 范围 | 结果 |
| --- | --- | --- |
| Go 单元测试与 vet | `task backend:check` | 通过 |
| Starlark runtime 并发安全 | `go test -race ./internal/acquisition ./internal/acquisition/script` | 通过 |
| SQLite migration / persistence | `task db:integration:sqlite` | 通过 |
| 模拟器 | `uv run python -m unittest discover -s tests -v` | 19 项通过 |
| 前端 lint / build | `task frontend:lint`、`task frontend:build` | 通过（仅有既存 chunk 体积警告） |
| 前端回归 | datetime、realtime、route-loading、layout 脚本 | 通过 |
| 四传输采集浏览器回归 | `task e2e:acquisition` | 通过（8 devices / 4 channels） |
| 脚本管理与运行观察浏览器 E2E | `task e2e:acquisition-script` | 通过（draft/Validate/Publish v1/v2/v3/v4、`SCRIPT_RUNTIME`/`SCRIPT_LIMIT` 错误可见、静态状态隔离、Rollback/bind/unbind/runtime state） |
| ZNCK-I 真实动态 runtime | `task e2e:znck-i` | 通过（RTU + TCP；`7→0→7`；恢复后 FC16/FC03 再次触发；raw event；同码抑制） |
| GitHub Actions PostgreSQL gate | [Database compatibility gate run 35096551202](https://github.com/EziosWJ/edge-collector/actions/runs/35096551202) | 通过（`PostgreSQL integration contract`、SQLite contract、backend unit/vet 均成功） |

## 环境限制

- 本地 PostgreSQL integration 未执行：当前 WSL 的 Docker Desktop integration / daemon 不可用，`docker version` 返回该 WSL 发行版未发现 Docker，因此无法启动隔离 PostgreSQL；对应 GitHub Actions PostgreSQL gate 已在上表 run 中通过。
- `golangci-lint` 未安装，未执行；`gofmt`、`go test`、`go vet` 已执行。
- 脚本页面的 browser/API E2E 由 `react-admin/tests/acquisition-script-e2e.mjs` 负责，已在 TCP simulator 上验证完整管理闭环和 runtime state 可见性；独立 `react-admin/tests/znck-i-runtime-e2e.mjs` 已在真实 RTU/TCP simulator runtime 上补齐 ZNCK-I 的动态事务链路。浏览器页面不重复承载厂家协议请求顺序断言，协议顺序由该 runtime E2E、Go fake-session 和 simulator fixture 共同覆盖。

## 后续阶段边界

Starlark 动态事务已确认成为采集领域能力；业务告警历史与生命周期、MQTT 上报/远程控制、控制专用 UI、Modbus Server、工程量解析和生产化部署压力验证仍属于后续阶段。动态事件不等同于业务告警，脚本 FC05/FC16 也不等同于远程控制业务。
