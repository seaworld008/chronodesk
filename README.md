# ChronoDesk

ChronoDesk 是面向企业客服、运营团队和外部 AI Agent 的工单自动化平台。系统同时提供中文管理后台、稳定的 Agent REST API、MCP 与 A2A 原生入口，并以统一领域服务保证权限、并发控制、事件和审计语义一致。

## 当前技术基线

- 后端：Go 1.26、Gin、GORM、PostgreSQL 18、Redis 8。
- 前端：React 19、React Admin 5.15.1、MUI 7.3.11、React Router 7.18.2、Vite 8。
- Agent 协议：MCP `2026-07-28`、A2A 官方发布 `v1.0.1`（线协议 `1.0`）、
  CloudEvents `1.0`、OpenAPI `3.2.0`，只支持当前版本。

## 核心能力

- 工单全生命周期、对象级权限、SLA、自动化规则、通知、Webhook、WebSocket 与审计。
- 公开/内部评论，以及带下载授权、SHA-256 哈希和病毒扫描状态的附件。
- 独立 `service_principal`、短期 OAuth 令牌、最小 scope、策略、限流、熔断和完整操作审计。
- `Idempotency-Key`、`ETag` / `If-Match`、工单租约、CloudEvents 领域事件与可靠 Outbox。
- MCP 无状态 Streamable HTTP 和 OAuth Client Credentials 扩展；A2A
  `v1.0.1` / wire `1.0` JSON-RPC、SSE、Task、Artifact 与 Push。
- 全中文企业后台；列表默认单行省略、悬停显示全文、横向滚动，列宽可拖动并持久化。

## 快速开始

```bash
make install-deps
cp server/.env.example server/.env
make dev
```

- 管理后台：`http://localhost:3000`
- 健康检查：`http://localhost:8081/healthz`
- OpenAPI：`http://localhost:8081/openapi.yaml`
- Agent REST：`http://localhost:8081/api/v1`
- 人类后台 REST：`http://localhost:8081/api`
- MCP：`http://localhost:8081/mcp`
- A2A：`http://localhost:8081/a2a/v1`

首次连接既有数据库时，先执行结构与密钥迁移：

```bash
make db-migrate
cd server && go run ./cmd/secret-migrate
cd server && go run ./cmd/secret-migrate -validate-only
```

## 常用命令

```bash
make server-dev      # 启动后端
make web-dev         # 启动前端
make build           # 构建后端与前端
make test            # 后端、前端与 OpenAPI 质量门禁
make smoke           # Python API 冒烟套件
make openapi-lint    # OpenAPI 3.2 严格校验
```

## 文档

- [项目权威手册](docs/PROJECT_MANUAL.md)
- [文档索引](docs/README.md)
- [API 使用说明](docs/reference/API_DOCUMENTATION.md)
- [测试与质量控制指南](docs/testing_guide.md)
- [Agent 原生化完整测试报告](docs/testing/CHRONODESK_AGENT_NATIVE_FULL_TEST_REPORT_2026-07-29.md)

## 目录

```text
server/   Go API、领域服务与 Agent 协议适配器
web/      React Admin 中文企业管理端
docs/     项目手册、协议参考、规划与测试记录
```

根目录的 `AGENTS.md` 和 `CLAUDE.md` 用于 AI Agent 协作约束；业务文档统一维护在 `docs/`。
