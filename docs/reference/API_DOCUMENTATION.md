# ChronoDesk API 使用说明

ChronoDesk 面向外部 Agent 的权威机器契约由服务内嵌的 OpenAPI 3.2
文档提供：

- 本地地址：`http://localhost:8081/openapi.yaml`
- Agent 稳定 REST 根路径：`http://localhost:8081/api/v1`
- 人类管理端 REST 根路径：`http://localhost:8081/api`
- MCP：`POST http://localhost:8081/mcp`
- A2A：`POST http://localhost:8081/a2a/v1`
- A2A Agent Card：`GET http://localhost:8081/.well-known/agent-card.json`

`/openapi.yaml` 覆盖 `/api/v1`、OAuth/发现入口和 Agent 管理控制面；浏览器
后台使用的人类 REST 接口 `/api` 不属于对外机器契约。Agent SDK 生成、
契约测试和外部接入都必须直接读取 `/openapi.yaml`，不得从本文件复制请求或
响应 Schema。

## Agent REST 契约

`/api/v1` 使用独立服务主体和短期 OAuth 访问令牌，支持：

- RFC 8707 资源指示器以及独立的 MCP、REST、A2A audience。
- 最小权限 scope 与显式策略决策。
- `Idempotency-Key` 幂等写入。
- `ETag` / `If-Match` 乐观并发控制。
- Agent 工单租约、心跳和所有权校验。
- RFC 9457 风格的 `application/problem+json` 稳定错误。
- CloudEvents 1.0、可靠 Outbox 与不透明事件游标。

可用 scope、请求 Schema、错误码和 Webhook 定义均以 OpenAPI 文件为准。

## 人类管理端

`/api` 由浏览器管理后台使用，采用人类 JWT 会话、对象级工单授权和角色权限。
人类账号角色是封闭枚举，仅允许 `admin`、`supervisor`、`agent`、`customer`；
服务主体身份不复用人类角色。数据库迁移会将历史角色值一次性收敛到当前枚举，
旧角色令牌不会继续获得访问权限。
评论、附件、通知、自动化、用户、系统设置和 Agent 控制中心均复用领域服务，
不通过 MCP 或 A2A 回调自身的 HTTP 接口。

该人类 REST 接口只服务同仓库 Web 客户端，以后端路由、处理器回归测试和
`web/src/lib/dataProvider.ts` 的调用契约为准；不承诺供第三方 SDK 生成。需要
机器稳定能力时应使用 `/api/v1`、MCP 或 A2A。

## 协议版本

ChronoDesk 只实现当前单版本协议，不包含旧协议兼容分支：

- MCP `2026-07-28`
- A2A 官方发布 `v1.0.1`（线协议 `1.0`）
- CloudEvents `1.0`
- OpenAPI `3.2.0`

详细约束见同目录下的协议专题文档。
