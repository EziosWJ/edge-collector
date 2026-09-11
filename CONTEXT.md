# 项目上下文

> 本文件记录本项目的领域语言与术语。它是 `improve-codebase-architecture` / `diagnose` / `tdd` / `grill-with-docs` 等 skills 读取的"词汇表"——当输出里需要命名一个领域概念时，从这里取词，避免各自生造近义词。
>
> **不要在这里编造术语。** 只写已经真实存在于代码或对话里的词。如果项目结构暗示存在某个概念（例如脚手架里预留了 `src/modules/user/`），先不要假设它的业务含义——等业务含义在对话里浮现、并经 `/grill-with-docs` 确认后再补进来。

## 项目概览

- **项目代号**: Edge Collector
- **仓库类型**: monorepo
- **交流 / 输出语言**: 中文
- **项目定位**: 面向工业现场边缘设备的数据采集与协议接入项目；采用 monorepo，包含 React 管理后台与 Go 后端，业务代码统一在 `edge-collector-api/` 演进。
- **当前后端事实**: `edge-collector-api/` 是可运行的 Go 后端（Gin、配置、PostgreSQL/SQLite 连接池、Goose、统一响应、CORS、可观测性与 Swagger），PostgreSQL 是默认数据库，SQLite 支持单 API 实例使用本地持久文件。已迁移认证、角色、菜单、部门、用户、字典、系统配置、本地文件管理、日志管理和站内通知接口。文件内容存于配置的本地根目录（开发 Compose 使用持久化 Volume），数据库仅保存元数据和相对路径；删除保持元数据软删，不删除物理内容。所有管理模块的成功写操作写入操作审计日志；登录日志与操作日志的查询、详情与清空接口已迁移，清空受 `system.log-clear-enabled` 配置门控，操作日志查询通过 LEFT JOIN sys_user 回填 operator_name。站内通知支持 ADMIN 发布、用户分页查询和已读状态，用户角色集合实际变化时在同一事务写入角色变更通知。既有迁移接口和站内通知均有 PostgreSQL/SQLite 集成契约覆盖，Swagger 随实现同步生成。Java 参考后端 `base-api/` 已删除（可从 git 历史恢复）。
- **脚手架占位内容**: 脚手架里已出现大量"占位"示例（如 HelloWorld、UserTable、示例组件等），这些**不是真正的业务概念**，只是脚手架产物。真正的业务领域术语应来自后续的业务对话，而不是反向推导脚手架示例。

## 目录结构

```
/
├── react-admin/      # 前端（管理后台 SPA）
├── edge-collector-api/      # Go REST API（后端接口在此开发）
├── record/           # 项目过程记录
├── docs/
│   ├── adr/          # 架构决策记录（ADR）
│   └── agents/       # Agent skills 的仓库级配置
└── task/             # 任务上下文（读取范围边界时参考）
```

## Edge Collector 领域词汇（已确认）

- **通信通道**：现场设备通信所使用的 RS485、TCP 或 UDP 通道。
- **设备**：通过某个通信通道接入 Edge Collector 的现场工业设备。
- **实时数据**：设备运行期间需要持续轮询采集并按上报要求发送的数据。
- **告警状态**：需要持续轮询检查、用于判断设备是否产生新告警的状态。
- **告警详情**：告警触发后，按照具体设备协议进一步查询得到并需要即时上报的信息。
- **控制指令**：上级平台通过 MQTT 下发、要求 Edge Collector 对目标设备执行的操作。
- **MQTT 上报**：Edge Collector 作为 MQTT Client 向上级 Broker 发送实时数据、告警、设备状态或控制结果。
- **Modbus Server**：Edge Collector 面向其他设备或系统提供的 Modbus 数据服务，其对外地址不要求等同于现场设备原始寄存器地址。

> 当前需求基线见 `docs/requirements/edge-collector-requirements.md`。设备 Driver、调度器、任务类型、缓存结构等尚未确认，不作为当前领域词汇预先写入。

## 技术栈与演进状态

### 前端 react-admin
- 框架: React 19 + TypeScript + Vite 6
- 路由: react-router-dom v7
- 状态管理: Zustand
- 表单: react-hook-form + zod（@hookform/resolvers）
- 样式: Tailwind CSS + class-variance-authority + tailwind-merge
- 图标: lucide-react
- Lint: ESLint + typescript-eslint

### 当前后端 edge-collector-api（Go，唯一后端）

- 形态: 模块化单体（Modular Monolith）；当前已实现管理 REST API，后续在同一应用中增加现场采集、MQTT Client 与 Modbus Server 等已确认业务能力；不提前拆微服务。
- Web: Gin；数据访问: GORM + Go 标准 `database/sql`；Schema: Goose migration。
- 长期兼容目标: PostgreSQL、MySQL、SQLite；当前正式支持 PostgreSQL 和 SQLite，PostgreSQL 是默认数据库，MySQL 仍是后续兼容目标。SQLite 仅承诺单 API 实例、本地持久文件和小规模低写并发部署，具体边界见 ADR-0010。
- 配置: 使用 Koanf v2，覆盖顺序为默认值 → `config.yaml` → `config.{APP_ENV}.yaml` → `APP_` 环境变量；嵌套键使用双下划线（如 `APP_DATABASE__URL`）。数据库配置包含 driver 和 URL；PostgreSQL 继续拆分 URL、用户名和密码，SQLite 使用本地文件路径且禁止凭据；基础配置与环境模板提交，实际环境 YAML 可保存凭据但不得提交 Git，详见 ADR-0005。
- API: 新建接口优先使用 `/api/v1/...`、统一响应/错误码/分页。Gin Handler 与 DTO 注释是文档来源，使用 `swaggo/swag` 生成并提交 Swagger 2.0 文档；Swagger UI 仅在开发环境开放。迁移既有前端接口时，以 ADR-0002 的兼容契约为准，暂保留既有 `/api/**` 路径，直到另有版本化决策。
- CORS: 默认允许跨域 Bearer Token 请求且不启用 Cookie 凭据；可通过精确 `allowed_origins` 配置收紧来源范围，不使用允许凭据的通配来源。
- 认证: 目标为 JWT 加数据库管理的动态角色和菜单关系；`ADMIN` 是内置角色，`admin`、`user` 只是角色示例。JWT 使用 HS256，密钥由运行配置提供，包含 `sub`、`jti`、`iat`、`exp` 并校验 `issuer`、`audience`，不包含角色或菜单。Gin middleware 首版只校验登录态，不按 `permissionCode` 拦截接口；会话持久化在所选数据库的 `auth_session` 表中，JWT `jti` 用于校验和登出即时撤销；不引入 Redis、Casbin、ABAC、多租户权限或组织树数据权限。
- 可观测性: 使用 `log/slog` 记录 request_id、请求方法与路径、状态、耗时、user_id 和错误；`/health` 只检查进程存活，`/ready` 检查所选数据库并在不可用时返回 503，`/metrics` 不要求 JWT、仅通过内部网络或反向代理白名单供 Prometheus 抓取且不应用默认 CORS。业务审计日志须落库，不能由应用日志替代：middleware 将 request_id、IP、User-Agent 写入标准 `context.Context`，Service 显式记录审计，Repository 持久化；认证记录成功与失败登录，其他操作仅在业务成功后记录。
- 其他目标组件: 本地文件系统加 Docker Volume（文件服务与存储实现解耦）、Excelize、Docker 与 Docker Compose；不提前引入 Kubernetes、OpenTelemetry tracing、Redis 分布式锁或微服务治理基础设施。Edge Collector 已确认需要作为 MQTT Client 与上级 Broker 通信；这里“不提前引入 MQ”仅指不为内部架构预设 RocketMQ/Kafka 等消息队列基础设施，不限制 MQTT 业务接入。

通用架构取舍见 [ADR-0004](docs/adr/0004-backend-architecture-and-database-strategy.md)，SQLite 正式生产支持边界见 [ADR-0010](docs/adr/0010-sqlite-production-support.md)。

## Go 后端架构约定（目标实现必须遵守）

### 初始目录布局

```text
edge-collector-api/
├── cmd/api/                 # HTTP 服务入口
├── cmd/migrate/             # 显式 Goose migrate 命令
├── configs/                 # 基础配置与环境模板；实际环境 YAML 不提交
├── migrations/              # Schema 与 seed migration
├── docs/                    # 提交的 Swagger 生成文件
├── internal/app/            # 依赖组装与路由注册
├── internal/config/         # Koanf 配置加载
├── internal/platform/
│   ├── database/            # GORM、连接池、方言隔离
│   └── http/                # 统一响应、错误和通用 middleware
└── internal/auth/           # 首个业务模块
```

测试与被测模块同目录放置为 `*_test.go`；不创建全局 controller/service/repository 目录。

### 模块与依赖

- 代码优先按业务模块组织，而不是把 controller/service/repository/entity/dto 分散为全局技术目录。模块按需包含 `handler.go`、`service.go`、`repository.go`、`dto.go`、`model.go`；不为形式补齐空文件。
- 推荐边界为 `Handler → Service → Repository → GORM → database/sql → 数据库`。依赖在 `internal/app` 或实际 composition root 手工组装：Config → Database → Repository → Service → Handler → Router。
- Handler 只处理 HTTP 参数、DTO、参数校验、状态码和 API response；不写业务逻辑、不直接访问数据库。
- Service 使用 `context.Context`，负责规则、校验、状态流转和编排；不依赖 Gin Context、GORM 或具体数据库。简单审批以 `Approve()`、`Reject()` 等明确方法表达，不使用万能 `UpdateStatus()`。
- Repository 负责查询、持久化和事务中的数据库操作。普通模块可直接使用含 `db *gorm.DB` 的具体 Repository；仅在确有多实现或跨模块能力边界时定义 interface。禁止 Java 式空壳 `RepositoryImpl`、Factory。
- 不使用 Fx、Dig、Wire、Service Locator、业务 Singleton 或全局 Service。基础设施在启动时创建一次并通过依赖传递共享。

### 数据库与 Migration

- 业务查询优先采用 PostgreSQL、MySQL、SQLite 均稳定支持的 CRUD、普通事务、WHERE/JOIN/GROUP BY/ORDER BY、LIMIT/OFFSET、普通索引/唯一约束/外键、聚合、LIKE/IN/NULL 及基础标量类型。
- 不要让 JSONB、ARRAY、ILIKE、RETURNING、DISTINCT ON、扩展、专属 UUID、MySQL ENUM/函数/UPSERT、专属全文搜索或存储过程扩散到业务层。确有需要时，隔离在 infrastructure/database 或 Repository 层，并记录原因、提供针对性测试；驱动判断不得进入 Handler 或 Service。
- 启动时只创建一个 GORM DB；通过 `gormDB.DB()` 获取并配置同一个 `*sql.DB` 的连接池（MaxOpenConns、MaxIdleConns、ConnMaxLifetime、ConnMaxIdleTime）。Repository/Service 持有的是池化 DB 句柄，不是固定 TCP 连接；不另引入连接池框架。
- Schema 变更必须随代码提交版本化 Goose migration。生产环境和 API 进程均不自动执行 migration 或 `AutoMigrate()`；部署前由独立的 `migrate up` 步骤执行，Docker Compose 使用一次性 migrate 服务并在成功后启动 API。本地开发也执行相同的显式命令。表、索引和约束与管理员、根部门、`ADMIN`、菜单、字典、系统配置等内置数据分属独立 migration；种子数据只执行一次，不由 API 自动补种。PostgreSQL 与 SQLite 使用独立的 schema/seed migration 树，逻辑版本号锁步；方言差异集中在 migration 和 `internal/platform/database`，不扩散到 Handler 或 Service。
- 实体主键延续现有接口的数据形态，使用数据库生成的 `int64` 数值 ID；PostgreSQL 通过 identity/sequence 生成，业务层不得依赖具体方言语法，也不在迁移中切换为 UUID。
- 数据库兼容意味着真实集成测试通过，不只是能建立连接。PostgreSQL 与 SQLite 的验收范围为 CRUD、事务、分页、排序、普通 JOIN/聚合、权限、审批、通知与审计日志；MySQL 尚未进入正式支持矩阵。
- 单元测试不连接数据库；PostgreSQL 集成测试使用 Docker 提供的临时隔离实例，SQLite 集成测试使用临时本地文件；两者均验证 migration、Repository、认证会话和 HTTP 契约。自动化测试不得连接实际部署 DSN 或其数据。

### 部署数据库拓扑

- 开发 Docker Compose 启动独立的 PostgreSQL 命名 volume，再依次运行 migrate 与 API；该数据库只绑定本机端口。
- 实际部署通过外部 PostgreSQL 运行配置执行 migrate 与 API，不依赖 Compose 数据库容器。
- SQLite 部署使用单个 API 实例和本地持久卷中的数据库文件；应用启用外键、WAL、忙等待超时和 UTC，锁等待耗尽统一返回 503，不重放业务事务。需要多副本或更高写并发时使用 PostgreSQL。

### 运行时约定

- 少量定时任务优先 `time.Timer`/`time.Ticker`，只有需要 cron 表达式才引入轻量库。多实例互斥不能把 PostgreSQL advisory lock 当通用方案；需要时另行设计 lock table、lease 或唯一任务键加事务/超时。
- 新依赖必须说明解决的真实问题；不得为“以后可能需要”预先引入服务注册、RPC、配置中心、分布式事务、MQ、Redis、Gateway 或 Kubernetes。未来拆分时，由业务模块自然演变为 REST 服务，Gateway 再保持外部 URL 兼容。

## Agent 开发规则

1. 先理解现有模块边界，优先在既有模块内完成改动；不要随意新增抽象层或修改 generated code。
2. Handler 不直接操作数据库，Service 不依赖 Gin Context、GORM 或具体数据库，Repository 负责数据访问。
3. 数据库专属能力必须局部化；修改 Schema 必须增加 migration；修改 REST API 必须同步 Swagger/OpenAPI 或契约文档。
4. 不创建业务 Singleton、全局 Service，或未被需求证明的复杂架构。
5. Go 服务落地后，完成改动至少依次执行 `go fmt ./...`、`go test ./...`、`go vet ./...`、`golangci-lint run`；若环境或项目尚不具备某命令，必须说明原因和未执行项。

## 现状与目标差异

- Go 服务已具备 Gin、GORM、Goose、Prometheus、`log/slog`、Koanf、Docker Compose、Swagger、JWT 会话认证与站内通知能力，并可执行格式化、测试与静态检查；Excelize 及其余业务模块仍按 Issue 顺序实施。
- ADR-0002 规定的既有 `/api/**`、响应结构和 Bearer Token 外部契约已实现；与新接口 `/api/v1` 规范并存时，迁移兼容优先。

## 已知领域术语

**站内通知**：在本系统内向用户传递的消息，来源包括管理员手动发布和业务自动触发；首版接收范围支持全体用户或指定用户。

**全体用户通知**：接收范围为发布时已有且启用的用户的站内通知，发布后新建的用户不补收该通知。

**角色变更通知**：用户角色分配操作中，角色集合实际改变并保存成功后，自动发送给受影响用户的站内通知，包含变更前后的角色；重复保存相同角色不产生通知，修改角色定义或角色菜单权限暂不产生通知。

**通知已读**：用户打开通知详情或执行全部已读后，该用户对应通知的阅读状态；只展示列表或预览不算已读。

### 认证与登录防护

**凭据校验失败**：登录请求已经进入身份凭据核验，但提交的用户名与密码未形成有效认证结果。

**登录防护拦截**：登录请求在身份凭据核验前，因请求频率或连续失败达到防护边界而被暂缓或拒绝的结果。

**登录尝试准入**：登录请求在身份凭据核验前获得继续执行的资格；准入受来源地址和用户名维度的频率边界共同约束。

**菜单搜索**：供当前用户查找其侧栏导航中可跳转页面入口的功能，包含站内页面与外链；目录用于区分页面所属层级，检索对象不包含用户、文件等业务数据。
_Avoid_：全局搜索（容易被理解为包含业务数据的检索）

**单文件上限**：单次上传中，一个文件内容允许占用的最大字节数；当前上限为 50 MiB（52,428,800 字节），现有 API 文案中的“50 MB”指该二进制阈值。该限制属于文件业务边界，不替代 HTTP 请求体限制。

**批次文件数上限**：一次批量上传允许包含的最大文件数量；当前上限为 20 个文件。

**批次总大小上限**：一次批量上传中所有文件内容字节数之和允许达到的最大值；当前上限为 200 MiB（209,715,200 字节）。不包含 multipart 边界和其他表单字段开销。

**HTTP 请求体上限**：请求进入 multipart 解析前允许读取的原始 HTTP body 最大字节数；单文件接口当前为 55 MiB（57,671,680 字节），批量接口当前为 210 MiB（220,200,960 字节）。所有 `multipart/form-data` 请求都必须有策略，未登记的接口直接拒绝。

**批量上传**：一次 multipart 请求提交多个文件；批次超过 HTTP 请求体、文件数或批次总大小上限时整批拒绝，批次合法但其中单个文件超过单文件上限时保留逐文件成功/失败结果。业务字段 `businessModule` 最多 50 个 Unicode 字符且最多 200 字节，`remark` 最多 500 个 Unicode 字符且最多 2,000 字节，文件名最多 255 字节。

| 术语 | 同义词 / 禁用词 | 定义 |
| --- | --- | --- |
| 操作审计日志 | 操作日志（查询功能名称）、技术日志 | 对管理模块一次成功写操作的业务记录，标识执行者、请求及被操作资源。 |

## 边界与职责约定（已确认）

- **前端改动范围**: `react-admin/src/**`，不要新建顶层 `src/`。
- **后端改动范围**: `edge-collector-api/**`，不要在仓库根目录新建散落的 Go `src/`。
- **脚手架结构参考**: 只看单个子模块，不要批量扫目录。
- **架构参考**: 只在 `docs/` 或 `experience/` 下找摘要。

## 何时本文件需要更新

以下信号之一出现时，应触发本文件（和/或 `docs/adr/`）的更新，通常由 `/grill-with-docs` 完成：

- 业务对话里**首次出现**一个项目通用名词（例如"训问""任务单"）。
- 两个模块对同一个概念使用不同名字——应在这里趋向统一。
- 某次架构决策被沉淀下来（例如"为什么选 Zustand 而非 Redux"）——应写进 `docs/adr/`。

---

_本文件最初由 `/setup-matt-pocock-skills` 与 `/grill-with-docs` 协作初始化。后续每一条术语都应该有可追溯的业务来源。_
