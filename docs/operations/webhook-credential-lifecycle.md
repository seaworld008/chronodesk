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
| 紧急撤销 | 配置立即禁用且四项可变凭据清空 | `pending/failed/dead → expired`；prepared 可终止，未知或已授权的 `processing` 仅报告 | 所有尚未粉碎的凭据以 `revoked` 粉碎 |

普通编辑、停用或删除都不是撤销。只有确认必须终止已经提交但尚未开始的投递时，
才执行紧急撤销。

## `dispatch_started_at` 三态与竞态边界

Webhook `processing` 投递必须按以下三态判读：

| 值 | 含义 | 撤销与恢复规则 |
| --- | --- | --- |
| `NULL` | 旧版本或未知状态 | 保守计为 in-flight；首次 claim 可完成，崩溃后不能 reclaim，只能等待 deadline cleanup |
| `dispatch_started_at == locked_at` | prepared，绑定当前 claim generation，尚未提交 dispatch authorization | 紧急撤销可终止；stale claim 可安全进入新的 prepared generation |
| `dispatch_started_at > locked_at` | dispatch authorization 已提交 | 可能已经外部可见或仍在进程内；不能自动重发，只在 deadline cleanup 时关闭 |

新 Worker 在 claim 时把 `dispatch_started_at` 与 `locked_at` 写为同一个 claim
时间，形成 generation-bound prepared。紧接 HTTP client `Do` 之前，它在一个短
事务内取得 config 生命周期锁，并以 CAS 把 marker 改为严格晚于 `locked_at` 的
时间戳。该 config 锁是 dispatch start 与紧急撤销的线性化边界：

- 撤销先提交：prepared 投递转为 `expired`，后续 start CAS 失败，HTTP 调用数为零；
- start 先提交：撤销把该行报告为 in-flight，不承诺零 HTTP；
- 严格晚于 `locked_at` 的 marker 只证明授权提交，不证明请求已经离开进程。

若 Worker 在写入 dispatch-authorized marker 后、持久化明确传输结果前崩溃，
系统不会自动 reclaim 或重发该次投递，以免产生不确定的重复外部副作用；它只会在
原七天 deadline 到达后由 cleanup 关闭。混合版本期间的 `NULL` 也不得猜测或改写
为 prepared。若发现 `dispatch_started_at < locked_at`，应按数据库边界漂移
fail closed，不得把它解释为可安全 reclaim。

PostgreSQL 和 SQLite 的数据库 tuple fence 约束
`processing → processing` claim generation 变化。唯一允许的 stale reclaim 是
prepared → 新 prepared，并且必须同时满足：

- `attempts` 恰好增加一；
- `locked_at` 严格向前；
- `locked_by` 非空；
- `lock_token` 是不同于旧 token 的新 UUIDv7；
- `dispatch_started_at` 同步重绑为新的 `locked_at`。

该 fence 阻断旧 Worker stale-reclaim `NULL` 或 dispatch-authorized 行，也阻断
marker 保留、降级或跨 generation 复用。旧 Worker 仍可从 `pending/failed` 首次
claim 并留下 `NULL`，当前尝试可以完成；若它崩溃，该未知尝试不能再被领取。

## 权限与前置检查

1. 使用目标 Project 的 active、精确 `project_admin` Membership。平台管理员、
   项目经理或应急平台角色都不能代替项目管理员。
2. 在 Webhook Settings 刷新 exact-admin preflight，取得目标配置当前
   `resource_version`。soft-delete 后只允许通过 tombstone preflight 选择目标。
   该投影仅包含配置 ID、状态、删除标记、紧急撤销标记和版本，不包含 URL、
   secret、access token 或密文 envelope。普通 `PUT` 与 `DELETE` 都要求强
   `If-Match`，并在配置变更的同一事务 CAS 此版本；若刷新后发生任一变更，旧
   `If-Match` 必须失败，操作员应重新确认当前状态。
3. 检查投递列表中 `pending`、`failed`、`dead` 和 `processing` 的数量，并记录
   事件 ID/投递 ID 等非敏感标识。`processing` 的 in-flight 计数包含
   legacy/unknown `NULL` 和严格晚于 `locked_at` 的 marker；它表示不能安全召回，
   不证明请求已经发出。
4. 为一次操作生成新的随机 `Idempotency-Key`。网络超时后重试同一意图时必须复用
   同一个 key 和原请求；不要用新 key 猜测重试。

Human API 的固定路径为：

```http
POST /api/projects/{projectKey}/admin/agents/webhooks/{webhookID}/emergency-revoke
If-Match: "v{resource_version}"
Idempotency-Key: {random-unique-key}
```

请求没有 body。读取 tombstone/preflight 不等于授权执行，UI 必须再显示一个独立的
不可逆确认。POST 会重新校验 exact `project_admin`、版本和终态。响应只允许包含
配置 ID、`disabled`、过期数量、in-flight 数量、粉碎数量、`revoked` 和通用
receipt。

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
   - mutable config 的 `secret`、`previous_secret`、
     `previous_secret_expires_at`、`access_token` 已全部清空；
   - 审计和 Domain Event 只有标识、状态、计数、版本，且没有 URL 或凭据。
4. 等待已报告的 in-flight 尝试自然结束或到达 deadline。该计数不能用于判断请求
   是否已经离开进程，也不能作为自动重发依据。如第三方提供独立 token 吊销或
   入站规则，可按其官方流程并行处理，但不要在 ChronoDesk 记录中粘贴第三方秘密。
5. 若仍需此集成，创建或更新配置并签发全新第三方凭据。被粉碎的 snapshot 不能
   恢复，也不能通过 replay 复活；已经紧急撤销的配置是终态，必须创建新的配置，
   不能通过普通编辑恢复。

## 七天有限 replay 与清理

- snapshot 的绝对 deadline 是父 Domain Event 时间加七天，配置变更、回放和 Worker
  重试都不能延长。
- 成功投递以 `succeeded` 粉碎凭据；超过 deadline 以 `expired` 粉碎；紧急撤销以
  `revoked` 粉碎。已经粉碎或过期的行跳过解密和 rewrap。
- `expired` 不会设置父 Domain Event 的 `PublishedAt`；若同一事件已因其他
  destination 成功而存在 `PublishedAt`，也不得清空或改写，必须原样保留。
- stale reclaim 只适用于 marker 等于当前 `locked_at` 的 prepared generation。
  `NULL` 保守等待，dispatch-authorized marker 崩溃态不自动重发；两者只在原
  deadline 到达后由 cleanup 关闭。
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

紧急撤销与 Worker 共享数据库锁协议。`dispatch_started_at` 是兼容的 nullable
列，数据库 tuple fence 是跨版本安全边界；滚动发布必须按以下顺序：

1. 先完成可信备份及隔离恢复演练，运行结构迁移和 startup schema/RLS 门禁。
2. 先增加 nullable 列并保持 `NULL`，安装并验证 PostgreSQL/SQLite tuple fence。
   不得设置默认值，也不得把存量 `NULL` 批量回填或重绑为 prepared；否则会把未知
   或已开始的旧投递误报为可安全撤销。
3. 逐步部署写 generation-bound prepared、在 `Do` 前提交 strictly-later marker
   的新 Worker。与其共存的旧 Worker 仍可首次 claim `pending/failed` 并留下
   `processing/NULL`；当前尝试可完成，但数据库必须阻断其 stale reclaim
   `NULL`、prepared 或 dispatch-authorized generation。
4. 观察旧 `NULL` 投递自然完成或等到原 deadline cleanup，确认处理队列排空。
   期间不得通过改 marker 伪造排空，也不得把 dispatch-authorized marker 降级为
   prepared。
5. 使用 loopback/injectable 演练 revoke-first 零 HTTP、start-first in-flight、
   mixed-version `NULL` 保守处理、旧 claim tuple 拒绝和新 prepared reclaim，
   再启用或继续开放 Human admin endpoint、OpenAPI 和 Web UI。任何共存版本都
   必须遵守 config barrier 和 HTTP gate；不遵守者不能与已开放的紧急撤销入口
   共存。

应用层 endpoint/UI 可以回滚，数据库兼容列和 tuple fence 必须保留。回滚到仍
遵守 config barrier 和 HTTP gate 的旧 Worker 会在首次 claim 时重新产生 `NULL`；
当前尝试可以完成，崩溃后必须保守等待 deadline，不能 stale reclaim。不得删除列
或 fence、回填 prepared、恢复凭据、降级 dispatch-authorized marker，或把
`expired` 改回可投递状态。若回滚版本不遵守共同锁协议，先关闭 Webhook egress
和 claim，保持数据库不变并采用前向修复。

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
- revoke-first 后 client factory 与 HTTP 计数为零；start-first 稳定报告
  in-flight，不把 dispatch-authorized marker 当作已离开进程的证明；
- mixed-version `processing/NULL` 不 reclaim、不改写，等待自然完成或 deadline；
- PostgreSQL/SQLite 拒绝旧 Worker 对 `NULL`/dispatch-authorized generation 的
  stale claim tuple，且只允许满足完整 tuple 的 prepared → 新 prepared；
- dispatch-authorized marker 后崩溃不自动重发，仅由 deadline cleanup 关闭；
- response、receipt、event、audit、日志和演练报告不含 URL 或秘密。

把命令、环境类型、非敏感计数和结论写入发布证据；不要收集请求正文、响应正文、
数据库连接串或凭据值。
