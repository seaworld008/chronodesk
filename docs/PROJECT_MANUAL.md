# ChronoDesk 项目权威手册

> 更新时间：2026-02-07  
> 适用分支：`main`（当前仓库工作区）

## 1. 项目定位
ChronoDesk 是一个面向客服/运营团队的工单管理系统，覆盖“认证登录 -> 工单流转 -> 自动化执行 -> 通知触达 -> 运营分析 -> 系统配置”的闭环。当前实现由 Go 后端与 React-Admin 前端组成，支持本地开发、Docker 一体化运行，以及 Go/Python/Shell 多层测试手段。

## 2. 当前能力总览

### 2.1 业务能力
- 认证与账号安全：注册/登录/JWT 刷新、OTP、可信设备、登录历史。
- 工单管理：CRUD、分配、转派、升级、状态流转、历史记录、批量更新。
- 自动化：规则管理、执行日志、SLA 配置、模板、快速回复、批量动作。
- 通知中心：站内通知、已读/未读、偏好设置，邮件与 Webhook 集成。
- 系统治理：管理员用户管理、审计日志、系统配置、统计看板。

### 2.2 技术栈
- Backend：Go 1.21、Gin、GORM、PostgreSQL、Redis、JWT。
- Frontend：React 18、TypeScript、React-Admin、MUI、Vite、Shadcn UI。
- Tooling：Makefile、`dev.sh`、Docker Compose、Go test、Pytest、Playwright（web/e2e）。

## 3. 架构与代码组织

### 3.1 仓库结构
```text
.
├── server/                 # Go API 服务
│   ├── cmd/migrate/        # 迁移/种子入口
│   ├── internal/
│   │   ├── auth/           # 认证与OTP
│   │   ├── config/         # 环境配置加载
│   │   ├── database/       # PostgreSQL/Redis 初始化与迁移
│   │   ├── handlers/       # HTTP Handler 层
│   │   ├── middleware/     # 鉴权/CORS/日志/限流
│   │   ├── models/         # GORM 模型
│   │   ├── scheduler/      # 定时任务执行
│   │   ├── services/       # 业务服务层
│   │   └── websocket/      # WebSocket 实时通知骨架
│   └── main.go             # API 启动与路由装配
├── web/                    # React Admin 管理端
│   ├── src/admin/          # 业务页面
│   ├── src/lib/            # dataProvider/authProvider/apiClient
│   ├── src/components/     # UI 组件
│   └── src/layout/         # 后台布局
├── docs/                   # 项目文档中心
├── Makefile                # 根级统一命令
├── dev.sh                  # 本地前后端进程管理脚本
└── docker-compose.yml      # 本地容器编排
```

### 3.2 后端分层
- Handler 层：参数解析、鉴权上下文、HTTP 响应。
- Service 层：核心业务逻辑（工单、自动化、通知、配置、审计）。
- Model 层：数据库实体与字段约束。
- Middleware 层：认证、角色校验、审计日志、限流、安全头等。

### 3.3 前端分层
- `AdminApp` 统一注册资源：`tickets`、`users`、`notifications`、`automation-rules`、`automation-logs`。
- `dataProvider` 负责 React-Admin 与后端 REST 参数适配。
- `authProvider` 负责登录态、token 刷新与权限控制。
- `CustomRoutes` 补充系统设置、邮箱、Webhook、可信设备页面。

## 4. 运行时与端口约定

### 4.1 本地开发（推荐）
```bash
./dev.sh start
```
- Backend：`http://localhost:8081`
- Frontend：`http://localhost:3000`

### 4.2 分开运行
```bash
make server-dev
make web-dev
```

### 4.3 Docker 一体化
```bash
make dev
# 或 docker-compose up -d
```

## 5. API 路由地图（基于 `server/main.go`）

### 5.1 公共与健康检查
- `GET /healthz`
- `GET /api/ping`
- `GET /api/health`
- `GET /api/redis/test`

### 5.2 认证与账号
- `POST /api/auth/register`
- `POST /api/auth/login`
- `POST /api/auth/logout`
- `POST /api/auth/refresh`
- `POST /api/auth/forgot-password`
- `POST /api/auth/reset-password`
- `POST /api/auth/verify-email`
- `POST /api/auth/resend-verification`
- `GET /api/auth/me`（鉴权）
- `GET|PUT /api/auth/profile`（鉴权）
- `POST /api/auth/change-password`（鉴权）
- `POST /api/auth/enable-otp`（鉴权）
- `POST /api/auth/disable-otp`（鉴权）
- `POST /api/auth/verify-otp`（鉴权）
- `POST /api/auth/otp/backup-codes`（鉴权）

### 5.3 工单
- `GET|POST /api/tickets`
- `GET|PUT|DELETE /api/tickets/:id`
- `POST /api/tickets/:id/assign`
- `POST /api/tickets/:id/transfer`
- `POST /api/tickets/:id/escalate`
- `POST /api/tickets/:id/status`
- `GET /api/tickets/:id/history`
- `GET /api/tickets/stats`
- `GET /api/tickets/my-tickets`
- `GET /api/tickets/unassigned`
- `GET /api/tickets/overdue`
- `GET /api/tickets/sla-breach`
- `POST /api/tickets/bulk-assign`
- `POST /api/tickets/bulk-status`
- `POST /api/tickets/bulk-update`

### 5.4 用户中心
- `GET|PUT /api/user/profile`
- `PUT /api/user/password`
- `GET /api/user/login-history`
- `DELETE /api/user/login-history/:id`
- `GET /api/user/stats`
- `POST /api/user/avatar`
- `GET /api/user/trusted-devices`
- `DELETE /api/user/trusted-devices/:id`

### 5.5 管理员域（`/api/admin`）
- 邮件配置：`GET|PUT /email-config`，`POST /email-config/test`
- 用户管理：`GET/POST /users`，`GET/PUT/DELETE /users/:id`，批量与重置接口
- 审计日志：`GET /audit-logs`
- 全局配置：`/configs`（CRUD、批量更新、导入导出、缓存管理）
- 统计分析：`/analytics/system|business|dashboard|timerange|export|realtime`
- 自动化：`/automation/rules|logs|sla|templates|quick-replies|batch`
- 通知管理：`POST /notifications`，`DELETE /notifications/:id`

### 5.6 通知/WebSocket/Webhook
- 通知：`GET /api/notifications`、已读、偏好、未读计数
- WebSocket：`GET /api/ws`（鉴权后连接）
- Webhook：`/api/webhooks` 下配置/测试/日志/统计接口

## 6. 配置与环境变量
配置入口：`server/internal/config/config.go`，样例：`server/.env.example`。

关键配置域：
- Server：`PORT`、`ENVIRONMENT`、`GIN_MODE`
- DB：`DB_HOST`、`DB_PORT`、`DB_USER`、`DB_PASSWORD`、`DB_NAME`
- Redis：`REDIS_HOST`、`REDIS_PORT`、`REDIS_PASSWORD`
- Auth：`JWT_SECRET`、`JWT_EXPIRES_IN`
- Security：`BCRYPT_COST`、`RATE_LIMIT_*`
- CORS：`CORS_ALLOWED_*`

说明：项目脚本与 Docker 默认以 `8081` 作为后端端口，若调整端口，请同步更新 `dev.sh`、`docker-compose.yml`、前端环境变量。

## 7. 测试与质量门槛

### 7.1 后端
```bash
make test-server
cd server && make fmt && make vet
```

### 7.2 前端
```bash
make test-web
cd web && npm run build
```

### 7.3 冒烟与回归
```bash
cd server && make smoke
./test_integration.sh
server/test_notification_system.sh
```

## 8. 安全与运维要点
- 管理员接口统一经过角色鉴权与审计日志中间件。
- JWT 密钥在生产环境必须替换默认值。
- 建议开启 HTTPS、收紧 CORS 白名单、限制上传类型与大小。
- 定期清理构建产物与日志（避免将大文件提交到 Git）。

## 9. 已知风险与待办方向
- WebSocket 仍偏骨架实现，未完全打通未读计数实时回写。
- 前端 ESLint 存在历史告警，需分模块治理。
- 部分历史文档与路径已归档，新增内容请优先更新本手册与 `docs/README.md`。

## 10. 维护规范（文档真相源）
- 架构/功能/接口变化后，先更新本文件，再更新专题文档。
- 根目录仅保留入口文档（`README.md`）与 agent 控制文件（`AGENTS.md`、`CLAUDE.md`）。
- 历史资料放入 `docs/archive/`，参考资料放入 `docs/reference/`。
