# ChronoDesk OpenAPI 3.2

ChronoDesk 只发布 OpenAPI `3.2.0` 机器契约，不保留旧 OpenAPI 文档或
降级分支。规范由服务端嵌入并通过 `/openapi.yaml` 原样提供。

## 契约门禁

仓库固定使用当前最新 Redocly CLI `2.41.1` 和 Spectral CLI `6.16.2`。
发布前必须依次执行：

```bash
make openapi-lint
cd server
go test ./internal/openapi -count=1
cd internal/openapi
npx --yes @redocly/cli@2.41.1 lint openapi.yaml \
  --extends recommended-strict --format=stylish
npx --yes @stoplight/spectral-cli@6.16.2 lint openapi.yaml \
  --ruleset .spectral.yaml --fail-severity warn --display-only-failures
```

所有检查必须保持零 error、零 warning。Go 契约测试还会拒绝重复 YAML
映射键，以及 `required`、`enum`、`allOf`、`anyOf`、`oneOf`、
`parameters`、`security` 和 `tags` 中的重复结构。新增或修改接口时，需要
同步提供唯一 `operationId`、OAuth scope、结构化错误、封闭 JSON Schema、
示例和至少一个 4xx 响应。

Spectral `6.16.2` 内置的是 OpenAPI 3.0 元 Schema，会把 3.2 合法的
`jsonSchemaDialect`、类型联合、`const`、`webhooks` 和 SPDX license
identifier 误判为错误，其旧 Nimma enum 选择器还会在 3.2 Schema 上崩溃。
因此 `.spectral.yaml` 只关闭这三项已知不兼容规则；OpenAPI 3.2 的完整
结构、引用和示例校验由 Redocly `recommended-strict` 承担，Spectral 的
其余版本无关设计规则继续全部启用。

CloudEvent webhook 使用 `webhookHmac` 安全方案，Agent API 使用 OAuth
最小 scope。MCP `2026-07-28` 和 A2A `1.0` 的协议 Schema 也由同一文档
约束。OAuth token 按 `/mcp`、`/api/v1`、`/a2a/v1` 三个 RFC 8707
resource 分别签发，跨 audience 请求必须返回 `401`。

## 管理端并发与幂等契约

`/api/v1/admin` 下每个写操作都必须携带 `Idempotency-Key`。除创建顶层
服务主体外，还必须携带当前资源版本对应的强 `If-Match: "v<number>"`。
完全相同的幂等重试返回首次响应的状态、正文、ETag 和相关缓存头，不会
重复产生业务副作用或领域事件。

| 写操作 | `If-Match` 来源 | 成功响应版本 |
| --- | --- | --- |
| 全局只读开关 | Overview `global_read_only_version` | `ETag` |
| 全局紧急停止 | Overview `emergency_stop_version` | `ETag` |
| 创建服务主体 | 不需要，新建顶层资源 | 新主体 `ETag` |
| 主体状态、凭据轮换、凭据撤销 | Overview `principals[].resource_version` | 主体 `ETag` |
| 创建策略 | Overview `principals[].resource_version` | 策略 `ETag`；父主体 `X-Parent-ETag` |
| 停用策略 | 策略列表 `data[].resource_version` | 策略 `ETag` |
| 强制释放租约 | Overview `leases[].resource_version` | 租约 `ETag` |
| 记录附件扫描 | Overview `attachments[].resource_version` | 附件 `ETag` |
| 重放 Outbox | Overview `outbox[].resource_version` | 投递 `ETag` |

管理 Overview、策略列表、主体控制响应和强制释放租约响应均使用封闭
Schema，不允许以 `additionalProperties: true` 逃避字段契约。每个可变
Overview 资源及每条策略都显式声明 `resource_version`；全局控制使用独立
版本字段。策略条件是唯一允许扩展的管理数据，但其值通过递归 JSON value
Schema 约束，而不是无类型开放对象。

所有管理写操作声明 `400`、`401`、`403`、`409`、`413`、`500`、`503`；
现有资源操作另有 `404`，需要前置条件的操作另有 `428`。版本冲突使用
`AdminConflict`，其 ETag 返回当前资源验证器，调用方必须重新读取 Overview
或策略列表后再决定是否重试。

## 工具链选择

当前服务只嵌入并提供 YAML，不依赖旧版代码生成器或文档构建器。Redocly
CLI 已支持 OpenAPI 3.2 的校验，因此没有保留 3.1 副本。若引入新的生成
工具，必须先验证其 3.2 支持，不得通过降低主契约版本绕过。

MCP 只发布无会话的 `2026-07-28` Streamable HTTP POST 契约，不发布旧版本
头、会话 ID、初始化握手或独立 GET/SSE 入口。A2A 只发布 `/a2a/v1`，
`A2A-Version` 和 Agent Card 的 `protocolVersion` 均固定为 `1.0`；不存在
未版本化 `/a2a` 兼容入口。

权威资料：

- [OpenAPI 3.2.0 官方规范](https://spec.openapis.org/oas/v3.2.0.html)
- [Redocly 的 OpenAPI 3.2 支持说明](https://redocly.com/blog/openapi-3-2)
- [Redocly CLI changelog](https://redocly.com/docs/cli/changelog)
- [Spectral OpenAPI ruleset](https://docs.stoplight.io/docs/spectral/4dec24461f3af-open-api-rules)
