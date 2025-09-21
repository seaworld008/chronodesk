# 工单管理系统 · 项目总览

本文档整理了当前仓库的体系结构、运行方式、核心功能以及待办事项，便于后续继续迭代或新成员快速上手。

## 1. 系统概述
- **定位**：面向客服/运营团队的全生命周期工单平台，覆盖创建、分配、升级、自动化和统计分析。
- **核心特性**：多角色权限、实时通知、仪表盘联动、自动化规则与日志、配置管理、Webhook/邮件渠道。
- **技术栈**：Go + Gin 后端、PostgreSQL + Redis、React 18 + React-Admin + Vite 前端、Docker Compose 辅助部署。

## 2. 架构速览
| 层级 | 说明 |
| --- | --- |
| **Frontend** | `web/` · React 18 + TypeScript，基于 React-Admin 组织页面，TanStack Query 做数据拉取与缓存。 |
| **Backend** | `server/` · Go 1.21 + Gin，分层为 `handlers` + `services` + `models`，通过 GORM 操作数据库。 |
| **Persistence** | PostgreSQL 15（关系数据）、Redis 7（缓存/Session/锁）。 |
| **背景任务** | `internal/scheduler` 按 cron 扫描并执行自动化、SLA 检查等任务。 |
| **交付** | `Makefile`、`dev.sh`（本地一键起停），`docker-compose.yml`（容器化）。 |

## 3. 目录结构
```
├── server/                # Go 后端
│   ├── cmd/migrate        # 数据迁移/播种工具
│   ├── internal/
│   │   ├── auth           # 登录、JWT、OTP
│   │   ├── config         # 配置解析（.env -> Config）
│   │   ├── database       # GORM 初始化、迁移
│   │   ├── handlers       # HTTP 层（tickets、automation、users、settings 等）
│   │   ├── middleware     # 鉴权、请求日志、限流
│   │   ├── models         # GORM 模型 + DTO
│   │   ├── scheduler      # 定时任务执行器
│   │   ├── services       # 业务逻辑（ticket/automation/notification/...）
│   │   └── websocket      # 实时推送骨架
│   ├── docs/              # Swagger 输出（需手动生成）
│   └── main.go            # 应用入口，装配路由及中间件
├── web/                   # React Admin 前端
│   ├── src/admin          # 业务模块（dashboard、tickets、automation、settings、users、notifications）
│   ├── src/components     # UI 组件（Shadcn + MUI 封装）
│   ├── src/lib            # 数据提供器、API/通知工具
│   ├── src/types          # 共享类型定义
│   └── src/utils          # 辅助函数
├── docs/                  # 设计与方案文档
├── dev.sh                 # 本地后端/前端一键起停脚本
├── Makefile               # 常用命令封装
├── docker-compose.yml     # PostgreSQL + Redis + 应用容器编排
└── README.md              # 快速入门与链接
```

## 4. 本地开发与运行
### 4.1 依赖
- Go 1.21+
- Node.js 18+
- PostgreSQL 15、Redis 7（本机或 Docker）
- （可选）Docker & Docker Compose

### 4.2 初始化
```bash
make install-deps          # 安装 Go / Node 依赖
cp server/.env.example server/.env
```

### 4.3 开发运行
- **一键启动**：`./dev.sh start`（或 `./dev.sh restart`）会先清理端口，再分别拉起 `make run`（后端）与 `npm run dev`（前端）。
- **手动启动**：
  ```bash
  # 后端
  cd server && make run

  # 前端
  cd web && npm run dev
  ```
- **Docker**：`docker-compose up -d`，默认暴露 `http://localhost:8081`（API）与 `http://localhost:3000`（前端）。

### 4.4 测试 & 质量
- Go 单元测试：`cd server && go test ./...`（当前全部通过，包含自动化过滤器回归用例）。
- 前端 Lint：`cd web && npm run lint`（⚠️ 仍存在遗留的 `no-explicit-any`、未使用变量等问题，需逐步清理）。
- 其他：仓库附带 Python/PyTest 脚本（`server/tests/`）用于 API 回归，运行前需创建虚拟环境并安装 `requirements-test.txt`。

## 5. 后端服务说明
### 5.1 Routing & 中间件
- Gin 路由在 `main.go` 中组装，公共前缀 `/api`，管理员端在 `/api/admin/**`。
- 核心中间件：请求日志、JWT 鉴权、角色校验、速率限制、CORS。管理员操作启用 `middleware.AdminAudit()`。

### 5.2 关键模块
| 模块 | 主要职责 |
| --- | --- |
| `auth` | 登录、注册、OTP、密码策略。`TODO`：设备记忆、OTP 审计、登录审计落库。 |
| `services/ticket_service.go` | 工单 CRUD、统计、分配、历史记录，支持多条件筛选（已支持逗号分隔的 status/priority）。 |
| `handlers/ticket_workflow_handler.go` | 分配、升级、批量更新等操作入口。 |
| `services/automation_service.go` | 自动化规则 CRUD、执行、日志记录、SLA 计算、Quick Reply。 |
| `handlers/automation_handler.go` | 自动化相关 HTTP API，含 `/rules`、`/logs`、`/sla`、`/templates`、`/quick-replies`。 |
| `services/notification_service.go` | 邮件/站内通知、模板变量（部分 TODO：读取环境配置、持久化审计）。 |
| `services/admin_audit_service.go` | 管理员操作审计日志记录与查询，配合中间件持久化敏感操作。 |
| `scheduler` | 通过 cron 轮询工单执行自动化检查、定时任务。 |
| `websocket` | WebSocket 客户端/集线器骨架，保留 `TODO` 用于未读计数、消息确认。 |

### 5.3 配置
- 所有环境变量映射在 `internal/config`，示例见 `server/.env.example`。
- 常用配置：数据库连接、Redis、JWT、SMTP、上传限制、CORS、日志、速率限制。

### 5.4 已知 TODO / 优化点
- OTP 设备记忆与异常审计仍缺少落库逻辑。
- 模型中诸多 JSON 字段（如 `TicketHistory.Details`）仍以字符串保存，缺失反序列化/验证。
- 通知与 WebSocket 方面仍缺少真实的消息更新与审计落库。

## 6. API 摘要（常用）
> 详情请参考 `server/API_DOCUMENTATION.md`（Swagger 生成后可替换）。

| 分组 | Method | Path | 描述 |
| --- | --- | --- | --- |
| Auth | `POST` | `/api/auth/login` | 账号密码登录，返回 JWT。 |
| Auth | `POST` | `/api/auth/logout` | 注销当前 token。 |
| Tickets | `GET` | `/api/tickets` | 工单分页列表（支持多条件、搜索、排序）。 |
| Tickets | `POST` | `/api/tickets` | 创建工单。 |
| Tickets | `GET` | `/api/tickets/{id}` | 获取工单详情。 |
| Tickets | `PUT/PATCH` | `/api/tickets/{id}` | 更新工单。 |
| Tickets | `DELETE` | `/api/tickets/{id}` | 删除工单（软删）。 |
| Tickets | `GET` | `/api/tickets/stats` | 仪表盘 KPI（含 SLA、待分配等字段）。 |
| Ticket Workflow | `POST` | `/api/tickets/{id}/assign` | 分配工单。 |
| Ticket Workflow | `POST` | `/api/tickets/{id}/escalate` | 升级/转交。 |
| Automation | `GET` | `/api/admin/automation/rules` | 自动化规则列表（支持 `rule_type`/`trigger_event`/`is_active`/`search`）。 |
| Automation | `POST` | `/api/admin/automation/rules` | 创建规则。 |
| Automation | `PUT` | `/api/admin/automation/rules/{id}` | 更新规则。 |
| Automation | `DELETE` | `/api/admin/automation/rules/{id}` | 删除规则。 |
| Automation | `GET` | `/api/admin/automation/rules/{id}/stats` | 单规则执行统计。 |
| Automation | `GET` | `/api/admin/automation/logs` | 自动化执行日志（支持规则、工单、成功状态筛选）。 |
| Automation | `POST` | `/api/admin/automation/sla` | 创建 SLA 配置。 |
| Notifications | `GET` | `/api/admin/notifications/templates` | 通知模板列表。 |
| Analytics | `GET` | `/api/admin/analytics/tickets/timeseries` | 工单趋势（规划中，部分 handler 已占位）。 |

## 7. 前端管理后台说明
### 7.1 模块分布
- `src/admin/dashboard`：仪表盘与 `TicketDashboard`，整合 KPI、趋势、SLA 告警、自动化入口。
- `src/admin/tickets`：工单列表、详情、编辑、表单、批量操作；仪表盘中的紧急/最新工单点击后将直接跳转到详情页。
- `src/admin/automation`：规则 CRUD、规则表单、日志列表（与后台最新过滤参数联动）。
- `src/admin/settings`：系统/邮件/Webhook 配置表单（React-Hook-Form + Zod）。
- `src/admin/notifications`、`src/admin/users`：通知与用户管理界面。
- `src/lib/dataProvider.ts`：React-Admin 与后端之间的 REST 适配器（已补充 automation filters）。

### 7.2 UI & 状态
- 组件基于 MUI + Shadcn，`RatioRow` 等组件确保响应式栅格。
- 数据拉取依赖 React-Admin 内置 `dataProvider`，部分模块结合 TanStack Query 做缓存与刷新。
- 仪表盘提供时间范围切换、手动刷新，紧急/最新工单、过滤器均保持与工单列表一致。

### 7.3 构建与校验
- 启动：`npm run dev`
- 构建：`npm run build`
- Lint：`npm run lint`（目前未通过，需逐步修复 `any` 与未使用变量）。

## 8. 运维与脚本
- **dev.sh**：`start` / `stop` / `restart` / `status`，默认清理 `3000-3005`、`8080-8081` 端口并持久化 PID。
- **Makefile**：封装 build/test/docker/migrate；`test-web` 暂为空操作，可在未来指向 `npm run lint`。
- **docker-compose.yml**：启动 Postgres、Redis、后端、前端容器，适合作为本地一体化环境基础。
- **日志位置**：
  - 后端：`backend.log`
  - 前端：`frontend.log`
  - 单进程 PID：`pids/backend.pid`、`pids/frontend.pid`

## 9. 测试与质量现状
- ✅ `go test ./...` 通过，新增的自动化过滤测试确保逗号分隔参数兼容。
- ⚠️ 前端 ESLint 仍报 70+ 个错误，集中在 automation/settings 等模块；建议分支清理后再接入 CI。
- ⚠️ 多处 `TODO` 涉及安全与稳定（JWT 校验、OTP、管理员校验、WebSocket 未读数）。需排期落地。
- 📄 Python/PyTest、Shell 冒烟脚本仍可手动执行，但尚未纳入统一 CI 流程。

## 10. 下一步建议
1. **补齐安全链路**：完善 OTP 设备记忆及登录审计，拓展 JWT 配置管理。
2. **清理前端 Lint**：统一类型定义，移除遗留 `any` 与无用变量，同时让 `make test-web` 实际运行 lint。
3. **自动化增强**：补充 SLA 工作时间计算、执行动作落库，自动化日志页可增加详情抽屉。
4. **WebSocket 落地**：实现未读数统计、消息确认，与通知中心打通。
5. **文档自动生成**：结合 `swag` 生成 Swagger，保持 `API_DOCUMENTATION.md` 与代码同步更新。

— 文档更新时间：2025-09-20
