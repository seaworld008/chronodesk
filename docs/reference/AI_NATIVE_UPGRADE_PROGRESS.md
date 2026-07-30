# ChronoDesk AI 原生多项目升级开发检查点

- 检查点日期：2026-07-30
- 目标分支：`main`
- 开发分支：`codex/ai-native-multiproject`
- 基线提交：`cdd42a047728`
- 交付 PR：[GitHub #7](https://github.com/seaworld008/chronodesk/pull/7)
- 升级策略：开发期一次性破坏性升级，不保留 `/api/v1`、隐式项目或旧队列投影兼容层
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
| P0 契约真相 | 基本完成 | Webhook CloudEvent/HMAC、A2A Card、前端幽灵字段、Assignment 展示、SLA 单一口径、Agent Admin 项目显式 OpenAPI、OpenAPI 派生前端类型基础 | 最终 OpenAPI/文档仍需与后续 Proposal 机器执行 Adapter 和关系 API 对齐 |
| P1 多项目内核 | 基本完成 | Organization/BusinessUnit/Project、Team/Queue、Membership/Grant、UUIDv7、项目编号、项目切换器、跨项目工作台、显式 v2/MCP/A2A、FORCE RLS 与运行角色门禁 | 平台角色仍是旧 `admin/supervisor/agent/customer`；OIDC/SAML/SCIM 未实现；Team/Queue 等控制表的最终 RLS 分类仍需审查 |
| P2 集成中心 | 部分完成 | Connector/Connection/Mapping/Inbox/Receipt/ExternalLink/Sync/Conflict/DeadLetter、入站 HMAC 与重放保护、Webhook 不可变投递快照、Go/Python/TypeScript SDK、`chronodeskctl` | 邮件双向同步、CSV 迁移、Kafka/AMQP、内网 Relay 生产实现、Java/.NET 可编译 SDK 尚未完成 |
| P3 配置与行业包 | 部分完成 | RequestType/Workflow/ConfigurationRelease、JSON Schema 运行时校验、版本化状态图、SLA/自动化项目化、签名行业包、IT/SRE/HR/财务参考包、配置 Intake API/动态建单表单 | 可视化设计器、完整审批/风险/路由配置发布体验、包升级 UI、跨项目协作单仍未完成 |
| P4 AI 协作控制面 | 部分完成 | AgentRun、ActionProposal、ApprovalTask/Decision、Handoff、EvidenceReference、摘要/版本/过期失效、人工接管基础、查询与管理 Handler | 本检查点正在收口 Proposal 白名单真实执行链；Agent/MCP Client Registry、A2A 主动委派、完整 AI 工作台尚未实现 |
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

## 4. 当前暂停点与未完成收口

暂停时不得把下列内容视为已发布契约：

1. EntityLink/TicketRelation Human API 已挂载到项目路由；后续如向 Agent
   开放，必须再原子更新 OpenAPI、MCP/A2A 和 SDK，不得把 Human API 当作
   机器契约。
2. Proposal 服务层已升级为封闭 `ActionExecutorRegistry` 和真实领域命令，
   但项目显式 Agent REST/MCP/A2A 执行 Adapter 尚未接入。
3. 本检查点的实现统一通过 PR #7 交付；恢复开发时以 PR 合并结果、最新
   `main` 和 `git log` 为权威状态，不依赖旧工作树数量。

## 5. 已知技术债与下一轮优先级

下一次继续开发时按以下顺序推进：

1. 同步 OpenAPI、文档、SDK 和 Web 类型：Agent Admin 项目路径、Proposal 执行、EntityLink/TicketRelation。
2. 完成平台角色破坏性升级：`platform_admin/security_auditor/emergency_operator/member`；项目职责只使用项目角色。
3. 实现 OIDC、SAML 和 SCIM；SCIM 不得自动授予项目管理员，并保留受控 break-glass 管理员。
4. 实现“创建目标项目协作单”的跨项目流程，不移动源工单、不跨两个项目安全边界持有长事务。
5. 补齐邮件、CSV、Kafka/AMQP、内网 Relay、Java/.NET SDK 和连接诊断的生产实现。
6. 完成行业配置可视化设计器、发布/升级/回滚 UI 和审批/风险策略配置。
7. 接通对象存储上传、病毒扫描、隔离解析 Worker 生命周期，再实现分类、摘要、字段补全、相似工单、项目/队列建议、知识引用和回复草稿 Copilot。
8. 完成 WORM、保留归档、备份恢复、容量/SLO 和故障演练。

需要重点复核的架构风险：

- 项目 HTTP 中间件目前让完整请求处于数据库事务内；涉及对象存储、搜索或模型调用的 Handler 必须拆为短数据库阶段，避免长事务。
- Team、Queue、Membership 和 Principal Grant 属于控制面还是完整 RLS 业务表，需要统一决定并用架构测试锁定。
- Analytics、用户统计和平台管理查询不得以零范围绕过 RLS。
- 所有 CloudEvent 都应填充真实 `configurationversion` 和 `policydecisionid`，不能只在建单事件中完整。

## 6. 恢复开发步骤

```bash
git switch codex/ai-native-multiproject
git status --short
git log -1 --oneline
make doctor
make install-deps
make verify
```

恢复后先阅读：

1. `CONTEXT.md`
2. `ARCHITECTURE.md`
3. 本文
4. `docs/adr/0005-project-is-the-runtime-boundary.md`
5. `docs/adr/0006-versioned-configuration-and-ai-actions.md`
6. `docs/adr/0007-inbox-mapping-and-external-identity.md`

若本检查点已经通过 PR 合并，则从最新 `main` 新建 `codex/` 前缀分支继续，不复用已合并分支。

## 7. 发布门禁记录

只有真实执行成功的命令可以标为通过。PR 检查与合并状态以
[GitHub PR #7](https://github.com/seaworld008/chronodesk/pull/7) 为权威记录。

- [x] `git diff --check`
- [x] `cd server && go test ./... -count=1`
- [x] `cd server && go test -race ./... -count=1`
- [x] `cd server && go vet ./...`
- [x] `cd web && npm run typecheck`
- [x] `cd web && npm run lint`
- [x] `make test-sdk`
- [x] `make openapi-lint`
- [x] `make asyncapi-lint`
- [x] `make test-python-static`
- [x] `make security`
- [x] `make build`
- [x] `make verify`
- [x] 隔离 fresh-volume Docker Compose 启动、85/85 模型迁移、FORCE RLS、
  Server 健康检查与 `chronodesk-migrate -seed`

本机 Python 门禁工具已完整安装并验证：`ruff 0.16.0`、
`pytest 9.1.1`、`server/requirements-test.txt` 全量依赖，且
`python3 -m pip check` 无损坏依赖。
