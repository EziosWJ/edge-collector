# ADR-0017 MQTT 实际验收方案与记录

状态：#40～#46 实现与本地契约已完成，#47 可重复验收夹具已加入；真实 Docker Broker/PostgreSQL 矩阵在当前环境阻塞。

本文只记录真实 Broker + `modbus-simulator` 的可重复验收入口，不改变 ADR-0017 或 Implementation Spec 的行为定义，也不关闭 GitHub Issue。

## 运行入口

所有命令从仓库根目录执行。默认由 harness 临时启动标准 `eclipse-mosquitto:2.0` 和 PostgreSQL 容器；Broker 配置只使用标准 MQTT 功能，不依赖私有 extension。

| 范围 | 命令 |
| --- | --- |
| simulator 配置、协议和 PTY 测试 | `task simulator:test` |
| SQLite + MQTT 5/3.1.1 全链路 | `task mqtt:e2e:sqlite` |
| PostgreSQL + MQTT 5/3.1.1 全链路 | `task mqtt:e2e:postgres` |
| 两种数据库完整矩阵 | `task mqtt:integration` |
| 单次运行 / 指定协议 | `MQTT_E2E_REQUIRED=1 MQTT_E2E_DATABASE=sqlite MQTT_E2E_PROTOCOL=MQTT_5 node scripts/adr0017-mqtt-e2e.mjs` |

端口可通过 `MQTT_E2E_BROKER_PORT`、`MQTT_E2E_POSTGRES_PORT`、`MQTT_E2E_API_PORT` 覆盖；设备 PTY 可通过 `MQTT_E2E_MODBUS_ALIAS` 覆盖，但必须与 simulator 配置中的 alias 一致。脚本默认使用临时 SQLite 文件，测试结束删除；PostgreSQL 只使用本次 Compose project 的临时容器，不连接部署 DSN。

## 测试夹具

- `testdata/mqtt/docker-compose.yml` 提供 `eclipse-mosquitto:2.0` 与可选 PostgreSQL 17。Mosquitto 监听容器内 1883，允许匿名仅用于本地验收，关闭持久化，避免残留 retained/outbox 之外的历史状态。
- `testdata/mqtt/mosquitto.conf` 开启连接日志与标准 MQTT listener；没有 broker-specific hook。
- `modbus-simulator/config/adr0017-mqtt-e2e.yaml` 只启动现有 `rtc_clock` fixture。FC03 读取 100、101、102，FC16 完整写入这三个 raw 寄存器；模拟器日志中的 `function=10 address=100 count=3 result=OK` 是真实控制动作计数依据。
- `scripts/adr0017-mqtt-e2e.mjs` 启动 `uv` simulator、Go `cmd/migrate`/`cmd/api` 和容器内官方 `mosquitto_sub/pub`，通过 HTTP 配置 MQTT、创建脚本/通道/设备，再从真实 MQTT wire 与 simulator request log 断言结果。

脚本使用 Starlark `command(ctx,name,args)` 执行确定的 FC16 RTC 写入；`after_poll` 只从最近 committed raw snapshot 产生每秒 generic event。测试不会把 raw 写入 reliable outbox，也不会把 simulator 的寄存器值解释成工程量或业务告警。

## #47 验收覆盖矩阵

| 验收主题 | 真实断言 |
| --- | --- |
| 连接与 LWT | retained edge online、retained device status、异常杀死 API 后 `reason=last_will`；等待长连接后比较 payload timestamp 与 harness 接收时间，明确不命名为 `offlineAt` |
| raw/latest | raw schema、100..102、block validity、QoS0/retain=false；Broker 离线期间 acquisition 仍 ONLINE，数据库 outbox 中没有 `raw-register-snapshot/v1`，恢复后只恢复 latest |
| reliable event | committed `device-event/v1` 使用 QoS1/non-retained，Broker 离线产生后重连补发并 drain durable row |
| command safe boundary | MQTT 发布 ACCEPTED 后由现有 channel runtime 执行 FC16；simulator log 验证完整 FC03 poll 在写入前完成，写入请求保持同一 channel 串行 |
| command 幂等 | 相同 commandId（含 JSON key 重排）重复发布不增加 simulator FC16 写；不同 payload 得到 `COMMAND_ID_CONFLICT` 且不写设备 |
| final/restart | final journal/outbox 已 durable 但 Broker 不可用时强制杀死 API；相同数据库重启后只补发 SUCCEEDED result，simulator 写计数不增加 |
| capacity/fairness | 临界 outbox reservation 下超额 command 不产生 FC16；command burst 产生 `QUEUE_FULL`，同时 simulator 仍继续 FC03 poll |
| secret | GET config、API/runtime 输出、simulator/MQTT payload 和 SQLite/PG 配置检查均不包含 password/master secret；回读只依赖 configured 标志 |
| protocol/database | 同一 harness 以 MQTT 5 和 MQTT 3.1.1 分别运行，并以 SQLite/PG 分别迁移和验收 |

#40～#45 的持久化、runtime、latest projector、outbox、safe-boundary 和 intake 细粒度契约仍由各模块单测/数据库集成测试负责；本矩阵只补真实 Broker、真实 simulator、进程重启和跨模块边界。

## 可重复性与跳过条件

普通开发者直接运行 `node scripts/adr0017-mqtt-e2e.mjs` 时，如果缺少 Docker、Go 或 uv，脚本会打印 `SKIP ADR-0017 MQTT E2E: missing ...` 并退出 0；Taskfile 入口统一设置 `MQTT_E2E_REQUIRED=1`，因此 CI/正式验收遇到依赖缺失会失败而不会伪报通过。Docker image 拉取、Broker 端口占用、PostgreSQL 启动失败或 API/runtime 契约失败会保留具体错误并使 required 入口失败。

完整的离线、LWT、进程重启场景需要 harness 管理 Broker（默认 `MQTT_E2E_MANAGE_SERVICES=1`）。外部 Broker 可以通过 `MQTT_E2E_BROKER_URL` 和 `MQTT_E2E_MANAGE_SERVICES=0` 连接，但由于脚本不能安全替外部服务执行 stop/restart，不能用该模式声称通过完整 #47 矩阵；应改用默认 Compose 入口，或由人工提供等价的 stop/start hook 后记录结果。

## #40～#47 Acceptance Criteria 自检

以下勾选以代码、单元测试和已实际运行的命令为依据；需要真实 PostgreSQL、Mosquitto 或进程重启的项，在当前无 Docker daemon 的环境保留为未完成，不用静态夹具替代。

### #40 配置、Secret、Outbox、Journal persistence

- [x] 单实例 `mqtt_config`、默认值、服务端范围校验及 PostgreSQL/SQLite schema/seed migration。
- [x] password/private key 使用部署级 master secret 加密；keep/set/clear、缺少 master secret 和 owner-only read-only 文件路径有测试。
- [x] outbox 的唯一 messageId、priority/order、QoS1/PUBACK 删除、rows+bytes 容量和 cleanup worker 已实现。
- [x] command FINAL reservation 在 ACCEPTED/enqueue 前原子准入，释放与 FINAL durable commit 同事务；满容量返回 `RELIABLE_RESULT_CAPACITY_EXHAUSTED` 且不会执行设备动作。
- [x] 未收到 PUBACK 的 command FINAL 不因 retention 到期删除；未完成 journal 不清理；并发 reservation 测试不超卖。
- [x] command journal 以 commandId 主键保存 dedupe/hash、状态、时间和最终结果；SQLite persistence contract 通过。
- [ ] PostgreSQL persistence contract/完整集成需在可用 Docker daemon 环境执行。

### #41 MQTT Client runtime、状态机、Topic/Payload

- [x] `DISABLED/CONNECTING/CONNECTED/RECONNECTING/ERROR`、disabled 不连接、最新 committed config refresh、旧 client 关闭和 bounded backoff。
- [x] MQTT 5 与 3.1.1 transport、command subscription、稳定 edge/device identity、Topic builder 和 v1 codec 已实现。
- [x] edge online/LWT、device status、raw/event/command/result 的 QoS/retain contract 和 RFC3339 UTC 时间已覆盖单测。
- [x] LWT 使用预注册 timestamp、`reason=last_will`，不产生 `offlineAt`；旧异步回调不会把停用状态改回重连。
- [x] MQTT 错误状态与 acquisition 运行状态隔离，错误日志/API 使用稳定类型或脱敏文本。
- [ ] 真实 MQTT 5/3.1.1 Broker connect/reconnect/LWT 需在 Docker 环境执行。

### #42 Raw Snapshot、Device Status latest-state

- [x] raw 在完整 static + `after_poll` + CurrentState commit 后投影，per-device latest/coalesce、独立 publish interval 和 deterministic block/register 顺序。
- [x] block validity、last-known value、首次 null、lastSuccess/lastAttempt/error 语义保留；raw 不做业务解释。
- [x] raw QoS0/non-retained，device status QoS1/retained，reconnect republish；publish failure 不改变 acquisition。
- [x] slow/offline broker 只保留 bounded latest，不写 raw reliable outbox；projector retry 不 busy-loop。
- [ ] 真实 Broker offline/恢复后 latest 断言需在 Docker 环境执行。

### #43 Reliable Outbox、Event/Result projection

- [x] 独立 worker 只在 CONNECTED drain，按 priority/createdAt 顺序、单条 timeout、attempt/稳定错误记录和 PUBACK 后删除。
- [x] committed Starlark event 才投影 `device-event/v1`；event 使用 QoS1/non-retained reliable outbox，不能操作 acquisition session。
- [x] command ACCEPTED/rejection/FINAL 使用 reliable outbox，FINAL 优先且 retention 不删除未确认 critical result。
- [x] worker ingress 有界、离线不阻塞 acquisition，重启继续处理 durable rows；SQLite/worker/retention 测试通过。
- [ ] 真实 Broker 断线、PUBACK 前断开、重启 drain 需在 Docker 环境执行。

### #44 Starlark command 与 channel safe boundary

- [x] 可选 callable `command(ctx,name,args)` 与旧脚本兼容；平台在 intake 验证 handler，JSON args 进入 Starlark。
- [x] command 仅由 channel runner 在完整 cycle 后 safe boundary 执行；MQTT callback 不持有/调用 Modbus session。
- [x] 同 channel 串行、bounded queue、poll fairness、跨 channel 可并行；复用 host/pacer/limits/state/event/overlay 语义。
- [x] command 与 after_poll 共用 `(deviceId, publishedVersion)` state scope；成功提交 state/event，失败回滚，已发 Modbus write 不回滚。
- [x] command 开始时捕获最新已发布脚本版本；不可用、队列满、runtime/Modbus/limit/output/cancel 错误分类有覆盖。
- [ ] 通过真实 simulator 的 command safe-boundary/公平性矩阵纳入 #47，待 Docker Broker 可用后执行。

### #45 Command Intake、幂等 Journal、Reliable Result

- [x] strict JSON/schema/unknown-field/topic-body/deviceId/expiry/args-size 校验；平台错误不进入 Starlark。
- [x] canonical payload hash 不受 JSON key 顺序影响；same commandId 不重复执行，different payload 返回 `COMMAND_ID_CONFLICT` 且无设备动作。
- [x] device enabled、script binding、handler、queue 和 FINAL capacity 按顺序检查；准入事务原子写 ACCEPTED+journal+reservation，容量错误精确为 `RELIABLE_RESULT_CAPACITY_EXHAUSTED`。
- [x] safe-boundary 前后状态 `ACCEPTED/RUNNING/terminal`、FINAL journal/outbox 原子化、reservation release 和稳定错误 payload 已覆盖。
- [x] ACCEPTED/crash/restart recovery、RUNNING 未知执行状态、terminal duplicate requeue 不创建第二执行 token；并发 duplicate intake 只入队一次。
- [ ] QoS1、Broker 断线和 final durable/no-PUBACK 的真实线缆矩阵待 #47 Docker 执行。

### #46 管理 API、Swagger、React

- [x] config、test-connection、runtime state、outbox stats、command journal list/detail REST endpoints 已注册并受 Bearer auth 保护。
- [x] config API 覆盖全部非 secret 字段，secret 仅返回 configured 标志；test connection 支持当前/preview 配置、bounded timeout 且不改变正式 runtime。
- [x] 成功配置写入 operation audit；password/private key 不进入 API、ciphertext、日志、审计或 MQTT payload。
- [x] Swagger 2.0 与 React 类型/API 已同步；管理页覆盖连接/TLS/重连/raw/outbox/journal、runtime health、连接测试、分页详情和 PermissionGuard。
- [x] keep/set/clear 不提交 placeholder/masked value；frontend secret-action、时间展示、lint/build/browser contract 测试通过。

### #47 真实 Broker + modbus-simulator E2E 与文档

- [x] 已加入标准 Eclipse Mosquitto + PostgreSQL Compose、现有 simulator PTY fixture、SQLite/PG 和 MQTT 5/3.1.1 Taskfile/harness 入口。
- [x] harness 覆盖 raw/status、offline latest/no raw outbox、event drain、LWT timestamp、safe-boundary FC16、duplicate/conflict、capacity zero-action、fairness、script switch、secret surface 和 final restart recovery 断言。
- [x] ADR/Spec/CONTEXT/README/requirements、Swagger、React 类型/页面和本验收记录已同步；既有 acquisition、script、ZNCK-I 回归 E2E 通过。
- [ ] SQLite/PG × MQTT 5/3.1.1 的真实 Mosquitto 全矩阵未能在当前环境执行：Docker wrapper 报 Docker Desktop integration 未接通。
- [ ] PostgreSQL integration 同样未能执行，需在具备 Docker daemon 的环境完成并把结果追加到本节。

## 阶段 checklist

- [x] #40 MQTT config/secret/outbox/journal persistence
- [x] #41 MQTT runtime/topic/payload
- [x] #42 raw/status latest-state
- [x] #43 reliable outbox/event projection
- [x] #44 Starlark command/safe boundary
- [x] #45 command intake/journal/result
- [x] #46 API/Swagger/React 管理面
- [ ] #47 SQLite/PG × MQTT5/3.1.1 真实 Broker 矩阵（环境阻塞）

填写最终结果时应把每次实际命令、通过项、未执行项和环境限制追加到“本次执行结果”，不要用未运行的 CI/本地命令替代实际证据。

## 本次执行结果

| 验收项 | 命令 | 结果 |
| --- | --- | --- |
| simulator tests | `task simulator:test` | 通过（沙箱外真实执行，22 项）；沙箱内一次执行因真实 TCP/UDP socket 权限策略失败，非断言失败 |
| MQTT harness syntax / fixture config | `node --check scripts/adr0017-mqtt-e2e.mjs`；`UV_CACHE_DIR=/tmp/edge-collector-uv-cache uv run modbus-simulator --check-config --config config/adr0017-mqtt-e2e.yaml` | 通过 |
| Mosquitto Compose config | `docker compose --project-name edge-collector-adr0017-config --file testdata/mqtt/docker-compose.yml config --quiet` | 未执行：当前 WSL 的 Docker wrapper 报告未接通 Docker Desktop integration；需在具备 Docker daemon 的环境重跑 |
| SQLite MQTT 5 real E2E attempt | `MQTT_E2E_REQUIRED=1 MQTT_E2E_DATABASE=sqlite MQTT_E2E_PROTOCOL=MQTT_5 node scripts/adr0017-mqtt-e2e.mjs` | 未执行全链路；required 入口明确失败：当前 WSL 执行环境报告 `SKIP ... missing docker`，Docker Desktop/WSL integration 未接通 |
| SQLite MQTT 5/3.1.1 Taskfile entry | `task mqtt:e2e:sqlite` | 入口可执行；第一条 MQTT 5 invocation 因 required 模式发现 Docker 不可用而退出（Task exit 201 / harness exit 1），后续协议未运行 |
| PostgreSQL MQTT 5/3.1.1 | `task mqtt:e2e:postgres` | 入口可执行；当前 Docker wrapper 未接通 daemon，required harness 未进入全链路 |
| existing acquisition browser/API E2E | `task e2e:acquisition` | 通过（8 devices、4 channels；夹具使用 `APP_ENV=test` 避免 dev seed 干扰） |
| existing acquisition script E2E | `task e2e:acquisition-script` | 通过（script version 1～4、runtime error/limit 分类） |
| existing ZNCK-I runtime E2E | `task e2e:znck-i` | 通过（RTU + TCP、2 devices） |
| backend check | `task backend:check` / `task check` | 通过；包含 Go tests 与 vet |
| frontend checks | `task frontend:lint`、`task frontend:build`、`task check` | 通过；Vite 仅报告既有大 chunk warning |
| SQLite MQTT persistence contract | `go test -tags=integration ./integration -run 'SQLiteMQTT' -count=1` | 通过 |
| PostgreSQL MQTT persistence contract | `go test -tags=integration ./integration -run 'PostgresMQTT' -count=1` | 按测试约定 skip：Docker daemon 不可用 |
| PostgreSQL integration | `task db:integration:postgres` | 未通过/环境阻塞：Docker Desktop integration 未接通；既有 PG helper 无法启动容器 |
| real MQTT E2E | `task mqtt:integration`、`task mqtt:e2e:sqlite`、`task mqtt:e2e:postgres` | required 入口明确失败：缺少可用 Docker；未将其标记为通过 |
| golangci-lint | `golangci-lint` | 未执行：工具未安装；`go test`/`go vet` 已通过 |
