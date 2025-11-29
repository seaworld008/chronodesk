# ChronoDesk 项目情况综述

> 更新日期：2025-11-14 · 基于 `main` 分支最新代码（commit 81ec7e2）

本文旨在为后续开发者提供一份可执行的项目现状速览，涵盖架构、核心模块、运行方式、测试体系以及当前的风险/待办项。可配合 `docs/project_overview.md`、`docs/testing_guide.md`、`docs/task_backlog.md` 等文件交叉阅读。

## 1. 项目定位与功能边界
- **产品目标**：ChronoDesk 是一套多角色（Admin / Agent / User）工单管理平台，覆盖工单创建 → 指派 → 升级 → 自动化 → SLA 监控 → 仪表盘展示，同时提供安全链路（OTP、可信设备）、通知中心（邮件 / Webhook / WebSocket）和系统配置中心。
- **技术栈**：Go 1.21 + Gin + GORM + PostgreSQL + Redis 构成 API；前端为 Vite + React 18 + React-Admin + MUI + Shadcn；配有 Makefile、`dev.sh`、Docker Compose、Pytest 冒烟脚本等配套工具。
- **运行模式**：`./dev.sh start` 一键拉起 server(8081) + web(3000)；`docker-compose.yml` 提供容器化编排；`make build` / `make test` 统一构建与测试流程。

## 2. 目录与代码组织
- `server/`：Go 后端主目录，包含 `cmd/migrate`、`internal/*`、`main.go`、Swagger 文档、Pytest 资源、脚本与测试报告。目录下存在若干已编译二进制（`server`、`main`、`gongdan-system`、`ticket-system`），需要通过 `make clean` 清理后再提交。
- `web/`：React Admin 管理端，`src/admin` 为业务模块（dashboard、tickets、automation、settings、notifications、users、security），`src/lib` 提供数据/认证适配器，`src/layout` 与 `src/components` 封装 UI。
- `src/components/ui/`：仓库共享的 Shadcn primitives（`alert-dialog.tsx`、`avatar.tsx`、`checkbox.tsx`、`skeleton.tsx`），供前端或未来多端复用。
- `docs/`：设计与流程资料（project_overview、testing_guide、task_backlog、test_plan、dashboard_redesign、handovers、planning 等）。
- 根目录脚本/工具：`Makefile`、`dev.sh`、`docker-compose.yml`、`test_integration.sh`、`test_cleanup_functionality.sh`、`deploy_production.sh` 及若干日志(`backend.log`、`frontend.log`、`server-dev.log` 等)与 PID 目录。

## 3. 后端服务（`server/`）概述
### 3.1 启动流程
`main.go` 负责：加载 `.env`（`godotenv`）、读取 `internal/config`、初始化 PostgreSQL + Redis（`internal/database` 支持 TCP 与 Upstash HTTP 客户端）、选择 Gin 模式、可选的自动迁移、构建认证模块 (`internal/auth`)、启动调度器（`services.NewSchedulerService`）及清理调度（`internal/scheduler`）。随后组装 Gin Router：
- `/healthz` 与 `/api/health` 健康检查。
- `/api/auth/*` 登录、刷新、OTP、记住设备等；内部利用 `ginAdapter` 将 `auth.Handler` 接入。
- `/api/tickets/*` CRUD、历史、统计、批量操作、升级/转派等；由 `TicketHandler` + `TicketWorkflowHandler` 驱动。
- `/api/user/*` 个人资料、密码修改、登录历史、可信设备、头像上传与统计。
- `/api/notifications` 提供通知列表/偏好/已读接口，管理员可创建通知。
- `/api/admin/*` 权限管理（用户 CRUD、审计日志、系统配置、清理任务、全局配置、Analytics、自动化、Webhook）。
- `/api/webhooks` 管理 Webhook 配置/日志/统计；`/api/ws` 暴露 WebSocket 通道（当前仍为基础实现，TODO：区分用户及未读数推送）。
- `/api/redis/test` 快速验证 Redis 连接。

### 3.2 核心模块
- `internal/auth/`：封装注册/登录/刷新/OTP/邮箱验证/密码重置/可信设备，`handler.go` 提供 Gin 无关接口，`jwt.go` 负责 token 签发解析，`otp.go` 支持备份码。
- `internal/services/`：
  - `ticket_service.go` + `ticket_workflow_handler.go`：提供 Ticket CRUD、批量指派/状态调整、统计（含 SLA 违约、我的工单、未分配等），同时写入 `TicketHistory` 并调用 `NotificationService`。
  - `automation_service.go`：管理规则/SLA/模板/快速回复；解析 JSON 条件、执行动作、记录执行日志、维护统计（含 `RuleStats`）；与 `SchedulerService` 协同周期执行。
  - `scheduler_service.go`：应用级任务调度（SLA 检查、自动化轮询、清理过期数据、统计刷新等），支持 Cron 表达式、运行状态记录、手动触发。
  - `cleanup_service.go` + `internal/scheduler`：针对登录历史等数据的定时清理，提供配置化保留天数、批处理大小及执行日志。
  - `notification_service.go` / `email_notification_service.go`：整合站内通知、Webhook、邮件发送，与 `email_config_service.go`、`websocket` 模块配合；支持通知偏好、未读统计与模板变量（部分 TODO：环境参数、未读回执）。
  - `admin_user_service.go`、`admin_audit_service.go`、`analytics_service.go`、`config_service.go`、`email_config_service.go`、`trusted_device_service.go`、`user_service.go` 等负责管理员管理、操作日志、仪表盘统计、系统配置、邮箱连接测试、可信设备与用户画像。
  - `escalation_service.go`、`enhanced_ticket_service.go`：提供 SLA 升级、仪表盘指标等扩展能力。
- `internal/handlers/`：对应服务层封装 HTTP 输入/输出：`admin_user_handler`、`admin_audit_handler`、`analytics_handler`、`automation_handler`、`config_handler`、`system_handler`（配置 + 清理执行 + 日志）、`email_config_handler`、`notification_handler`、`ticket_handler`、`ticket_workflow_handler`、`user_handler`、`webhook_handler` 等。
- `internal/middleware/`：环境化的 CORS/日志/速率限制/JWT/角色校验配置，`LogAdminOperation` 会把管理员请求写入审计日志。
- `internal/websocket/`：`hub.go`/`client.go` 管理连接，`notification_service.go` 提供向在线用户推送通知的骨架（TODO：未读计数、确认回写）。
- `internal/models/`：GORM 模型（Ticket/TicketHistory/TicketComment、AutomationRule/ExecutionLog、Notification/Preference/WebhookConfig/Log、SystemConfig、CleanupConfig、AdminAuditLog、TrustedDevice 等）。大量字段存储 JSON（`jsonb`）并提供封装的 `SetValue`、`GetValue`、`StringList` 类型。
- `internal/database/`：PostgreSQL 连接池、Redis TCP/HTTP 客户端、`RunMigrations`、`fast_migrate`、`sample_data`。`DATABASE_MIGRATION.md` 和 `create_notification_tables.sql` 补充说明。

## 4. 前端管理后台（`web/`）
- **入口**：`src/main.tsx` → `<AdminApp />`。`AdminApp.tsx` 自定义 MUI 主题、菜单、AppBar、Layout，并注册 React-Admin 资源：`tickets`、`users`、`notifications`、`automation-rules`、`automation-logs`，同时通过 `CustomRoutes` 注入系统设置、邮件/Webhook 设置、可信设备页面。
- **认证/数据层**：
  - `src/lib/authProvider.ts`：处理登录/注销/刷新，支持 OTP、trusted device token (`trustedDeviceToken`)、`remember_device` 逻辑，自动续签并进行管理员资源保护。
  - `src/lib/dataProvider.ts`：将 React-Admin 的分页、排序、过滤转换为 Go API 期望的 Query 参数（含 tickets 复合筛选、automation logs/rules 特殊过滤）。
  - `src/lib/apiClient.ts`、`retry.ts`、`validators.ts`、`utils.ts`：提供封装的 `fetch`、重试与表单校验。
- **业务模块**：
  - `src/admin/tickets/`：包含 `TicketList`（状态/优先级/类型筛选、批量按钮、`StatusChip`、`PriorityChip` 等 UI）、`TicketDashboard`（KPI、趋势、SLA 告警、快速入口）、`TicketShow`/`TicketEdit`/`TicketCreate`/`TicketWorkflowActions`、`TicketBulkUpdateButton`。
  - `src/admin/automation/`：规则 CRUD（Form/Show/Edit）、日志列表（过滤 rule_id / ticket_id / success）、SLA 配置与快速回复操作由后端接口支撑。
  - `src/admin/settings/`：系统/邮箱/Webhook/安全/工作时间/清理配置表单（React Hook Form + Zod），`SimpleWorkingSystemSettings` 作为设置总览页。
  - `src/admin/notifications/`、`src/admin/users/`、`src/admin/security/TrustedDevices.tsx`、`src/admin/common/BackButton.tsx` 等补充模块。
- **UI 与布局**：`src/layout/CustomLayout.tsx`、`CustomAppBar.tsx` 自定义 RA 布局；`src/components/auth/LoginPage.tsx` 提供定制登录体验；`src/components/layout/RatioRow.tsx`、`styles/` 负责响应式布局。
- **其他**：`AdminApp` 旁保留 `TestApp*.tsx` 作为实验入口；`types/automation.ts`、`types/index.ts` 汇总共享类型；`utils/date.ts` 封装时间显示。

## 5. 共享 UI（`src/components/ui/`）
仓库根 `src/components/ui` 存放 Shadcn 组件（`alert-dialog`、`avatar`、`checkbox`、`skeleton`），按 `@/*` alias 引入，可供 React Admin 或未来独立前端页面使用，确保任一新增组件遵循相同目录结构便于复用。

## 6. 运行与运维工具
- **Makefile**：封装 `build-server`、`build-web`、`test`、`server-dev`、`web-dev`、`db-migrate`、`swagger`、`docker-up/down`、`install-deps` 等常用指令；`make test-web` 现已代理 `npm run lint` 以确保前端质量门槛一致。
- **`dev.sh`**：管理 PID、端口清理、server/web 启停与状态检查，输出日志到根目录 (`backend.log`、`frontend.log`)。
- **`docker-compose.yml`**：定义 Postgres、Redis、server、web 容器，默认暴露 8081/3000；server 容器挂载本地代码并保持 `GIN_MODE=debug`。
- **脚本与日志**：`test_integration.sh`、`test_cleanup_functionality.sh`、`server/test_notification_system.sh` 等 Shell；`server/test_*.py` Python 用于专项验证；`server/reports/` 保存 Pytest HTML；`backend_agent.log`、`frontend_run.log` 等用于排查历史运行状态。
- **其他资源**：`server/requirements-test.txt` 管理 Pytest 依赖；`server/api_test_summary.md`、`server/project_status_report.json` 记录历史测试/状态；`create_notification_tables.sql`、`fix_email_configs.py` 等脚本辅助迁移与修复。

## 7. 测试与质量体系
- Go 层：`make test-server` → `go test ./...`，涵盖 Ticket/Automation/TrustedDevice 等单测；`make fmt`/`make vet` 维持风格。
- 前端：`npm run lint` 仍存在历史报错（`any`、未使用变量等），但可直接通过 `make test-web` 触发，保持与后端 `make test-server` 一致的入口。
- Pytest：`server/tests/` 结构化 `auth`、`tickets`、`automation`、`system`、`utils`，`docs/test_plan.md` 描述分阶段目标；`make smoke` 聚合执行并输出 HTML 报告。
- 脚本级冒烟：根目录 `test_integration.sh`、`server/test_notification_system.sh`、`test_system_settings.py` 等针对邮件/通知/设置路径。
- 文档：`docs/testing_guide.md`、`docs/test_plan.md` 梳理执行顺序、依赖与风险，提交前需在 PR 描述记录已跑检查。

## 8. 文档与规划资源
- `README.md` 提供快速入门、账号、API 示例；`API_DOCUMENTATION.md`、`REQUIRED_API_ENDPOINTS.md` 保持接口快照；`server/DATABASE_MIGRATION.md` 解释迁移机制。
- `docs/project_overview.md`（总体架构）、`docs/testing_guide.md`（测试流程）、`docs/task_backlog.md`（待办拆解）、`docs/test_plan.md`（自动化测试计划）。
- `docs/planning/` 与 `docs/handovers/` 收录任务跟踪、迭代计划、交接说明、UX 方案；`docs/dashboard_redesign.md` 针对仪表盘。
- 额外说明文档包括 `test-backend-config.md`、`test-database-models.md`、`test-init.md` 等，用于实验记录。

## 9. 已知风险与待办
- **WebSocket & 通知**：`internal/websocket` 仍缺少未读计数与消息确认（`docs/task_backlog` 的 WS-NOTICE-01/02/03）；`NotificationService` 中的环境变量读取留有 TODO。
- **自动化日志**：`automation_service` 尚未把 `actions_executed`、`changes` 全量写入日志，SLA 工作时间计算待补齐（AUTO-LOG-01/02/03）。
- **JSON 字段反序列化**：TicketHistory、TicketComment、Notification 等 JSONB 字段仍以字符串操作，需引入统一类型（MODEL-JSON-01/02）。
- **前端质量**：`npm run lint` 仍有若干遗留，但已可通过 `make test-web` 统一触发，方便在 CI/本地维持一致门槛。
- **Swagger/文档同步**：`make swagger` 需纳入常规流程，README / 文档待更新（DOC-01/02）。
- **编译产物**：`server/` 下存在多个几十 MB 的可执行文件（`server`、`main`、`gongdan-system`、`ticket-system`），需在提交前清理以免污染仓库。
- **WebSocket 用户区分**：`/api/ws` 连接仅通过 `user_id` 注入 `ws_user_id`，尚未对不同用户分组/安全通道做处理。
- **Redis/Email 配置**：部分默认值写死在代码中（如 `notification_service`、`email_notification_service`），后续需通过系统配置中心或环境变量统一管理。

## 10. 下一步建议
1. **完成 task backlog**：优先解决 WebSocket 未读/回执、自动化日志、JSON 类型化等高影响任务；前端可通过 `make test-web` 执行 `npm run lint`，后续可纳入 CI。
2. **强化 CI**：在 CI 中串联 `go test`、`npm run lint`、`make smoke`，并生成/上传报告，确保 `docs/testing_guide` 中列出的检查都可自动化执行。
3. **清理产物与依赖**：在构建/测试后执行 `make clean` 或 Git clean，避免可执行文件进入版本控制；同时梳理 `server/bin`、`logs/`、`pids/` 的持久化策略。
4. **完善通知链路**：实现 WebSocket 未读同步、邮件配置环境化、Webhook 日志的详情抽屉（前端）等，使通知中心真正闭环。
5. **文档同步**：每次架构/接口调整后同步更新 `docs/project_overview.md`、`docs/task_backlog.md`、`server/API_DOCUMENTATION.md`，并考虑在 README 中标记 Swagger 生成命令与测试指令。

> 如需进一步深入某模块，可从对应源码与文档对照入手，例如：Ticket 逻辑查看 `server/internal/services/ticket_service.go` + `web/src/admin/tickets/*`；自动化查看 `server/internal/services/automation_service.go` + `web/src/admin/automation/*`；系统配置查看 `server/internal/handlers/system_handler.go` + `web/src/admin/settings/*`。
