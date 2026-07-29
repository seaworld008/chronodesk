# ChronoDesk Agent 原生化全功能测试报告

- 测试日期：2026-07-29
- 测试分支：`feat/agent-native-platform`
- 测试环境：最终源码构建的本地 API / Web，连接云 PostgreSQL 与新建的真实
  Upstash Redis
- 总体结论：**本地、云服务与干净容器发布门禁通过**

## 1. 执行摘要

本轮已完成 Agent 原生业务底座、服务主体与策略控制、MCP `2026-07-28`、
A2A 官方发布 `v1.0.1`（线协议 `A2A-Version: 1.0`）、评论与附件、可靠
Outbox、幂等、版本与租约、对象级权限、全站中文化以及企业列表的可调列宽改造。

最终结果：

| 范围 | 结果 | 说明 |
| --- | --- | --- |
| Go 单元/集成测试 | 通过 | 全包普通与 Race 测试均通过 |
| Python 黑盒 API 回归 | 通过 | `15 passed`，通过真实 API 与云 PostgreSQL |
| Playwright 浏览器 E2E | 通过 | 干净 Docker 环境 `13 passed`，连续两轮结果一致 |
| 前端类型/Lint/生产构建/审计 | 通过 | TypeScript、ESLint、生产构建和依赖审计均通过 |
| Chrome 全功能回归 | 通过 | 全页面中文、侧栏、表格不换行与列宽拖拽均验证 |
| OpenAPI/OAuth | 通过 | OpenAPI 3.2 严格 lint 零错误、零警告，发现与 scope 契约正常 |
| MCP 最新协议 | 通过 | 官方 Inspector `2.0.0` 真实 E2E 与订阅通过 |
| A2A `v1.0.1` | 通过 | 线协议 `1.0`；官方 TCK 经授权代理验证的目标子集通过 |
| 云 PostgreSQL | 通过 | 健康检查与直连 `SELECT 1` 成功 |
| 云 Redis | 通过 | 新建的真实 Upstash Redis 连接与应用集成通过 |
| Docker / 安全扫描 | 通过 | 空卷启动、幂等迁移、15 项 API、13 项浏览器、优雅停机、非 root 与 Gitleaks 均通过 |
| PR / main 交付 | GitHub 记录 | [PR #3](https://github.com/seaworld008/chronodesk/pull/3) 是本次变更与远端 CI/合并状态的权威记录 |

## 2. 自动化测试结果

### 2.1 Go

以下命令全部通过：

```text
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

`govulncheck` 结果为：

- 业务代码可达漏洞：0
- 仅报告未导入、业务不可达的 `openpgp` 模块 warning；不在当前编译或调用路径

重点新增覆盖：

- 人类、服务主体、系统三类 Actor 的授权与审计。
- 同一幂等键重放、短 processing lease、崩溃恢复和并发原子接管。
- 旧 worker 的 fencing，禁止覆盖新 owner 的完成结果。
- Ticket 版本冲突和 Agent 租约冲突。
- 自动化事件、SLA、状态流转和 Outbox 恢复。
- 附件清理失败重试、重复投递幂等、路径穿越拒绝和 CloudEvent 脱敏。
- A2A 权限撤销后，旧 Task/Artifact 的重放、查询、列表和 SSE 均重新授权。
- 人类账号停用、删除、降权或修改密码后，旧 access token 立即失效。
- 生产关闭自动迁移时，缺少关键 Agent 原生字段会在启动阶段失败，而不是让调度器
  带着旧 Schema 运行。

### 2.2 Python 黑盒 API

连接最终 API 和云 PostgreSQL 执行黑盒回归。最终结果：

```text
15 passed
```

同一 15 项套件还在全新 Docker PostgreSQL/Redis 卷上重新执行，结果同为
`15 passed`。Docker 场景从镜像构建、依赖健康、迁移/种子、Web 启动到测试后
销毁卷均完整走通。

覆盖工单生命周期、自动化、系统配置、注册、刷新、登出、OTP、可信设备和备用码。

认证夹具已改为在每个用例结束后由管理员软删除临时用户，避免给云数据库遗留
测试账号。工单、规则、模板、SLA 和系统配置用例也执行了对应清理或恢复。

发布收尾已移除不适用于黑盒 HTTP 测试的 `pytest-cov` 配置；Go 覆盖率由
`go test` 单独度量，Pytest 只报告实际 Interface 验收结果，不再产生无意义的
“No data was collected” 提示。

### 2.3 Playwright 浏览器 E2E

在由最终 Docker 镜像、全新 PostgreSQL/Redis 卷和幂等种子数据组成的环境中执行：

```text
PLAYWRIGHT_REUSE_SERVER=1 TEST_BASE_URL=http://127.0.0.1:3000 make e2e
13 passed
```

完整 13 项套件连续执行两轮均通过。测试固定单 worker，因为场景会修改共享管理员
账号、系统配置和测试数据；这也符合生产登录限流对同一 Docker/NAT 来源 IP 的
约束。除专门的登录用例外，其余场景复用短期 API 会话，避免测试框架自身制造
无意义的匿名登录流量。

覆盖真实 UI 登录、工单创建/分配/解决/关闭/删除、通知、自动化规则、邮件与系统
配置、用户和设置导航，以及客户身份下的通知筛选隔离、Agent 管理菜单隐藏和直接
路由防护。所有定位器均基于当前中文可访问名称，不依赖已删除的英文文案。

### 2.4 前端与依赖

以下命令全部通过：

```text
npm run typecheck
npm run lint
npm run build
npm run audit:security
make build
git diff --check
```

此外，后端与前端 Docker 镜像构建通过。后端使用构建阶段单并发编译和
Alpine `3.24.1` 最小运行镜像，运行用户为非 root `chronodesk`，不包含 Go
工具链；独立 `chronodesk-migrate` 二进制支持容器内迁移。Compose 会等待
PostgreSQL、Redis 和 API 三方健康后才启动 Web。Gitleaks 扫描未发现当前源码
凭据泄漏，报告和扫描输出均未记录数据库或 Redis 凭据。

GitHub Advanced Security 的 PR 增量检查又发现两项高风险问题：依赖审计脚本存在
文件检查/读取竞态，可信设备令牌曾由 JSON 返回并写入 Web Storage。两项均未使用
suppress：源码扫描改为 `O_NOFOLLOW` 打开并在同一文件描述符上完成
`fstat/read/close`；可信设备凭据改由后端 `HttpOnly`、
`SameSite=Strict`、生产环境 `Secure` Cookie 管理，前端与 JSON 契约均无法读取。
Go 安全契约、Python OTP 黑盒、Web 防回归扫描和完整 Docker 浏览器套件均通过。

容器内 43 个模型迁移与种子流程连续执行两次，第二次正确识别已有数据；API 接收
SIGTERM 后记录停机信号、停止调度器并完成 HTTP 优雅关闭，重新启动后
PostgreSQL、Redis 与 API 健康状态恢复。测试结束前 Outbox 为
`succeeded:180`，`pending/processing/failed/dead=0`，全部服务重启次数为 0。

安全审计只保留一个当前不可达的 React Router RSC 专用例外，复核截止日期为
`2026-09-30`。当前项目未使用 RSC；审计脚本会在出现 RSC 调用或到期时自动失败。

依赖已更新到当前兼容线。以下大版本未盲目升级：

- MUI 保持 `7.3.11`：MUI 9 与当前 React-Admin 存在已复现的运行时不兼容。
- React Router 保持 `7.18.2`：React-Admin 当前依赖范围不支持 Router 8。
- TypeScript 保持 `6.0.3`：TypeScript 7 属于新的不兼容大版本。

## 3. Chrome 全功能验收

使用用户指定的 Chrome 插件执行。最终候选的浏览器错误基线之后：

- Console error：`0`
- 页面全局横向溢出：`0`
- 可见英文通用操作提示扫描：`0`

覆盖页面与流程：

- 登录、错误凭据中文反馈、会话保持。
- 仪表盘。
- 工单列表、搜索、筛选、列选择、创建、详情、编辑。
- 分配、升级、状态流转入口及高风险确认。
- 评论公开性、内部评论、附件公开性和上传入口。
- 工单历史、Actor 展示及操作名称中文化。
- 通知中心。
- 用户列表、创建、编辑、详情和删除安全确认。
- 自动化规则、自动化日志和中文空状态。
- 邮件设置、Webhook、系统配置与账号安全。
- AI Agent 控制中心、策略、租约、事件、Outbox 和熔断入口。

RBAC 实测：

- 非管理员不显示管理菜单，直接访问管理路由会被拒绝或重定向。
- `403` 不会错误清空登录会话。
- Agent 查看非本人创建且未分配给自己的工单时，编辑、流转、评论和附件写入口
  均隐藏或只读。
- 管理员高风险操作使用中文确认，不使用 React-Admin 的延迟撤销式删除。

附件 UI 的本地文件选择被 Chrome 扩展的
“Allow access to file URLs” 权限阻止，因此浏览器无法把本机测试文件交给页面。
应用侧附件 API、授权、哈希/扫描状态、Outbox 清理与重试已通过 API 和自动化测试；
此项记录为浏览器测试工具限制，不是应用接口失败。

## 4. 企业列表与列宽验收

所有 React-Admin Datagrid 和原生 MUI Table 均已接入统一企业表格能力：

- 默认单行显示，超长内容省略，关键字段提供完整内容 Tooltip。
- 表头拖动调整列宽。
- 双击恢复默认宽度。
- 键盘方向键调整，支持无障碍操作。
- 按表格和字段持久化到浏览器。
- 选择列与操作列固定。
- 窄屏或总列宽超出时，仅表格容器横向滚动。
- 左侧导航始终保持完整，不再被数据表撑宽或遮挡。

Chrome 实测证据：

```text
工单信息列：260 -> 284（拖动）
刷新页面：仍为 284（持久化）
双击：恢复 260
键盘 ArrowRight：每次增加 8px
通知表：table 1964px / wrapper 1226px / 页面无全局溢出
用户表：table 1732px / wrapper 1226px / 页面无全局溢出
工单表：table 1971px / wrapper 1226px / 页面无全局溢出
```

## 5. OpenAPI、OAuth、MCP 与 A2A

### 5.1 OpenAPI / OAuth

- OpenAPI：`3.2.0`
- Paths：31
- Operations：36
- Schemas：70
- 缺失 `operationId`：0
- 重复 `operationId`：0
- OAuth scopes：10
- Protected Resource Metadata：MCP、REST API、A2A 三个入口均为 200
- Authorization Server Metadata：200
- 三种 RFC 8707 resource 分别签发独立 audience；3×3 交叉验证只允许
  token 访问自身资源，另外六种组合均返回 401
- `/oauth/token` 使用独立的可信 ClientIP + 路由匿名限流，发现与健康入口不受影响

### 5.2 MCP `2026-07-28`

ChronoDesk 只实现 MCP `2026-07-28`，并使用官方 MCP Inspector
`2.0.0` 对最终服务执行真实 HTTP E2E：

- `server/discover`：通过，唯一版本为 `2026-07-28`
- `tools/list`：13 个工具，均声明 input/output schema
- `resources/list`：2 个资源
- `resources/templates/list`：3 个模板
- 真实工具调用与资源读取：通过
- 资源监听使用最新无状态 `subscriptions/listen`，真实订阅、事件接收与恢复通过
- 响应不产生旧 session header
- 旧 `initialize`：HTTP 404 / JSON-RPC `-32601`
- 旧 `2025-11-25`：HTTP 400 / JSON-RPC `-32022`

旧协议 Session、GET SSE、DELETE、旧 `resources/subscribe` 和兼容分支已移除。

### 5.3 A2A `v1.0.1`（线协议 `1.0`）

A2A 官方最新发布为 `v1.0.1`；补丁版本不参与线协议协商。ChronoDesk 的
Agent Card `protocolVersion` 与请求头均固定为 `1.0`，请求使用
`A2A-Version: 1.0`。

- Agent Card：200
- ETag 条件请求：304，空响应体
- Skills：5
- Streaming：声明并验证
- Push Notification：声明并验证
- Task 查询、列表、取消、`input-required`、SSE、游标重连和 Artifact：通过
- MCP 与 A2A 的 Ticket/Task 模型保持分离
- 权限撤销后，对持久化 Task 和 Artifact 的所有再读取入口重新授权

官方 TCK 经授权代理验证的目标子集结果：

| TCK 子集 | 结果 |
| --- | --- |
| Agent Card | `10/10` |
| JSON-RPC Error / ErrorInfo | `11 pass`，另有 `2` 项因能力未声明而跳过 |
| Data Model / serialization | `12/12` |
| SSE | `4/4` |

上述结果只代表与 ChronoDesk 已声明能力匹配、经授权代理可执行的官方 TCK
子集，**不表示已运行或通过官方 TCK 完整套件**。两项 capability skip 是
能力边界，不计为失败。

协议 Smoke 创建的临时凭据已撤销，服务主体已进入 `revoked`、只读和紧急停用
状态；16 条可识别测试 Task 均通过正式 A2A `CancelTask` 进入
`TASK_STATE_CANCELED`。Task、状态历史和操作审计按不可变要求保留，不做物理删除。
撤销后的旧访问令牌实测返回 401；最近 Outbox 中没有 `failed` 或 `dead` 投递。

## 6. 云服务验证

### 6.1 PostgreSQL：通过

- `/healthz`：HTTP 200，`dependencies.postgresql=ok`
- `/api/health`：HTTP 200，`database=connected`
- 云 PostgreSQL 直连 `SELECT 1`：成功
- 全部 Python 黑盒流程实际读写云 PostgreSQL：通过

最终启动前发现云库尚未应用新幂等 processing lease 字段：
`idempotency_records.completion_ttl_nanoseconds`。已执行幂等的定向增量迁移：

```text
go run ./cmd/migrate -only idempotency-processing-lease
```

迁移后：

- 最终服务在 `AUTO_MIGRATE=false` 下通过启动 Schema 校验。
- 同一 `Idempotency-Key` 连续创建两次均返回 201，`resource_id`、`event_id`、
  `operation_id` 和版本完全一致。
- 云库只生成 1 张工单；幂等记录为 `completed`，24 小时 TTL、完成时间和过期时间正确。
- `automation`、`event_stream`、`webhook` 三个 Outbox 目标均为 `succeeded`。
- 临时工单、服务主体、凭据、幂等记录及关联数据已精确清理。
- CloudEvent、策略决策和成功投递审计按不可变要求保留。

代码已增加启动 Schema 前置检查和可重复执行的定向迁移入口，避免未来漏迁移时
以部分可用状态启动。

观察到部分查询/测试步骤耗时偏高，例如完整工单生命周期约 45.59 秒、系统配置
回归约 30.60 秒。功能正确，但建议后续检查应用与数据库区域、连接池和索引命中。

### 6.2 Redis：通过

原云 Redis 端点失效后，最终候选改用新建的真实 Upstash Redis 重新完成验证：

- Upstash 连接、认证和 `PING`：通过。
- 应用 Redis 健康检查：通过。
- 缓存、跨实例限流、调度器租约与 Agent 运行控制路径：通过。
- 真实云 PostgreSQL、真实 Upstash Redis 与最终 API 的组合回归：通过。

所有连接信息均通过环境变量注入；测试报告、日志与提交内容未记录 endpoint、
token 或 password。

### 6.3 凭据泄漏处置：通过

发布前删除审计发现，历史一次性邮件修复脚本和已跟踪的本地 Claude 配置曾包含
同一条 Neon 数据库凭据。已按泄漏处置流程完成：

- 直接轮换云 PostgreSQL 登录角色密码。
- 验证新凭据可执行 `SELECT 1`，旧凭据已被拒绝。
- 原子更新仅本地、权限为 `0600` 且已忽略的 `server/.env`。
- 停止跟踪并忽略 `.claude/settings.local.json`，同时删除其中的数据库连接串。
- 删除含硬编码凭据的一次性修复脚本；当前源码快照 Gitleaks 复扫无泄漏。

历史 Git 对象中的旧密码已失效。没有执行会影响协作者克隆的历史重写；如组织策略
要求彻底擦除历史，应在团队协调窗口使用 `git filter-repo` 并强制所有工作副本
重新克隆。

## 7. 本轮修复的发布级缺陷

- 可信设备凭据曾进入登录 JSON 响应和浏览器 `localStorage`，一旦发生 XSS
  就会泄漏长期免 OTP 凭据；现只接受后端 HttpOnly Cookie，JSON 请求/响应和
  Web Storage 均由契约测试与源码扫描禁止。
- 前端依赖审计先 `stat` 再按路径读取文件，存在符号链接替换竞态；现拒绝源码
  symlink，并在带 `O_NOFOLLOW` 的同一文件描述符上校验和读取。
- MCP 与 A2A 曾分别实现不同的处理人校验，导致同一业务动作跨协议语义不一致；
  现统一收敛到领域服务并增加协议一致性测试。
- 未知或异常人类角色曾可能在前端权限分支中放行；现用统一访问控制 Module
  fail closed，客户身份不会请求管理员用户或 Agent 控制接口。
- 浏览器 E2E 并发登录会在 Docker/NAT 下命中真实匿名限流，同时英文 locator
  已与中文 UI 脱节；现固定共享状态套件为单 worker、复用短期会话并全面对齐中文
  可访问名称。
- A2A 持久化 Task/Artifact 在权限撤销后仍可重放读取。
- 幂等 processing 锁与 24 小时结果保留共用 TTL，崩溃后长期锁死。
- 管理员停用、删除或降权后，旧 access token 仍保留原角色权限。
- 删除工单后附件存储对象可能成为孤儿文件。
- React-Admin 表格总列宽撑大整个布局并遮挡左侧导航。
- React-Admin memo 导致列宽状态变化但表头不更新。
- Sticky 操作列背景透明导致底层字段文字叠加。
- 工单历史动作和系统 Actor 仍显示英文技术值。
- Python 认证回归创建的临时用户没有自动清理。
- 云 PostgreSQL 缺少新增幂等 lease 字段，但服务仍静默启动。
- Gin 默认信任所有代理，可能让伪造的转发头污染来源 IP 和审计；现改为默认不信任，
  仅通过 `TRUSTED_PROXIES` 显式配置。
- Python 工单生命周期用管理员自身收件箱断言客服通知，与对象级隔离矛盾；现改为
  独立临时客服、收件人自身查询、异步轮询 1 条分配和 2 条状态通知，并反向验证
  管理员不可见。
- `make smoke` 在 API/管理员登录失败时会整组跳过并返回成功；现改为严格检查
  `/healthz` 的 PostgreSQL/Redis 状态，登录或 token 缺失直接失败。
- 后端容器在启动阶段执行 `go run`，受限环境会编译 OOM；现改为多阶段构建、
  非 root 最小运行镜像和独立迁移二进制。
- 云库遗留的 11 张 `Auto Test Ticket <时间戳>` 自动化工单已按精确 ID 清理，
  关联通知同步删除，Outbox 最终无 pending/failed/dead，业务审计保留。
- Go 可执行入口曾是 879 行根 `main.go`，领域、协议和生命周期装配边界不清；
  现改为 `cmd/chronodesk` 最小入口、`internal/app` 组合根和可测试的优雅生命周期。
- scope 描述曾在模型、认证、MCP 与 OpenAPI 多处复制；现由
  `internal/agentcontract` 提供唯一协议中立契约，并用静态架构测试阻止领域层反向
  依赖 MCP、A2A、OpenAPI 或 HTTP Adapter。
- 仓库曾缺少许可证、路线图、贡献/安全/支持政策、ADR、CodeQL、Issue Forms 和
  可维护文档索引，并保留大量失效脚本与会话计划；现采用 Apache-2.0，补齐社区
  健康与供应链门禁，删除无消费者或已过期资产。30 个 Markdown 文件共 92 个
  相对链接复核为 0 个失效。

## 8. 发布决策

**本地、云服务与干净容器发布门禁通过。**

本报告确认本地与受控云环境中的最终测试证据。本次交付的远端检查、评审和
`main` 合并状态由
[GitHub PR #3](https://github.com/seaworld008/chronodesk/pull/3) 持久记录；
任何远端失败都必须修复并重新通过后才能合并。
