# 项目现状摘要（简版）

> 完整、可执行信息请优先参考 `docs/PROJECT_MANUAL.md`。

## 当前实现范围
- 已实现：认证安全、工单 CRUD 与工作流、自动化规则、通知中心、管理员配置与审计、仪表盘。
- 已具备：本地脚本开发模式（`dev.sh`）与 Docker 本地编排（`make dev`）。
- 已具备：Go 单元测试、Pytest 冒烟脚本、前端 lint 入口。

## 当前主要风险
- WebSocket 功能仍偏基础，实时未读同步链路待增强。
- 前端仍有历史 lint 告警，需要分模块清理。
- 历史文档已开始收敛，新增信息需统一更新到权威手册。

## 关键文档
- 权威手册：`docs/PROJECT_MANUAL.md`
- 文档索引：`docs/README.md`
- 测试指南：`docs/testing_guide.md`
- 任务跟踪：`docs/task_backlog.md`
