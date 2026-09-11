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

> 以上为项目需求边界，不代表相关采集能力已经全部实现。当前阶段首先完成项目基础设施与需求设计，再逐步落地设备接入能力。

## 目录结构

```text
├── edge-collector-api/   # Go REST API 与后续边缘采集后端
├── react-admin/          # React 管理后台
├── docs/                 # ADR、部署记录与项目文档
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
```

## ARM64 / RK3568

SQLite + CGO 的 ARM64 静态交叉编译已经在目标设备验证成功。验证环境、Zig musl 编译参数与部署结果见：

- [`docs/sqlite-arm64-test-deployment.md`](docs/sqlite-arm64-test-deployment.md)

该文档保留当时真实测试路径和文件名，用作已验证部署记录，不随项目重命名而改写。
