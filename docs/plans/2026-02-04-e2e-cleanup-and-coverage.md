# E2E Cleanup + Coverage Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 清理 E2E 测试数据（保留默认账号与角色账号），并补充系统设置/通知/工单流程的 E2E 覆盖，确保前后端逻辑一致。

**Architecture:** 在后端补齐“通知删除”能力以支持清理；前端 E2E 统一用 API 辅助创建/清理测试数据，并使用 UI 验证关键链路。

**Tech Stack:** Go (Gin/Gorm), React-Admin + Vite, Playwright, SQLite 测试数据库（服务层测试）。

---

### Task 1: 新增通知删除能力（管理员）

**Files:**
- Create: `server/internal/services/notification_service_delete_test.go`
- Modify: `server/internal/services/notification_service.go`
- Modify: `server/internal/handlers/notification_handler.go`
- Modify: `server/main.go`

**Step 1: Write the failing test**

```go
func TestDeleteNotification(t *testing.T) {
    db := openTestDB(t)
    if err := db.AutoMigrate(&models.User{}, &models.Notification{}); err != nil {
        t.Fatalf("migrate: %v", err)
    }

    user := models.User{Username: "e2e", Email: "e2e@example.com", PasswordHash: "hash", Role: models.RoleAdmin, Status: models.UserStatusActive}
    if err := db.Create(&user).Error; err != nil {
        t.Fatalf("create user: %v", err)
    }

    notification := models.Notification{
        Type: models.NotificationTypeSystemAlert,
        Title: "E2E-通知删除",
        Content: "E2E-通知删除",
        Priority: models.NotificationPriorityNormal,
        Channel: models.NotificationChannelInApp,
        RecipientID: user.ID,
    }
    if err := db.Create(&notification).Error; err != nil {
        t.Fatalf("create notification: %v", err)
    }

    service := NewNotificationService(db)
    if err := service.DeleteNotification(context.Background(), notification.ID); err != nil {
        t.Fatalf("delete notification: %v", err)
    }

    var count int64
    if err := db.Model(&models.Notification{}).Where("id = ?", notification.ID).Count(&count).Error; err != nil {
        t.Fatalf("count: %v", err)
    }
    if count != 0 {
        t.Fatalf("expected notification deleted")
    }
}
```

**Step 2: Run test to verify it fails**

Run: `cd server && go test ./internal/services -run TestDeleteNotification -v`
Expected: FAIL with "DeleteNotification undefined" or compile error

**Step 3: Write minimal implementation**

- 在 `NotificationServiceInterface` 增加 `DeleteNotification(ctx, id)`
- 在 `NotificationService` 实现删除逻辑（找不到返回错误）
- 在 `notification_handler.go` 增加 `DeleteNotification` 处理器（管理员接口）
- 在 `main.go` 增加路由 `admin.DELETE("/notifications/:id", ...)`

**Step 4: Run test to verify it passes**

Run: `cd server && go test ./internal/services -run TestDeleteNotification -v`
Expected: PASS

**Step 5: Commit**

```bash
git add server/internal/services/notification_service.go server/internal/handlers/notification_handler.go server/main.go server/internal/services/notification_service_delete_test.go
git commit -m "feat: add admin notification delete"
```

---

### Task 2: 统一 E2E 测试数据辅助与清理逻辑

**Files:**
- Create: `web/e2e/helpers/api.ts`
- Create: `web/e2e/helpers/testData.ts`
- Modify: `web/e2e/automation-rules.spec.ts`
- Modify: `web/e2e/email-settings.spec.ts`
- Modify: `web/e2e/tickets.spec.ts`

**Step 1: Write the failing test**

- 在 `automation-rules.spec.ts` 中引入 `helpers/testData` 的 `createAutomationRule` 与 `cleanupE2EData`，先使用它们（模块尚不存在）。

**Step 2: Run test to verify it fails**

Run: `cd web && npx playwright test e2e/automation-rules.spec.ts --project=chromium`
Expected: FAIL with module not found

**Step 3: Write minimal implementation**

- `helpers/api.ts`: 提供 `loginAsAdmin`, `apiRequest`（带 token）。
- `helpers/testData.ts`:
  - 常量 `E2E_PREFIX = "E2E-"`
  - `ensureRoleAccounts()`：保留并补齐账号
    - 超级管理员：`admin@example.com`（不修改）
    - 管理员：`admin.manager@example.com`（RoleAdmin）
    - 客服：`agent@example.com`（RoleAgent）
    - 访客：`customer@example.com`（RoleCustomer）
  - `createAutomationRule()` / `createNotification()` / `createTicket()`
  - `cleanupE2EData()`：删除前缀为 `E2E-` 的
    - 自动化规则 / 工单 / 通知
    - 测试用户（邮箱包含 `e2e_` 或 `test_` 且不在保留名单内）
    - 邮件配置回滚：`email_verification_enabled=false` + 清空 SMTP 字段

**Step 4: Run test to verify it passes**

Run: `cd web && npx playwright test e2e/automation-rules.spec.ts --project=chromium`
Expected: PASS

**Step 5: Commit**

```bash
git add web/e2e/helpers/api.ts web/e2e/helpers/testData.ts web/e2e/automation-rules.spec.ts web/e2e/email-settings.spec.ts web/e2e/tickets.spec.ts
git commit -m "test: add e2e data helpers and cleanup"
```

---

### Task 3: 新增系统设置 / 通知 / 工单完整链路 E2E

**Files:**
- Create: `web/e2e/system-settings.spec.ts`
- Create: `web/e2e/notifications.spec.ts`
- Create: `web/e2e/ticket-workflow.spec.ts`

**Step 1: Write the failing tests**

- System settings：修改 `security.password_min_length` -> 保存 -> 刷新验证 -> 回滚。
- Notifications：API 创建通知 -> UI 搜索可见 -> 删除。
- Ticket workflow：创建工单 -> 编辑分配 -> 变更状态至 resolved/closed -> 删除。

**Step 2: Run tests to verify they fail**

Run: `cd web && npx playwright test e2e/system-settings.spec.ts e2e/notifications.spec.ts e2e/ticket-workflow.spec.ts --project=chromium`
Expected: FAIL (缺少实现或接口)

**Step 3: Write minimal implementation**

- 复用 `helpers/testData.ts` 的 API/清理方法
- 确保测试数据带 `E2E-` 前缀，并在 `afterAll` 清理
- System settings：定位行并读取/修改数值，保存与回滚
- Notifications：用管理员 API 创建通知，UI 搜索验证，调用删除
- Ticket workflow：UI 操作创建、编辑、状态流转、删除

**Step 4: Run tests to verify they pass**

Run: `cd web && npx playwright test e2e/system-settings.spec.ts e2e/notifications.spec.ts e2e/ticket-workflow.spec.ts --project=chromium`
Expected: PASS

**Step 5: Commit**

```bash
git add web/e2e/system-settings.spec.ts web/e2e/notifications.spec.ts web/e2e/ticket-workflow.spec.ts
git commit -m "test: cover settings notifications and ticket workflow"
```

---

### Task 4: 全量验证

**Step 1: Go tests**
Run: `cd server && go test ./...`
Expected: PASS

**Step 2: Web lint**
Run: `cd web && npm run lint`
Expected: PASS

**Step 3: Playwright**
Run: `cd web && npx playwright test --project=chromium`
Expected: PASS

**Step 4: Commit any fixes**

```bash
git add -A
git commit -m "chore: fix e2e regressions" || true
```
