# ChronoDesk 全功能专业测试用例

> 基线日期：2026-07-29
>
> 目标版本：Agent-native / MCP `2026-07-28` / A2A `1.0.1`
>
> 适用范围：Go 服务、Human REST、Agent REST、MCP、A2A、PostgreSQL、Redis、Outbox、React 管理后台、真实 Chrome

## 1. 测试目标与发布判定

本用例集先于本轮新增自动化测试编写，是 ChronoDesk 合并 `main` 的验收契约。测试不以“页面能打开”或“接口返回 2xx”为完成标准，而要同时验证：

1. 业务结果正确；
2. 对象级权限、scope 与策略正确；
3. 数据库、缓存、Outbox、审计和资源版本保持一致；
4. 重试、并发、断线和进程退出后可恢复；
5. 人类后台全部中文、布局完整、表格不换行并可调整列宽；
6. MCP 与 A2A 只暴露当前稳定协议，不含旧协议执行路径；
7. 不泄露密码、token、密钥、内部路径、思维链或未授权内部评论；
8. 所有测试数据可识别、可清理，不破坏已有云数据。

发布必须同时满足：

- 所有 P0、P1 用例均有明确裁定：本轮可执行项 100% 通过；受环境约束未执行或仅
  部分覆盖的项目必须写明边界、风险与后续责任，不得伪造通过或留下未评估失败；
- P2 不得存在未评估失败；
- `go test ./...`、竞态、`go vet`、`govulncheck`、前端 typecheck/lint/build、安全审计、OpenAPI lint 全绿；
- PostgreSQL 迁移连续执行两次结果一致；
- Redis 真实读写、限流与租约测试通过；
- Docker 全新卷启动、迁移、种子、当次完整收集的 API 黑盒测试与 Playwright E2E 全部通过（数量记录到执行报告，不在契约中写死）；
- Chrome 实测所有一级页面和关键增删改流程，无控制台错误、无失败请求、无遮挡或横向内容丢失；
- Outbox 最终无非预期 `failed/dead_letter`，关键操作可由审计记录完整还原；
- GitHub CI、CodeQL、密钥扫描全部绿色。

## 2. 测试分层与自动化映射

| 层级 | 目的 | 主要实现 |
|---|---|---|
| Go 单元/组件 | 边界、状态机、事务、策略、序列化、并发 | `server/internal/**/*_test.go` |
| Go 集成 | SQLite/PostgreSQL 行为、Redis、Outbox、迁移、竞态 | Go integration tests、Docker Compose |
| API 黑盒 | Human REST、认证、错误码、分页、上传、管理功能 | `server/tests/**/*.py` |
| 协议契约 | OpenAPI、CloudEvents、MCP、A2A | Go contract tests、raw HTTP fixtures |
| 浏览器自动化 | 真实前后端、角色页面、表单、表格、附件、设置 | `web/e2e/**/*.spec.ts` |
| Chrome 人工验收 | 视觉完整性、中文、交互手感、控制台/网络 | Chrome 插件逐页检查 |
| 安全/韧性 | 越权、SSRF、注入、异常循环、恢复、限流 | Go/Python/Playwright/CI |

自动化状态标记：

- `GO`：Go 测试；
- `PY`：Pytest 黑盒；
- `PW`：Playwright；
- `CH`：Chrome 插件人工验收；
- `CI`：流水线或扫描器；
- `NEW`：本轮必须新增或补强。

逐条可定位证据见
[`CASE_EVIDENCE_MANIFEST.tsv`](CASE_EVIDENCE_MANIFEST.tsv)，人工、Chrome 与故障
注入步骤见
[`RELEASE_EVIDENCE_PROCEDURES.md`](RELEASE_EVIDENCE_PROCEDURES.md)。Manifest
只描述证据入口，`execution_record=not_recorded` 绝不等同于已经通过。

## 3. 测试身份与数据

| 代号 | 身份 | 权限用途 |
|---|---|---|
| H-ADMIN | 人类管理员 | 系统配置、用户、Agent 控制、全部工单 |
| H-SUP | 人类主管 | 队列、分配、升级、内部评论、统计 |
| H-AGENT-A/B | 两个人类客服 | 并发、对象级权限、分配与处理 |
| H-CUSTOMER-A/B | 两个客户 | 仅本人工单、公开评论、越权验证 |
| SP-FULL | 服务主体 | 最小工作 scope + 显式策略 |
| SP-READ | 只读服务主体 | 查询与订阅，所有写入应拒绝 |
| SP-DISABLED | 停用服务主体 | 所有 token/操作应拒绝 |
| SYSTEM | 系统 Actor | SLA、自动化、Outbox 与审计 |

所有新增真实数据使用唯一前缀 `E2E-<run-id>-`。测试结束仅清理本轮生成的数据；不得批量修改未带该前缀的既有数据。

## 4. 基础设施、启动与迁移

| ID | P | 操作 | 预期 | 自动化 |
|---|---:|---|---|---|
| INF-001 | P0 | 缺少数据库配置启动服务 | 启动失败且不打印 DSN | GO/CI |
| INF-002 | P0 | 缺少 Redis 或连接失败启动 | fail-closed；健康检查明确不可用 | GO/CI |
| INF-003 | P0 | 使用当前云 PostgreSQL 执行迁移 | 全部模型、索引、约束存在 | GO/PY |
| INF-004 | P0 | 同一云库连续执行两次迁移 | 第二次无破坏、无重复数据、退出 0 | GO/PY |
| INF-005 | P0 | 旧角色与旧自动化事件值迁移 | 精确归一化，未知值失败关闭 | GO |
| INF-006 | P0 | 凭据 `validate → rotate → validate` | 只处理当前密文信封；明文失败关闭 | GO/PY |
| INF-007 | P0 | 全新 Docker 卷启动 | PostgreSQL、Redis、API、Web 全部健康 | CI/PW |
| INF-008 | P1 | 进程收到 SIGTERM | 停止接流量，后台任务和连接优雅退出 | GO/PY |
| INF-009 | P0 | `/healthz` 正常 | 返回组件化状态且不含密钥 | PY/PW |
| INF-010 | P0 | PostgreSQL 中断后请求 | 返回稳定 5xx 错误，不 panic、不假成功 | GO |
| INF-011 | P0 | Redis 中断时限流/租约 | 高风险 Agent 写入失败关闭 | GO |
| INF-012 | P1 | 时区为 Asia/Shanghai | UI 与 API 时间一致，协议时间仍为 RFC3339 | GO/PW/CH |
| INF-013 | P1 | 迁移恢复点越界 | 明确拒绝，不执行部分 DDL | GO |
| INF-014 | P1 | Docker 非 root 用户运行 | API/迁移/凭据工具可运行，无多余权限 | CI |

## 5. 认证、会话与账号安全

| ID | P | 操作 | 预期 | 自动化 |
|---|---:|---|---|---|
| AUTH-001 | P0 | 使用合法资料注册 | 默认 `customer`，密码仅存 bcrypt，返回无敏感字段 | GO/PY |
| AUTH-002 | P0 | 注册缺字段、错误邮箱、弱密码、超长输入 | 400，稳定中文错误，不写库 | GO/PY |
| AUTH-003 | P0 | 重复邮箱并发注册 | 仅一个账号成功，其余 409 | GO |
| AUTH-004 | P0 | 正确账号密码登录 | 签发绑定 session 的短期 access/refresh token | GO/PY/PW |
| AUTH-005 | P0 | 错误密码连续登录 | 统一错误、防枚举、触发限流/锁定策略 | GO/PY |
| AUTH-006 | P0 | 不存在邮箱登录 | 与错误密码响应等价，不泄露账号存在性 | GO/PY |
| AUTH-007 | P0 | 缺失、畸形、过期、错误签名 token | 401 且无业务查询 | GO/PY |
| AUTH-008 | P0 | 数据库角色改变后使用旧 access token | `stale_token`，失败关闭 | GO |
| AUTH-009 | P0 | 使用旧 `user/superuser` JWT | 401，绝不映射放行 | GO |
| AUTH-010 | P0 | refresh token 首次使用 | 原会话旋转，旧 refresh 立即失效 | GO/PY |
| AUTH-011 | P0 | refresh token 重放 | 拒绝并撤销相关会话 | GO |
| AUTH-012 | P0 | 单设备注销 | 当前 refresh/session 失效 | GO/PY/PW |
| AUTH-013 | P0 | 全设备注销 | 所有现有 session 失效 | GO/PY |
| AUTH-014 | P0 | 修改密码 | 原会话撤销，新密码可登录，旧密码拒绝 | GO/PY/PW |
| AUTH-015 | P1 | 忘记/重置密码 token 正常、过期、重放 | 单次有效，过期/重放拒绝 | GO/PY |
| AUTH-016 | P1 | 邮件验证 token 正常、错误、重放 | 状态正确且幂等 | GO/PY |
| AUTH-017 | P0 | OTP 启用、验证、禁用 | secret 加密存储，状态机与会话正确 | GO/PY/PW |
| AUTH-018 | P0 | OTP 备用码使用与重放 | 每码单次有效，存储仅为哈希 | GO |
| AUTH-019 | P0 | 可信设备创建与撤销 | 仅 HttpOnly Cookie，不写 localStorage | GO/PW/CH |
| AUTH-020 | P0 | 凭据/密码/OTP 数据响应与日志扫描 | 不出现 hash、secret、token、cookie | GO/CI |
| AUTH-021 | P1 | 上传头像与访问头像 | 文件名安全、大小/MIME 校验、对象授权正确 | GO/PY/PW |
| AUTH-022 | P1 | 登录历史删除与可信设备撤销 | 仅本人可操作，UI 状态即时更新 | GO/PW/CH |

## 6. 人类角色与对象级授权

| ID | P | 操作 | 预期 | 自动化 |
|---|---:|---|---|---|
| RBAC-001 | P0 | Admin 读取/修改全部工单 | 允许并写审计 | GO/PY |
| RBAC-002 | P0 | Supervisor 查看队列、分配与升级 | 允许；系统管理仍拒绝 | GO/PY |
| RBAC-003 | P0 | Agent 读取未分配/分配给自己的工单 | 按可见性规则返回 | GO/PY |
| RBAC-004 | P0 | Agent 修改无权工单 | 403，不泄露内部字段 | GO/PY |
| RBAC-005 | P0 | Customer 读取本人创建工单 | 允许，内部评论/内部字段过滤 | GO/PY |
| RBAC-006 | P0 | Customer 读取另一客户工单 | 404 或策略化 403，不泄露是否存在 | GO/PY |
| RBAC-007 | P0 | Customer 添加公开评论 | 允许；内部评论类型拒绝 | GO/PY/PW |
| RBAC-008 | P0 | 非 Admin 访问用户/系统/Agent 控制 API | 403 | GO/PY/PW |
| RBAC-009 | P0 | 停用/删除用户继续使用 token | 401 | GO |
| RBAC-010 | P0 | 四种人类角色之外创建/更新用户 | 400；数据库 CHECK 也拒绝 | GO/PY |
| RBAC-011 | P1 | 管理员不能删除/降级最后一个管理员 | 明确拒绝，避免管理面失联 | GO/PY |
| RBAC-012 | P1 | Human 管理员角色变更审计 | 操作者、目标用户路径/ID、方法、结果、时间与来源完整；敏感 query 脱敏（Agent 写操作的 diff/event 契约不强加给 Human 管理面） | GO/PY |

## 7. 工单生命周期、查询与并发

| ID | P | 操作 | 预期 | 自动化 |
|---|---:|---|---|---|
| TKT-001 | P0 | 创建最小合法工单 | 编号唯一、version=1、创建 Actor/事件/审计一致 | GO/PY/PW |
| TKT-002 | P0 | 创建缺标题/描述、非法枚举、超长文本 | 400，不写任何半成品事件 | GO/PY |
| TKT-003 | P0 | 同一 `Idempotency-Key` 重复创建 | 仅一个工单，返回首次回执 | GO/PY |
| TKT-004 | P0 | 同一幂等键不同请求体 | 409 幂等冲突 | GO |
| TKT-005 | P0 | 按 ID 获取工单 | ETag/version/Actor/权限裁剪正确 | GO/PY |
| TKT-006 | P0 | 获取不存在或越权工单 | 稳定 404/403 机器错误 | GO/PY |
| TKT-007 | P0 | 合法字段更新 | version+1、ETag 更新、changed_fields 精确 | GO/PY/PW |
| TKT-008 | P0 | `If-Match` 缺失/错误 | 428/409 `version_conflict`，不覆盖数据 | GO/PY |
| TKT-009 | P0 | 两个写方同时使用同版本 | 仅一个成功，另一个 409 | GO |
| TKT-010 | P0 | 分配给有效 Agent | 人类/Actor 投影、事件、通知、历史一致 | GO/PY/PW |
| TKT-011 | P0 | 分配给停用用户或无效服务主体 | 400/409，不改变原分配 | GO/PY |
| TKT-012 | P0 | 取消分配 | 两类 assignee 字段同步清空，产生事件 | GO/PY |
| TKT-013 | P0 | 合法状态流转 | 仅允许状态机边，时间字段正确 | GO/PY/PW |
| TKT-014 | P0 | 非法状态跳转/终态修改 | 409，说明允许的下一状态 | GO/PY |
| TKT-015 | P0 | 解决、关闭、重开 | resolved_at/closed_at 与 SLA 统计正确 | GO/PY |
| TKT-016 | P1 | 升级工单 | 优先级/处理链、原因、事件、通知正确 | GO/PY/PW |
| TKT-017 | P1 | 转移工单 | 新处理人、原因、历史和通知正确 | GO/PY |
| TKT-018 | P0 | 删除单个工单 | 权限、软删/关联策略、删除事件正确 | GO/PY |
| TKT-019 | P0 | 批量删除含无权/不存在工单 | 整批策略明确，不出现部分静默成功 | GO/PY/PW |
| TKT-020 | P0 | 事务化批量更新 | 每项有回执，失败语义一致，版本不丢失 | GO/PY/PW |
| TKT-021 | P1 | 列表按状态/优先级/类型/来源/处理人筛选 | 结果准确，组合筛选稳定 | GO/PY/PW/CH |
| TKT-022 | P1 | 标题/编号搜索含 `% _ ' 中文` | 参数化、安全、结果正确 | GO/PY/PW |
| TKT-023 | P0 | cursor 首/中/尾/空页 | cursor 不透明、无重复/漏项 | GO/PY |
| TKT-024 | P0 | 超大 limit、负数、伪造 cursor | 限制到上限或 400，不放大查询 | GO/PY |
| TKT-025 | P1 | 我的、未分配、逾期、SLA 违约列表 | 对象权限和条件精确 | GO/PY/PW |
| TKT-026 | P1 | 标签、自定义字段、客户信息 | JSON Schema、中文显示、更新保持 | GO/PY/PW |
| TKT-027 | P0 | 工单历史查询 | 顺序稳定、真实 event_id/version、内部可见性正确 | GO/PY/PW |
| TKT-028 | P1 | WebSocket 收到本人站内通知已读/未读变化 | 仅认证收件人收到准确未读数；非法 ID 拒绝，Origin 受控；Ticket 领域事件订阅只走 MCP/Agent Events，不虚构 WebSocket Ticket 流 | GO/PW |
| TKT-029 | P0 | 业务事务回滚 | 工单、历史、事件、Outbox 全部不产生半写 | GO |
| TKT-030 | P1 | SLA 首次响应与解决耗时 | 边界时间和暂停状态计算正确 | GO |

## 8. 评论与附件

| ID | P | 操作 | 预期 | 自动化 |
|---|---:|---|---|---|
| CNT-001 | P0 | 添加公开评论 | version+1、评论/事件/历史/通知同事务 | GO/PY/PW |
| CNT-002 | P0 | 添加内部评论 | 仅 Agent/Supervisor/Admin 可见 | GO/PY/PW |
| CNT-003 | P0 | Customer 尝试内部/系统评论 | 403 | GO/PY |
| CNT-004 | P0 | 空、超长、HTML/提示注入评论 | 边界拒绝或作为不可信文本保存，不执行指令 | GO/PY |
| CNT-005 | P1 | 回复评论与 reply_count | 父评论必须属于同工单，计数原子更新 | GO/PY |
| CNT-006 | P0 | 上传合法小文件 | SHA-256、真实 MIME、scan=pending、事件一致 | GO/PY/PW |
| CNT-007 | P0 | 空文件、超大文件、危险扩展、MIME 欺骗 | 400/413/415，临时文件清理 | GO/PY |
| CNT-008 | P0 | 路径穿越文件名 | 安全归一化或拒绝，不能越出存储根 | GO/PY |
| CNT-009 | P0 | 病毒扫描 clean/infected/error | 只有 clean 可下载；状态审计完整 | GO/PY/PW |
| CNT-010 | P0 | 未授权下载或猜测附件 ID | 403/404，不泄露 storage_path | GO/PY |
| CNT-011 | P0 | 并发上传与版本冲突 | 无孤儿文件、仅合法版本成功 | GO |
| CNT-012 | P1 | 将已上传附件关联评论 | 正式附件表更新，不能跨工单关联 | GO/PY |
| CNT-013 | P1 | 旧附件投影清理 | 空值、JSON `null`、`[]` 可安全删除旧列；任何不可证明的非空引用必须阻断迁移且保持原数据不变 | GO |
| CNT-014 | P0 | API/日志响应扫描 | 不返回本地路径、provider URL、access token、原始 metadata | GO/CI |

## 9. 自动化、SLA 与调度

| ID | P | 操作 | 预期 | 自动化 |
|---|---:|---|---|---|
| AUT-001 | P0 | 创建当前 CloudEvent 类型规则 | 保存成功，条件/action Schema 完整 | GO/PY/PW |
| AUT-002 | P0 | 使用 `ticket.created` 等旧类型 | 中文 400，生产不接受兼容值 | GO/PY |
| AUT-003 | P0 | 未知类型/动作/字段/操作符 | 400，未知持久值迁移失败关闭 | GO |
| AUT-004 | P0 | 工单创建/更新/分配/评论事件 | 仅精确匹配规则执行一次 | GO/PY |
| AUT-005 | P0 | 重复投递同一 DomainEvent | 自动化动作去重 | GO |
| AUT-006 | P0 | 规则动作失败 | Outbox 可重试，日志含原因但无密钥 | GO/PY |
| AUT-007 | P0 | resolved/closed 旧规则迁移 | transitioned + status 条件保持原语义 | GO |
| AUT-008 | P1 | scheduled check | 使用当前持久 CloudEvent 类型，按计划执行 | GO |
| AUT-009 | P1 | SLA 配置创建/边界/停用 | 时间阈值与状态正确 | GO/PY/PW |
| AUT-010 | P0 | SLA 违约并发检查 | 只产生一次违约事件/升级 | GO |
| AUT-011 | P1 | 模板创建、读取、应用 | 字段映射与权限正确 | PY/PW |
| AUT-012 | P1 | 快捷回复创建与使用 | 使用计数、内容与权限正确 | PY/PW |
| AUT-013 | P1 | 规则日志与统计 | 顺序、状态、耗时、错误信息正确 | PY/PW |
| AUT-014 | P0 | 恶意规则值尝试 SQL/模板注入 | 参数化且不执行任意代码 | GO/PY |
| AUT-015 | P1 | 调度器多实例竞争 | Redis lease 保证只有一个实例执行 | GO |

## 10. 通知、Webhook、Outbox 与实时事件

| ID | P | 操作 | 预期 | 自动化 |
|---|---:|---|---|---|
| EVT-001 | P0 | 业务写入成功后立即终止进程 | DomainEvent/Outbox 已提交，重启后继续投递 | GO |
| EVT-002 | P0 | Webhook 订阅 canonical CloudEvent | 直接按 `event.Type` 匹配，不降级简写 | GO/PY/PW |
| EVT-003 | P0 | 重复 CloudEvent 投递 | 目标端和本端日志可去重 | GO |
| EVT-004 | P0 | Webhook 2xx/4xx/5xx/超时 | 成功、不可重试、指数退避分类正确 | GO |
| EVT-005 | P0 | 重试耗尽 | dead-letter 可观测、可由管理员回放 | GO/PY/PW |
| EVT-006 | P0 | 管理员回放 Outbox | 新 delivery attempt，原事件 ID 不变 | GO/PW |
| EVT-007 | P0 | Webhook URL 指向 localhost/私网/重定向私网 | SSRF 拒绝 | GO/PY |
| EVT-008 | P0 | Webhook secret/access token | 数据库存信封、API 响应与日志不泄露 | GO/CI |
| EVT-009 | P1 | 企业微信/钉钉/飞书/Slack/Teams/custom 模板 | 结构正确、超长内容安全截断 | GO |
| EVT-010 | P1 | Webhook 测试按钮 | 中文反馈，真实日志可查看 | PY/PW/CH |
| EVT-011 | P1 | 站内通知创建、读取、全部已读 | 数量、状态、未读计数一致 | GO/PY/PW |
| EVT-012 | P0 | 用户访问他人通知 | 403/404 | GO/PY |
| EVT-013 | P1 | 通知偏好更新 | 只影响本人且持久化 | GO/PY/PW |
| EVT-014 | P0 | CloudEvent 信封 | `id/source/type/subject/time/dataschema` 与扩展字段完整 | GO |
| EVT-015 | P0 | trace/correlation/causation 链 | 跨工单操作、自动化、Webhook 可追踪 | GO |
| EVT-016 | P0 | Outbox worker 多实例 | 锁与状态转换避免重复并发投递 | GO |
| EVT-017 | P1 | WebSocket 未授权订阅/跨用户事件 | 拒绝或过滤 | GO/PW |
| EVT-018 | P1 | 事件列表 cursor 恢复 | 无重复/漏项，伪造 cursor 拒绝 | GO/PY |

## 11. 服务主体、策略、租约与 Agent REST

| ID | P | 操作 | 预期 | 自动化 |
|---|---:|---|---|---|
| AGT-001 | P0 | 创建服务主体 | 独立身份、scope/策略/限额，无人类账号复用 | GO/PW |
| AGT-002 | P0 | 一次性返回 client secret | 仅创建/轮换响应一次，缓存禁止 | GO/PW |
| AGT-003 | P0 | 正确 client_credentials 换 token | 校验 issuer/audience/expiry/scope | GO/PY |
| AGT-004 | P0 | 错 audience/issuer/过期/撤销凭据 | 401 | GO |
| AGT-005 | P0 | scope 缺失调用接口 | 403 `policy_denied` + 机器原因码 | GO/PY |
| AGT-006 | P0 | 显式 deny 覆盖 allow | 拒绝并记录 policy_decision_id | GO |
| AGT-007 | P0 | 默认高风险动作 | 无显式策略时拒绝删除、批量、外部通知、系统管理 | GO |
| AGT-008 | P0 | 服务主体停用/紧急停用 | 现有 token 立即不可用 | GO/PY/PW |
| AGT-009 | P0 | 全局只读/紧急停止 | Agent 写入全部拒绝，读取按策略保留 | GO/PY/PW |
| AGT-010 | P0 | 单 Agent 速率/并发上限 | 429/策略错误、RFC 9333 headers 正确 | GO/PY |
| AGT-011 | P0 | 异常循环检测 | 重复操作触发熔断并审计 | GO |
| AGT-012 | P0 | Agent 列表/读取工单 | cursor、machine DTO、无假人类字段/内部泄露 | GO/PY |
| AGT-013 | P0 | Agent 创建工单 | Actor=service_principal、版本/事件/回执完整 | GO/PY |
| AGT-014 | P0 | Agent 更新工单无 lease | 409 `lease_conflict` | GO/PY |
| AGT-015 | P0 | claim 工单 | 返回唯一 lease_id/expires_at/ticket_version | GO/PY |
| AGT-016 | P0 | 两个 Agent 同时 claim | 仅一个成功 | GO |
| AGT-017 | P0 | heartbeat 正常/过期/非持有人 | 续租或明确冲突 | GO/PY |
| AGT-018 | P0 | release 正常/重复/非持有人 | 幂等或明确冲突，不释放他人租约 | GO/PY |
| AGT-019 | P0 | 租约到期 | 自动释放，新 Agent 可领取 | GO |
| AGT-020 | P0 | lease 有效但 version 过期 | 409 `version_conflict` | GO |
| AGT-021 | P0 | Agent 评论/附件 | lease、scope、版本、策略同时校验 | GO/PY |
| AGT-022 | P0 | 每次 Agent 写入审计 | actor/credential/policy/protocol/digest/diff/event 完整 | GO/PY |
| AGT-023 | P0 | 不提供思维链字段 | Schema 拒绝或忽略；仅保存简短理由/证据/来源 | GO |
| AGT-024 | P1 | 管理员强制释放租约/接管 | 立即生效并产生审计事件 | GO/PW/CH |
| AGT-025 | P1 | OAuth Protected Resource Metadata/AS discovery | URL、issuer、resource、scope、缓存正确 | GO/PY |

## 12. MCP `2026-07-28` 唯一协议

| ID | P | 操作 | 预期 | 自动化 |
|---|---:|---|---|---|
| MCP-001 | P0 | `server/discover` | 仅声明 `2026-07-28` 和实际能力 | GO/PY |
| MCP-002 | P0 | 缺少/旧 MCP version | `-32022`，不协商降级 | GO/PY |
| MCP-003 | P0 | 调用旧 `initialize` 或发送旧 `notifications/initialized` | 带 ID 的旧请求方法为 `-32601`；无 ID 客户端通知按当前 Streamable HTTP 先返回 `-32600`，绝不进入兼容分发 | GO/PY |
| MCP-004 | P0 | GET/DELETE `/mcp`、Session header、Last-Event-ID、`notifications/cancelled` | GET/DELETE 与客户端通知拒绝；已移除 Header 按最新版规范忽略且不回显；HTTP 取消通过关闭响应流完成 | GO/PY |
| MCP-005 | P0 | 每请求缺少必需 `_meta` | 协议错误 | GO |
| MCP-006 | P0 | `Mcp-Method` 与 body 不一致 | `-32020` | GO/PY |
| MCP-007 | P0 | `Mcp-Name` 与 tool/resource 不一致 | `-32020` | GO/PY |
| MCP-008 | P0 | `tools/list` | 稳定排序、严格 Schema、scope/风险/幂等注解完整 | GO |
| MCP-009 | P0 | 每个 ticket 工具 happy path | 结构化 output 符合 outputSchema | GO/PY |
| MCP-010 | P0 | 工具参数缺失、类型错误、额外危险字段 | `-32602`，无副作用 | GO |
| MCP-011 | P0 | 工具 scope/策略/lease/version 冲突 | 结构化机器错误准确 | GO/PY |
| MCP-012 | P0 | `resources/list/templates/read` | URI、MIME、可信标记、权限正确 | GO |
| MCP-013 | P0 | 不存在资源 | `-32602` | GO |
| MCP-014 | P0 | `subscriptions/listen` 指定资源 | 只推请求范围内变更，带 subscriptionId | GO/PY |
| MCP-015 | P0 | 旧 `resources/subscribe` | `-32601` | GO |
| MCP-016 | P0 | 列表/读取缓存字段 | `ttlMs/cacheScope` 成对且符合数据敏感度 | GO |
| MCP-017 | P0 | 所有普通结果 | `resultType=complete` | GO |
| MCP-018 | P1 | traceparent/tracestate/baggage | 传递到事件和审计，不信任非法值 | GO |
| MCP-019 | P0 | 提示注入文本作为工单/评论内容 | 仅数据，不改变工具描述或服务策略 | GO/PY |
| MCP-020 | P0 | 超大 body/深层 JSON/订阅超限 | 413/结构化错误，资源有界 | GO |

## 13. A2A `v1.0.1`

| ID | P | 操作 | 预期 | 自动化 |
|---|---:|---|---|---|
| A2A-001 | P0 | 获取 Agent Card | ETag、认证、skills、streaming/push、协议版本准确 | GO/PY |
| A2A-002 | P0 | Agent Card If-None-Match | 304 | GO |
| A2A-003 | P0 | 未认证/错误 token JSON-RPC | 401，响应契约正确 | GO/PY |
| A2A-004 | P0 | message/send 创建 Task | Task 与 Ticket 分表，linked_ticket_id 正确 | GO/PY |
| A2A-005 | P0 | ticket-intake/query/work/comment/escalation skills | 路由到正确领域命令 | GO/PY |
| A2A-006 | P0 | Task get/list 状态历史 | 独立状态机、顺序与 cursor 正确 | GO |
| A2A-007 | P0 | Task cancel | 只取消 Task，不隐式改变 Ticket 状态 | GO/PY |
| A2A-008 | P0 | `input-required` | 仅请求输入；Ticket 不自动 pending | GO |
| A2A-009 | P0 | 补充输入后继续 | context 连贯、无重复业务动作 | GO |
| A2A-010 | P0 | Artifact 返回工单快照/回执/附件链接 | 授权、MIME、完整性正确 | GO |
| A2A-011 | P0 | SSE 流式更新 | 状态顺序正确，断线按 cursor 恢复 | GO/PY |
| A2A-012 | P0 | Push 配置 SSRF/secret | 私网拒绝、secret 信封存储、Outbox 投递 | GO |
| A2A-013 | P0 | 重复 message/task 请求 | 幂等，不重复创建 Ticket/Task | GO |
| A2A-014 | P0 | 未知字段与已删除旧字段 | 真正未知字段按规范处理，已删除字段明确拒绝 | GO |
| A2A-015 | P0 | 跨服务主体读取/取消 Task | 403/404 | GO |
| A2A-016 | P1 | A2A 与 MCP 操作同一 Ticket | 共享版本、lease、策略和审计，无双写分叉 | GO/PY |

## 14. 管理后台与 Chrome 页面验收

| ID | P | 操作 | 预期 | 自动化 |
|---|---:|---|---|---|
| UI-001 | P0 | 登录、退出、刷新会话 | 全中文、焦点正确、错误可理解 | PW/CH |
| UI-002 | P0 | 左侧导航 1440/1280/1024 高度 | 所有功能项可见、可滚动、不被底部遮挡 | PW/CH |
| UI-003 | P0 | 工单仪表盘 | 卡片、图表、空态/加载/错误态完整 | PW/CH |
| UI-004 | P0 | 工单列表 | 单元格默认不换行、溢出省略、悬浮可读 | PW/CH |
| UI-005 | P0 | 工单列表拖动列宽 | 可调、最小/最大合理、刷新后持久 | PW/CH |
| UI-006 | P0 | 用户/通知/自动化/日志等全部列表 | 同一企业表格规范，无异常换行 | PW/CH |
| UI-007 | P0 | 低宽度列表 | 使用受控横向滚动，不压坏导航/操作列 | PW/CH |
| UI-008 | P0 | 创建工单所有 Tab | 必填、边界、选择器、中文验证正确 | PW/CH |
| UI-009 | P0 | 查看工单 | 详情、历史、评论、附件、权限动作完整 | PW/CH |
| UI-010 | P0 | 编辑/分配/状态/升级/删除 | 成功提示中文，数据刷新，无双击重复 | PW/CH |
| UI-011 | P0 | 评论和附件真实操作 | 上传进度、扫描状态、下载与失败提示正确 | PW/CH |
| UI-012 | P1 | 工单筛选/搜索/清空/分页 | URL/状态一致，刷新可恢复 | PW/CH |
| UI-013 | P0 | 用户列表/创建/详情/编辑 | 四角色、Tab、状态、敏感信息隐藏 | PW/CH |
| UI-014 | P0 | 通知列表/已读/全部已读 | 未读数同步，无英文残留 | PW/CH |
| UI-015 | P0 | 自动化规则列表/创建/详情/编辑 | 仅 canonical 事件，中文名+机器类型 | PW/CH |
| UI-016 | P1 | 自动化日志 | 时间、状态、错误、空态完整 | PW/CH |
| UI-017 | P0 | 系统设置读写 | 分类、验证、保存、失败恢复正确 | PW/CH |
| UI-018 | P0 | 邮件设置与连接测试 | 密码永不回显，结果中文 | PW/CH |
| UI-019 | P0 | Webhook 设置创建/测试/日志 | canonical 事件、URL/secret 行为正确 | PW/CH |
| UI-020 | P0 | Agent 控制中心总览 | 服务主体、scope/策略/凭据、租约、Outbox、审计完整 | PW/CH |
| UI-021 | P0 | 全局只读/紧急停止 | 高风险确认、状态醒目、恢复正确 | PW/CH |
| UI-022 | P1 | 强制释放租约/回放 Outbox/扫描附件 | 中文反馈、状态实时更新 | PW/CH |
| UI-023 | P1 | 可信设备页面 | 当前设备标记、撤销与空态正确 | PW/CH |
| UI-024 | P0 | 401/403/404/409/422/429/500 | 统一中文提示，不暴露内部堆栈 | PW/CH |
| UI-025 | P0 | 浏览器控制台与网络 | 0 console error；无失败静态资源/重复风暴 | PW/CH |
| UI-026 | P1 | 键盘 Tab/Enter/Escape | 焦点顺序、对话框和菜单可用 | PW/CH |
| UI-027 | P1 | 文本放大 200% | 关键信息/按钮不丢失、不重叠 | CH |
| UI-028 | P1 | 中文长标题、长邮箱、长事件 ID | 表格不换行，详情可完整查看 | PW/CH |
| UI-029 | P1 | 空数据、慢请求、断网后恢复 | 骨架/空态/重试清晰，无永久 loading | PW/CH |
| UI-030 | P0 | 全站英文残留扫描 | 页面操作提示与错误全面中文；协议机器值除外 | PW/CH |

## 15. 安全、性能与故障恢复

| ID | P | 操作 | 预期 | 自动化 |
|---|---:|---|---|---|
| SEC-001 | P0 | SQL 注入字符进入搜索/排序/过滤 | 参数化；排序仅白名单 | GO/PY/CodeQL |
| SEC-002 | P0 | XSS/HTML/Markdown 注入 | UI 不执行脚本，服务标记不可信 | GO/PW |
| SEC-003 | P0 | CRLF 注入邮件头、日志和 Webhook headers | 拒绝/规范化 | GO/CodeQL |
| SEC-004 | P0 | SSRF URL、DNS 重绑定、重定向链 | 每跳校验并拒绝私网 | GO/PY |
| SEC-005 | P0 | 超大整数/负数/32 位边界 | 稳定 400，不溢出、不超分配 | GO/CodeQL |
| SEC-006 | P0 | 超大 JSON、multipart、压缩炸弹 | body/内存/文件大小有界 | GO/PY |
| SEC-007 | P0 | CORS 允许/拒绝来源 | 仅配置来源，凭据策略正确 | GO/PY |
| SEC-008 | P0 | 安全响应头与 Cookie | CSP/HSTS/HttpOnly/SameSite 等符合环境 | GO/PW |
| SEC-009 | P0 | 密钥扫描、CodeQL、依赖扫描 | 0 未处理高危；受控例外有期限和不可达证据 | CI |
| SEC-010 | P0 | 日志注入与敏感字段 | 单行安全日志、摘要化、无秘密 | GO/CodeQL |
| PERF-001 | P1 | 10k 工单列表第一页 | 索引命中、响应时间在门槛内 | GO |
| PERF-002 | P1 | 并发列表与更新 | 无竞态、连接池稳定、错误率门槛内 | GO |
| PERF-003 | P1 | WebSocket/订阅并发上限 | 有界、超限明确拒绝 | GO |
| PERF-004 | P1 | Outbox 大量积压后恢复 | 有界批次、公平重试、最终清空 | GO |
| RES-001 | P0 | Outbox 投递中进程退出 | claim 超时后可恢复，不丢事件 | GO |
| RES-002 | P0 | Redis 短暂断开/恢复 | 租约与执行 guard 不出现双持有 | GO |
| RES-003 | P0 | PostgreSQL 事务超时 | 完整回滚，错误可观测 | GO |
| RES-004 | P1 | Webhook/A2A SSE 客户端断线 | cursor 恢复，无无限重放 | GO/PY |
| RES-005 | P0 | 异常自动化循环 | 熔断、告警、管理员可接管 | GO/PW |
| RES-006 | P1 | 数据备份恢复后启动 | 迁移、密文验证、版本约束全部通过 | GO |

## 16. 端到端发布验收场景

### E2E-AGENT-001：外部 Agent 完整处理工单

1. 读取 Protected Resource Metadata 和授权服务器发现；
2. 使用服务主体凭据获取最小 scope token；
3. 通过 MCP `server/discover` 和 `tools/list` 发现能力；
4. 查询未分配队列并读取工单；
5. claim 返回 lease；
6. 添加内部评论和附件；
7. heartbeat；
8. 更新优先级并 transition；
9. release；
10. 通过 `subscriptions/listen`/events 收到变更；
11. 审计中能按 correlation_id 还原全部动作。

预期：全过程没有假人类 Actor、没有重复事件、没有版本覆盖、没有未授权外部通知。

### E2E-HUMAN-001：人类客服后台完整处理工单

1. Admin 创建 Agent 与 Customer；
2. Customer 创建工单和公开评论；
3. Agent 在列表筛选到工单、调整列宽、打开详情；
4. Agent 分配给自己、添加内部评论、上传附件；
5. Supervisor 升级并转移；
6. Agent 解决，Customer 查看并关闭；
7. 所有页面提示为中文，客户永远看不到内部评论。

### E2E-RECOVERY-001：事务成功后进程退出

1. 创建写操作并在提交后、投递前停止 API；
2. 确认 DomainEvent 与 Outbox 已持久化；
3. 重启服务；
4. 确认 Webhook/自动化/A2A push 恢复投递；
5. 重复事件不造成第二次业务动作。

### E2E-CONCURRENCY-001：两个 Agent 竞争同一工单

1. 两个服务主体同时读取相同 version；
2. 同时 claim；
3. 获胜方更新并 heartbeat；
4. 失败方尝试评论/transition；
5. 验证仅持有有效 lease 与 version 的一方成功；
6. 到期或 release 后另一方可重新 claim。

## 17. 执行顺序

1. 静态检查、单元测试、契约测试；
2. 数据库迁移与真实云 PostgreSQL/Redis 集成；
3. Docker 全新环境与 API 黑盒；
4. Playwright 全功能 E2E；
5. Chrome 插件逐页真实操作与视觉/网络/控制台验收；
6. 安全、竞态、故障恢复与 Outbox 收敛检查；
7. 更新测试报告和用例到自动化映射；
8. 推送分支，等待 GitHub 全部检查绿色；
9. 合并 `main`，再次验证默认分支工作流与仓库安全状态。
