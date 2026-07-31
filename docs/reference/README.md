# Reference 文档

本目录存放 ChronoDesk 当前机器契约与 Agent 协议参考。运行时 `/openapi.yaml` 和 `server/internal/openapi/openapi.yaml` 是请求/响应 Schema 的唯一权威来源。

## 当前内容

- [API 使用说明](API_DOCUMENTATION.md)：项目显式的 `/api/v2/projects/{projectKey}` Agent 机器入口、Human `/api/platform`、`/api/projects`、`/api/workbench` 边界和协议发现地址。
- [MCP 2026-07-28](MCP_2026_07_28.md)：无状态 Streamable HTTP、最新单版本约束、OAuth Client Credentials 扩展与 Inspector。
- [A2A 1.0](A2A_1_0.md)：Agent Card、严格版本头、JSON-RPC、SSE、Task、Artifact 与 Push。
- [CloudEvents 1.0](CLOUDEVENTS_1_0.md)：领域事件信封、合法扩展属性、Outbox 消费和去重约束。
- [OpenAPI 3.2](OPENAPI_3_2.md)：Agent 单版本机器契约、Redocly/Spectral 严格门禁与管理员并发契约；Human Web P1 契约由 `/human-openapi.json` 独立发布。
- [数据库敏感字段静态加密](DATA_AT_REST_ENCRYPTION.md)：AES-256-GCM keyring、显式迁移、轮换与投递日志安全。
- [集成 SDK](INTEGRATION_SDKS.md)：Go、TypeScript、Python SDK 的生成、校验与发布边界。
- [集成工具](INTEGRATION_TOOLING.md)：`chronodeskctl`、Connection 测试和 Webhook 回放的运维入口。
- [AI 原生多项目升级开发检查点](AI_NATIVE_UPGRADE_PROGRESS.md)：当前实现、未闭环能力、验证状态和下一次恢复入口。

## 使用建议

- 项目全景、启动与迁移流程见 [项目权威手册](../PROJECT_MANUAL.md)。
- SDK 生成和 Agent 接入必须直接读取 `/openapi.yaml`；Human Web 类型与路由读取 `/human-openapi.json`。不要从手工文档复制 Schema，也不要将其中一个契约外推给另一调用方。
- ChronoDesk 只实现 MCP `2026-07-28`、A2A `1.0`、OpenAPI `3.2.0` 与 CloudEvents `1.0`，不保留旧协议兼容分支。
