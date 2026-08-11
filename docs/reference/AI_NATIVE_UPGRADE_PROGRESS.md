# ChronoDesk AI 原生多项目状态

> 更新时间：2026-08-11
> 状态口径：本页描述当前仓库实现和必须继续验证的边界；不依赖特定开发分支，也不预写未经当次发布门禁证明的远端结论。

## 已接受的运行模型

ChronoDesk 是单 Organization、多个 Project 的 AI 原生模块化单体。AI 原生不表示
平台内置模型或自动决策器；它表示 Human 和外部 Agent 可以通过受控 Adapter 调用同一
项目范围领域接口，并共享授权、版本、幂等、事件和审计语义。

当前入口为五类 Adapter：Human REST/WebSocket、Agent REST、MCP、A2A 与
Connector/Inbox。它们不得各自维护 Assignment、scope、policy、lease、version、
idempotency 或 CloudEvent/Outbox 规则。

协议版本固定为 MCP `2026-07-28`、A2A 官方发布 `v1.0.1`（wire `1.0`）、
OpenAPI `3.2.0` 和 CloudEvents `1.0`。不保留旧版兼容分支。

## 已实现且应持续守住的边界

### 项目范围与路由

- `Project` 是 Ticket、配置、知识、Agent authority、Connection 和后台任务的唯一运行边界；`BusinessUnit` 只用于治理和归集。
- `/api/platform/*` 是无 `ProjectScope` 的窄平台治理面；它不构造或授予 `ProjectRole`。
- `/api/projects/{projectKey}/*` 必须先经服务端将项目 key 解析为单一可信 `ProjectScope`。
- `/api/workbench/*` 只对 Human 的 active Membership 集合做跨项目读取；平台角色不会扩大此集合。
- Agent REST、MCP、A2A 与 Connector 的项目输入均是授权输入，不能直接构造可信 scope。

### 角色与会话

- `PlatformRole` 只允许 `platform_admin`、`security_auditor`、`emergency_operator`、`member`。
- `ProjectRole` 只允许 `project_admin`、`manager`、`agent`、`requester`、`observer`，并且只存在于 active `ProjectMembership`。
- 两组角色是独立、封闭、无序集合：平台治理身份不隐式获得项目成员资格。
- Human JWT 只有平台角色声明。认证时实时复核 active 用户和该角色；发生失配或身份失效的会话按稳定 `stale_token` 边界拒绝。每个项目请求实时解析 Membership，JWT 不含项目角色。

### 契约与迁移

- Agent 的权威机器契约是 `/openapi.yaml`；Human Web P1 契约发布于 `/human-openapi.json`，两者不互相替代。
- Human 契约包含 `PlatformRole`、`ProjectRole`、`HumanSessionUser` 和 `AuthorizedProjectAccess` 等类型，并使用角色 allowlist 描述平台与项目操作。
- 一次性角色拆分在已完成项目范围切换后执行，checkpoint 为 `20260730_platform_roles_v1_cutover`。旧角色首先被映射为显式项目 Membership 和平台身份，随后移除旧列；来源不明、checkpoint 不匹配或旧列残留均 fail closed。
- 历史 `admin` 映射为 `platform_admin`；可回填的项目职责为 `admin → project_admin`、`supervisor → manager`、`agent → agent`、`customer → requester`。既有 Membership 不会被覆盖，停用或删除用户不会被重新授予访问权。

### 写入原子性与工作流兼容

- 注册在单一后端事务内创建 User、Profile、refresh digest、LoginHistory、successful LoginAttempt、DomainEvent 与 welcome Outbox；任一写入失败都会回滚整个注册，不留下可登录的部分身份。
- Workflow 定义允许多个不同 state key 映射到同一 lifecycle category，继续守住 state/transition key、端点、role、initial/terminal 和规模上限；由于当前 Ticket 与协议只持久化 canonical `TicketStatus`，定义和发布会拒绝 source/target 属于同一 category 的不可执行边。
- 运行时将重复 category 状态之间合法的跨 category 边聚合为 canonical `(from, to)` edge union，并按 edge 合并允许角色；`AllowedTicketTransitions` 只返回去重后的 canonical status，不声称恢复或持久化 exact workflow state key。
- Human REST、Agent REST、MCP 与 A2A 的同状态 transition 均进入同一领域规则并明确返回 invalid transition；失败不会修改 Ticket version/status/updated_at，也不会生成成功历史、领域事件、Outbox 或 receipt。

## 当前完成度与未闭环范围

| 领域 | 已有基础 | 仍需在发布前或后续变更中验证/完成 |
| --- | --- | --- |
| 多项目身份 | 平台/项目角色拆分、Membership/Grant、项目范围路由与 RLS 方向 | OIDC、SAML、SCIM，Team/Queue 控制面边界的最终 RLS 覆盖 |
| Human Web | `/human-openapi.json`、类型化 P1 操作与工作台边界 | 未列入 P1 的遗留路由不可据此宣称稳定公共契约 |
| Agent 与协议 | Agent REST、MCP、A2A 的共享领域入口和版本门禁 | Proposal 执行 Adapter、关系 API 对外契约、SDK 同步与主动 A2A 委派 |
| 集成 | Connection、Mapping、Inbox、Receipt、ExternalLink、Outbox 基础模型 | 邮件双向同步、CSV、Kafka/AMQP、内网 Relay 与额外语言 SDK 的生产化 |
| Webhook / Outbox | 七天绝对凭据期限、终态粉碎、过期 cleanup、generation-bound 三态 dispatch marker、PG/SQLite tuple fence、双 HTTP gate、有限 replay、安全 tombstone/preflight 投影、live snapshot DEK validate/rewrap、project-scoped 紧急撤销与运维规程 | 真实备份/PITR 恢复演练与当次发布门禁 |
| 知识与检索 | 项目知识版本、ACL、摄取状态、OpenSearch 混合检索、引用反馈与模型策略 | 生产对象存储、扫描/解析/摄取 Worker、模型网关运行基线与完整 Copilot 闭环 |
| AI 协作 | Run、Proposal、Approval、Handoff、Lease 与审计模型 | 内置 Copilot、完整执行工作台、对象存储/扫描/解析 Worker 的生产闭环 |
| 运行可靠性 | RLS、审计哈希链、凭据维护、迁移 checkpoint | WORM、保留归档、备份恢复自动化、容量基准与故障演练 |

“已有基础”仅表示存在实现与相应契约/测试位置，不能代替本次提交的实际测试、真实依赖健康检查或远端 CI 结果。

## 2026-08-10 代码存档与 2026-08-11 继续开发检查点

本检查点按代码存档合并，不作为生产发布证明。最终整分支发布门禁、远端 CI
等待、真实依赖健康检查和合并后回归由需求方明确延后；恢复开发时必须先验证
当前 `origin/main`，不能把历史开发过程中的局部 GREEN 当作当前环境结论。

### 本检查点包含

- DOMPurify 安全版本、项目角色变化后的 workflow transition 刷新、空 due date
  no-op、注册会话消费、通知 email preference、OTP backup code step-up、
  observer attachment 404 等价、同 session refresh 跨 tab 同步，以及通知内嵌
  Ticket 的角色投影。
- Webhook snapshot 与 Outbox 共享不可延长的七天绝对 deadline；成功、过期和
  cleanup 路径单调清空 credential envelopes，并保留审计 metadata。
- Webhook claim 将 `dispatch_started_at` 与 `locked_at` 写为同一个 claim 时间，
  表示 generation-bound prepared；紧接 HTTP client `Do` 前的短事务取得 config
  锁，并以 CAS 把 marker 改为严格晚于 `locked_at` 的时间戳。`NULL` 表示
  legacy/unknown，strictly-later marker 只表示 dispatch authorization 已提交；
  两者都保守计为 in-flight，不据此声称请求已离开进程。
- claim、HTTP client 创建、`Do` 前 dispatch-start gate、finalize、cleanup 和
  replay 均复核 scope、event、snapshot、deadline 与 claim generation。
  PostgreSQL/SQLite tuple fence 仅允许满足 `attempts + 1`、`locked_at` 前进、
  非空 worker、新 UUIDv7 token 和 marker 重绑的 prepared → 新 prepared；
  它阻断旧 Worker stale-reclaim `NULL`/dispatch-authorized generation 及 marker
  降级。旧 Worker 首次 claim 留下的 `NULL` 可以完成，崩溃后不再领取；
  dispatch-authorized marker 后的不确定崩溃也不自动重发，两者只由原 deadline
  cleanup 关闭。
- `expired` 投递的 Agent/Human 安全投影、稳定
  `outbox_replay_expired` 409、两处管理 UI 的中文终态与 replay 隐藏。
- database-secret startup/maintenance 对每个 live snapshot 的三类 envelope
  执行 Project-scoped 验证和 rewrap；历史 AAD 继续绑定原 Webhook Config ID。
- exact `project_admin` 可通过 Human admin command 紧急撤销一个 Webhook；
  live/tombstone preflight 仅返回无秘密的最小投影，并与不可逆确认分离。command
  强制 preflight version、`If-Match` 与幂等 key，在同一 Project transaction
  禁用配置、清空 `secret`、`previous_secret`、
  `previous_secret_expires_at`、`access_token`，终止 prepared 投递、粉碎
  snapshot 凭据并报告 `NULL`/dispatch-authorized generation in-flight。
- 普通 Webhook `PUT`/`DELETE` 使用强 `If-Match`，并在变更配置的同一事务 CAS
  durable admin resource version。soft-delete 不撤销已冻结 snapshot；紧急撤销
  是不可复活的终态。revoke-first 在 dispatch-start barrier 前关闭投递并保持
  零 HTTP；start-first 只报告 in-flight。
- Webhook Settings 提供中文不可逆确认与安全计数；Agent Control 对 `expired`
  显示封闭中文终态并隐藏 replay。ADR-0009 与运维 runbook 已记录普通删除、
  mixed-version `NULL` 保守处理、generation tuple fence、禁止 prepared 回填或
  marker 降级、key rotation、发布/回滚及 backup/WAL/PITR 边界。撤销和过期不会
  设置、清空或改写父 Domain Event 的 `PublishedAt`。

### 明确未验证或未完成

1. **真实恢复演练与发布证明**
   - ADR-0009 和 Webhook credential runbook 已定义迁移发布/回滚、
     backup/WAL/PITR 与物理擦除边界；仍需在隔离环境执行真实备份恢复、撤销重放
     和 egress 关闭条件下的零 HTTP 演练。
   - 本页只记录仓库实现状态；当前分支的完整测试、远端 CI、合并后 main 回归和
     生产发布仍必须以当次发布证据为准。

### 恢复开发的强制起点

1. 从最新 `origin/main` 创建新的 `codex/` 分支或隔离 worktree，先阅读
   [`CONTEXT.md`](../../CONTEXT.md)、[`ARCHITECTURE.md`](../../ARCHITECTURE.md)
   和仓库 `AGENTS.md`。
2. 在修改代码前先建立基线：

   ```bash
   make doctor
   make verify
   make test-race
   make credential-validate
   make smoke
   make e2e
   ```

   同时运行 loopback-only PostgreSQL lifecycle/DEK integration tests、
   Human API generation check、Web typecheck/lint/build；任何未配置或未执行的
   外部依赖门禁必须如实记录为未验证。
3. 后续修改 emergency revoke、注册事务或 workflow 兼容语义时，先写
   mutation-sensitive RED，使用现有共享 domain interface，并分别完成独立
   code review。
4. Webhook 测试只允许 injectable client、mock route 或 loopback server；不得
   在开发/CI 中联系真实第三方 endpoint，也不得在命令、日志或报告中输出
   DSN、DEK、credential envelope 或 Webhook URL。
5. 最终交付必须重新执行完整本地门禁、最新远端 CI、review thread closure、
   clean merged-main 回归；本存档检查点不能替代这些证据。

## 验证与维护入口

1. 修改领域术语或授权边界前，阅读 [`CONTEXT.md`](../../CONTEXT.md)、[`ARCHITECTURE.md`](../../ARCHITECTURE.md) 和 [ADR-0008](../adr/0008-separate-platform-and-project-roles.md)。
2. 角色、路由、会话、OpenAPI 或迁移变更必须同步更新 Human/Agent/MCP/A2A 契约与负向授权测试；平台角色不能成为项目测试的捷径。
3. 迁移发布先按[数据库迁移手册](../operations/database-migrations.md)备份、运行、复核 checkpoint 和 runtime RLS；不手工删除 checkpoint 或旧角色列。
4. 当次发布使用[测试指南](../testing_guide.md)和[发布证据规程](../testing/RELEASE_EVIDENCE_PROCEDURES.md)记录真实命令、环境和结果。未执行的门禁必须如实标为未执行。
