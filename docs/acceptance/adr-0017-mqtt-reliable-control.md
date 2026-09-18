# ADR-0017 MQTT 实际验收方案与记录

状态：截至 2026-09-18，#39 总体范围及 #40～#47 已完成最终验收；SQLite/PostgreSQL × MQTT 5/3.1.1 真实矩阵通过。

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

端口可通过 `MQTT_E2E_BROKER_PORT`、`MQTT_E2E_POSTGRES_PORT`、`MQTT_E2E_API_PORT` 覆盖；设备 PTY 可通过 `MQTT_E2E_MODBUS_ALIAS` 覆盖，但必须与 simulator 配置中的 alias 一致。脚本默认使用临时 SQLite 文件，测试结束删除；PostgreSQL 只使用本次 Compose project 的临时容器，不连接部署 DSN。本次 Docker 主机的 PostgreSQL 端口 55433 映射不可达，实际矩阵统一使用 `MQTT_E2E_POSTGRES_PORT=15432`；这是执行环境端口映射差异，不是 PostgreSQL persistence/runtime/topic/payload contract 问题。

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

普通开发者直接运行 `node scripts/adr0017-mqtt-e2e.mjs` 时，如果缺少 Docker、Go 或 uv，脚本会打印 `SKIP ADR-0017 MQTT E2E: missing ...` 并退出 0；Taskfile 入口统一设置 `MQTT_E2E_REQUIRED=1`，因此 CI/正式验收遇到依赖缺失会失败而不会伪报通过。Docker image 拉取、Broker 端口占用、PostgreSQL 启动失败或 API/runtime 契约失败会保留具体错误并使 required 入口失败。本次验收在可用 Docker daemon 上完成，PostgreSQL 使用上段所述的 15432 端口覆盖。

完整的离线、LWT、进程重启场景需要 harness 管理 Broker（默认 `MQTT_E2E_MANAGE_SERVICES=1`）。外部 Broker 可以通过 `MQTT_E2E_BROKER_URL` 和 `MQTT_E2E_MANAGE_SERVICES=0` 连接，但由于脚本不能安全替外部服务执行 stop/restart，不能用该模式声称通过完整 #47 矩阵；应改用默认 Compose 入口，或由人工提供等价的 stop/start hook 后记录结果。

## #39～#47 Acceptance Criteria 自检

以下勾选以代码、单元测试和下方实际运行命令为依据；真实 PostgreSQL、Mosquitto、simulator 和进程重启项均有实际 E2E 证据，不用静态代码检查替代。

### #39 Parent Spec 总体范围

- [x] v1 Topic/Payload/QoS、raw latest-state、bounded reliable outbox、command journal、Starlark safe-boundary 和 secret 边界均由 #40～#47 覆盖。
- [x] #47 最终矩阵验证了 SQLite/PostgreSQL 与 MQTT 5/3.1.1 的同一应用层 contract；正式 telemetry/alarm 仍不属于本阶段。

### #40 配置、Secret、Outbox、Journal persistence

- [x] 单实例 `mqtt_config`、默认值、服务端范围校验及 PostgreSQL/SQLite schema/seed migration。
- [x] password/private key 使用部署级 master secret 加密；keep/set/clear、缺少 master secret 和 owner-only read-only 文件路径有测试。
- [x] outbox 的唯一 messageId、priority/order、QoS1/PUBACK 删除、rows+bytes 容量和 cleanup worker 已实现。
- [x] command FINAL reservation 在 ACCEPTED/enqueue 前原子准入，释放与 FINAL durable commit 同事务；满容量返回 `RELIABLE_RESULT_CAPACITY_EXHAUSTED` 且不会执行设备动作。
- [x] 未收到 PUBACK 的 command FINAL 不因 retention 到期删除；未完成 journal 不清理；并发 reservation 测试不超卖。
- [x] command journal 以 commandId 主键保存 dedupe/hash、状态、时间和最终结果；SQLite persistence contract 通过。
- [x] PostgreSQL persistence/runtime/topic/payload contract 已执行并通过；`payload_hash CHAR(64)` 保持现有 contract，生产 `CanonicalCommandHash()` 使用 SHA-256 并输出 64 位 hex。

### #41 MQTT Client runtime、状态机、Topic/Payload

- [x] `DISABLED/CONNECTING/CONNECTED/RECONNECTING/ERROR`、disabled 不连接、最新 committed config refresh、旧 client 关闭和 bounded backoff。
- [x] MQTT 5 与 3.1.1 transport、command subscription、稳定 edge/device identity、Topic builder 和 v1 codec 已实现。
- [x] edge online/LWT、device status、raw/event/command/result 的 QoS/retain contract 和 RFC3339 UTC 时间已覆盖单测。
- [x] LWT 使用预注册 timestamp、`reason=last_will`，不产生 `offlineAt`；旧异步回调不会把停用状态改回重连。
- [x] MQTT 错误状态与 acquisition 运行状态隔离，错误日志/API 使用稳定类型或脱敏文本。
- [x] 真实 MQTT 5/3.1.1 Broker connect/reconnect/LWT 已在 Mosquitto + simulator 矩阵执行。

### #42 Raw Snapshot、Device Status latest-state

- [x] raw 在完整 static + `after_poll` + CurrentState commit 后投影，per-device latest/coalesce、独立 publish interval 和 deterministic block/register 顺序。
- [x] block validity、last-known value、首次 null、lastSuccess/lastAttempt/error 语义保留；raw 不做业务解释。
- [x] raw QoS0/non-retained，device status QoS1/retained，reconnect republish；publish failure 不改变 acquisition。
- [x] slow/offline broker 只保留 bounded latest，不写 raw reliable outbox；projector retry 不 busy-loop。
- [x] 真实 Broker offline/恢复后的 latest、raw 不入 outbox 和 retained status 已在四格矩阵断言。

### #43 Reliable Outbox、Event/Result projection

- [x] 独立 worker 只在 CONNECTED drain，按 priority/createdAt 顺序、单条 timeout、attempt/稳定错误记录和 PUBACK 后删除。
- [x] committed Starlark event 才投影 `device-event/v1`；event 使用 QoS1/non-retained reliable outbox，不能操作 acquisition session。
- [x] command ACCEPTED/rejection/FINAL 使用 reliable outbox，FINAL 优先且 retention 不删除未确认 critical result。
- [x] worker ingress 有界、离线不阻塞 acquisition，重启继续处理 durable rows；SQLite/worker/retention 测试通过。
- [x] 真实 Broker 断线、FINAL durable 后停止 Broker、API 重启及重启 drain 已在四格矩阵断言。

### #44 Starlark command 与 channel safe boundary

- [x] 可选 callable `command(ctx,name,args)` 与旧脚本兼容；平台在 intake 验证 handler，JSON args 进入 Starlark。
- [x] command 仅由 channel runner 在完整 cycle 后 safe boundary 执行；MQTT callback 不持有/调用 Modbus session。
- [x] 同 channel 串行、bounded queue、poll fairness、跨 channel 可并行；复用 host/pacer/limits/state/event/overlay 语义。
- [x] command 与 after_poll 共用 `(deviceId, publishedVersion)` state scope；成功提交 state/event，失败回滚，已发 Modbus write 不回滚。
- [x] command 开始时捕获最新已发布脚本版本；不可用、队列满、runtime/Modbus/limit/output/cancel 错误分类有覆盖。
- [x] 通过真实 simulator 的 command safe-boundary/公平性矩阵已纳入 #47；burst 中每个已收到 command 都在 journal 和 result 中进入 `SUCCEEDED/FAILED/EXPIRED`，queue 满返回 `QUEUE_FULL`，普通 FC03 poll 持续。

### #45 Command Intake、幂等 Journal、Reliable Result

- [x] strict JSON/schema/unknown-field/topic-body/deviceId/expiry/args-size 校验；平台错误不进入 Starlark。
- [x] canonical payload hash 不受 JSON key 顺序影响；same commandId 不重复执行，different payload 返回 `COMMAND_ID_CONFLICT` 且无设备动作。
- [x] device enabled、script binding、handler、queue 和 FINAL capacity 按顺序检查；准入事务原子写 ACCEPTED+journal+reservation，容量错误精确为 `RELIABLE_RESULT_CAPACITY_EXHAUSTED`。
- [x] safe-boundary 前后状态 `ACCEPTED/RUNNING/terminal`、FINAL journal/outbox 原子化、reservation release 和稳定错误 payload 已覆盖。
- [x] ACCEPTED/crash/restart recovery、RUNNING 未知执行状态、terminal duplicate requeue 不创建第二执行 token；并发 duplicate intake 只入队一次。
- [x] QoS1、Broker 断线和 final durable/no-PUBACK 的真实线缆矩阵已通过；恢复阶段只补发 FINAL，simulator 未再次执行 FC16。

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
- [x] SQLite/PG × MQTT 5/3.1.1 的真实 Mosquitto 全矩阵已执行并通过。
- [x] PostgreSQL MQTT contract、simulator tests、Go backend check 与文档/脚本验收结果已追加到“本次执行结果”。

## 阶段 checklist

- [x] #40 MQTT config/secret/outbox/journal persistence
- [x] #41 MQTT runtime/topic/payload
- [x] #42 raw/status latest-state
- [x] #43 reliable outbox/event projection
- [x] #44 Starlark command/safe boundary
- [x] #45 command intake/journal/result
- [x] #46 API/Swagger/React 管理面
- [x] #47 SQLite/PG × MQTT5/3.1.1 真实 Broker 矩阵

填写最终结果时应把每次实际命令、通过项、未执行项和环境限制追加到“本次执行结果”，不要用未运行的 CI/本地命令替代实际证据。

## 本次执行结果

| 验收项 | 命令 | 结果 |
| --- | --- | --- |
| simulator tests | `task simulator:test` | 通过，22 项 |
| MQTT harness / Compose config | `node --check scripts/adr0017-mqtt-e2e.mjs`；`docker compose --project-name edge-collector-adr0017-config --file testdata/mqtt/docker-compose.yml config --quiet` | 通过 |
| SQLite + MQTT 5 | `MQTT_E2E_REQUIRED=1 MQTT_E2E_DATABASE=sqlite MQTT_E2E_PROTOCOL=MQTT_5 node scripts/adr0017-mqtt-e2e.mjs` | 通过；`simulatorControlWrites=7` |
| SQLite + MQTT 3.1.1 | `MQTT_E2E_REQUIRED=1 MQTT_E2E_DATABASE=sqlite MQTT_E2E_PROTOCOL=MQTT_3_1_1 node scripts/adr0017-mqtt-e2e.mjs` | 通过；`simulatorControlWrites=7` |
| PostgreSQL + MQTT 5 | `MQTT_E2E_REQUIRED=1 MQTT_E2E_DATABASE=postgres MQTT_E2E_PROTOCOL=MQTT_5 MQTT_E2E_POSTGRES_PORT=15432 node scripts/adr0017-mqtt-e2e.mjs` | 通过；`simulatorControlWrites=7` |
| PostgreSQL + MQTT 3.1.1 | `MQTT_E2E_REQUIRED=1 MQTT_E2E_DATABASE=postgres MQTT_E2E_PROTOCOL=MQTT_3_1_1 MQTT_E2E_POSTGRES_PORT=15432 node scripts/adr0017-mqtt-e2e.mjs` | 通过；`simulatorControlWrites=7` |
| 完整矩阵 Taskfile 入口 | `MQTT_E2E_POSTGRES_PORT=15432 task mqtt:integration` | 通过；四格均输出 `status=passed`，每格 final recovery 均未重复 FC16 |
| backend check | `GOCACHE=/tmp/edge-collector-go-build task backend:check` | 通过；全量 Go tests 与 `go vet ./...` |
| SQLite MQTT persistence contract | `go test -tags=integration ./integration -run 'SQLiteMQTT' -count=1` | 通过 |
| PostgreSQL MQTT persistence/runtime/topic/payload contract | `GOCACHE=/tmp/edge-collector-go-build go test -tags=integration ./integration -run '^TestPostgresMQTT(Persistence|RuntimeTopicPayload)Contract$' -count=1` | 通过；读取 `configs/config.dev.yaml` 连接 PostgreSQL，在随机 schema 内执行完整 schema/seed migration，测试后 schema 清理为 0 |
| frontend checks | 本轮未修改前端，未重复运行 `task frontend:lint/build/browser-test` | 不影响本次收尾；既有验收记录为通过 |

四格 E2E 都包含同一套 reliable-final 场景：command 真实执行并使 simulator FC16 写计数增加一次；FINAL journal 已为 `SUCCEEDED` 且在 Broker 停止后 outbox 仍有未确认行；API 被强制重启后只补发该 FINAL，恢复阶段 FC16 写计数不再增加。Burst 场景先确认外部 collector 已收到全部 6 个 command，再逐一确认 journal 与 result 均为 terminal；其中 queue 满的命令返回 `QUEUE_FULL`，普通 FC03 poll 继续增长。

本次 burst fixture 将 `outboxMaxBytes` 设为 8 MiB，因为生产每个 command 的 FINAL reservation 为 256 KiB；容量专测仍将 `outboxMaxRows=2`，避免把 queue-full/fairness 验收误测成字节容量拒绝。PostgreSQL `payload_hash CHAR(64)` 未修改；`CanonicalCommandHash()` 的 SHA-256 64 位 hex 与该 schema 一致。
