# Task 2 Report: Ticket Content and P0 Pagination

## Status

完成 Task 2 的工单内容主链路与 P0 人工列表分页：

- 工单顶层评论、独立回复、附件、历史均使用严格 `page` / `page_size`
  envelope，默认 25、最大 100，并返回 `total`、`page`、`page_size`、
  `total_pages`。
- 顶层评论只查询 `parent_id IS NULL`，以服务端子查询返回当前可见范围内
  的 `reply_count`；回复通过独立、同权限边界的分页端点加载。
- 评论与回复按 `(created_at ASC, id ASC)`，附件与历史按
  `(created_at DESC, id DESC)` 稳定排序。
- 逾期与 SLA 违约工单在服务端分页，并分别使用
  `(due_date ASC, id ASC)` 与 `(created_at ASC, id ASC)`。
- 通知 Handler 严格拒绝 `page/page_size` 的 0、负数、非整数和 101，
  默认 25、最大 100；未改动用户现有的 `notification_service.go` 脏改。
- React Admin `getManyReference` 特殊路由现会透传 query；评论、回复、
  附件、历史均使用固定页控件，页面切换替换当前页数据，不累积无限 DOM。
- 会话面板覆盖加载、空、错误、重试状态；回复也有独立加载、空、错误和
  重试状态。
- Human OpenAPI 3.2.0 新增回复端点、严格分页参数和分页 envelope，并已
  重新生成 TypeScript 合约。
- 新增工单内容、历史、通知接收者范围所需的项目/对象/时间复合索引。

## Implementation

后端：

- `server/internal/handlers/ticket_content_handler.go`
  - 顶层评论分页、可见回复计数。
  - `GET /projects/{projectKey}/tickets/{ticketID}/comments/{commentID}/replies`。
  - 附件分页。
  - 统一严格分页解析和 page envelope。
- `server/internal/services/ticket_service.go`
  - 历史、逾期、SLA 违约列表服务端分页。
  - 请求者历史可见性在 `COUNT/OFFSET/LIMIT` 前进入 SQL，避免隐藏记录
    造成空页或泄漏总数。
- `server/internal/handlers/ticket_workflow_handler.go`
  - 历史、逾期、SLA Handler 严格分页。
- `server/internal/handlers/notification_handler.go`
  - 通知默认 25、最大 100，非法值 fail closed。
- `server/internal/database/migrate.go`
  - 评论/回复、附件、历史、通知复合分页索引。

前端与合约：

- `web/src/lib/dataProvider.ts`
  - `comments` / `ticket_history` 特殊引用路由透传分页和排序 query。
- `web/src/admin/tickets/TicketConversationPanel.tsx`
  - 评论、回复、附件的独立分页与重试。
- `web/src/admin/tickets/TicketShow.tsx`
  - 历史记录显示 React Admin 分页控件，支持 25/50/100。
- `server/internal/humanopenapi/openapi.json`
  - 回复端点、分页参数、分页响应字段。
- `web/src/lib/generated/human-api.ts`
  - 从 Human OpenAPI 重新生成。

## Test evidence

通过：

- `cd server && GOCACHE=/tmp/chronodesk-gocache GOTMPDIR=/tmp/chronodesk-gotmp go test ./...`
- `cd web && npm run check`
  - Human API 生成一致性
  - Human API 合约测试
  - audit explorer tests
  - TypeScript typecheck
  - ESLint
  - production dependency security audit
  - Vite production build
- `cd web && npx playwright test e2e/ticket-content.spec.ts --list`
  - 聚焦 Playwright 用例可发现并可编译。

新增聚焦覆盖：

- 150 条顶层评论：第 2 页固定 25 条、总页数 6、相同时间戳以 ID 稳定
  排序。
- 顶层评论不嵌入回复，`reply_count` 正确；3 条回复由独立端点分页。
- 评论与回复伪造跨项目行不会进入 total、页面或 `reply_count`。
- 150 条历史：第 2 页固定 25 条、跨项目隔离、稳定倒序。
- 150 条逾期与 SLA 违约工单：服务端第 2 页固定 25 条、稳定排序。
- 评论、附件和通知严格拒绝 page/page_size 的 0、负数、非整数与 101。
- E2E 合约断言评论/附件首屏请求携带 `page=1&page_size=25`。

未执行实时 Playwright 浏览器交互：最终验证时本机
`127.0.0.1:8080` 与 `127.0.0.1:5173` 均未启动；没有为了本任务改变
用户 Docker/开发服务状态。

## Concerns and follow-up

1. Agent control overview 的 Domain Events、Outbox、Policy Decisions 仍使用
   固定 `Limit(100)`，没有 opaque cursor。按主任务的 scope 指令，本提交
   不扩大到 Agent overview 协议/UI 重构；应在 Task 5 为三个 append-only
   流分别增加 `(timestamp,id)` opaque cursor、无伪 total 的前后页控件。
2. 通知查询的默认/最大/非法参数已在 Handler 闭合，但
   `server/internal/services/notification_service.go` 当前只按所选主列排序，
   未追加 ID tie-breaker。该文件有明确要求保留的用户脏改，本任务未触碰；
   后续应在合并该用户改动时将 GORM `OrderBy` 扩展为“所选列 + `id` 同方向”
   并增加同时间戳跨页测试。
3. 实时 Playwright 需在健康的 PostgreSQL、Redis、server 和 web 环境中
   复跑 `web/e2e/ticket-content.spec.ts`。

## Scope protection

未修改、暂存或提交用户现有的：

- `Makefile`
- `server/cmd/postgres-test/`
- `server/internal/services/notification_service.go`
- `server/internal/services/notification_service_escalation_test.go`

未修改 MCP `2026-07-28`、A2A wire `1.0` 或 Agent OpenAPI 协议版本。
