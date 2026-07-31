# ChronoDesk 文档中心

本目录只保留当前可执行文档、架构决策、协议参考、运维指南和可复核测试报告。
历史计划与会话交接材料由 Git 历史保留，不作为当前事实来源。

## 从这里开始

- [项目权威手册](PROJECT_MANUAL.md)：功能、范围、技术基线和公共 Interface。
- [架构总览](../ARCHITECTURE.md)：Module、Interface、Seam、Adapter 和依赖规则。
- [领域词汇](../CONTEXT.md)：Ticket、Actor、Assignment、Lease、Event 与 Outbox
  的统一含义。
- [开发与质量门禁](testing_guide.md)：本地、Docker、云服务、协议和发布验证。

## 架构决策

- [ADR 索引](adr/README.md)
- [只保留当前 Agent 协议](adr/0001-current-only-agent-protocols.md)
- [统一 Actor 与 Assignment 模型](adr/0002-actor-and-assignment-model.md)
- [事务 Domain Event 与 Outbox](adr/0003-transactional-events-and-outbox.md)
- [最小可执行入口与应用组合根](adr/0004-application-composition-root.md)
- [Project 是运行时安全与配置边界](adr/0005-project-is-the-runtime-boundary.md)
- [平台角色与项目角色分离](adr/0008-separate-platform-and-project-roles.md)

## 运维

- [数据库迁移](operations/database-migrations.md)
- [数据库静态加密](reference/DATA_AT_REST_ENCRYPTION.md)

## 协议与机器契约

- [Agent REST 与 API 使用说明](reference/API_DOCUMENTATION.md)
- [MCP 2026-07-28](reference/MCP_2026_07_28.md)
- [A2A v1.0.1 / wire 1.0](reference/A2A_1_0.md)
- [CloudEvents 1.0](reference/CLOUDEVENTS_1_0.md)
- [OpenAPI 3.2](reference/OPENAPI_3_2.md)
- [Reference 索引](reference/README.md)
- [AI 原生多项目状态](reference/AI_NATIVE_UPGRADE_PROGRESS.md)

Agent 请求/响应 Schema 的唯一权威来源是
`server/internal/openapi/openapi.yaml`，运行时由 `/openapi.yaml` 提供。Human Web
P1 另由 `server/internal/humanopenapi/openapi.json` 和运行时
`/human-openapi.json` 提供；两个契约分别服务不同调用方，不应相互推断覆盖范围。

## 测试证据

- [Agent 原生化完整测试报告](testing/CHRONODESK_AGENT_NATIVE_FULL_TEST_REPORT_2026-07-30.md)

## 维护规则

1. 领域术语变化先更新 `CONTEXT.md`。
2. 跨 Module 的持久架构选择必须增加 ADR。
3. Interface、角色、路由、迁移或运行命令变化同步更新 `PROJECT_MANUAL.md`、根 README、测试指南和相应 ADR/参考页。
4. 失效计划、提示词、一次性报告和本机路径不得继续留在当前文档树。
