# 工单管理系统（Go Gin + React）——Cursor 实现提示词（2025-08）

> 角色设定：你是**拥有 20 年经验的全栈开发大师**，精通 Go/Gin、PostgreSQL、Redis、React/TypeScript、Vite、Tailwind、shadcn/ui、TanStack Query、JWT 安全与 RBAC。请严格按本提示词产出**可直接运行**的前后端分离项目代码与文档。**目前仅开发“工单（Ticket）功能 + 基础账号体系”**，其它模块先不实现。所有外部服务连接用**环境变量占位**，我后续自行配置。

---

## 0. 目标与范围（MVP）
- **范围只包含：**
  - 账号体系：邮箱注册 + 登录（邮箱+密码为主，可选邮箱验证码无密码登录作为可开关特性）。
  - 工单功能（Ticket）：创建、列表、详情、编辑、状态流转、优先级、指派、评论、标签、基础筛选/排序/分页。
  - 角色/权限（RBAC）：`admin`、`agent`、`user` 三种角色。
- **暂不做**：知识库、仪表盘、统计报表、SLA 计时、Webhook/集成、消息推送、支付等。保留**扩展位**和**干净架构**，后续易扩展。
- **部署**：仅需本地可跑；生产部署留出 Docker 支持与 `.env` 占位。

---

## 1. 技术栈与约束
### 后端
- 语言/框架：Go 1.22+、Gin
- ORM：GORM（PostgreSQL 驱动），或 sqlc + pgx（二选一，推荐 GORM 以提速）
- DB：PostgreSQL（**Vercel 提供的 Neon**，使用 `DATABASE_URL` 占位）
- 缓存：Redis（**Vercel 提供的 Upstash Redis**，使用 `REDIS_URL` 占位）
- 鉴权：JWT（`golang-jwt/jwt/v5`），`HS256`
- 文档：Swagger（`swaggo/swag`），自动生成到 `/swagger/*`
- 依赖建议：
  - `github.com/gin-gonic/gin`
  - `gorm.io/gorm`, `gorm.io/driver/postgres`
  - `github.com/redis/go-redis/v9`
  - `github.com/golang-jwt/jwt/v5`
  - `github.com/google/uuid`
  - `github.com/spf13/viper`（或 `caarlos0/env`）
  - `golang.org/x/crypto/bcrypt`
  - `github.com/go-playground/validator/v10`
  - 可选邮件：`gopkg.in/gomail.v2`（支持关闭真实发信，开发期打印控制台）

### 前端
- Vite + React + TypeScript
- UI：TailwindCSS + shadcn/ui + lucide-react 图标
- 数据：TanStack Query（React Query）+ Axios/Fetch 包装
- 路由：React Router v6
- 状态：轻量（仅必要的 UI 状态用 Zustand，可选）
- 设计：响应式、暗黑模式支持、表格组件具备分页/排序/筛选/关键字搜索、表单校验（react-hook-form + zod）

---

## 2. 环境变量（仅占位，不提交真实密钥）
在后端与前端分别提供 `.env.example`，**全部使用占位**，例如：

### 后端 `.env.example`
```
# --- Core ---
APP_NAME=ticketing
APP_ENV=development
APP_PORT=8080
APP_BASE_URL=http://localhost:8080

# --- Postgres (Neon on Vercel) ---
DATABASE_URL=postgres://<USER>:<PASS>@<HOST>:<PORT>/<DB>?sslmode=require

# --- Redis (Upstash on Vercel) ---
REDIS_URL=redis://:<PASSWORD>@<HOST>:<PORT>

# --- Auth/JWT ---
JWT_SECRET=change-me-please-32bytes-min
JWT_TTL_MINUTES=120
REFRESH_TTL_MINUTES=43200

# --- CORS ---
CORS_ALLOW_ORIGINS=http://localhost:5173

# --- Email (可选，开发期可禁用真实发信) ---
EMAIL_ENABLED=false
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_USERNAME=postmaster@example.com
SMTP_PASSWORD=yourpassword
SMTP_FROM="Ticketing <noreply@example.com>"

# --- Optional: Email OTP 登录/注册（无密码） ---
AUTH_ENABLE_EMAIL_OTP=true
EMAIL_OTP_TTL_SECONDS=600

# --- Rate Limit ---
RATE_LIMIT_PER_MINUTE=120
```

### 前端 `.env.example`
```
VITE_API_BASE_URL=http://localhost:8080/api
VITE_APP_NAME=ticketing
```

---

## 3. 数据模型与迁移（PostgreSQL）
> 使用 GORM AutoMigrate（同时输出 SQL 迁移文件到 `migrations/`）。

### 表与字段（建议）
- `users`
  - `id` (uuid, pk)
  - `email` (unique not null)
  - `password_hash` (nullable, 当使用 OTP 时可为空)
  - `role` (enum: `admin`|`agent`|`user`, default `user`)
  - `is_email_verified` (bool, default false)
  - `created_at`, `updated_at`
- `tickets`
  - `id` (uuid, pk)
  - `title` (text, not null)
  - `description` (text)
  - `status` (enum: `open`|`in_progress`|`resolved`|`closed`, default `open`)
  - `priority` (enum: `low`|`medium`|`high`|`urgent`, default `medium`)
  - `creator_id` (uuid -> users.id)
  - `assignee_id` (uuid -> users.id, nullable)
  - `tags` (text[] 或建立关联表，MVP 可 text[])
  - `created_at`, `updated_at`, `closed_at` (nullable)
- `ticket_comments`
  - `id` (uuid, pk)
  - `ticket_id` (uuid -> tickets.id)
  - `author_id` (uuid -> users.id)
  - `content` (text, not null)
  - `created_at`
- （可选）`attachments`（MVP 留空实现，接口 stub）
- （可选）`audit_logs`（记录关键动作，MVP 可选）

> 为常用查询建索引：`tickets(status, priority, created_at)`, `tickets(assignee_id)`, `GIN(tickets.tags)`。

---

## 4. 权限与可见性（RBAC）
- `admin`：查看/编辑全部工单；可改任意人指派与状态；管理用户角色。
- `agent`：查看全部工单；可接单/被指派；可修改自己负责工单的状态。
- `user`：仅能查看自己创建的工单；可追加评论；可关闭自己创建但未被管理员锁定的工单。
- 后端中间件：基于 JWT 解析 `user_id` 和 `role`，在 handler 内做**细粒度**授权检查。

---

## 5. 后端 API 设计（OpenAPI/Swagger）
> 统一前缀 `/api`。所有请求/响应给出 DTO 与校验。错误返回统一结构：`{ "error": "message", "code": "..." }`。

### 5.1 Auth
- `POST /api/auth/register`  
  - 入参：`email`, `password`（>=8，含大小写/数字；MVP 可放宽只长度校验）  
  - 行为：创建用户，`is_email_verified` 初始 false；若 `EMAIL_ENABLED=true` 则发送验证邮件（或 OTP）。
- `POST /api/auth/login`  
  - 入参：`email`, `password`  
  - 出参：`access_token`, `refresh_token`, `user`
- `POST /api/auth/refresh`  
  - 入参：`refresh_token`  
  - 出参：新的 `access_token`
- `GET /api/auth/me`（需登录）
- `POST /api/auth/logout`（可在 Redis 记录黑名单或刷新令牌轮换）
- **可选（OTP 无密码流程，受 `AUTH_ENABLE_EMAIL_OTP` 控制）**
  - `POST /api/auth/request-otp`（`email`），将 6 位验证码写入 Redis（TTL= `EMAIL_OTP_TTL_SECONDS`）
  - `POST /api/auth/verify-otp`（`email`,`otp`）成功后签发 JWT；若用户不存在则自动注册（无密码）。

### 5.2 Tickets
- `POST /api/tickets`（user/agent/admin）创建工单
  - 入参：`title`(必填), `description`, `priority`, `tags`
- `GET /api/tickets`（分页 + 条件）
  - 查询参数：`page`, `page_size`, `q`(模糊搜索标题/描述), `status`, `priority`, `assignee_id`, `creator_id`, `tag`, `order_by`(created_at|updated_at), `order`(asc|desc)
  - 权限：user 仅返回自己创建的；agent/admin 返回全部
- `GET /api/tickets/:id`（权限同上）
- `PATCH /api/tickets/:id`（编辑标题/描述/优先级/标签）
- `PATCH /api/tickets/:id/status`（状态流转；记录 `closed_at`）
- `PATCH /api/tickets/:id/assign`（仅 agent/admin，可指派/接单）
- `POST /api/tickets/:id/comments`（任何有权限可见者都可评论）
- `GET /api/tickets/:id/comments`
- **（可选）附件**：`POST /api/tickets/:id/attachments`（先留 stub 与 TODO 标记，不实现存储）

### 5.3 健康检查 & 元信息
- `GET /healthz`
- `GET /version`（从 `APP_NAME` 与 git 信息生成）

---

## 6. 后端实现细节
- 目录结构（建议）
```
server/
  cmd/server/main.go
  internal/
    config/
    db/            # gorm init, migrations
    redis/
    domain/        # entity/DTO/validator
    repository/    # GORM 仓储接口实现
    service/       # 业务聚合 & 权限校验
    http/
      middleware/  # jwt, cors, ratelimit, request-id, logging, recover
      handler/     # auth, tickets
      router.go
    pkg/
      email/       # 可切换 console sender / smtp sender
      security/    # password, jwt, otp
      util/
  migrations/
  Makefile
  go.mod go.sum
  .env.example
```

- 中间件：
  - **CORS**：`CORS_ALLOW_ORIGINS` 白名单
  - **JWT**：从 `Authorization: Bearer` 解析
  - **限流**：基于 Redis 简单令牌桶（IP+路由键），默认 `RATE_LIMIT_PER_MINUTE`
  - **RequestID**、**访问日志**、**全局异常恢复**

- 验证与错误处理：
  - `validator` 标签校验 DTO，统一返回格式
  - 数据库错误（唯一索引冲突等）转为业务错误码

- Swagger：
  - 使用 swag 注解，`make swag` 生成文档，路由 `/swagger/*`

- 测试：
  - `repository` 与 `service` 层分别写单测（mock Redis/DB 或使用 testcontainers），覆盖关键路径（注册/登录、创建/查询/状态流转/评论）。

---

## 7. 前端实现细节（Vite + React + TS）
- 目录建议：
```
web/
  src/
    app/
      providers.tsx  # react-query, theme
      router.tsx
    components/
      ui/            # 来自 shadcn/ui
      forms/
      layout/
    features/
      auth/
        pages/Login.tsx
        pages/Register.tsx
        pages/VerifyOTP.tsx    # 可选
        api.ts
        types.ts
      tickets/
        pages/TicketList.tsx
        pages/TicketDetail.tsx
        components/TicketForm.tsx
        components/CommentList.tsx
        api.ts
        types.ts
    lib/
      axios.ts        # 请求拦截/错误处理/携带 token
      auth.ts         # token 存储/刷新
      hooks/
    styles/
      globals.css
  index.html
  vite.config.ts
  tailwind.config.ts
  tsconfig.json
  .env.example
```

- UI/交互：
  - 登录/注册页（邮箱+密码；如果 `AUTH_ENABLE_EMAIL_OTP=true`，提供“验证码登录/注册”切换）
  - 工单列表：表格（分页/排序/筛选/关键字搜索），状态、优先级、标签可筛选；行点击进详情
  - 工单详情：基本信息、状态流转（下拉）、指派（选择用户，仅 agent/admin 可见）、评论区（富文本可简化为 Markdown 文本域）
  - 创建/编辑工单：弹窗或单页表单
  - 顶部导航：登录用户信息 + 角色显示；注销按钮
  - 错误与 Loading 状态友好提示；表单校验（zod + react-hook-form）

- 鉴权处理：
  - 登录后 `access_token` 持久化（localStorage）
  - Axios 拦截器注入 token；401 触发刷新/重登
  - 基于路由守卫保护业务路由

- 风格：
  - Tailwind + shadcn/ui（Button, Input, Select, Dialog, Table, Badge, Tabs 等）
  - 支持暗黑模式（类名 `dark`）

---

## 8. 运行与构建
- **后端**：
  - `make dev`：热重载（用 `air` 或 `reflex`），读取 `.env`
  - `make migrate`：执行迁移
  - `make test`：单测
  - `make swag`：生成 swagger
  - 输出 README，说明本地开发如何改用 Docker（附 `docker-compose.yml` 提供 Postgres/Redis 开箱）

- **前端**：
  - `pnpm i && pnpm dev`（或 npm/yarn），读取 `VITE_API_BASE_URL`
  - `pnpm build` 产物至 `dist/`
  - 提供 `.env.example` 与 README

- **Docker**：
  - `Dockerfile`（多阶段构建）与 `compose` 示例（仅本地，不提交密钥），后端暴露 `8080`，前端 `5173`

---

## 9. 验收标准（Definition of Done）
1. 后端启动后：`/healthz` 返回 200；`/swagger/index.html` 可访问。
2. 使用 `.env.example` 改成本地/占位配置即可跑通（真实 Neon/Upstash 链接由我替换）。
3. 前端能完成：注册→登录→创建工单→列表查看→筛选/排序/分页→详情→评论→状态/指派变更。
4. `user` 只能看到自己工单；`agent/admin` 能看到全部。
5. 代码结构清晰，分层与接口职责明确；关键逻辑有单测。
6. README 清晰列出：环境变量说明、启动步骤、常见问题、下一步扩展建议。

---

## 10. 生成物清单
- `server/` 完整后端代码 + `migrations/` + `Makefile` + `Dockerfile` + `README.md` + `.env.example`
- `web/` 完整前端代码 + `README.md` + `.env.example`
- `openapi.json` 或 Swagger 在线文档
- `docker-compose.yml`（本地开发用）

---

## 11. 额外实现要求与质量守则
- 严格区分 DTO 与 DB Model；在 `service` 层做权限与状态机校验。
- 所有**写操作**添加审计日志接口（MVP 可将日志打印到 stdout，并预留 `audit_logs` 表迁移）。
- 所有**列表接口**统一分页返回：`{"list":[], "page":1, "page_size":20, "total":123}`。
- 对 tickets 的状态流转做状态机守卫：`open -> in_progress -> resolved -> closed`（允许 `reopen` 到 `in_progress`）。
- 评论不可编辑（MVP），可后续加撤回/编辑。
- 严禁将密钥硬编码入库；**全部**通过环境变量注入。
- 错误码与 UX 友好：用户侧提示简洁明了，日志中保留详细原因。

---

## 12. 下一步扩展（仅写在 README，不实现）
- 附件上传（S3/Cloudflare R2/OSS）
- 工单 SLA/统计报表与图表
- 多租户（tenant_id）
- Webhook/Slack/钉钉通知
- 富文本/图片评论
- 全文搜索（pg_trgm / 外部搜索引擎）

---

## 13. 请开始生成
按上述规格直接输出**完整可运行项目**，并：
1) 在后端 README 中给出 `curl` or HTTPie 的最小验证脚本（注册/登录/创建/列表）。  
2) 在前端 README 中给出演示 GIF（可使用占位或说明如何录制）。  
3) 严格使用 `.env.example` 的**占位变量**作为连接字符串，不要提交任何真实密钥。  
4) 代码风格统一，重要公共函数写注释。

> 完毕后，请在根 README 列出所有命令与主要技术决策简述。谢谢。

---

## 14. 任务规划文档

详细的任务规划和实施计划请参考：[task-plan.md](./task-plan.md)

该文档包含：
- 16 个详细任务的完整分解
- 每个任务的序号、状态、优先级和预估时间
- 4 个开发阶段的执行计划
- 关键里程碑和验收标准
- 风险评估与应对策略