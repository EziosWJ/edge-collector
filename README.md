# Edge Collector

**Edge Collector（工业边缘采集服务）** 是面向工业现场边缘设备的数据采集与协议接入项目，目标运行环境包括 RK3568 + Buildroot / Ubuntu。

项目由原脚手架的 `iot` 分支迁移而来，已保留并验证 SQLite、ARM64 静态交叉编译、管理后台、认证/RBAC、配置、日志等基础能力。后续业务能力在本仓库独立演进。

## 项目目标

当前需求范围包括：

- 通过 RS485 串口、TCP、UDP 接入现场设备，主要采集 Modbus 协议数据；
- 实时数据持续轮询采集并按策略上报；
- 设备告警按设备协议判定，触发后即时采集并上报，未触发时继续参与轮询；
- 通过 MQTT Client 上报采集数据，并订阅控制指令；
- 对开关类设备执行分闸、合闸等控制；
- 作为 Modbus Server 向其他设备或系统提供指定数据；
- 不同设备的寄存器布局、告警查询流程和控制逻辑允许独立适配。

> 采集底座、ADR-0016 Starlark 动态事务和 ADR-0017 MQTT 上报/控制已在现有四种 Modbus transport 上落地；正式业务告警、Modbus Server、生产化压力验证与部署完善仍按后续阶段推进。

正式需求基线：[`docs/requirements/edge-collector-requirements.md`](docs/requirements/edge-collector-requirements.md)

动态事务决策与实现规格：[`ADR-0016`](docs/adr/0016-user-configurable-starlark-modbus-dynamic-transactions.md)、[`Implementation Spec`](docs/specs/starlark-modbus-dynamic-transactions.md)。ZNCK-I 验收夹具说明见 [`modbus-simulator/README.md`](modbus-simulator/README.md)。

## 目录结构

```text
├── edge-collector-api/   # Go REST API 与后续边缘采集后端
├── react-admin/          # React 管理后台
├── docs/                 # ADR、需求、部署记录与项目文档
├── CONTEXT.md            # 项目上下文与架构约定
└── Taskfile.yml          # 跨平台开发任务
```

## 技术栈

**后端 `edge-collector-api/`**

Go 1.26 · Gin · GORM · PostgreSQL / SQLite · Goose · Koanf · JWT · Prometheus · Swagger

**前端 `react-admin/`**

React 19 · TypeScript · Vite 6 · Tailwind CSS · shadcn/ui · react-router-dom · Zustand · react-hook-form + zod

## 快速开始

首次使用时复制开发配置：

```sh
cp edge-collector-api/configs/config.dev.example.yaml edge-collector-api/configs/config.dev.yaml
```

常用命令：

```text
task db:migrate          # PostgreSQL migration
task db:migrate:sqlite   # SQLite migration
task api                 # 启动 Go API
task api:sqlite          # SQLite profile 启动 API
task web                 # 启动 React
task dev                 # PostgreSQL + API + Web
task dev:sqlite          # SQLite + API + Web
task test                # 后端测试
task check               # 后端检查 + 前端 lint/build
task db:check            # PostgreSQL/SQLite 兼容性检查
task e2e:acquisition     # 采集 API/模拟器/浏览器验收
```

### 本地 MQTT 配置

MQTT Broker 登录密码和 Edge Collector 的 MQTT 主密钥是两个不同的凭据：

- **Broker 用户名/密码**：Edge Collector 连接 Mosquitto 时使用，例如本地开发用户 `edge_collector`；
- **`APP_MQTT__MASTER_SECRET`**：只由 Edge Collector 后端使用，用于加密/解密数据库中保存的 MQTT Broker 密码，不会作为 Broker 登录密码发送。

通过管理页面保存 MQTT 密码时，后端会先使用主密钥执行应用层加密再写入 SQLite/PostgreSQL。因此，同一数据库必须持续使用相同的主密钥；如果更换主密钥，之前保存的 MQTT 密码将无法解密，需要恢复原主密钥或重新保存 MQTT 配置。

使用 Taskfile 启动本地开发环境时无需手工设置主密钥：

```sh
task dev:sqlite
# 或
task dev
```

`task api`、`task api:sqlite`、`task dev` 和 `task dev:sqlite` 会使用仓库内固定的 **DEV ONLY** 主密钥兜底，因此本地数据库中的 MQTT 密码在重启后仍可解密。该默认值公开在 `Taskfile.yml` 中，只适用于本机开发数据，不得用于生产环境。

需要使用已有数据库或自定义主密钥时，可以显式覆盖：

```sh
APP_MQTT__MASTER_SECRET='your-existing-stable-master-secret' task dev:sqlite
```

直接执行 `go run ./cmd/api`、使用 IDE 启动 API，或在生产环境部署时，不会依赖 Taskfile 的本地兜底值，必须显式提供主密钥。后端支持：

```sh
APP_MQTT__MASTER_SECRET='a-stable-deployment-secret' go run ./cmd/api

# 或从只读、仅 owner 可访问的文件加载
APP_MQTT__MASTER_SECRET_FILE=/run/secrets/edge-collector-mqtt-master-secret go run ./cmd/api
```

生产环境应由 Secret Manager、Kubernetes Secret、systemd credentials/EnvironmentFile 等部署设施提供独立随机主密钥，并确保重启、升级和数据恢复后仍使用同一值，不要使用 Taskfile 中的开发默认值。

本地 Mosquitto 常见配置为：

```text
Broker:   mqtt://127.0.0.1:1883
Username: edge_collector
Password: 以本地 edge-dev-infra / Mosquitto 开发环境配置为准
```

如果页面保存 MQTT 配置时出现 `MQTT master secret is not configured`，说明 API 不是通过上述 Task 开发入口启动，且当前进程也没有显式注入主密钥。

## ARM64 / RK3568

SQLite + CGO 的 ARM64 静态交叉编译已经在目标设备验证成功。验证环境、Zig musl 编译参数与部署结果见：

- [`docs/sqlite-arm64-test-deployment.md`](docs/sqlite-arm64-test-deployment.md)

该文档保留当时真实测试路径和文件名，用作已验证部署记录，不随项目重命名而改写。
