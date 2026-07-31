# ChronoDesk 项目绑定 SDK

权威契约始终是运行时 `/openapi.yaml` 和仓库内
`server/internal/openapi/openapi.yaml`。`sdk/` 中的客户端是针对首个集成
里程碑提供的最小项目绑定封装，不复制领域规则。

## 共同约束

- 构造客户端时必须传入合法 `project_key`。
- Agent REST 固定调用 `/api/v2/projects/{projectKey}`。
- OAuth Client Credentials 必须显式选择 `api`、`mcp` 或 `a2a` audience。
- OAuth 响应中的 `project_key` 和 `resource` 必须与请求完全一致。
- 除回环开发地址外强制 HTTPS；三种客户端都拒绝自动重定向。
- 默认请求超时为 30 秒，Go/Python/TypeScript 均拒绝无界或超过 5 分钟的超时。
- SDK 不实现 Assignment、Lease、Version、Policy 或 Idempotency 规则；这些
  规则仍由 ChronoDesk 共享领域服务执行。
- 工单文本、评论、附件名和外部 Payload 是不可信数据，SDK 不将其解释为
  Prompt、工具描述、命令或 URL。

## Go

```go
anonymous, err := chronodesk.New("https://desk.example", "OPS")
if err != nil {
    return err
}
token, err := anonymous.ExchangeClientCredentials(ctx, chronodesk.ClientCredentials{
    ClientID:     clientID,
    ClientSecret: clientSecret,
    Audience:     chronodesk.AudienceAPI,
    Scopes:       []string{"tickets:read"},
})
if err != nil {
    return err
}
client, err := anonymous.WithToken(token.AccessToken)
if err != nil {
    return err
}
tickets, err := client.ListTickets(ctx, chronodesk.TicketListOptions{Limit: 20})
```

验证：

```bash
cd sdk/go
go test ./...
```

可编译示例位于 `sdk/go/examples/list-tickets`，只输出项目键、数量和
`request_id`，不输出 Token 或工单正文。

## Python

```python
from chronodesk import Audience, ChronoDeskClient, ClientCredentials

anonymous = ChronoDeskClient("https://desk.example", "OPS")
token = anonymous.exchange_client_credentials(
    ClientCredentials(
        client_id=client_id,
        client_secret=client_secret,
        audience=Audience.API,
        scopes=("tickets:read",),
    )
)
client = anonymous.with_access_token(token.access_token)
tickets = client.list_tickets(limit=20)
```

验证：

```bash
PYTHONPATH=sdk/python \
  ./.venv/bin/python -m unittest discover -s sdk/python/tests -p 'test_*.py'
```

先运行 `make install-deps` 创建仓库 `.venv`。也可以直接运行 `make test-sdk`，
它会使用同一虚拟环境执行 Python SDK 门禁。

可执行示例位于 `sdk/python/examples/list_tickets.py`。

## TypeScript

TypeScript SDK 的 Client Credentials 交换只允许在可信 Node.js/服务端运行时
使用，不得把 Service Principal Secret 打包进浏览器代码。浏览器管理端继续
使用独立的人类会话，不复用机器凭据。

```ts
import { ChronoDeskClient } from "@chronodesk/sdk";

const anonymous = new ChronoDeskClient("https://desk.example", "OPS");
const token = await anonymous.exchangeClientCredentials({
  clientId,
  clientSecret,
  audience: "api",
  scopes: ["tickets:read"],
});
const client = anonymous.withAccessToken(token.access_token);
const tickets = await client.listTickets({ limit: 20 });
```

验证：

```bash
cd sdk/typescript
npm ci
npm test
```

`sdk/typescript/examples/list-tickets.ts` 与库和契约测试一起接受严格 TypeScript
编译。

## Java 与 .NET 状态

`sdk/generator/java.yaml` 和 `sdk/generator/dotnet.yaml` 仅是从权威 OpenAPI
评审生成结果的配置。仓库当前没有提交 Java/.NET 生成物，也没有 Maven、
Gradle 或 `dotnet build` 消费者门禁。因此这两种语言目前不是受支持 SDK，
不得作为已发布能力对外承诺。

## 统一门禁

```bash
make install-sdk-deps
make test-sdk
```

三种已实现 SDK 的契约测试都会检查项目路径、OAuth `project_key`、RFC 8707
`resource` 和当前单版本能力。
