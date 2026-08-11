# Webhook 凭据生命周期与紧急撤销运行手册

本文用于 Webhook 凭据疑似泄露、错误投递或必须立即终止未完成投递的场景。
架构依据见
[ADR-0009](../adr/0009-webhook-delivery-credential-lifecycle.md)；静态加密与 DEK
维护见[数据库静态加密](../reference/DATA_AT_REST_ENCRYPTION.md)。

## 先判断要执行哪一种操作

| 操作 | 新事件 | 已冻结投递 | snapshot 凭据 |
| --- | --- | --- | --- |
| 编辑 | 使用新配置 | 按原配置继续，最长至原七天 deadline | 保留至成功、过期或撤销 |
| 停用 | 不再新建 | 按原配置继续，最长至原 deadline | 同上 |
| 普通删除 | 不再新建；配置 soft-delete | 按原配置继续，最长至原 deadline | 同上 |
| 紧急撤销 | 配置立即禁用 | `pending/failed/dead → expired`；`processing` 仅报告 | 所有尚未粉碎的凭据以 `revoked` 粉碎 |

普通编辑、停用或删除都不是撤销。只有确认必须终止已经提交但尚未开始的投递时，
才执行紧急撤销。

## 权限与前置检查

1. 使用目标 Project 的 active、精确 `project_admin` Membership。平台管理员、
   项目经理或应急平台角色都不能代替项目管理员。
2. 在 Webhook Settings 刷新列表，取得目标配置当前 `resource_version`。确认
   Project、配置名称和非敏感配置 ID；不要复制或记录 URL、secret、access token
   或密文 envelope。普通配置更新和软删除会在同一事务递增此版本；若刷新后发生
   任一变更，旧 `If-Match` 必须失败，操作员应重新确认当前状态。
3. 检查投递列表中 `pending`、`failed`、`dead` 和 `processing` 的数量，并记录
   事件 ID/投递 ID 等非敏感标识。`processing` 可能已经发出，无法召回。
4. 为一次操作生成新的随机 `Idempotency-Key`。网络超时后重试同一意图时必须复用
   同一个 key 和原请求；不要用新 key 猜测重试。

Human API 的固定路径为：

```http
POST /api/projects/{projectKey}/admin/agents/webhooks/{webhookID}/emergency-revoke
If-Match: "v{resource_version}"
Idempotency-Key: {random-unique-key}
```

请求没有 body。UI 会显示不可逆确认。响应只允许包含配置 ID、`disabled`、
过期数量、in-flight 数量、粉碎数量、`revoked` 和通用 receipt。

## 执行与核验

1. 在 Webhook Settings 选择“紧急撤销”，逐项阅读不可逆说明后确认。
2. `409 version_conflict` 表示 preflight 已陈旧：刷新并重新核对状态，再以新的
   操作意图和幂等 key 提交。`409 idempotency_conflict/in_progress` 不得绕过；
   等待或查询同一请求结果。`403/404` 按无授权或资源不可见处理，不跨项目探测。
3. 成功后核对：
   - 配置状态是 `disabled`；
   - `pending/failed/dead` 已投影为中文终态“已过期”，且没有回放入口；
   - `succeeded/expired` 历史未被改写；
   - `processing` 数量与响应一致；
   - 审计和 Domain Event 只有标识、状态、计数、版本，且没有 URL 或凭据。
4. 等待已报告的 in-flight 尝试结束。它们的第二 HTTP gate 会在尚未发出时拒绝；
   已经离开进程的请求不能召回。如第三方提供独立 token 吊销或入站规则，可按其
   官方流程并行处理，但不要在 ChronoDesk 记录中粘贴第三方秘密。
5. 若仍需此集成，创建或更新配置并签发全新第三方凭据。被粉碎的 snapshot 不能
   恢复，也不能通过 replay 复活。

## 七天有限 replay 与清理

- snapshot 的绝对 deadline 是父 Domain Event 时间加七天，配置变更、回放和 Worker
  重试都不能延长。
- 成功投递以 `succeeded` 粉碎凭据；超过 deadline 以 `expired` 粉碎；紧急撤销以
  `revoked` 粉碎。已经粉碎或过期的行跳过解密和 rewrap。
- `expired` 不会设置父 Domain Event 的 `PublishedAt`；若同一事件已因其他
  destination 成功而存在 `PublishedAt`，必须原样保留。
- cleanup、claim、replay、finalize 和 HTTP gate 都遵循
  `Project/auth → WebhookConfig → OutboxDelivery → Snapshot` 锁序。

## DEK 轮换

1. 在 keyring 中加入新 DEK 并设为 primary，暂时保留旧 DEK。
2. 停止并调查任何正在进行的 credential maintenance 或结构迁移，避免并发维护。
3. 执行 `credential-maintain -rotate`。它在一个事务中按稳定 Project、配置和 live
   snapshot 顺序 rewrap；任一坏 envelope 或 CAS 冲突使全部回滚。
4. 使用 old+new keyring 执行 `-validate-only`。
5. 在隔离维护环境用 new-only keyring 再执行 `-validate-only`。live snapshot
   继续使用历史 AAD `webhook_configs/<config-id>/<field>`。
6. 只有所有实例加载 new-only 配置并验证成功后才能移除旧 DEK。已经粉碎或过期
   的空字段不应被 rewrap。

命令输出只能保留数量和安全错误码，不得输出 DSN、DEK、credential envelope、
Webhook URL 或秘密。

## 发布、迁移与回滚顺序

紧急撤销与 Worker 共享数据库锁协议。滚动发布必须按以下顺序：

1. 先完成可信备份及隔离恢复演练，运行结构迁移和 startup schema/RLS 门禁。
2. 部署包含 unscoped config 锁锚、有限 replay、claim/finalize/cleanup 与双 HTTP
   gate 的 Worker 版本；此阶段不要暴露紧急撤销入口。
3. 排空或停止所有旧 Webhook Worker，确认没有缺少 config barrier 的旧实例。
4. 启动全部新 Worker 并通过 loopback/injectable 的 claim/replay/HTTP gate
   演练。
5. 最后启用 Human admin endpoint、OpenAPI 和 Web UI。

应用层 endpoint/UI 可以回滚，但生命周期兼容 Worker 必须保留。不得在存在 live
snapshot 或已经发生 revoke 后回滚到缺少 config barrier/双 HTTP gate 的 Worker。
若必须回滚该 Worker，先关闭 Webhook egress 和 claim，保持数据库不变，采用前向
修复；不要恢复凭据字段或把 `expired` 改回可投递状态。

## 备份、WAL、PITR 与物理擦除边界

紧急撤销执行的是逻辑 crypto-shred：当前行的 credential envelope 被清空，应用
之后无法解密或投递。它不表示旧密文已从以下位置即时物理擦除：

- PostgreSQL WAL、流复制槽和只读副本；
- 云数据库快照、PITR 增量、离线备份和存储介质快照；
- 在撤销前完成的合规归档。

所有备份必须独立加密、限制访问并执行有期限的销毁策略。不能为了加速物理擦除而
跳过审计、直接修改 WAL 或删除未经验证的 DEK。

PITR 恢复到撤销前时间点会恢复旧配置和旧密文。恢复演练和真实恢复都必须：

1. 在隔离网络、Webhook egress 默认关闭的环境恢复；
2. 校验备份完整性、RLS、schema checkpoint 和 keyring；
3. 从不可变审计/事件或外部 incident 清单重新应用恢复点之后的 revoke；
4. 运行 credential validation，确认已撤销 snapshot 为空且不可 replay；
5. 仅在 loopback/injectable 零 HTTP 演练通过后重新开放 egress。

DEK 删除是独立的高风险动作。只有 live 配置与 snapshot 已在 new-only keyring 下
验证、备份保留和法务要求允许、恢复流程也不再需要旧 key 时，才能按密钥管理制度
销毁旧 DEK。

## 安全演练

测试和演练只允许 injectable client、mock route 或 loopback server，不得联系真实
第三方 endpoint。最小演练应覆盖：

- exact `project_admin` 成功，经理、平台角色和跨项目访问失败；
- stale version、缺失 `If-Match`、重复/冲突幂等 key；
- transaction 中途失败后配置、投递、snapshot 全部回滚；
- revoke 与 claim/replay 竞态无死锁、无复活；
- revoke 后 client factory 与真实 HTTP 计数都为零；
- response、receipt、event、audit、日志和演练报告不含 URL 或秘密。

把命令、环境类型、非敏感计数和结论写入发布证据；不要收集请求正文、响应正文、
数据库连接串或凭据值。
