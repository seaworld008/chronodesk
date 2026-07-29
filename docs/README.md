# 文档中心（docs）

本目录是 ChronoDesk 的统一文档入口。项目当前面向人类后台和外部 AI Agent；接口、协议与命令说明均以当前代码和机器契约为准。

## 1. 必读文档

- [项目权威手册](PROJECT_MANUAL.md)：功能、架构、技术栈、迁移、API 分层、安全与范围边界。
- [根目录快速开始](../README.md)：安装、启动和常用质量门禁。
- [测试与质量控制指南](testing_guide.md)：Go、前端、OpenAPI、Pytest 与集成测试顺序。

## 2. 参考文档（Reference）

- [API 使用说明](reference/API_DOCUMENTATION.md)：`/api/v1` Agent 机器入口、`/api` 人类入口和运行时 OpenAPI。
- [MCP 2026-07-28](reference/MCP_2026_07_28.md)：最新单版本 MCP、OAuth Client Credentials 和 Inspector。
- [A2A v1.0.1](reference/A2A_1_0.md)：官方最新发布，线协议固定为
  `A2A-Version: 1.0`，覆盖 Agent Card、JSON-RPC、SSE 与 Push。
- [CloudEvents 1.0](reference/CLOUDEVENTS_1_0.md)：领域事件信封、扩展属性与去重。
- [OpenAPI 3.2](reference/OPENAPI_3_2.md)：机器契约与严格 lint 门禁。
- [数据库静态加密](reference/DATA_AT_REST_ENCRYPTION.md)：keyring、显式凭据迁移和轮换。
- [Reference 索引](reference/README.md)：全部当前参考资料。

## 3. 规划与交接

- [`planning/`](planning/)：任务跟踪、阶段计划与里程碑。
- [`plans/`](plans/)：按日期组织的实现计划。
- [`handovers/`](handovers/)：会话交接资料。

## 4. 测试报告

- [Agent 原生化完整测试报告](testing/CHRONODESK_AGENT_NATIVE_FULL_TEST_REPORT_2026-07-29.md)：MCP/A2A、云 PostgreSQL/Redis、API 与浏览器功能验收。

## 5. 历史归档

- [`archive/root-legacy/`](archive/root-legacy/)：过期 API、初始化和一次性测试资料。
- [`archive/agent-control/`](archive/agent-control/)：根目录 Agent 控制文件的保留说明。

## 6. 文档维护规则

- 架构或接口变更时先更新 [项目权威手册](PROJECT_MANUAL.md)。
- 请求/响应 Schema 只维护在 `server/internal/openapi/openapi.yaml`，运行时由 `/openapi.yaml` 提供。
- 新增专题文档必须登记到本索引；失效内容移动到 `archive/`，不得混入当前操作指南。
