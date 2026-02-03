# Optimization & Consistency Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 统一后端中间件与 CORS、增强统计与过滤性能、稳定 WebSocket/通知链路、并清理前端响应解析与无用页面，保持前后端功能一致。

**Architecture:** 后端引入可配置中间件链与 WebSocket Origin 策略；统计使用聚合 SQL + 可选缓存；通知发送加入并发池与退避重试；数据库新增 `pg_trgm`/`GIN` 索引；前端 `dataProvider` 收敛响应解析并清理无用入口。

**Tech Stack:** Go (Gin/GORM), PostgreSQL, Redis, React-Admin, Vite, ESLint

---

### Task 1: 为工单筛选补齐 SLA/逾期/未分配过滤（后端）

**Files:**
- Modify: `server/internal/services/ticket_service.go`
- Modify: `server/internal/handlers/ticket_handler.go`
- Create: `server/internal/services/ticket_service_filters_test.go`

**Step 1: Write the failing test**
```go
func TestGetTicketsFilters_SLAOverdueUnassigned(t *testing.T) {
    db := setupTestDB(t)
    seedTicketsForFilters(t, db)

    svc := NewTicketService(db)

    // SLA breached filter
    tickets, total, err := svc.GetTickets(context.Background(), TicketFilters{
        SLABreached: boolPtr(true),
    })
    require.NoError(t, err)
    require.Equal(t, int64(1), total)
    require.Len(t, tickets, 1)

    // Overdue filter
    tickets, total, err = svc.GetTickets(context.Background(), TicketFilters{
        IsOverdue: boolPtr(true),
    })
    require.NoError(t, err)
    require.Equal(t, int64(1), total)
    require.Len(t, tickets, 1)

    // Unassigned filter
    tickets, total, err = svc.GetTickets(context.Background(), TicketFilters{
        Unassigned: boolPtr(true),
    })
    require.NoError(t, err)
    require.Equal(t, int64(1), total)
    require.Len(t, tickets, 1)
}
```

**Step 2: Run test to verify it fails**
Run: `cd server && go test ./internal/services -run TestGetTicketsFilters_SLAOverdueUnassigned -v`
Expected: FAIL with missing fields/logic.

**Step 3: Write minimal implementation**
```go
// TicketFilters add fields
SLABreached *bool
IsOverdue   *bool
Unassigned  *bool
```
```go
// In GetTickets apply filters
if filters.SLABreached != nil {
    query = query.Where("sla_breached = ?", *filters.SLABreached)
}
if filters.IsOverdue != nil {
    now := time.Now()
    if *filters.IsOverdue {
        query = query.Where("due_date < ? AND status NOT IN (?, ?)", now, models.TicketStatusResolved, models.TicketStatusClosed)
    } else {
        query = query.Where("(due_date IS NULL OR due_date >= ?) OR status IN (?, ?)", now, models.TicketStatusResolved, models.TicketStatusClosed)
    }
}
if filters.Unassigned != nil {
    if *filters.Unassigned {
        query = query.Where("assigned_to_id IS NULL")
    } else {
        query = query.Where("assigned_to_id IS NOT NULL")
    }
}
```
```go
// In handler parse query params
slaBreached := c.Query("sla_breached")
if slaBreached != "" { filters.SLABreached = parseBoolPtr(slaBreached) }
// same for is_overdue, unassigned
```

**Step 4: Run test to verify it passes**
Run: `cd server && go test ./internal/services -run TestGetTicketsFilters_SLAOverdueUnassigned -v`
Expected: PASS

**Step 5: Commit**
```bash
git add server/internal/services/ticket_service.go server/internal/handlers/ticket_handler.go server/internal/services/ticket_service_filters_test.go
git commit -m "feat: add ticket filters for sla/overdue/unassigned"
```

---

### Task 2: 统计接口聚合 SQL + SLA/升级/未分配等字段（后端）

**Files:**
- Modify: `server/internal/services/ticket_service.go`
- Create: `server/internal/services/ticket_service_stats_test.go`

**Step 1: Write the failing test**
```go
func TestGetTicketStatistics_Aggregates(t *testing.T) {
    db := setupTestDB(t)
    seedTicketsForStats(t, db)
    svc := NewTicketService(db)

    stats, err := svc.GetTicketStatistics(1, "admin")
    require.NoError(t, err)
    require.Equal(t, int64(4), stats.Total)
    require.Equal(t, int64(1), stats.Open)
    require.Equal(t, int64(1), stats.InProgress)
    require.Equal(t, int64(1), stats.SLABreached)
    require.Equal(t, int64(1), stats.Escalated)
    require.Equal(t, int64(1), stats.Unassigned)
}
```

**Step 2: Run test to verify it fails**
Run: `cd server && go test ./internal/services -run TestGetTicketStatistics_Aggregates -v`
Expected: FAIL with zero/incorrect counts.

**Step 3: Write minimal implementation**
```go
// Use SUM(CASE...) aggregation (SQLite friendly)
row := struct {
    Total, Open, InProgress, Pending, Resolved, Closed int64
    Overdue, Unassigned, HighPriority, SLABreached, Escalated int64
}{ }

query := s.db.Model(&models.Ticket{})
if role == "agent" { query = query.Where("assigned_to_id = ?", userID) }

now := time.Now()
err := query.Select(`
    COUNT(*) AS total,
    SUM(CASE WHEN status = 'open' THEN 1 ELSE 0 END) AS open,
    SUM(CASE WHEN status = 'in_progress' THEN 1 ELSE 0 END) AS in_progress,
    SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END) AS pending,
    SUM(CASE WHEN status = 'resolved' THEN 1 ELSE 0 END) AS resolved,
    SUM(CASE WHEN status = 'closed' THEN 1 ELSE 0 END) AS closed,
    SUM(CASE WHEN due_date < ? AND status NOT IN ('resolved','closed') THEN 1 ELSE 0 END) AS overdue,
    SUM(CASE WHEN assigned_to_id IS NULL THEN 1 ELSE 0 END) AS unassigned,
    SUM(CASE WHEN priority IN ('high','urgent') THEN 1 ELSE 0 END) AS high_priority,
    SUM(CASE WHEN sla_breached = true THEN 1 ELSE 0 END) AS sla_breached,
    SUM(CASE WHEN is_escalated = true THEN 1 ELSE 0 END) AS escalated
`, now).Scan(&row).Error
```

**Step 4: Run test to verify it passes**
Run: `cd server && go test ./internal/services -run TestGetTicketStatistics_Aggregates -v`
Expected: PASS

**Step 5: Commit**
```bash
git add server/internal/services/ticket_service.go server/internal/services/ticket_service_stats_test.go
git commit -m "perf: aggregate ticket statistics"
```

---

### Task 3: 统计结果 Redis 缓存（后端）

**Files:**
- Modify: `server/internal/services/ticket_service.go`
- Modify: `server/main.go`
- Create: `server/internal/services/ticket_service_cache_test.go`

**Step 1: Write the failing test**
```go
func TestGetTicketStatistics_Cache(t *testing.T) {
    db := setupTestDB(t)
    seedTicketsForStats(t, db)
    cache := &fakeRedis{store: map[string]string{}}
    svc := NewTicketServiceWithCache(db, cache, 30*time.Second)

    stats1, err := svc.GetTicketStatistics(1, "admin")
    require.NoError(t, err)

    // mutate db
    db.Model(&models.Ticket{}).Where("id = ?", 1).Update("status", "closed")

    stats2, err := svc.GetTicketStatistics(1, "admin")
    require.NoError(t, err)
    require.Equal(t, stats1.Total, stats2.Total)
    require.Equal(t, stats1.Closed, stats2.Closed) // still cached
}
```

**Step 2: Run test to verify it fails**
Run: `cd server && go test ./internal/services -run TestGetTicketStatistics_Cache -v`
Expected: FAIL (constructor missing / no cache usage)

**Step 3: Write minimal implementation**
```go
// Add cache + ttl in service
func NewTicketServiceWithCache(db *gorm.DB, cache database.RedisInterface, ttl time.Duration) TicketServiceInterface {
    return &TicketService{db: db, notificationService: NewNotificationService(db), statsCache: cache, statsCacheTTL: ttl}
}

// Build cache key by role/user
key := fmt.Sprintf("ticket_stats:%s:%d", role, userID)
// Get/Set with JSON marshal
```
```go
// In main.go use db.Redis if available
cacheTTL := getStatsTTLFromEnv() // e.g. 30s default
svc := services.NewTicketServiceWithCache(db.DB, db.Redis, cacheTTL)
```

**Step 4: Run test to verify it passes**
Run: `cd server && go test ./internal/services -run TestGetTicketStatistics_Cache -v`
Expected: PASS

**Step 5: Commit**
```bash
git add server/internal/services/ticket_service.go server/main.go server/internal/services/ticket_service_cache_test.go
git commit -m "perf: cache ticket statistics"
```

---

### Task 4: WebSocket Origin 校验（后端）

**Files:**
- Modify: `server/internal/websocket/client.go`
- Modify: `server/main.go`
- Create: `server/internal/websocket/origin_test.go`

**Step 1: Write the failing test**
```go
func TestOriginAllowed(t *testing.T) {
    allowed := []string{"https://admin.example.com", "https://*.example.org"}
    require.True(t, originAllowed("https://admin.example.com", allowed, false))
    require.True(t, originAllowed("https://foo.example.org", allowed, false))
    require.False(t, originAllowed("https://evil.com", allowed, false))
}
```

**Step 2: Run test to verify it fails**
Run: `cd server && go test ./internal/websocket -run TestOriginAllowed -v`
Expected: FAIL (function missing)

**Step 3: Write minimal implementation**
```go
var wsAllowedOrigins []string
var wsAllowAll bool

func ConfigureOriginCheck(allowed []string, allowAll bool) {
    wsAllowedOrigins = allowed
    wsAllowAll = allowAll
}

func originAllowed(origin string, allowed []string, allowAll bool) bool {
    if allowAll { return true }
    for _, item := range allowed {
        if item == "*" || item == origin || matchWildcard(item, origin) { return true }
    }
    return false
}

// Use in upgrader.CheckOrigin
CheckOrigin: func(r *http.Request) bool {
    return originAllowed(r.Header.Get("Origin"), wsAllowedOrigins, wsAllowAll)
}
```
```go
// main.go setup
allowAll := cfg.Server.Environment != "production"
websocketPkg.ConfigureOriginCheck(cfg.CORS.AllowedOrigins, allowAll)
```

**Step 4: Run test to verify it passes**
Run: `cd server && go test ./internal/websocket -run TestOriginAllowed -v`
Expected: PASS

**Step 5: Commit**
```bash
git add server/internal/websocket/client.go server/internal/websocket/origin_test.go server/main.go
git commit -m "feat: add websocket origin check"
```

---

### Task 5: Webhook 并发池 + 退避重试（后端）

**Files:**
- Modify: `server/internal/services/notification_service.go`
- Create: `server/internal/services/notification_service_test.go`

**Step 1: Write the failing test**
```go
func TestWebhookRetryBackoff(t *testing.T) {
    attempts := 0
    send := func() error {
        attempts++
        if attempts < 3 { return errors.New("fail") }
        return nil
    }

    err := runWithRetry(send, 3, 10*time.Millisecond)
    require.NoError(t, err)
    require.Equal(t, 3, attempts)
}
```

**Step 2: Run test to verify it fails**
Run: `cd server && go test ./internal/services -run TestWebhookRetryBackoff -v`
Expected: FAIL (helper missing)

**Step 3: Write minimal implementation**
```go
func runWithRetry(send func() error, max int, base time.Duration) error {
    var err error
    for i := 0; i < max; i++ {
        err = send()
        if err == nil { return nil }
        if i < max-1 {
            time.Sleep(base * time.Duration(1<<i))
        }
    }
    return err
}
```
```go
// In SendNotification, dispatch with semaphore
sem := make(chan struct{}, ns.webhookMaxConcurrent)
for _, cfg := range configs {
    sem <- struct{}{}
    go func(c *models.WebhookConfig) {
        defer func(){ <-sem }()
        _ = runWithRetry(func(){ return ns.sendWebhook(ctx, c, event) }, cfg.RetryCount+1, time.Duration(cfg.RetryInterval)*time.Second)
    }(cfg)
}
```

**Step 4: Run test to verify it passes**
Run: `cd server && go test ./internal/services -run TestWebhookRetryBackoff -v`
Expected: PASS

**Step 5: Commit**
```bash
git add server/internal/services/notification_service.go server/internal/services/notification_service_test.go
git commit -m "feat: add webhook retry and concurrency limit"
```

---

### Task 6: 中间件统一接入 + CORS 配置化（后端）

**Files:**
- Modify: `server/main.go`
- Modify: `server/internal/config/config.go` (若需新增 env)

**Step 1: Write the failing test**
```go
func TestCORSConfigFromEnv(t *testing.T) {
    os.Setenv("CORS_ALLOWED_ORIGINS", "https://a.com,https://b.com")
    cfg, err := config.Load()
    require.NoError(t, err)
    require.Equal(t, []string{"https://a.com", "https://b.com"}, cfg.CORS.AllowedOrigins)
}
```

**Step 2: Run test to verify it fails**
Run: `cd server && go test ./internal/config -run TestCORSConfigFromEnv -v`
Expected: FAIL if mapping未使用/覆盖.

**Step 3: Write minimal implementation**
```go
// main.go
middlewares := middleware.SetupMiddlewares(middlewareConfig)
r.Use(middleware.WrapGinMiddlewares(middlewares)...)

// remove manual CORS middleware block
```

**Step 4: Run test to verify it passes**
Run: `cd server && go test ./internal/config -run TestCORSConfigFromEnv -v`
Expected: PASS

**Step 5: Commit**
```bash
git add server/main.go server/internal/config/config.go
git commit -m "refactor: wire middleware and config-driven cors"
```

---

### Task 7: 迁移新增 pg_trgm + GIN 索引（数据库）

**Files:**
- Modify: `server/cmd/migrate/main.go`
- Modify: `server/internal/models/migrations.go` (如有重复索引列表)

**Step 1: Write the failing test**
（该任务为数据库迁移，暂无自动化测试；采用手动验证）

**Step 2: Apply minimal implementation**
```sql
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_tickets_title_trgm ON tickets USING gin (title gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_tickets_description_trgm ON tickets USING gin (description gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_tickets_tags_gin ON tickets USING gin (tags);
```

**Step 3: Manual verification**
Run: `cd server && make migrate`
Expected: Migration成功，无报错

**Step 4: Commit**
```bash
git add server/cmd/migrate/main.go server/internal/models/migrations.go
git commit -m "perf: add trigram and gin indexes"
```

---

### Task 8: 前端响应解析简化 + 清理测试页面

**Files:**
- Modify: `web/src/lib/dataProvider.ts`
- Delete: `web/src/TestApp.tsx`
- Delete: `web/src/TestApp2.tsx`

**Step 1: Write the failing test**
（前端当前无测试框架，建议仅 lint + 手动 smoke）

**Step 2: Write minimal implementation**
```ts
// Extract parseListResponse(json, headers, resource)
// Keep special cases for automation-rules/logs, others use standard response
```

**Step 3: Manual verification**
Run: `cd web && npm run lint`
Expected: 不引入新增 lint 错误

**Step 4: Commit**
```bash
git add web/src/lib/dataProvider.ts web/src/TestApp.tsx web/src/TestApp2.tsx
git commit -m "refactor: simplify dataProvider responses"
```

---

### Task 9: 全量验证

**Step 1: Backend**
Run: `cd server && go test ./...`
Expected: PASS

**Step 2: Frontend**
Run: `cd web && npm run lint`
Expected: 允许存在当前基线错误，但不得新增

**Step 3: Manual smoke**
- 登录 → 工单列表 → 筛选（SLA/逾期/未分配）
- 仪表盘统计对齐
- 通知/自动化列表加载

---

## 需要你确认的测试例外
- **Task 7（数据库迁移）**：采用手动验证，未写自动化测试
- **Task 8（前端变更）**：当前无测试框架，使用 lint + 手动验证
- **Task 6（中间件接入）**：属于接线变更，自动化测试仅覆盖配置读取

