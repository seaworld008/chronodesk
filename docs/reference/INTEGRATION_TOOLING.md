# ChronoDesk 集成诊断工具

`chronodeskctl` 面向部署管理员、连接器开发者和外部 Agent 开发者，用于在不
修改服务器状态的前提下检查健康状态、OAuth 项目绑定、机器能力、项目连接
状态和 Webhook 签名。

## 构建

```bash
make build-server
./server/bin/chronodeskctl help
```

工具只接受 ChronoDesk 根地址，例如 `https://desk.example`。除本机回环地址
外强制使用 HTTPS，并使用系统信任库验证证书。项目必须通过 `--project-key`
显式传入。HTTP 客户端拒绝重定向，避免凭据被转发到意外目标。

## 健康检查

```bash
./server/bin/chronodeskctl health \
  --base-url https://desk.example
```

只有 `/healthz` 返回 `status=ok` 且 PostgreSQL、Redis、Agent Control
依赖全部为 `ok` 时命令才成功。

## OAuth Client Credentials

客户端密钥只从指定环境变量读取，不存在接收明文密钥的命令行参数：

```bash
export CHRONODESK_CLIENT_SECRET='从安全存储临时注入'

./server/bin/chronodeskctl oauth client-credentials \
  --base-url https://desk.example \
  --project-key OPS \
  --audience api \
  --client-id 4d24ebd5-70bf-43f8-b0d1-e140d987292f \
  --scope 'tickets:read' \
  --token-output /tmp/chronodesk-ops-api.token
```

`--audience` 必填，只能为：

| 参数 | RFC 8707 `resource` | 用途 |
|---|---|---|
| `api` | `${APP_URL}/api/v2` | 项目级 Agent REST |
| `mcp` | `${APP_URL}/mcp` | MCP `2026-07-28` |
| `a2a` | `${APP_URL}/a2a/v1` | A2A wire `1.0` |

终端输出只包含 Token 的 SHA-256 摘要和绑定元数据，不包含 Client Secret 或
Access Token。`--token-output` 使用 `0600` 创建新文件，并拒绝覆盖已有文件。

## 项目能力诊断

能力诊断需要上一节生成的 `api` audience Token：

```bash
./server/bin/chronodeskctl project capabilities \
  --base-url https://desk.example \
  --project-key OPS \
  --token-file /tmp/chronodesk-ops-api.token
```

命令验证 Agent REST `v2`、MCP `2026-07-28` 和 A2A `1.0` 单版本契约。
Token 的项目声明、项目路径和服务端 Principal Grant 任一不一致都会失败。

## 项目连接诊断

连接运行中心属于 Human REST 管理面，不能使用机器 OAuth Token。将具备项目
管理员、经理或观察者权限的人类短期 Token 注入环境变量：

```bash
export CHRONODESK_HUMAN_TOKEN='短期管理 Token'

./server/bin/chronodeskctl project connections \
  --base-url https://desk.example \
  --project-key OPS
```

命令读取：

- `/api/projects/OPS/integrations/overview`
- `/api/projects/OPS/integrations/connections`

它只显示已脱敏的状态视图，不返回连接配置或密钥引用。任一连接为 `error` 或
带有 `last_error_code` 时进程返回非零，适合部署后门禁。

## 入站 Webhook Dry-run

Dry-run 只生成预览，不发送网络请求。入站签名使用版本化、换行分隔的精确字节
序列：

```text
v1
{timestamp}
{projectKey}
{connectionPublicID}
{mappingPublicID}
{Idempotency-Key}
{externalResourceType}
{externalResourceID}
{normalizedContentType}
{exact_raw_body}
```

也就是每个文本字段后追加一个 `\n`，最后直接追加未经改写的 Body 字节；Body
末尾是否有换行也属于签名内容。项目、Connection、Mapping、消息 ID、外部对象
标识和 Content-Type 都经过认证，不能在签名后修改。

```bash
export CHRONODESK_WEBHOOK_SECRET='至少 32 字节的临时密钥'

./server/bin/chronodeskctl webhook dry-run \
  --base-url https://desk.example \
  --project-key OPS \
  --connection-id 018fb09b-8fa6-79d2-b000-f9f270452554 \
  --mapping-id 018fb09b-8fa6-79d2-b000-f9f270452555 \
  --external-resource-type legacy.ticket \
  --external-resource-id EXT-42 \
  --idempotency-key source-event-42 \
  --content-type 'Application/JSON; Charset="UTF-8"' \
  --body ./event.json
```

`--content-type` 只接受 `application/json` 或
`application/cloudevents+json`，可以带唯一的 `charset=utf-8` 参数。工具会
按服务端相同规则显式规范化为小写、无参数的媒体类型，并用规范值同时构造
`Content-Type` Header 和签名。

输出包含目标 URL、所需 Header、`v1=` 小写十六进制签名、Body 字节数与
SHA-256 摘要；不会输出密钥或原始 Body，不会改写 Body，也不会发送请求。
签名在服务端重放窗口内具有认证能力，诊断日志和工单中应继续按凭据处理并
脱敏。

## 出站 Webhook 验签

验证 ChronoDesk 发出的自定义 Domain Event Webhook：

```bash
./server/bin/chronodeskctl webhook verify \
  --body ./received-body.bin \
  --timestamp 1785369600 \
  --signature 'v1=...' \
  --previous-secret-env CHRONODESK_WEBHOOK_PREVIOUS_SECRET \
  --max-age 5m
```

验证对当前密钥和可选上一版本密钥分别使用常量时间比较，并在签名前检查
时间戳重放窗口。务必保存 HTTP 层收到的原始字节；重新序列化 JSON 会改变
签名。出站签名输入仍严格为 `timestamp + "." + exact_raw_body`，不要使用
上述入站集成 framing。验证轮换 Header 时，将
`X-ChronoDesk-Signature-Previous` 的值传给 `--signature`，并通过
`--previous-secret-env` 指向上一版本密钥。
