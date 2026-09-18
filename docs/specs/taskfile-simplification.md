# Taskfile 任务入口收敛 Spec

## 状态

Implemented

## 背景

根目录 `Taskfile.yml` 同时承载日常开发、Agent 验证、数据库兼容性和专项 E2E 入口。随着功能增加，出现了重复任务、只做编译校验却名为构建的任务，以及与实际用途不一致的测试任务名称。

日常开发的主要工作流是 `task dev`；其他任务主要服务于 Agent 按改动范围执行验证。因此 Taskfile 应保留清晰的聚合入口和必要的专项验收入口，不为每个底层命令重复建立包装任务。

## 目标

- 以 `task dev` 作为 PostgreSQL 开发环境的主入口，以 `task dev:sqlite` 作为 SQLite 开发环境的主入口。
- 为 Agent 提供少量稳定的聚合验证入口：后端、前端、全量检查和数据库兼容性检查。
- 删除重复、无仓库使用记录或语义误导的任务。
- 保留能表达独立验收边界的 E2E、模拟器和 MQTT 任务。
- 保证任务名称、CLAUDE.md 和其他验证文档保持一致。

## 任务契约

### 日常开发入口

| 任务 | 用途 |
|---|---|
| `task dev` | 执行 PostgreSQL migration，并并行启动 Go API 和 React 前端 |
| `task dev:sqlite` | 执行 SQLite migration，并并行启动 SQLite API 和 React 前端 |
| `task api` / `task api:sqlite` | 只启动对应配置的 API |
| `task web` | 只启动 React 开发服务器 |

### Agent 验证入口

| 任务 | 用途 |
|---|---|
| `task test` | 后端单元测试 |
| `task backend:check` | `task test` 加 `go vet` |
| `task frontend:lint` | 前端 ESLint |
| `task frontend:build` | 前端 TypeScript/Vite 构建 |
| `task frontend:browser-test` | 前端浏览器回归测试；不启动 API、Docker 或模拟器 |
| `task check` | 后端检查、前端 lint/build 和浏览器回归测试 |
| `task db:check` | 后端检查以及 PostgreSQL、SQLite 两套数据库集成契约 |

涉及数据库兼容性时使用 `task db:check`；只涉及后端时使用 `task backend:check`；只涉及前端时按需运行前端 lint、build 和浏览器回归测试。

### 专项验收入口

以下任务保留独立入口，因为它们依赖不同的运行环境或代表不同的验收边界：

- `db:integration:postgres`、`db:integration:sqlite`
- `e2e:acquisition`、`e2e:acquisition-script`、`e2e:znck-i`
- `simulator:test`
- `mqtt:e2e:sqlite`、`mqtt:e2e:postgres`、`mqtt:integration`
- `frontend:datetime-test`

专项任务只在改动范围涉及对应链路时运行，不纳入日常 `task dev` 启动流程。时间处理相关改动仍需单独运行 `task frontend:datetime-test`。

## 已实施的收敛

- 删除 `db:migrate:schema` 和 `db:migrate:seed`；常规入口统一执行全量 migration。
- 删除与 `db:integration:postgres` 完全重复的 `backend:integration`。
- 删除 `backend:test` 包装任务，保留 `task test` 作为后端单元测试入口。
- 让 `backend:check` 依赖 `task test`，自身只执行 `go vet ./...`。
- 删除 `dev:sqlite` 上重复的 `APP_CONFIG_PROFILE` 设置；SQLite profile 由迁移和 API 子任务各自负责。
- 删除 `build`；原任务对多个 Go `main` package 只做编译校验，并不产生可部署的后端制品。
- 将 `frontend:realtime-test` 改为 `frontend:browser-test`，并纳入现有 MQTT 配置、实时状态、布局、采集配置和路由回归脚本。
- 同步更新 `CLAUDE.md` 和通知功能验证文档中的任务名称及推荐验证顺序。

## 非目标

- 不改变 API、数据库 schema、前端功能或测试断言。
- 不把需要 Docker、模拟器或真实 MQTT Broker 的专项 E2E 塞进默认 `task check`。
- 不引入 Taskfile matrix 或额外抽象来压缩少量相似的 MQTT 命令。
- 不把 `task build` 改造成发布流水线；如未来需要制品构建，应另行定义输出目录、文件名和发布契约。

## 验收标准

- `task --list` 只展示当前有效任务，不再出现已删除的重复或误导性入口。
- `task --summary check` 的依赖包含 `backend:check`、前端 lint/build 和 `frontend:browser-test`。
- `task test` 执行 `go test ./...` 并通过。
- `task backend:check` 在后端测试通过后执行 `go vet ./...`。
- 支持浏览器运行环境时，`task frontend:browser-test` 中的回归脚本全部通过；无法启动本地 Vite/Playwright 时应明确报告环境阻塞。
- Taskfile、`CLAUDE.md` 和引用这些任务的验证文档不再使用已删除的任务名。

## 关联文件

- [`Taskfile.yml`](../../Taskfile.yml)
- [`CLAUDE.md`](../../CLAUDE.md)
- [`docs/notification-plan.md`](../notification-plan.md)
