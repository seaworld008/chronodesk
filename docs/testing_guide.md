# 测试与质量控制指南

本指南适用于当前 Go 1.26.5、React 19、MCP `2026-07-28`、A2A `1.0`
和 OpenAPI 3.2 代码。除特别说明外，命令均在仓库根目录执行。

发布范围、236 条功能/安全断言和自动化层级以
[ChronoDesk 全功能专业测试用例](testing/CHRONODESK_COMPREHENSIVE_TEST_CASES_2026-07-29.md)
为准。新增功能必须先补用例 ID，再补自动化和实现。

236 条用例与具体 Go/Pytest/Playwright/Chrome/故障注入证据的唯一机器台账是
[`CASE_EVIDENCE_MANIFEST.tsv`](testing/CASE_EVIDENCE_MANIFEST.tsv)。提交前执行：

```bash
make test-python-static
```

校验器会拒绝缺失、重复、未知 Case ID，以及不存在的测试文件/测试符号；证据状态
不得写成“缺口”或“部分”。Manifest 的 `execution_record=not_recorded` 表示它只
描述证据入口，不代表测试已经执行或通过。人工、Chrome 和故障注入的复现与留痕
要求见
[`RELEASE_EVIDENCE_PROCEDURES.md`](testing/RELEASE_EVIDENCE_PROCEDURES.md)。

## 1. 环境准备

```bash
make install-deps
cp server/.env.example server/.env
```

`make install-deps` 会在仓库根目录创建或复用已忽略的 `.venv`，并把
`server/requirements-test.txt` 完整安装到其中。所有 Make Python 门禁都使用
`.venv/bin/python`，不会写入系统 Python；如需指定创建虚拟环境的解释器，可运行
`make BOOTSTRAP_PYTHON=python3.12 install-deps`（`PYTHON=...` 仍是兼容别名）。

需要端到端或浏览器测试时，先启动完整环境：

```bash
make dev
curl --fail http://localhost:8081/healthz
```

`/healthz` 必须返回 PostgreSQL 与 Redis 均为 `ok`。使用云 PostgreSQL/Redis 时，将连接信息放入本地 `server/.env` 或安全注入环境变量，不要写入测试报告、命令输出或 Git。

首次使用数据库时先执行结构迁移与当前格式验证：

```bash
make db-migrate
make credential-validate
```

验证失败时不得用维护工具摄入明文。密钥轮换使用
`make credential-rotate`；不支持的密码哈希使用
`make credential-quarantine` 隔离，完成正常密码重置后再显式重新启用。

## 2. 提交前标准门禁

```bash
make verify
```

`make verify` 当前依次执行：

1. Go 格式、完整测试、Vet 与 `govulncheck`
2. Web TypeScript、ESLint、生产依赖安全策略与构建
3. Redocly 2.41.1 与 Spectral 6.16.2 的 OpenAPI 3.2 严格校验
4. `chronodesk`、数据库迁移、凭据维护命令与 Web 生产资源构建

涉及平台/项目角色、Human 会话、路由边界或迁移时，还必须执行对应的 Go 契约与
迁移测试，并确认：平台角色没有隐式项目访问，项目角色必须来自 active
Membership，Membership 撤销立即失效，过期平台角色声明返回 `stale_token`，以及
`20260730_platform_roles_v1_cutover` 的前置 checkpoint、映射和不匹配路径 fail
closed。不要把 `make verify` 的格式/构建结果写成已完成真实 PostgreSQL 切换的证据。

`make build` 输出 `server/bin/chronodesk`、
`server/bin/chronodesk-migrate` 和 `web/dist/`。

## 3. 后端与安全回归

```bash
make test-server
make test-race
```

涉及认证、领域事务、Outbox、调度或并发控制时，不能只运行单个包；修复后需要重新执行完整 `make test-server`。

对真实 Redis 的跨实例租约、并发、限流和循环检测执行显式集成测试：

```bash
cd server
CHRONODESK_REDIS_INTEGRATION=1 go test ./internal/services \
  -run 'Test(RedisAgentExecutionGuard|SchedulerRedisLease)Integration' \
  -count=1
```

该测试从 `server/.env` 读取当前 `REDIS_URL` 或 Upstash REST 配置，并使用随机测试键，不应改用旧凭据。

## 4. 前端质量与浏览器测试

前端标准门禁：

```bash
make test-web
make build-web
```

`make test-web` 已包含 TypeScript、ESLint 零 warning 和生产依赖安全策略检查，不再把历史 Lint 问题作为例外。

启动 API 与 Web 后，可执行现有 Playwright 套件：

```bash
make e2e
```

CI 的完整 Smoke 只在 PR 最新合并结果上执行，并由严格 `main` 分支保护阻止未验证
代码进入默认分支；合并后不再重复运行同一源码树。一次性回环 CI 使用 3 个文件级
worker，本地、共享与远端环境仍固定单 worker。流水线触发器、保护规则与并发验证
规程见 [CI 流水线与分支保护](testing/CI_PIPELINE.md)。

重点检查：

- 登录、工单生命周期、公开/内部评论和附件。
- 用户、通知、自动化、系统设置、邮件、Webhook 与 Agent 控制中心。
- 平台治理、项目切换与跨项目工作台：没有 Membership 的平台角色不能读取项目
  Ticket，撤销 Membership 后项目入口和工作台都立即拒绝或移除该项目。
- Human Web 从 `/human-openapi.json` 使用的路由、角色和 DTO 与 Agent
  `/openapi.yaml` 保持独立，不将一个契约的未覆盖路径当成另一个的公开能力。
- 页面提示和错误信息均为中文。
- 工单及其他列表不自动换行，长内容省略并可查看全文。
- 表头列宽可通过鼠标或键盘调整，刷新后仍保持；操作列固定，横向滚动不遮挡侧栏。

## 5. Agent 协议与机器契约

运行 MCP、A2A、Agent 领域适配和 OpenAPI 契约测试：

```bash
cd server
go test ./internal/mcp ./internal/a2a ./internal/agentplatform ./internal/openapi -count=1
```

OpenAPI 独立门禁：

```bash
make openapi-lint
```

验收应确认：

- MCP 只接受 `2026-07-28`，发现结果包含 OAuth Client Credentials 扩展，无旧 Session/GET SSE 兼容路径。
- A2A 只接受 `1.0` PascalCase JSON-RPC 方法，Agent Card、SSE 游标、Task、Artifact 与 Push 契约一致。
- `/api/v2` OAuth token 严格校验 REST audience 和单一 `project_key`，不能访问 `/mcp` 或 `/a2a/v1`。
- 相同 `Idempotency-Key` 只产生一次副作用；错误请求体复用同一 key 返回冲突。
- `ETag` / `If-Match` 和有效租约共同防止两个 Agent 覆盖同一工单。
- CloudEvent 可按 `(source, id)` 去重，Outbox 可重试/回放，事件 cursor 可恢复。

MCP Inspector 2 的最新配置和命令见 [MCP 2026-07-28 接入说明](reference/MCP_2026_07_28.md)；完整 Schema 以运行时 `/openapi.yaml` 为准。

## 6. API/Python 冒烟与集成

```bash
make smoke
cd server && go test ./internal/services -run 'Notification|Webhook' -count=1
```

`make smoke` 覆盖认证、工单生命周期、通知收件人隔离、自动化和系统配置，
并生成 `server/reports/smoke.html`。黑盒测试不伪造 Python 对 Go 源码的覆盖率。

Pytest 默认访问 `http://localhost:8081/api`；可通过 `TEST_API_BASE_URL`、`TEST_HEALTHCHECK_URL`、`TEST_ADMIN_EMAIL` 和 `TEST_ADMIN_PASSWORD` 指向受控测试环境。

## 7. 发布前检查清单

- [ ] PostgreSQL/Redis 健康检查通过，结构迁移与密钥验证完成。
- [ ] 涉及角色切换时，已在备份/隔离环境复核
  `20260730_platform_roles_v1_cutover` checkpoint、旧列删除和 active Membership
  映射；异常来源保持 fail closed。
- [ ] 平台/项目角色矩阵、`stale_token`、Membership 撤销及工作台范围的负向授权测试通过。
- [ ] 236 Case Evidence Manifest 校验通过，且当次报告记录所有发布规程结果。
- [ ] `make verify` 通过。
- [ ] Go race、vet 和真实 Redis 集成测试通过。
- [ ] MCP Inspector、A2A 生命周期和 OpenAPI 契约通过。
- [ ] Pytest 全套及通知/Webhook 专项通过。
- [ ] Playwright 覆盖主要中文页面，表格、列宽、侧栏和响应式布局无回归。
- [ ] Chrome 插件逐页实测控制台、网络、中文提示、真实增删改和页面完整性。
- [ ] PR 的 Checks 列出实际执行命令与结果，不包含凭据、数据库连接串或 token。

当前验收结果记录在
[P1 平台角色与项目角色切换发布证据](testing/P1_PLATFORM_ROLES_RELEASE_EVIDENCE_2026-07-31.md)；
[Agent 原生化完整测试报告](testing/CHRONODESK_AGENT_NATIVE_FULL_TEST_REPORT_2026-07-30.md)
保留为前一候选的历史证据。
