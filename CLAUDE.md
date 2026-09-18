# CLAUDE.md

请使用中文交流和思考！

## 项目结构

```
project/
├── react-admin/   # 前端项目（React），直接在此开发
├── edge-collector-api/   # 后端项目（Go），直接在此开发
```

## 上下文管理规则

**避免全量读取脚手架项目，按需读取：**

1. **不要递归列出** `react-admin/` 或 `edge-collector-api/` 全部文件
2. **用 Explore agent 搜索**，而不是直接 Read 整个目录
3. **一次只读一个子模块**（如 `src/components/UserTable.tsx`），不要批量读整个目录
4. **需要架构参考时**，只读 `docs/` 或 `experience/` 下的摘要文件

## 工作模式

- **直接在脚手架项目内开发**，不新建 `src/` 目录
- push 已禁用，可以放心在 `react-admin/` 和 `edge-collector-api/` 中改动代码
- 参考脚手架结构时，按需读取具体文件，不要批量扫描

## Taskfile 开发与启动入口

根目录 `Taskfile.yml` 是项目日常开发、启动和验证的统一入口。Agent 优先使用这些任务，不要在已有等价任务时自行拼接目录切换、环境变量或启动命令。

- 后端本地配置使用 `edge-collector-api/configs/config.dev.yaml`；数据库和 JWT 配置放在该文件中，不另建根目录 `.env.local` 作为后端运行配置。
- `task db:migrate`：执行全部 Goose schema 和 seed migration；首次启动、数据库重建或修改 migration 后执行。
- `task api`：启动 Go API，默认监听 `:8099`。
- `task web`：启动 React 开发服务器，默认监听 `:5173`。
- `task dev`：先执行数据库 migration，再并行启动 API 和前端；适合全新环境或需要确保数据库已更新时使用。
- `task test`：执行后端单元测试。
- `task backend:check`：执行后端测试和 `go vet`。
- `task db:integration:postgres`：执行 PostgreSQL 数据库集成契约，需要 Docker。
- `task db:integration:sqlite`：执行 SQLite 数据库集成契约。
- `task frontend:lint`：执行前端 ESLint。
- `task frontend:build`：执行前端 TypeScript/Vite 构建。
- `task frontend:browser-test`：执行前端浏览器回归测试，不启动 API、Docker 或模拟器。
- `task check`：执行后端检查、前端 lint/build 和浏览器回归测试。
- `task db:check`：执行后端检查以及 PostgreSQL、SQLite 两套数据库集成契约。

日常开发优先使用 `task dev`（SQLite 场景使用 `task dev:sqlite`）。Agent 验证时，后端改动执行 `task backend:check`，前端改动执行 `task frontend:lint`、`task frontend:build` 或 `task frontend:browser-test`，跨后端和前端改动执行 `task check`，涉及数据库兼容性时执行 `task db:check`。专项 E2E、模拟器和 MQTT 验收只在改动范围涉及对应链路时运行。长时间运行的任务必须配合健康检查或明确的超时与清理；API 使用 `/health` 检查存活、`/ready` 检查数据库就绪。

**Tradeoff:** These guidelines bias toward caution over speed. For trivial tasks, use judgment.

## 时间处理契约

跨 API 边界的时间表示为 UTC instant：后端和数据库保存 UTC，JSON 使用带时区的 RFC3339 字符串（例如 `2026-09-14T05:43:46.123Z`）。前端接收到这类值后先解析为时间 instant，再按产品规定的展示时区格式化；当前管理后台展示时区为 `Asia/Shanghai`。

- 所有时间展示统一经过 `react-admin/src/lib/datetime.ts` 的格式化函数。
- `formatDateTime` 和 `formatDateOnly` 必须通过日期解析与 `Intl.DateTimeFormat` 完成时区转换，并输出现有页面约定的格式。
- 时间字段新增、API DTO 修改或时间格式化逻辑修改时，必须覆盖 UTC 转 `Asia/Shanghai`、跨午夜和非法输入回退场景，并运行 `task frontend:datetime-test`。
- API 时间字段保持 RFC3339 时区信息；时间展示代码使用解析后的值，不通过 `replace`、`slice` 等字符串操作模拟时区转换。
- 纯日期字段（仅 `YYYY-MM-DD`）按日期处理，不添加时区；它与带时区的时间 instant 是不同的数据类型。

## 1. Think Before Coding

**Don't assume. Don't hide confusion. Surface tradeoffs.**

Before implementing:
- State your assumptions explicitly. If uncertain, ask.
- If multiple interpretations exist, present them - don't pick silently.
- If a simpler approach exists, say so. Push back when warranted.
- If something is unclear, stop. Name what's confusing. Ask.

## 2. Simplicity First

**Minimum code that solves the problem. Nothing speculative.**

- No features beyond what was asked.
- No abstractions for single-use code.
- No "flexibility" or "configurability" that wasn't requested.
- No error handling for impossible scenarios.
- If you write 200 lines and it could be 50, rewrite it.

Ask yourself: "Would a senior engineer say this is overcomplicated?" If yes, simplify.

## 3. Surgical Changes

**Touch only what you must. Clean up only your own mess.**

When editing existing code:
- Don't "improve" adjacent code, comments, or formatting.
- Don't refactor things that aren't broken.
- Match existing style, even if you'd do it differently.
- If you notice unrelated dead code, mention it - don't delete it.

When your changes create orphans:
- Remove imports/variables/functions that YOUR changes made unused.
- Don't remove pre-existing dead code unless asked.

The test: Every changed line should trace directly to the user's request.

## 4. Goal-Driven Execution

**Define success criteria. Loop until verified.**

Transform tasks into verifiable goals:
- "Add validation" → "Write tests for invalid inputs, then make them pass"
- "Fix the bug" → "Write a test that reproduces it, then make it pass"
- "Refactor X" → "Ensure tests pass before and after"

For multi-step tasks, state a brief plan:
```
1. [Step] → verify: [check]
2. [Step] → verify: [check]
3. [Step] → verify: [check]
```

Strong success criteria let you loop independently. Weak criteria ("make it work") require constant clarification.

---

---

**These guidelines are working if:** fewer unnecessary changes in diffs, fewer rewrites due to overcomplication, and clarifying questions come before implementation rather than after mistakes.

---

## Agent skills

### Issue tracker

工作项存放在本仓库的 GitHub Issues，skills 通过 `gh` CLI 读写。详见 `docs/agents/issue-tracker.md`。

### Triage labels

triage 流转使用 5 个默认标签（needs-triage / needs-info / ready-for-agent / ready-for-human / wontfix）。详见 `docs/agents/triage-labels.md`。

### Domain docs

单上下文：根目录 `CONTEXT.md` + `docs/adr/`。详见 `docs/agents/domain.md`。
