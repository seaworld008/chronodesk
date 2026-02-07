# Security And API Contract Fixes Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 一次性修复已确认的 6 个问题，按优先级先完成 P0/P1（安全风险），再完成 P2（前后端契约与路由不一致）。

**Architecture:** 后端以最小入侵方式修复安全问题：密码哈希升级为 `bcrypt` 且保留旧哈希兼容登录迁移；工单排序改为字段白名单；认证模块从统一配置注入 JWT 密钥并移除硬编码。OTP 备用码在验证成功后立即持久化。前后端契约问题通过“后端兼容 + 前端对齐”双保险处理，避免线上中断。

**Tech Stack:** Go 1.21 + Gin + GORM + PostgreSQL + React Admin + TypeScript + ESLint

---

### Task 1: 建立安全修复测试基线（P0/P1）

**Files:**
- Create: `server/internal/auth/password_test.go`
- Modify: `server/internal/services/ticket_service_filters_test.go`
- Create: `server/internal/auth/auth_service_otp_test.go`

**Step 1: 先写失败测试（密码哈希升级）**

```go
func TestHashPassword_UsesBcryptPrefix(t *testing.T) {
    svc := NewSimplePasswordService(8, "ignored")
    hash, err := svc.HashPassword("StrongPass1!")
    require.NoError(t, err)
    require.True(t, strings.HasPrefix(hash, "$2"))
}
```

**Step 2: 运行单测确认失败（RED）**

Run: `cd server && go test ./internal/auth -run TestHashPassword_UsesBcryptPrefix -v`
Expected: FAIL（当前实现输出 64 位 hex，不是 bcrypt）

**Step 3: 先写失败测试（排序字段注入防护）**

```go
func TestGetTickets_SortFieldInjectionFallsBackToCreatedAt(t *testing.T) {
    // 准备至少2条工单，注入非法 sort_by，断言返回顺序等同 created_at desc
}
```

**Step 4: 运行单测确认失败（RED）**

Run: `cd server && go test ./internal/services -run TestGetTickets_SortFieldInjectionFallsBackToCreatedAt -v`
Expected: FAIL（当前直接拼接 ORDER BY）

**Step 5: 先写失败测试（OTP 备用码持久化）**

```go
func TestVerifyOTP_BackupCodePersistsRemoval(t *testing.T) {
    // fake repo 返回启用OTP用户，backup_codes含目标码
    // 调用 VerifyOTP 后断言 userRepo.Update 被调用且备用码被移除
}
```

**Step 6: 运行单测确认失败（RED）**

Run: `cd server && go test ./internal/auth -run TestVerifyOTP_BackupCodePersistsRemoval -v`
Expected: FAIL（当前 VerifyOTP 成功路径未持久化）

**Step 7: 提交测试基线**

```bash
git add server/internal/auth/password_test.go server/internal/services/ticket_service_filters_test.go server/internal/auth/auth_service_otp_test.go
git commit -m "test: add failing tests for security fixes"
```

### Task 2: 修复 P0 密码哈希（bcrypt + 旧哈希兼容迁移）

**Files:**
- Modify: `server/internal/auth/password.go`
- Modify: `server/internal/auth/auth.go`
- Test: `server/internal/auth/password_test.go`
- Test: `server/internal/auth/auth_service_otp_test.go`（如需复用 fake repo）

**Step 1: 最小实现（GREEN）**

```go
// password.go
func (s *SimplePasswordService) HashPassword(password string) (string, error) {
    bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
    return string(bytes), err
}

func (s *SimplePasswordService) VerifyPassword(hashedPassword, password string) error {
    if strings.HasPrefix(hashedPassword, "$2") {
        return bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
    }
    // legacy sha256 fallback
    if s.legacyHash(password) == hashedPassword { return nil }
    return errors.New("password verification failed")
}
```

**Step 2: 登录成功后升级旧哈希（最小变更）**

在 `AuthService.Login` 的密码验证成功分支：
- 若当前 `PasswordHash` 为旧格式（非 `$2` 前缀）
- 重新 `HashPassword` 并 `userRepo.Update`
- 失败仅记录告警，不阻塞本次登录

**Step 3: 跑单测确认通过（GREEN）**

Run: `cd server && go test ./internal/auth -run 'TestHashPassword_UsesBcryptPrefix|TestVerifyPassword_LegacySHA256Compatible' -v`
Expected: PASS

**Step 4: 跑 auth 全量单测**

Run: `cd server && go test ./internal/auth -v`
Expected: PASS

**Step 5: 提交**

```bash
git add server/internal/auth/password.go server/internal/auth/auth.go server/internal/auth/password_test.go server/internal/auth/auth_service_otp_test.go
git commit -m "fix: migrate password hashing to bcrypt with legacy compatibility"
```

### Task 3: 修复 P1 排序注入风险（白名单排序）

**Files:**
- Modify: `server/internal/services/ticket_service.go`
- Test: `server/internal/services/ticket_service_filters_test.go`

**Step 1: 最小实现（GREEN）**

```go
var allowedSortFields = map[string]string{
    "created_at": "created_at",
    "updated_at": "updated_at",
    "priority": "priority",
    "status": "status",
    "due_date": "due_date",
    "ticket_number": "ticket_number",
}

func sanitizeSort(sortBy, sortOrder string) (string, string) {
   // sortBy 不在白名单 => created_at
   // sortOrder 非 asc/desc => DESC
}
```

将 `query.Order(fmt.Sprintf(...))` 替换为基于白名单的安全拼接（或 `clause.OrderByColumn`）。

**Step 2: 跑单测**

Run: `cd server && go test ./internal/services -run TestGetTickets_SortFieldInjectionFallsBackToCreatedAt -v`
Expected: PASS

**Step 3: 跑 services 全量**

Run: `cd server && go test ./internal/services -v`
Expected: PASS

**Step 4: 提交**

```bash
git add server/internal/services/ticket_service.go server/internal/services/ticket_service_filters_test.go
git commit -m "fix: whitelist ticket sort fields to prevent SQL injection"
```

### Task 4: 修复 P1 JWT 硬编码密钥问题（配置注入）

**Files:**
- Modify: `server/internal/config/config.go`
- Modify: `server/internal/auth/integration.go`
- Modify: `server/main.go`
- Modify: `server/.env.example`
- Test: `server/internal/config/config_test.go`

**Step 1: 先写失败测试（RED）**

```go
func TestLoadConfig_RequiresJWTRefreshSecretInProduction(t *testing.T) {
    // ENVIRONMENT=production 且 JWT_REFRESH_SECRET 使用默认占位值时应报错
}
```

**Step 2: 运行确认失败**

Run: `cd server && go test ./internal/config -run TestLoadConfig_RequiresJWTRefreshSecretInProduction -v`
Expected: FAIL

**Step 3: 最小实现（GREEN）**
- `JWTConfig` 增加 `RefreshSecret`
- `Load()` 读取 `JWT_REFRESH_SECRET`
- `Validate()` 在 production 下校验 `JWT_SECRET` 与 `JWT_REFRESH_SECRET` 都不是占位值
- `NewAuthModule` 改为接收配置并禁止内部硬编码默认密钥
- `main.go` 使用 `cfg` 注入认证模块

**Step 4: 跑配置与主链路测试**

Run: `cd server && go test ./internal/config -v`
Expected: PASS

**Step 5: 提交**

```bash
git add server/internal/config/config.go server/internal/config/config_test.go server/internal/auth/integration.go server/main.go server/.env.example
git commit -m "fix: remove hardcoded jwt secrets and enforce config in production"
```

### Task 5: 修复 P1 OTP 备用码持久化缺陷

**Files:**
- Modify: `server/internal/auth/auth.go`
- Test: `server/internal/auth/auth_service_otp_test.go`

**Step 1: 最小实现（GREEN）**
- `VerifyOTP` 分支中，当 `verifyBackupCode` 返回 true 后，立即 `userRepo.Update(ctx, user)`
- 若持久化失败，返回服务错误，不应当“验证成功但数据未落库”

**Step 2: 跑单测**

Run: `cd server && go test ./internal/auth -run TestVerifyOTP_BackupCodePersistsRemoval -v`
Expected: PASS

**Step 3: 跑 auth 全量**

Run: `cd server && go test ./internal/auth -v`
Expected: PASS

**Step 4: 提交**

```bash
git add server/internal/auth/auth.go server/internal/auth/auth_service_otp_test.go
git commit -m "fix: persist backup code consumption during otp verification"
```

### Task 6: 修复 P2 重置密码请求字段不一致

**Files:**
- Modify: `web/src/lib/authProvider.ts`
- Modify: `server/internal/auth/auth.go`
- Test: `server/internal/auth/handler_reset_password_test.go`

**Step 1: 先写失败测试（RED）**

```go
func TestResetPassword_AcceptsLegacyAndNewField(t *testing.T) {
   // payload: {token, new_password} 与 {token, password} 都应被解析
}
```

**Step 2: 最小实现（GREEN）**
- 前端改为发送 `new_password`
- 后端请求结构加兼容字段：`Password string 'json:"password,omitempty"'`
- 处理时优先 `new_password`，为空则回退 `password`

**Step 3: 跑验证**

Run: `cd server && go test ./internal/auth -run TestResetPassword_AcceptsLegacyAndNewField -v`
Expected: PASS

Run: `cd web && npm run lint`
Expected: PASS

**Step 4: 提交**

```bash
git add web/src/lib/authProvider.ts server/internal/auth/auth.go server/internal/auth/handler_reset_password_test.go
git commit -m "fix: align reset password payload contract"
```

### Task 7: 修复 P2 工单批量删除路由缺失

**Files:**
- Modify: `server/internal/handlers/ticket_handler.go`
- Modify: `server/internal/services/ticket_service.go`
- Modify: `server/main.go`
- Modify: `web/src/lib/dataProvider.ts`
- Test: `server/internal/services/ticket_service_delete_test.go`
- Test: `server/internal/handlers/ticket_handler_bulk_delete_test.go`

**Step 1: 先写失败测试（RED）**

```go
func TestBulkDeleteTickets_RemovesAllRequestedTickets(t *testing.T) {
   // 调用批量删除后，ID 集合均不可再查询
}
```

**Step 2: 最小实现（GREEN）**
- 后端新增 `POST /api/tickets/bulk-delete`（保持 JSON body 兼容）
- Handler 读取 `ids`，循环调用 `DeleteTicket`（带当前 user_id / role）
- 返回 `{deleted_ids, failed_ids}`
- 前端 `deleteMany('tickets')` 改为调用新路由

**Step 3: 跑验证**

Run: `cd server && go test ./internal/services -run TestBulkDeleteTickets_RemovesAllRequestedTickets -v`
Expected: PASS

Run: `cd server && go test ./internal/handlers -run TestBulkDeleteTickets -v`
Expected: PASS

Run: `cd web && npm run lint`
Expected: PASS

**Step 4: 提交**

```bash
git add server/internal/handlers/ticket_handler.go server/internal/services/ticket_service.go server/main.go web/src/lib/dataProvider.ts server/internal/services/ticket_service_delete_test.go server/internal/handlers/ticket_handler_bulk_delete_test.go
git commit -m "feat: add ticket bulk-delete endpoint and wire frontend"
```

### Task 8: 最终回归与验收

**Files:**
- Modify (if needed): `README.md`（仅当接口变化需要文档同步）

**Step 1: 后端全量验证**

Run: `cd server && go test ./...`
Expected: PASS

**Step 2: 静态检查**

Run: `cd server && go vet ./...`
Expected: PASS

Run: `cd web && npm run lint`
Expected: PASS

**Step 3: 冒烟验证（可选但建议）**

Run: `./test_integration.sh`
Expected: 关键接口健康检查通过

**Step 4: 验收清单**
- 密码新注册/改密使用 bcrypt
- 旧 SHA256 密码用户首次成功登录后完成无感迁移
- 工单排序非法参数不再进入 SQL
- 认证模块不再包含硬编码 JWT 密钥
- OTP 备用码每次使用后落库并失效
- 重置密码新旧字段均可用（前端已统一 new_password）
- 批量删除工单前后端链路可用

**Step 5: 汇总提交**

```bash
git log --oneline -n 8
```

