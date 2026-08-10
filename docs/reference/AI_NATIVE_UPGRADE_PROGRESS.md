# ChronoDesk AI 原生多项目状态

> 更新时间：2026-08-10
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

## 当前完成度与未闭环范围

| 领域 | 已有基础 | 仍需在发布前或后续变更中验证/完成 |
| --- | --- | --- |
| 多项目身份 | 平台/项目角色拆分、Membership/Grant、项目范围路由与 RLS 方向 | OIDC、SAML、SCIM，Team/Queue 控制面边界的最终 RLS 覆盖 |
| Human Web | `/human-openapi.json`、类型化 P1 操作与工作台边界 | 未列入 P1 的遗留路由不可据此宣称稳定公共契约 |
| Agent 与协议 | Agent REST、MCP、A2A 的共享领域入口和版本门禁 | Proposal 执行 Adapter、关系 API 对外契约、SDK 同步与主动 A2A 委派 |
| 集成 | Connection、Mapping、Inbox、Receipt、ExternalLink、Outbox 基础模型 | 邮件双向同步、CSV、Kafka/AMQP、内网 Relay 与额外语言 SDK 的生产化 |
| Webhook / Outbox | 七天绝对凭据期限、终态粉碎、过期 cleanup、双 HTTP gate、有限 replay、安全管理投影、live snapshot DEK validate/rewrap | 紧急撤销命令与 UI、运维 ADR/runbook、备份/PITR 演练 |
| 知识与检索 | 项目知识版本、ACL、摄取状态、OpenSearch 混合检索、引用反馈与模型策略 | 生产对象存储、扫描/解析/摄取 Worker、模型网关运行基线与完整 Copilot 闭环 |
| AI 协作 | Run、Proposal、Approval、Handoff、Lease 与审计模型 | 内置 Copilot、完整执行工作台、对象存储/扫描/解析 Worker 的生产闭环 |
| 运行可靠性 | RLS、审计哈希链、凭据维护、迁移 checkpoint | WORM、保留归档、备份恢复自动化、容量基准与故障演练 |

“已有基础”仅表示存在实现与相应契约/测试位置，不能代替本次提交的实际测试、真实依赖健康检查或远端 CI 结果。

## 2026-08-10 代码存档检查点与继续开发顺序

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
- claim、HTTP client 创建、`Do` 前第二 gate、finalize、cleanup 和 replay
  均复核 scope、event、snapshot、deadline 与 claim generation。
- `expired` 投递的 Agent/Human 安全投影、稳定
  `outbox_replay_expired` 409、两处管理 UI 的中文终态与 replay 隐藏。
- database-secret startup/maintenance 对每个 live snapshot 的三类 envelope
  执行 Project-scoped 验证和 rewrap；历史 AAD 继续绑定原 Webhook Config ID。

### 明确未完成

1. **Webhook emergency revoke**
   - 仍缺 exact `project_admin` 的 preflight `resource_version` 读取、强制
     `If-Match`/Idempotency-Key 的 revoke 命令、配置禁用、未终态 snapshot
     粉碎、pending/failed/dead 过期、in-flight 计数、secret-free event/audit、
     Webhook Settings UI 和 PostgreSQL race。
   - 普通 Webhook edit/disable/delete 仍保留 deadline 前已经提交的 immutable
     delivery；它们不是 emergency revoke。
2. **注册后端原子性**
   - 前端已消费 verification-disabled 的完整 session，但后端仍应把
     User、Profile、refresh digest、LoginHistory、successful LoginAttempt、
     DomainEvent 与 welcome Outbox 收敛到一个
     `AtomicRegistrationRepository` transaction。
3. **Workflow 重复 lifecycle category 兼容**
   - 后续应允许不同 state key 投影到相同 canonical category，同时继续拒绝
     duplicate state/transition key、未知端点、非法/重复 role；运行时权限语义是
     同一 canonical `(from, to)` 的 edge union。
4. **运维文档与演练**
   - 仍需 ADR-0009、Webhook credential runbook、迁移发布/回滚、backup/WAL/PITR
     边界和真实恢复演练；应用层 shred 不等于物理介质即时擦除。

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
3. 优先完成 emergency revoke，再完成注册原子性和 workflow 兼容。每一项先写
   mutation-sensitive RED，使用现有共享 domain interface，分别做独立 code
   review 后再进入下一项。
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
