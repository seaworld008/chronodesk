# ChronoDesk AI 原生多项目升级开发检查点

- 检查点日期：2026-07-31
- 目标分支：`main`
- 开发分支：`codex/p1-platform-roles-ultra`
- 本轮起点：`f4064c5`
- 升级策略：开发期一次性破坏性升级，不保留 `/api/v1`、隐式项目或旧队列投影兼容层
- 当前状态：P1A Task 1-4（平台角色模型与一次性迁移、删除项目访问的全局角色
  绕过、原子更新 OpenAPI/Web/黑盒契约、全量验证与进度持久化）已在当前工作树
  闭环；P1 整体仍为部分完成
- 证据边界：本文只记录本轮实际执行结果，不以历史 PR、旧测试数量或跳过的
  集成测试替代当前工作树证据
- 状态说明：本文记录当前真实实现状态，原始总体规划仍是目标；标为“部分完成”的能力不得当作生产可用

## 1. 已锁定的架构决策

1. 领域层级固定为 `Organization → BusinessUnit → Project → Team/Queue → Ticket`。
2. `Project` 是权限、数据、配置、Agent、知识、集成和后台任务的唯一运行边界；`BusinessUnit` 只负责归集与治理。
3. 保持模块化单体，Human REST、Agent REST、MCP、A2A 和 Connector Adapter 调用同一领域接口。
4. 所有写入继续使用 `ActorRef + PolicyDecision + Lease + Version + Idempotency + CloudEvent/Outbox` 可信内核。
5. PostgreSQL 项目业务表使用 `ENABLE/FORCE ROW LEVEL SECURITY`，应用运行角色必须是非 owner、`NOSUPERUSER`、`NOBYPASSRLS`。
6. 行业能力由版本化、Ed25519 签名的 Solution Package 提供，不复制行业代码。
7. AI 写操作必须经过结构化 `ActionProposal`；审批绑定不可变摘要、策略版本、有效期和工单版本，不保存思维链。
8. MCP 固定为 `2026-07-28`，A2A wire 固定为 `1.0`，OpenAPI 固定为 `3.2.0`，CloudEvents 固定为 `1.0`。

对应决策记录：

- [ADR-0005：Project 运行边界](../adr/0005-project-is-the-runtime-boundary.md)
- [ADR-0006：版本化配置与 AI Proposal](../adr/0006-versioned-configuration-and-ai-actions.md)
- [ADR-0007：Inbox、Mapping 与外部身份](../adr/0007-inbox-mapping-and-external-identity.md)

## 2. 分阶段完成度

| 阶段 | 当前状态 | 已实现重点 | 尚未闭环 |
| --- | --- | --- | --- |
| P0 契约真相 | 部分完成 | Webhook CloudEvent/HMAC、A2A Card、Assignment 展示、SLA 单一口径、Agent Admin 项目显式 OpenAPI；Human Web P1 契约已覆盖 58 条路径、78 个操作，并生成前端类型与路由 | Human 契约未覆盖的遗留路由不得推断为已发布；Agent Proposal 执行 Adapter、关系 API 及其 SDK/协议文档仍需对齐 |
| P1 多项目内核 | 部分完成（P1A Task 1-4 已闭环） | Organization/BusinessUnit/Project、Membership/Grant、UUIDv7、项目编号、项目切换器、跨项目工作台、显式 v2/MCP/A2A、FORCE RLS 与运行角色门禁；平台角色已收敛为 `platform_admin/security_auditor/emergency_operator/member`，项目职责只使用五种项目角色 | OIDC/SAML/SCIM 未实现；Team/Queue 控制面的最终边界与 RLS 分类仍需闭环；目标项目协作单等跨项目流程尚未实现 |
| P2 集成中心 | 部分完成 | Connector/Connection/Mapping/Inbox/Receipt/ExternalLink/Sync/Conflict/DeadLetter、入站 HMAC 与重放保护、Webhook 不可变投递快照、Go/Python/TypeScript SDK、`chronodeskctl` | 邮件双向同步、CSV 迁移、Kafka/AMQP、内网 Relay 生产实现、Java/.NET 可编译 SDK 尚未完成 |
| P3 配置与行业包 | 部分完成 | RequestType/Workflow/ConfigurationRelease、JSON Schema 运行时校验、版本化状态图、SLA/自动化项目化、签名行业包、IT/SRE/HR/财务参考包、配置 Intake API/动态建单表单 | 可视化设计器、完整审批/风险/路由配置发布体验、包升级 UI、跨项目协作单仍未完成 |
| P4 AI 协作控制面 | 部分完成 | AgentRun、ActionProposal、ApprovalTask/Decision、Handoff、EvidenceReference、摘要/版本/过期失效、人工接管基础、查询与管理 Handler、封闭执行 Registry | 项目显式 Agent REST/MCP/A2A 执行 Adapter、Agent/MCP Client Registry、A2A 主动委派和完整 AI 工作台尚未实现 |
| P5 知识与 Copilot | 部分完成 | 知识文章/版本/ACL/摄取任务/Chunk/Citation/Feedback/IndexState、OpenSearch 混合检索、排序前 Project+ACL 过滤、ModelProvider/HTTP Gateway、项目模型策略、摄取 Worker 领域实现 | 生产对象存储上传、病毒扫描器、隔离解析器、Worker 生命周期接线、内置 Copilot 功能尚未完成 |
| P6 稳定版 | 部分完成 | OpenTelemetry、W3C Trace Context、Prometheus 指标、项目审计哈希链、数据库追加写约束 | WORM 导出 Adapter、保留归档、备份恢复自动化、容量基准和故障演练尚未完成 |

## 3. 已落地的关键实现

### 3.1 项目隔离与可信上下文

- 所有项目命令使用服务端构造的 `OperationContext`，包含 `ProjectScope`、Actor、来源协议、Credential、Trace 和 Correlation。
- Human 通过 `ProjectMembership`，Service Principal 通过 `ProjectPrincipalGrant` 授权；A2A `tenant` 和请求体项目字段不作为可信范围。
- 项目业务表加入显式 `organization_id/project_id` 与 PostgreSQL RLS 清单。
- 项目 HTTP 请求使用事务内 `SET LOCAL` 等价设置和响应缓冲，业务失败回滚。
- 后台 Worker 枚举服务端可信项目集合，每个项目使用独立短事务。
- 跨项目工作台只读取当前 Human 的有效成员项目集合，平台角色不隐式扩大普通工作台范围。
- Human 平台角色与项目职责使用两个封闭枚举；平台治理入口不会构造
  `ProjectAccess`，项目业务入口只接受当前 active Membership。

关键目录：

- `server/internal/models/project.go`
- `server/internal/services/project_service.go`
- `server/internal/scopeddb/`
- `server/internal/database/project_rls*.go`
- `server/internal/handlers/project_handler.go`
- `server/internal/services/cross_project_workbench_service.go`

### 3.2 工单配置运行时

- 工单创建强制绑定已发布的 `RequestTypeVersion` 和 `WorkflowVersion`。
- JSON Schema 2020-12 在写入时校验；拒绝外部 `$ref` 和保留安全字段进入 `custom_fields`。
- 工单状态迁移读取不可变工作流图，并映射到统一生命周期类别。
- 项目配置支持草稿、模拟、发布、不可变 Release、签名包安装、差异、兼容检查和回滚。
- Web 建单页从 `/api/projects/{projectKey}/configuration/intake` 加载版本化请求类型、工作流和表单 Schema。
- 项目创建和启动阶段会生成默认配置，已有工单保留创建时版本。

### 3.3 集成与事件

- 入站统一经过连接解析、精确原始 Body HMAC、时间窗口、消息去重、Mapping 校验、共享领域命令、Receipt/ExternalLink/Event/Outbox 同事务提交。
- 入站签名覆盖版本、时间戳、项目、连接、映射、消息 ID、外部对象类型/ID、Content-Type 和原始 Body，字段替换不能复用签名。
- 出站 Webhook 在业务事务内冻结订阅、过滤器、模板、URL、加密凭据、重试和超时为不可变 Snapshot。
- Worker 只投递 `snapshot:<uuidv7>`，不在运行时重新查当前订阅；配置修改、禁用、新订阅或密钥轮换不改变历史事件。
- Webhook 使用 `application/cloudevents+json`，HMAC 覆盖时间戳和原始 Body，支持当前/上一密钥双轮换。
- A2A Push、邮件 Outbox、通知、WebSocket、自动化和 SLA Worker 均已加入项目范围。

### 3.4 AI 协作与审计

- `AgentRun` 记录模型、Prompt、工具、策略版本、用量、成本和结果摘要，不保存思维链。
- `ActionProposal` 保存结构化动作、预览、证据摘要、风险、目标 Ticket 版本和规范化摘要。
- 审批在 Proposal 内容、策略、有效期或 Ticket 版本变化时失效；高风险动作支持双人审批。
- 人工接管原子撤销 Agent execution claim 和 Ticket Lease、终止 Run、重新分配 Human 并写 Handoff/Event。
- 审计账本按项目形成 SHA-256 哈希链，并以数据库约束阻止已写条目的更新和删除。

### 3.5 知识、模型与检索

- PostgreSQL 保存文章、版本、ACL、摄取、Chunk、引用、反馈和索引状态的权威元数据。
- OpenSearch 查询把组织、项目、ACL、已发布和病毒扫描通过条件放入候选检索阶段，随后才 Rerank。
- 引用包含文章、版本、Chunk、页码/片段、内容哈希、排序与分数。
- `ModelProvider` 同时抽象 Generate、Embed 和 Rerank；项目策略控制 Provider/模型白名单、外发、脱敏、预算和限流。

### 3.6 行业实体与工单关系

- 新增类型化 `EntityLink`：asset、device、application、contract、customer、location、other。
- 新增项目内 `TicketRelation`：parent_of、duplicate_of、blocks、collaborates_with。
- 两类记录使用 UUIDv7、Actor 审计、不可变约束、项目 RLS、Ticket 乐观版本和 CloudEvent。
- 跨项目直接 Relation 被拒绝；目标项目协作单仍是后续实现项。

### 3.7 Human Web P1 契约

- 服务内嵌并发布 `/human-openapi.json`；当前 P1 范围包含 58 条路径、78 个
  操作，每个操作都有唯一 `operationId`、类型化成功响应和准确的路径参数。
- 已发布写请求使用封闭顶层 Schema；运行时严格 JSON 解码拒绝未发布字段、
  重复/尾随 JSON，平台与项目角色 allowlist 同时由契约测试和 Handler 测试锁定。
- Web 从该契约生成 `humanApiOperations`、`humanApiRoutes` 和 TypeScript 类型；
  freshness 与 Web contract check 防止继续手写重复的 P1 路径和 DTO。
- 该契约只服务同仓库 Human Web P1，不替代对外 Agent OpenAPI，也不把未列入的
  Human 遗留路由声明为稳定机器契约。

## 4. P1A Task 1-4 闭环与边界

本轮 Task 1-4 均已有实现、契约测试和真实服务证据，不再借用历史 PR 的测试
数量作为完成依据：

1. **Task 1：平台角色模型与一次性迁移。** Human 持久身份、JWT/会话和管理 DTO
   统一使用四种 `PlatformRole`；项目职责只保存在显式 Membership 的五种
   `ProjectRole` 中。迁移使用锁、封闭 CHECK 与最终 checkpoint，失败回滚，
   旧角色令牌和未知值 fail closed。
2. **Task 2：删除项目访问的全局角色绕过。** 用户、配置、邮件、审计及项目
   创建/归档使用 `/api/platform/*` 精确 allowlist；工单、通知、自动化、
   Webhook、Agent 管理和 Membership 使用 `/api/projects/{projectKey}/*`。
   平台管理员不会隐式获得项目工单或跨项目工作台范围；Membership 撤销、
   Actor 降级和项目归档会在并发命令/Worker claim 前重新验证。
3. **Task 3：原子更新 OpenAPI、Web 与黑盒契约。** 58 条路径、78 个操作的
   Human OpenAPI 3.2.0 文档、后端运行时 DTO/状态码、严格请求边界、前端
   codegen/freshness 与黑盒身份模型已对齐；未发布字段不能从 Web 表单或直接
   HTTP 写入。
4. **Task 4：全量验证、进度持久化与提交。** fresh-volume 服务栈、
   迁移/seed、91 条协议黑盒、73 条真实浏览器流程、真实 Redis、真实
   PostgreSQL normal/race、全量 `make verify` 和 `make test-race` 均已执行
   通过，本文同步持久化结果。异步 Webhook 由浏览器断言 HTTP 202 入队，再
   轮询到 Worker 写入最终日志；Git 提交、PR 和 CI 状态仍只以实际仓库记录为准，
   本文不预写提交 ID 或远端成功。

下列能力仍不得视为已发布契约：

1. EntityLink/TicketRelation Human API 已挂载到项目路由；后续如向 Agent
   开放，必须再原子更新 OpenAPI、MCP/A2A 和 SDK，不得把 Human API 当作
   机器契约。
2. Proposal 服务层已升级为封闭 `ActionExecutorRegistry` 和真实领域命令，
   但项目显式 Agent REST/MCP/A2A 执行 Adapter 尚未接入。
3. P1A Task 1-4 完成不等于 P1、P0-P6 或生产稳定版全部完成；剩余范围以
   下节列出的缺口为准。

## 5. 已知技术债与下一轮优先级

P1A Task 1-4 闭环后按以下顺序推进：

1. 实现 OIDC、SAML 和 SCIM；SCIM 不得自动授予项目管理员，并保留受控
   break-glass 管理员。
2. 明确 Team/Queue/Membership/Principal Grant 控制面边界，完成 Team/Queue
   管理体验及相应 RLS/架构测试。
3. 同步 Agent OpenAPI、MCP/A2A、SDK 和文档中的 Proposal 执行及
   EntityLink/TicketRelation 能力。
4. 实现“创建目标项目协作单”的跨项目流程，不移动源工单、不跨两个项目
   安全边界持有长事务。
5. 补齐邮件、CSV、Kafka/AMQP、内网 Relay、Java/.NET SDK 和连接诊断的
   生产实现。
6. 完成行业配置可视化设计器、发布/升级/回滚 UI 和审批/风险策略配置。
7. 接通对象存储上传、病毒扫描、隔离解析 Worker 生命周期，再实现分类、摘要、
   字段补全、相似工单、项目/队列建议、知识引用和回复草稿 Copilot。
8. 完成 WORM、保留归档、备份恢复、容量/SLO 和故障演练。

需要重点复核的架构风险：

- 项目 HTTP 中间件目前让完整请求处于数据库事务内；涉及对象存储、搜索或模型调用的 Handler 必须拆为短数据库阶段，避免长事务。
- Team、Queue、Membership 和 Principal Grant 属于控制面还是完整 RLS 业务表，需要统一决定并用架构测试锁定。
- Analytics、用户统计和平台管理查询不得以零范围绕过 RLS。
- 所有 CloudEvent 都应填充真实 `configurationversion` 和 `policydecisionid`，不能只在建单事件中完整。
- Knowledge 搜索结果仍需在 Rerank 前从 PostgreSQL 重新加载权威
  Chunk 片段与页码；索引命中只能提供候选 ID/分数，不能把索引正文直接外发或
  写入 Citation。
- Knowledge 最终引用事务仍需按固定顺序锁定 Team/TeamMembership、Article、
  Version、Chunk、ACL 与 `ProjectModelPolicy`，并用真实 PostgreSQL barrier
  证明撤权、取消发布和数据外发策略变更不能穿透最终写入。
- Knowledge 全量重建仍需持久 generation fence 与 alias CAS，防止旧 Worker
  在新 generation 完成后把检索别名回退到旧索引。
- Webhook Snapshot 已冻结 `RetryInterval/RateLimit/RateLimitWindow`，但生产
  Worker 的重试调度与跨实例持久限流仍未消费这些字段；在完成并发 Worker
  测试前不得把该运行时能力标为闭环。

## 6. 本轮工作树与继续开发

本轮证据对应 `codex/p1-platform-roles-ultra` 从 `f4064c5` 开始的当前工作树。
后续提交、PR 与 CI 必须以实际 Git 状态和 CI 结果为准，不得把本文的本地通过
记录写成尚未发生的远端合并结果。

继续开发前先阅读：

1. `CONTEXT.md`
2. `ARCHITECTURE.md`
3. 本文
4. `docs/adr/0005-project-is-the-runtime-boundary.md`
5. `docs/adr/0006-versioned-configuration-and-ai-actions.md`
6. `docs/adr/0007-inbox-mapping-and-external-identity.md`

## 7. 发布门禁记录

以下均是本轮当前工作树的非跳过执行结果；时间只在已有原始结果时记录。

- [x] 隔离 fresh-volume Docker Compose：PostgreSQL、Redis、OpenSearch 和
  Server 全部健康；当前模型迁移 87/87，`chronodesk-migrate -seed` 通过。
- [x] fresh-volume 容器内执行
  `chronodesk-credential-maintain -validate-only`，凭据存储校验通过。
- [x] fresh-volume `make smoke`：91/91 Human REST、Agent REST、MCP、A2A
  及失败驱动黑盒通过，耗时 15.18 秒；其中零 Membership 的新
  `platform_admin` 通过真实 `/api/platform/projects` 发现平台项目，普通
  `/api/projects` 仍返回空集合。
- [x] fresh-volume Playwright：73/73 通过，耗时 4.4 分钟；覆盖现存 13 张
  企业表，平台用户详情无项目工单投影，异步 Webhook 真实完成
  HTTP 202 → Worker 最终投递日志、平台治理清单与归档浏览器闭环；对应
  生产修复经独立审查无新增发现。
- [x] 三个真实 PostgreSQL 专项在最终工作树非跳过执行：normal 7.357 秒、
  race 10.328 秒；覆盖 FORCE RLS 下项目归档、归档与 Outbox claim 竞争，
  以及平台管理员在 `ListPlatformProjects`、`CreateProject`、
  `ArchiveProject` 三类操作中的并发降权 barrier 与同事务重验。
- [x] 本轮前序 grouped 真实 PostgreSQL normal/race 也已通过；最终三项命令
  使用 fresh PostgreSQL DSN 与 `CHRONODESK_POSTGRES_INTEGRATION=1` 显式
  执行，未把环境缺失导致的 SKIP 计为证据。
- [x] `cd server && go test ./... -count=1` 全量通过。
- [x] `CHRONODESK_REDIS_INTEGRATION=1 REDIS_URL=redis://127.0.0.1:16379/15 make test-redis-integration`
  使用 fresh Redis 非跳过通过，services 耗时 5.350 秒。
- [x] Human OpenAPI 3.2.0：58 条路径、78 个操作；lint 为 0，Web freshness
  与 contract check 通过。
- [x] `make verify` 从头完整通过：Go 全包与 vet、Human 契约/freshness、Web
  typecheck/lint/security/build（13,657 modules）、Go/Python/TypeScript SDK、
  Python toolchain/static 与 91 项收集、证据 manifest 236/236 及 self-test、
  OpenAPI Redocly/Spectral 零告警、AsyncAPI、govulncheck、npm audit 和全量构建。
- [x] `make test-race` 全仓通过，覆盖 agentplatform、auth、database、handlers、
  services、websocket 等包。
