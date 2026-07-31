# ChronoDesk API 使用说明

ChronoDesk 面向外部 Agent 的权威机器契约由服务内嵌的 OpenAPI 3.2
文档提供：

- 本地地址：`http://localhost:8081/openapi.yaml`
- Agent 稳定 REST 根路径：`http://localhost:8081/api/v2/projects/{projectKey}`
- 人类管理端 REST 根路径：`http://localhost:8081/api`
- MCP：`POST http://localhost:8081/mcp`
- A2A：`POST http://localhost:8081/a2a/v1`
- A2A Agent Card：`GET http://localhost:8081/.well-known/agent-card.json`

`/openapi.yaml` 覆盖项目显式 `/api/v2/projects/{projectKey}`、OAuth/发现入口和
Agent 管理控制面。Agent SDK 生成、契约测试和外部接入都必须直接读取
`/openapi.yaml`，不得从本文件复制请求或响应 Schema。浏览器后台使用的 Human
P1 契约单独发布在 `http://localhost:8081/human-openapi.json`，只供同仓库 Web
客户端生成类型和路由，不得替代对外 Agent 机器契约。

## Agent REST 契约

`/api/v2/projects/{projectKey}` 使用独立服务主体和短期、单项目 OAuth 访问令牌，支持：

- RFC 8707 资源指示器以及独立的 MCP、REST、A2A audience。
- 最小权限 scope 与显式策略决策。
- `Idempotency-Key` 幂等写入。
- `ETag` / `If-Match` 乐观并发控制。
- Agent 工单租约、心跳和所有权校验。
- RFC 9457 风格的 `application/problem+json` 稳定错误。
- CloudEvents 1.0、可靠 Outbox 与不透明事件游标。

可用 scope、请求 Schema、错误码和 Webhook 定义均以 OpenAPI 文件为准。

## 人类管理端

`/api` 由浏览器管理后台使用，采用人类 JWT 会话、对象级工单授权和两层角色权限。
平台角色是封闭枚举 `platform_admin`、`security_auditor`、
`emergency_operator`、`member`；项目职责只来自显式 Membership 中的
`project_admin`、`manager`、`agent`、`requester`、`observer`。平台角色不会
隐式扩大普通项目或跨项目工作台范围，服务主体身份也不复用人类角色。数据库迁移
会将历史角色值一次性收敛到当前枚举，旧角色令牌不会继续获得访问权限。
评论、附件、通知、自动化、用户、系统设置和 Agent 控制中心均复用领域服务，
不通过 MCP 或 A2A 回调自身的 HTTP 接口。

该人类 REST 接口只服务同仓库 Web 客户端。`/human-openapi.json` 是当前发布的
完整 Human Web P1 机器契约，也是请求/响应 Schema 的唯一来源；Web 必须使用由
该契约生成的 `humanApiOperations`、`humanApiRoutes` 和 TypeScript 类型，不得
手写重复的 P1 路径或 DTO。生成 freshness、处理器契约测试和 CI 门禁共同阻止
契约与实现漂移。未列入该文档的 Human 路由属于未契约遗留面，不应被推断为已
发布能力，也不承诺供第三方 SDK 生成。需要机器稳定能力时应使用项目显式 Agent
REST v2、MCP 或 A2A。

项目范围解析或即时重校验失败时，Human Web 返回稳定错误码
`project_access_revoked`。浏览器只有在响应路径中的 Project Key 与当前选择
一致时才清理该选择；`project_role_denied`、工单对象 ACL 和其他普通 `403`
只拒绝当前操作，不改变仍然有效的项目上下文。

## 协议版本

ChronoDesk 只实现当前单版本协议，不包含旧协议兼容分支：

- MCP `2026-07-28`
- A2A 官方发布 `v1.0.1`（线协议 `1.0`）
- CloudEvents `1.0`
- OpenAPI `3.2.0`

详细约束见同目录下的协议专题文档。
