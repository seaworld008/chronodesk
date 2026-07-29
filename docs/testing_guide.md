# 测试与质量控制指南

本指南适用于当前 Go 1.26.5、React 19、MCP `2026-07-28`、A2A `1.0`
和 OpenAPI 3.2 代码。除特别说明外，命令均在仓库根目录执行。

## 1. 环境准备

```bash
make install-deps
cp server/.env.example server/.env
```

需要端到端或浏览器测试时，先启动完整环境：

```bash
make dev
curl --fail http://localhost:8081/healthz
```

`/healthz` 必须返回 PostgreSQL 与 Redis 均为 `ok`。使用云 PostgreSQL/Redis 时，将连接信息放入本地 `server/.env` 或安全注入环境变量，不要写入测试报告、命令输出或 Git。

首次使用既有数据库时先执行：

```bash
make db-migrate
cd server && go run ./cmd/secret-migrate
cd server && go run ./cmd/secret-migrate -validate-only
```

## 2. 提交前标准门禁

```bash
make verify
```

`make verify` 当前依次执行：

1. Go 格式、完整测试、Vet 与 `govulncheck`
2. Web TypeScript、ESLint、生产依赖安全策略与构建
3. Redocly 2.41.1 与 Spectral 6.16.2 的 OpenAPI 3.2 严格校验
4. `chronodesk`、迁移命令与 Web 生产资源构建

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

重点检查：

- 登录、工单生命周期、公开/内部评论和附件。
- 用户、通知、自动化、系统设置、邮件、Webhook 与 Agent 控制中心。
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
- `/api/v1` OAuth token 严格校验 REST audience，不能访问 `/mcp` 或 `/a2a/v1`。
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
- [ ] `make verify` 通过。
- [ ] Go race、vet 和真实 Redis 集成测试通过。
- [ ] MCP Inspector、A2A 生命周期和 OpenAPI 契约通过。
- [ ] Pytest 全套及通知/Webhook 专项通过。
- [ ] Playwright 覆盖主要中文页面，表格、列宽、侧栏和响应式布局无回归。
- [ ] PR 的 Checks 列出实际执行命令与结果，不包含凭据、数据库连接串或 token。

完整验收结果记录在 [Agent 原生化完整测试报告](testing/CHRONODESK_AGENT_NATIVE_FULL_TEST_REPORT_2026-07-29.md)。
