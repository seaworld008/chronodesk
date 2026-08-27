# ChronoDesk 项目权威手册

> 更新时间：2026-07-31
> 适用分支：`main`（当前仓库工作区）

## 1. 项目定位

ChronoDesk 是面向单 Organization、多个相互隔离 Project 的 AI 原生企业工单自动化平台。它为客服和运营人员提供中文管理后台，也为外部 AI Agent 和企业系统提供独立身份、稳定机器契约和原生协议入口。

Human REST、Agent REST、MCP、A2A 与 Connector 五类 Adapter 直接复用同一套领域服务、策略引擎、事务事件和审计模型。系统不提供进程内模型运行时、提示词编排平台或自治规划器；项目范围的知识生命周期、ACL 过滤混合检索、引用反馈和模型策略由 ChronoDesk 管理，搜索与模型网关由部署方提供。

## 2. 当前能力

### 2.1 人类工单后台

- 账号认证：注册、登录、单次使用的刷新令牌、会话撤销、邮箱验证、密码重置、TOTP、备用恢复码与可信设备。
- 工单管理：创建、查询、编辑、分配、转派、升级、状态流转、历史、SLA、模板、快速回复与批量管理。
- 协作内容：公开/内部评论；附件上传、对象存储抽象、下载授权、SHA-256 哈希、公开范围和病毒扫描状态。
- 运营能力：自动化规则、通知中心、邮件、Webhook、WebSocket、统计分析、系统配置与管理员审计。
- 企业界面：全中文操作提示；工单、用户、通知、自动化、Webhook、系统配置和 Agent 控制列表使用单行省略、悬停全文、横向滚动、固定操作列和可持久化的手动列宽。

### 2.2 组织、项目与角色边界

- `Project` 是工单、配置、知识、Agent 授权、Connection 和后台任务的唯一运行边界；一个 Organization 的多个 Project 不共享访问权。
- 平台角色只有 `platform_admin`、`security_auditor`、`emergency_operator`、`member`；项目角色只有 `project_admin`、`manager`、`agent`、`requester`、`observer`。两者均为封闭、无序集合，互不继承。
- Human JWT 仅含平台角色声明；认证中间件实时复核 active 用户与平台角色，声明陈旧或身份失效时拒绝为 `stale_token`。项目角色由每次请求实时查询的 active `ProjectMembership` 决定，绝不写入 JWT。
- `/api/platform/*` 只承载显式平台治理，不能形成 `ProjectScope`；`/api/projects/{projectKey}/*` 必须先解析单一 active Membership；`/api/workbench/*` 只汇总调用者 active Membership 所覆盖的项目。
- Human Web P1 的类型化契约由 `/human-openapi.json` 发布；它不替代 Agent REST OpenAPI，也不把未列入的遗留路由宣布为稳定契约。

### 2.3 Agent 原生底座（P0-P1）

- 工单、评论、附件、租约、事件与幂等记录由事务化领域服务统一提交。
- 领域事件采用 CloudEvents 1.0；`domain_events` 与 `outbox_deliveries` 支持失败重试、回放和可观测投递。
- 创建与命令接口使用 `Idempotency-Key`；资源使用 `version`、`ETag` 和 `If-Match` 防止并发覆盖。
- 机器接口返回稳定 JSON Schema、操作回执、不透明 cursor 和 `application/problem+json` 错误。
- AI Agent 使用独立 `service_principal`、可轮换凭据、短期 OAuth 访问令牌、最小 scope 和显式策略，不复用人类账号。
- PostgreSQL 记录服务主体、凭据、策略决策、Actor、请求摘要、变更集、事件和管理员接管；不存储模型思维链。
- Redis 提供跨实例限流、并发保护、调度器租约和 Agent 运行控制；支持全局/单主体紧急停止与只读模式。

### 2.4 项目知识与检索基础

- 知识文章、不可变版本、ACL、摄取任务、分块、索引状态、引用和反馈均绑定单一 `ProjectScope`。
- 混合检索通过 ACL 过滤的 OpenSearch 索引执行；模型选择、外部传输、保留策略和预算由版本化项目策略约束。
- 生产对象存储、病毒扫描、解析/摄取 Worker 和模型推理由部署方接入；这些外部依赖不改变 ChronoDesk 对授权、引用和审计的所有权。

### 2.5 MCP 原生入口（P2）

- `POST /mcp` 只实现 MCP `2026-07-28` 无状态 Streamable HTTP。
- 工具覆盖工单查询/创建/更新、领取/续租/释放、分配/流转、评论、附件、历史和策略预检。
- 只读资源覆盖工单、队列、历史、Ticket Schema 和能力说明；`subscriptions/listen` 通过 SSE 推送资源变化。
- 原生发布 `io.modelcontextprotocol/oauth-client-credentials` 扩展，供无人值守服务主体使用。
- MCP、Agent REST 和 A2A 使用独立 RFC 8707 resource/audience，令牌不能跨协议复用。

完整约束见 [MCP 2026-07-28 接入说明](reference/MCP_2026_07_28.md)。

### 2.6 A2A 服务端入口（P3）

- `GET /.well-known/agent-card.json` 发布支持 ETag 的 Agent Card。
- `POST /a2a/v1` 实现 A2A 官方最新发布 `v1.0.1`，线协议固定为
  `A2A-Version: 1.0`；支持消息、Task 查询/取消、状态历史、Artifact、SSE
  订阅与 Push 配置。
- A2A Task 与业务 Ticket 分开建模，通过标准 metadata 关联工单；`input-required` 不会隐式修改工单状态。
- 首期 skills 为工单受理、查询、处理、评论和升级；Push 投递复用可靠 Outbox，并执行 HTTPS/SSRF 防护。

完整约束见 [A2A v1.0.1 接入说明](reference/A2A_1_0.md)。

## 3. 技术基线

| 层级 | 当前版本/组件 |
| --- | --- |
| 后端 | Go 1.26.7、Gin 1.12、GORM 1.31 |
| 数据 | PostgreSQL 18（Compose 18.4）、Redis 8（Compose 8.8）、OpenSearch 3.5 |
| 前端 | React 19.2.8、React Admin 5.15.1、MUI 7.3.11 |
| 路由与构建 | React Router 7.18.2、TypeScript 6、Vite 8.1.5 |
| 机器契约 | OpenAPI 3.2.0、CloudEvents 1.0 |
| Agent 协议 | MCP 2026-07-28、A2A v1.0.1（wire 1.0） |
| 工程工具 | Makefile、Docker Compose、Go test、Pytest、Playwright |

## 4. 架构与代码组织

```text
.
├── server/
│   ├── cmd/
│   │   ├── chronodesk/           # 最小可执行入口
│   │   ├── migrate/              # 唯一结构迁移入口
│   │   └── credential-maintain/  # 凭据验证、密钥轮换与密码隔离
│   ├── internal/
│   │   ├── app/                  # 组合根、后台任务与优雅退出
│   │   ├── a2a/                  # A2A v1.0.1 / wire 1.0 与 Task 存储
│   │   ├── agentauth/            # 服务主体 OAuth 与 audience 校验
│   │   ├── agentcontract/        # 协议无关 scope 与机器契约
│   │   ├── agentplatform/        # Agent REST、MCP/A2A 领域适配与控制面
│   │   ├── auth/                 # 人类账号与凭据安全
│   │   ├── database/             # PostgreSQL、Redis 与 Schema 校验
│   │   ├── handlers/             # 人类后台 HTTP Handler
│   │   ├── humanopenapi/          # Human Web OpenAPI 3.2 契约
│   │   ├── mcp/                  # MCP 2026-07-28 Server
│   │   ├── models/               # 业务、Actor、事件与 Agent 模型
│   │   ├── openapi/              # 内嵌 OpenAPI 3.2 权威契约
│   │   ├── asyncapi/             # 内嵌 CloudEvents/流契约
│   │   ├── eventcontract/        # 规范 CloudEvent 类型目录
│   │   ├── scopeddb/             # 项目范围事务路由
│   │   ├── security/             # AES-GCM keyring、SSRF 与迁移校验
│   │   ├── services/             # 事务化领域服务与 Outbox
│   │   └── websocket/            # 人类实时通知
│   └── Dockerfile                # 非 root 多阶段生产镜像
├── web/
│   └── src/
│       ├── admin/                # 工单、用户、通知、自动化、设置、Agent 控制
│       ├── components/tables/    # 企业表格与持久化列宽
│       ├── i18n/                 # React Admin/MUI 中文本地化
│       ├── layout/               # 侧栏与顶部栏
│       └── lib/                  # dataProvider、authProvider、API 客户端
├── docs/                         # ADR、运维、协议参考与测试证据
├── Makefile                      # 根级统一命令
└── docker-compose.yml            # PostgreSQL、Redis、API、Web 编排
```

协议 Adapter 只负责解析、调用和错误映射；领域 Module 执行对象级授权、
Assignment、策略、事务和审计。人类、`service_principal` 与 `system` 统一使用
`ActorRef`，协议 Adapter 不得通过内部 HTTP 回调自身 Interface，也不得复制领域
规则。完整依赖规则见根目录 [架构说明](../ARCHITECTURE.md)。

## 5. 启动、迁移与配置

### 5.1 Docker 一体化开发

```bash
make dev
docker compose exec server chronodesk-migrate -seed
```

- 管理后台：`http://localhost:3000`
- API/协议服务：`http://localhost:8081`
- 健康检查：`http://localhost:8081/healthz`

`/healthz` 分别报告 PostgreSQL 与 Redis 状态；任一必要依赖不可用时返回 `503`。

### 5.2 分开运行

```bash
make server-dev
make web-dev
```

后端启动前必须已有可用的 PostgreSQL、Redis 和最新 Schema。生产环境推荐显式迁移，不依赖应用启动时自动建表。

### 5.3 数据库迁移

结构迁移只有一个权威入口：

```bash
make db-migrate
# 或
cd server && make migrate
```

只有明确需要初始化业务数据时才执行：

```bash
cd server && make migrate-seed
```

演示账号与演示工单不属于基础种子数据；只可在隔离开发环境执行
`make migrate-sample`。远程 PostgreSQL 必须使用 TLS，明文放行开关不得用于
共享或生产环境。

`migrate-drop` 仅用于一次性开发数据库，必须交互输入 `DROP`；不得用于共享或生产环境。

数据库完成结构迁移后，必须只读验证当前凭据格式：

```bash
cd server
go run ./cmd/credential-maintain -validate-only
```

维护命令不会摄入历史明文。发现不支持的密码哈希时，管理员可显式执行
`-quarantine`，随后完成正常密码重置并在审查后重新启用账号；信封密钥轮换
使用 `-rotate`。

详见 [数据库敏感字段静态加密](reference/DATA_AT_REST_ENCRYPTION.md)。

### 5.4 关键配置

配置样例为 `server/.env.example`：

- PostgreSQL：`DATABASE_URL` 或 `DB_*`。
- Redis：`REDIS_*`；Redis 是运行、限流、调度和 Agent 控制的必要依赖。
- 知识检索：`OPENSEARCH_*` 与 `MODEL_GATEWAY_*`；搜索索引和模型网关由部署方
  管理，未配置模型网关时不会执行向量嵌入或模型推理。
- 人类认证：`JWT_SECRET`、`JWT_REFRESH_SECRET` 均至少 32 个字符且彼此独立；
  issuer 固定为 `APP_URL`，REST audience 固定为 `${APP_URL}/api`。
  `BCRYPT_COST` 必须在 10–16，默认及推荐值为 12，非法值会阻止启动。
- Agent 身份：`AGENT_JWT_SECRET`、`AGENT_CREDENTIAL_PEPPER`、`AGENT_ISSUER`、TTL 与并发/附件限制。
- 静态加密：`CHRONODESK_DATA_ENCRYPTION_PRIMARY_KEY_ID`、`CHRONODESK_DATA_ENCRYPTION_KEYS`。
- 网络安全：`CORS_ALLOWED_*`、`TRUSTED_PROXIES` 和请求限流。

生产密钥必须由密钥管理系统注入，禁止提交 `.env` 或凭据。

## 6. 公共接口

### 6.1 权威机器契约

- OpenAPI：`GET /openapi.yaml`
- Agent 能力：`GET /api/v2/projects/{projectKey}/capabilities`
- Agent REST 根路径：`/api/v2/projects/{projectKey}`
- 人类后台 REST 根路径：`/api`
- MCP：`POST /mcp`
- A2A Agent Card：`GET /.well-known/agent-card.json`
- A2A JSON-RPC：`POST /a2a/v1`
- OAuth token：`POST /oauth/token`
- OAuth 发现：`/.well-known/oauth-authorization-server` 和各 resource 的 Protected Resource Metadata

`/api/v2/projects/{projectKey}` 是供 Agent 与 SDK 使用的项目显式机器入口；`/api` 是供 React Admin 和人类会话使用的业务入口，不提供旧 Agent 协议兼容层。所有 Schema、scope、错误码、示例和 Webhook 定义以运行时 `/openapi.yaml` 为准。

### 6.2 Human REST 与工作台契约

- `/human-openapi.json` 是 Human Web P1 的公开类型化契约；页面类型和路由必须从该契约生成或校验。
- `/api/platform/*` 使用精确平台角色 allowlist，适用于平台项目治理、用户和安全审计等治理资源，且不返回或授予项目角色。
- `/api/projects/{projectKey}/*` 是明确项目范围的人类工作入口；项目 key 只是授权输入，可信范围来自服务端解析的 active Membership。
- `/api/workbench/tickets` 只在已授权项目集合内做跨项目聚合，不能被平台角色或客户端项目列表扩大范围。

### 6.3 Agent REST 契约

`/api/v2/projects/{projectKey}` 提供工单、历史、评论、附件、租约和事件游标；Human 管理员的 Agent 控制面位于 `/api/projects/{projectKey}/admin/agents`，同样只能访问路径所绑定的已授权项目。最小 scope 为：

```text
tickets:read
tickets:create
tickets:update
tickets:assign
tickets:transition
comments:write
attachments:read
attachments:write
events:subscribe
tasks:manage
```

写请求按操作要求携带 `Idempotency-Key`、`If-Match` 和 `X-Ticket-Lease`。成功响应包含操作 ID、资源 ID/版本、事件 ID、变更字段和策略决策 ID；版本或租约冲突返回稳定的 `409` 机器错误。

### 6.4 人类后台 REST

`/api` 使用人类 JWT 会话与角色/对象级授权，覆盖认证、工单、评论、附件、分类、处理人、通知、用户中心、自动化、系统设置、统计、邮件、Webhook、WebSocket 与 Agent 管理后台。客户只能访问授权工单和公开协作内容，内部评论、扫描状态、凭据和策略由更高权限控制。

### 6.5 只保留当前协议

ChronoDesk 不保留 MCP、A2A 或 OpenAPI 的旧版本协商、旧路径别名和降级实现：

- MCP `2026-07-28`
- A2A 官方发布 `v1.0.1`（wire `1.0`）
- OpenAPI `3.2.0`
- CloudEvents `1.0`

接入细节见 [API 使用说明](reference/API_DOCUMENTATION.md) 和 [Reference 索引](reference/README.md)。

## 7. 安全与审计

- 人类密码使用 bcrypt；刷新/验证/重置/OTP token 使用域分离摘要；TOTP seed 和长期外部凭据使用版本化 AES-256-GCM 信封加密。
- OAuth 严格校验 `iss`、`aud`、`exp`、凭据状态和 scope；MCP、REST 与 A2A audience 分离。
- 每次 Agent 写入记录 Actor、credential、policy decision、来源协议、请求摘要、变更集和事件 ID。
- 评论和附件内容统一视为不可信数据；服务端不替 Agent 抓取任意外部 URL。
- Webhook 与 A2A Push 仅允许通过 DNS 固定、无代理、无重定向的公网 HTTPS 目标，阻断 SSRF 和 DNS rebinding。
- 附件按对象授权下载，并记录文件哈希与病毒扫描状态。
- Redis 限流按可信客户端/用户和路由隔离；健康检查、发现元数据和静态契约不共享凭据写入限流桶。

## 8. 测试与质量门禁

提交前的根级门禁：

```bash
make verify
```

`make verify` 会执行格式、Go 测试/Vet、前端 TypeScript/Lint/依赖安全、
Redocly + Spectral OpenAPI 校验、`govulncheck` 和生产构建。

需要完整回归时执行：

```bash
make test-race
make smoke
make e2e
```

详细顺序、环境依赖和专项测试见 [测试与质量控制指南](testing_guide.md)。

## 9. 当前范围边界

- 单组织私有化部署；当前不包含多租户、计费和租户隔离。
- Agent 模型推理和规划器由外部平台或部署方网关承载；ChronoDesk 已实现项目知识、
  ACL 过滤检索、引用反馈和模型策略基础。
- 生产对象存储、病毒扫描、解析/摄取 Worker 与完整 Copilot 工作台仍需闭环。
- A2A 首期只提供服务端，不主动发现或委派给外部 Agent。
- MCP Apps 与 MCP Tasks 未声明；A2A Task 是独立协议模型。

这些是明确的产品边界，不是隐藏的兼容分支。

## 10. 文档维护规范

- 架构、命令和公共接口变化后，先更新本文件，再更新专题文档与 `docs/README.md`。
- API 请求/响应只以 `server/internal/openapi/openapi.yaml` 和运行时 `/openapi.yaml` 为机器真相源。
- 根目录只保留快速入口、治理、架构与 Agent 协作文件；过期计划和会话交接资料由
  Git 历史保留，不进入当前文档树。
- 失效脚本、旧版本协议和一次性测试结论不得继续出现在当前操作指南中。
