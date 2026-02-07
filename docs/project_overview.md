# 项目总览（简版）

本文件作为快速总览入口，完整信息请以 `docs/PROJECT_MANUAL.md` 为准。

## 系统定位
ChronoDesk 是面向客服/运营团队的工单管理系统，支持多角色权限、工单工作流、自动化规则、通知中心、系统配置和运营分析。

## 技术架构
- 后端：Go + Gin + GORM + PostgreSQL + Redis
- 前端：React 18 + React-Admin + MUI + Vite
- 工具链：Makefile、`dev.sh`、Docker Compose、Go test、Pytest

## 核心入口
- API 启动与路由：`server/main.go`
- 前端入口：`web/src/AdminApp.tsx`
- 文档索引：`docs/README.md`

## 开发与运行
```bash
./dev.sh start
# API: http://localhost:8081
# Web: http://localhost:3000
```

## 下一步阅读建议
1. `docs/PROJECT_MANUAL.md`（权威手册）
2. `docs/testing_guide.md`（测试流程）
3. `docs/task_backlog.md`（待办与风险）
