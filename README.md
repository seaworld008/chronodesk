# ChronoDesk

> ChronoDesk – A modern support ticket platform powered by Go (Gin) + PostgreSQL and React + TypeScript. It features OTP-secured authentication, automation rules, configurable system settings, and end-to-end pytest smoke suites for CI/CD。需要更深入的架构说明，请查看 [docs/project_overview.md](docs/project_overview.md)。

## 1. 核心特性
- 多角色权限：Admin / Agent / User；JWT 鉴权 + 密码强度校验。
- 工单全流程：创建、指派、升级、批量操作、历史追溯、SLA 告警。
- 仪表盘：KPI、趋势、风险、自动化入口，列表点击直达详情。
- 自动化：规则/动作配置、执行日志、SLA 模板、快速回复。
- 通知中心：邮件/Webhook 模板、WebSocket 骨架预留未读计数。
- 配置中心：系统、邮箱、Webhook 配置表单，支持实时验证。

## 2. 技术栈一览
| 层级 | 说明 |
| --- | --- |
| Backend | Go 1.21 · Gin · GORM · PostgreSQL · Redis · JWT · Swagger |
| Frontend | React 18 · TypeScript · React-Admin · MUI · Shadcn/UI · Vite |
| 工具 | Makefile · dev.sh · Docker Compose · ESLint/Prettier |

更多细节（目录、模块、API 摘要、待办）请见：[项目总览](docs/project_overview.md)。

## 3. 快速开始
### 3.1 克隆 & 依赖
```bash
git clone <repo-url>
cd gongdan-system
make install-deps              # go mod tidy + npm install
cp server/.env.example server/.env
```

### 3.2 一键起停
```bash
./dev.sh start                 # 启动后端 (8081) 与前端 (3000)
./dev.sh restart               # 重启，自动清理端口
./dev.sh stop                  # 停止服务并清理 PID
```

### 3.3 手动运行
```bash
# 后端
cd server && make run          # 等价 go run main.go

# 前端
cd web && npm run dev          # 默认 http://localhost:3000
```

### 3.4 Docker 环境
```bash
docker-compose up -d           # 启动 PostgreSQL + Redis + 后端 + 前端
```

## 4. 常用 Make 命令
| 命令 | 说明 |
| --- | --- |
| `make build` | 构建 server (`bin/server`) + web (`dist/`) |
| `make test` | 运行 `go test ./...`（前端测试占位） |
| `make server-dev` / `make web-dev` | 单独启动后端 / 前端开发模式 |
| `make db-migrate` | 执行数据库迁移 (`cmd/migrate`) |
| `make swagger` | 基于 `swag` 生成 Swagger 文档 |
| `make docker-up` / `make docker-down` | Docker Compose 启动 / 停止 |

## 5. 运行与监控
- 后端日志：`backend.log`
- 前端日志：`frontend.log`
- 运行状态：`./dev.sh status`
- PID 文件：`pids/backend.pid`、`pids/frontend.pid`

## 6. 测试与质量
完整执行顺序及命令记录在 [测试与质量控制指南](docs/testing_guide.md)。常用入口：

- **后端单测**：`cd server && go test ./...`
- **前端 Lint**：`cd web && npm run lint`（历史告警待逐步清理）
- **Pytest 冒烟**：`cd server && make smoke`（报告位于 `server/reports/`）
- **集成脚本**：`./test_integration.sh`、`server/test_notification_system.sh`

## 7. 账号与权限
### 7.1 默认/种子账号
| 角色 | 用户名 | 邮箱 | 密码 | 状态 |
| --- | --- | --- | --- | --- |
| admin | admin | admin@example.com | Admin123! | active |

> 通过 `make db-migrate` 或 `go run cmd/migrate/main.go -seed` 创建。

### 7.2 手动创建示例
| 角色 | 用户名 | 邮箱 | 密码 | 说明 |
| --- | --- | --- | --- | --- |
| user | superuser | superuser@example.com | SecurePass2025!@# | API 测试 |

### 7.3 密码策略
- ≥ 8 位，必须包含大小写字母 + 数字 + 特殊字符
- 禁用常见弱密码 (`admin`, `password`, `123456` 等)
- 不得与用户名/邮箱一致

### 7.4 可信设备策略
- 支持“记住设备”跳过后续 OTP 校验，重登时带上 `device_token`
- 后端可在系统配置中调节策略：
  - `security.trusted_device_ttl_hours`（默认 720 小时 / 30 天）
  - `security.trusted_device_max_per_user`（默认 5 台，设为 0 则不限）
- 用户可在后台“账号安全”页面查看并撤销可信设备

## 8. API 快照
常用接口示例：
```bash
# 登录获取 token
curl -X POST http://localhost:8081/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"superuser@example.com","password":"SecurePass2025!@#"}'

# 登录并记住当前设备（返回 trusted_device_token，需妥善存储并作为 device_token 参与后续登录）
curl -X POST http://localhost:8081/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{
        "email":"superuser@example.com",
        "password":"SecurePass2025!@#",
        "otp_code":"123456",
        "remember_device":true,
        "device_name":"Personal MacBook"
      }'

# 携带 token 访问工单列表
curl -H 'Authorization: Bearer <token>' http://localhost:8081/api/tickets

# 查看管理员操作审计日志
curl -H 'Authorization: Bearer <token>' "http://localhost:8081/api/admin/audit-logs?page=1&limit=20"
```

完整接口描述请参考：[server/API_DOCUMENTATION.md](server/API_DOCUMENTATION.md)。

## 9. 已知待办与风险
- 可信设备记忆 / 登录审计已上线，可通过 `/api/user/trusted-devices` 自助查看与撤销；后续可根据需要加入后台 TTL/配额配置。
- WebSocket 仅搭建骨架，未实现未读数/消息确认。
- 多数 GORM 模型的 JSON 字段尚未反序列化，后续可引入自定义类型增强校验。
- 前端 ESLint 未通过；`make test-web` 仍为空实现，建议接入 lint/UT。

## 10. 参考资料
- [项目总览 · 架构/模块/API/待办](docs/project_overview.md)
- [API 文档 (Markdown)](server/API_DOCUMENTATION.md)
- [仪表盘改版方案](docs/dashboard_redesign.md)
- [自动化测试脚本](server/tests/)

欢迎根据文档继续扩展或修复功能，提交前请确保：`go test ./...` 通过，并逐步推动前端 Lint 清零。
