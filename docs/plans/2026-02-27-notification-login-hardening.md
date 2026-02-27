# Notification and Login Hardening Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 修复登录历史、会话删除、WebSocket 已读同步与 Webhook 环境记录的行为缺陷，确保接口与实时推送一致且可测试。

**Architecture:** 以服务层为行为中心：用户会话删除逻辑下沉到 `UserService`，通知已读与未读数同步通过 websocket 包内统一 hook 入口处理。排序安全通过服务层白名单统一约束，避免 handler 漏网参数导致拼接风险。Webhook 环境字段由 `NotificationService` 初始化时读取运行环境，不再硬编码。

**Tech Stack:** Go, Gin, Gorm, SQLite(in-memory tests), WebSocket hub, GitHub Actions。

---

### Task 1: 登录历史排序安全化 + 会话删除真实生效

**Files:**
- Modify: `server/internal/services/user_service.go`
- Modify: `server/internal/handlers/user_handler.go`
- Test: `server/internal/services/user_service_login_history_test.go`

**Step 1: 写失败测试（排序白名单）**
- 在 `user_service_login_history_test.go` 增加非法 `OrderBy/Order` 输入场景。
- 断言：服务不报错且回退默认排序规则。

**Step 2: 写失败测试（删除会话）**
- 增加删除会话后 `is_active=false`、`logout_time` 被写入的断言。
- 增加删除他人会话返回 not found 的断言。

**Step 3: 最小实现**
- 为 `GetLoginHistory` 增加排序字段和方向白名单函数。
- 新增 `DeleteLoginSession(ctx, userID, historyID)` 服务方法。
- `DeleteLoginSession` handler 调用服务层，并按 `404/500` 分支返回。

**Step 4: 运行定向测试**
- Run: `cd server && go test ./internal/services -run 'TestGetLoginHistory|TestDeleteLoginSession' -v`

### Task 2: WebSocket mark_read 真实落库与未读数同步

**Files:**
- Modify: `server/internal/websocket/integration.go`
- Modify: `server/internal/websocket/client.go`
- Modify: `server/internal/handlers/notification_handler.go`
- Modify: `server/main.go`
- Test: `server/internal/websocket/integration_test.go`

**Step 1: 写失败测试（单条已读推送真实未读数）**
- 构造在线 client，调用 hook。
- 断言收到 `unread_count` 且 count 为传入值（非固定 0）。

**Step 2: 写失败测试（WS mark_read 调用业务处理）**
- 注册全局已读处理器，调用 `client.handleMarkRead`。
- 断言处理器被调用，并收到未读数推送消息。

**Step 3: 最小实现**
- 增加 websocket 级别的“已读处理器”注册与调用入口。
- 在 `main.go` 注入处理器：执行 `MarkAsRead` + `GetUnreadCount`。
- `client.handleMarkRead` 调用该入口，不再仅日志。
- `notification_handler.MarkAsRead` 成功后查询真实未读数并推送。

**Step 4: 运行定向测试**
- Run: `cd server && go test ./internal/websocket -v`

### Task 3: Webhook 日志环境字段修复

**Files:**
- Modify: `server/internal/services/notification_service.go`
- Test: `server/internal/services/notification_service_environment_test.go`

**Step 1: 写失败测试**
- `ENVIRONMENT=production` 初始化服务后触发 `sendWebhook`。
- 断言保存的 `webhook_logs.environment == production`。

**Step 2: 最小实现**
- `NotificationService` 增加 `environment` 字段。
- `NewNotificationService` 从 `ENVIRONMENT` 读取并设置默认值。
- `sendWebhook` 写日志时使用该字段。

**Step 3: 运行定向测试**
- Run: `cd server && go test ./internal/services -run TestSendWebhookLogEnvironment -v`

### Task 4: 全量验证 + 同步

**Files:**
- Verify only

**Step 1: 后端回归**
- Run: `cd server && go test ./...`

**Step 2: 前端回归**
- Run: `cd web && npm run lint && npm run build`

**Step 3: 提交与推送**
- Run: `git add ... && git commit -m 'fix: harden login history and notification flows'`
- Run: `git push origin main`

**Step 4: Actions 验证**
- 在 GitHub Actions 查看最新 run 状态，记录最终结论。
