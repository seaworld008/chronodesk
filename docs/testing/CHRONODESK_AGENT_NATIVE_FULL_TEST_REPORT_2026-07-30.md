# ChronoDesk Agent 原生化全功能测试报告

- 报告日期：2026-07-30
- 测试对象：分支 `feat/agent-native-platform` 的合并前工作树候选
- 测试范围：Go 服务、Python 黑盒 API、React 管理后台、Chrome 真实交互、
  PostgreSQL/Redis、OpenAPI、OAuth、MCP、A2A、容器与安全门禁
- 详细用例：
  [ChronoDesk 全面测试用例](CHRONODESK_COMPREHENSIVE_TEST_CASES_2026-07-29.md)
- 证据索引：[Case Evidence Manifest](CASE_EVIDENCE_MANIFEST.tsv)
- 发布规程：[人工、Chrome 与故障注入证据规程](RELEASE_EVIDENCE_PROCEDURES.md)
- 远端交付：[GitHub PR #3](https://github.com/seaworld008/chronodesk/pull/3)

> 本报告不记录数据库连接串、Redis 端点、密码、Token、Cookie 或其他凭据。
> 提交 SHA 暂不填写：报告生成时对象仍是“合并前工作树候选”，最终提交与远端
> Checks 以 PR #3 为准。以下本地结果属于工作树候选观察证据；提交后还要由远端
> CI/CodeQL 将结果绑定到不可变提交。

## 1. 执行结论

当前候选已完成本地最终源码、干净容器、真实云依赖与协议互操作门禁；仍必须等待
PR Checks 和 CodeQL 全绿，才能合并。**不得把本报告解读为已经合并到 `main`**。

已经取得的主要证据：

| 验证面 | 实际结果 | 证据边界 |
| --- | --- | --- |
| 测试设计 | 236 条用例，P0 185、P1 51 | 是用例与证据定位覆盖，不是 236 条全部执行通过 |
| Evidence Manifest | 236/236 用例均有证据定位 | `execution_record` 仍全部为 `not_recorded` |
| Python 黑盒 API | `76 passed` | 真实 API、PostgreSQL/Redis 就绪检查失败时 fail closed |
| Playwright | 13 个 spec、27 个测试，`27 passed` | 最终 Docker 浏览器环境，单 worker 执行 |
| Chrome 真实交互 | 通过已列流程；最终 Console 0 error/warn/issue | 使用 Chrome DevTools plugin；bundled extension 通道不可用 |
| OpenAPI 静态契约 | 34 Path Items、39 个路径操作、87 Schemas、1 Webhook | 共 40 个唯一 `operationId`，含 Webhook 操作 |
| PostgreSQL 迁移 | 本地连续两次成功；云端完整扫描后安全恢复并再次幂等重跑 | 7 个 A2A 关联标识列均由运行时门禁验证为 255 字符 |
| MCP | 自动化协议契约与 Inspector 2.0 真实互操作冒烟通过 | 不能称为完整官方 conformance |
| A2A | 线协议 `1.0` 与发布版 `v1.0.1` 契约已覆盖 | 官方 TCK 的公开 Agent Card 子集 10/10；不能称完整 TCK 通过 |
| 真实 Upstash Redis | TCP/RESP 与 REST/HTTPS PING 2/2；两个集成测试通过 | 未对共享云服务执行破坏性故障或压力测试 |
| PR / main | 待最终 Checks 和 CodeQL 全绿后合并 | PR #3 是远端状态的权威记录 |

## 2. 测试用例与证据模型

### 2.1 用例规模

236 条用例按优先级分布：

| 优先级 | 数量 | 含义 |
| --- | ---: | --- |
| P0 | 185 | 身份、权限、业务一致性、协议、安全、数据完整性与发布阻断项 |
| P1 | 51 | 管理体验、可观测性、韧性、性能与补充协议行为 |
| 合计 | 236 | 当前 Agent 原生系统的发布验收基线 |

Evidence Manifest 按主要证据类型分布：

| 类型 | 数量 | 如何判定 |
| --- | ---: | --- |
| `automated` | 99 | 可由 Go/Pytest 等自动化证据独立覆盖 |
| `hybrid` | 87 | 必须组合自动化、Playwright、Chrome 或人工证据 |
| `release_manual` | 32 | 每次发布需要人工或受控环境记录 |
| `fault_injection` | 15 | 只能在一次性 Docker/预发布环境执行 |
| `ci_gate` | 3 | 由远端 CI/安全门禁给出最终结果 |
| 合计 | 236 | 分类互斥 |

### 2.2 必须避免的误读

- Manifest 校验证明“每条用例都能定位到预期证据”，不证明证据已在本次候选上执行。
- `automated 99` 是用例的主要证据分类，不等于当前一次命令只收集 99 个测试。
- `hybrid 87` 只有在组合证据均完成后才可判定；不能仅凭其中一个 Go 测试通过。
- `release_manual 32`、`fault_injection 15` 不得被单元测试结果替代。
- Manifest 的 236 条 `execution_record` 当前均为 `not_recorded`；本次真实执行结果
  由本报告和 PR Checks 单独记录，未回填伪造的逐项状态。

## 3. 已执行的自动化测试

### 3.1 Go 与数据库专项

最新 A2A 标识长度与幂等冲突、Actor 投影幂等和 SMTP 取消竞态修复后，最终源码
已执行并通过：

```text
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

`govulncheck` 结果为实际调用路径 0 个漏洞；依赖模块中另有 1 个已知漏洞，但当前
代码没有调用对应路径。GitHub Actions 的 `actionlint`、Compose 配置校验以及
`git diff --check` 也全部退出 0。

隔离 PostgreSQL 迁移集成测试已通过：

```text
CHRONODESK_POSTGRES_INTEGRATION=1 \
go test ./internal/database \
  -run TestPostgresA2AIdentifierMigrationIntegration -count=1 -v
```

该测试在一次性 schema 中验证：

- 旧 `varchar(64)` / `varchar(128)` 结构可幂等升级。
- 迁移连续运行两次不破坏主键和唯一索引。
- 下列 7 个列均升级为 255 字符，并可写入 255 个 Unicode 字符：
  - `agent_tasks.context_id`
  - `agent_tasks.execution_message_id`
  - `agent_messages.id`
  - `agent_messages.context_id`
  - `agent_task_events.context_id`
  - `domain_events.correlation_id`
  - `domain_events.causation_id`
- 协议边界按 Unicode 字符数而不是 UTF-8 字节数判断，255 字符接受、256 字符拒绝。
- 若结构被人为回退到 64 字符，运行时前置校验会 fail closed。

### 3.2 Python 黑盒 API

最终 Docker 服务上执行完整 Pytest 黑盒回归：

```text
76 passed in 16.69s
```

套件按 HTTP 公开接口验证，而不是直接绕过服务访问数据库。覆盖：

- 注册、登录、刷新、登出、OTP、备用码、可信设备与旧会话撤销。
- 人类、服务主体、系统 Actor 的身份和权限边界。
- 工单创建、列表、查询、更新、分配、流转、升级、历史和通知隔离。
- 公开/内部评论、附件上传、哈希、扫描状态和下载授权。
- 幂等创建与状态流转、版本冲突、租约冲突和事件游标。
- 服务主体、scope、策略、凭据轮换、熔断与只读控制。
- MCP `2026-07-28` 当前协议、旧传输拒绝和资源/工具契约。
- A2A `1.0` Agent Card、Task、Message、Artifact、SSE 与授权隔离。
- 自动化、SLA、Webhook、Outbox、系统配置和机器安全错误结构。

测试夹具会先验证 `/healthz` 中 PostgreSQL 与 Redis 均为 `ok`；依赖不可用、
认证失败或清理失败时测试直接失败，不通过跳过伪造成功。

### 3.3 Playwright

浏览器 E2E 目录包含 13 个 spec，共 27 个测试。最终执行：

```text
27 passed (3.1m)
```

覆盖：

- 中文登录成功/失败与认证页面。
- 工单创建、编辑、工作流、评论、附件、通知和清理。
- 用户、自动化规则/日志、系统设置、邮件、Webhook、可信设备。
- AI 智能体控制中心、服务主体、策略、凭据、租约、事件、Outbox 与熔断。
- 企业表格不换行、列宽调整、持久化、双击复位和左侧导航完整性。
- 管理员/客户菜单与直接路由防护。

场景会修改共享管理员及系统数据，因此固定单 worker，避免测试框架自身制造跨场景
竞态和登录限流噪声。

### 3.4 前端与依赖快照

前端门禁命令为：

```text
npm run typecheck
npm run lint
npm run audit:security
npm run build
```

四项命令及 `knip` 未使用导出检查均退出 0；生产构建共转换 13,646 个模块并成功
生成 `web/dist`。依赖审计结果不是“零例外”，边界如下。

本次验收环境的核心版本：

| 组件 | 版本 |
| --- | --- |
| MUI | 9.2.0 |
| React Admin | 5.15.1 |
| React | 19.2.8 |
| Playwright | 1.62.0 |
| Pytest | 9.1.1 |
| pytest-html | 4.2 |
| requests | 2.34.2 |
| Ruff | 0.16 |

依赖审计只保留一个有时限的 React Router RSC 专用例外，复核截止日期为
`2026-09-30`。ChronoDesk 当前不使用 RSC；安全脚本在检测到 RSC 使用或例外到期时
必须失败。该例外不能被描述为“零例外”或“漏洞已修复”。

## 4. Chrome 插件真实页面验收

### 4.1 工具边界

用户指定的 bundled Chrome extension channel 在当前环境返回 unavailable，未宣称
该扩展通道成功。实际使用可用的 **Chrome DevTools plugin** 驱动隔离的真实 Chrome，
完成页面操作、DOM/布局、Console 与 Network 验收。

本次交互观察记录：

| 项目 | 记录 |
| --- | --- |
| Run ID | `Chrome插件全链路验证-20260730-0352` |
| 执行日期/时区 | `2026-07-30` / `Asia/Shanghai` |
| 源码标识 | `feat/agent-native-platform` 合并前工作树候选 |
| 环境 | 本地 Docker Compose、Chrome DevTools plugin |
| 视口 | `1440×900`、`820×360`、浏览器最小可达 `500×320` |
| 摘要证据 | 本报告 4.2–4.4 的业务、布局、Console 与 Network 聚合 |

为避免把 Cookie、Token 或业务数据写入版本库，本次未提交原始 HAR、浏览器 profile
或截图；因此这里是可复核的交互观察记录，不冒充绑定最终提交 SHA 的不可变制品。

### 4.2 真实业务操作

已通过 UI 完成并核验：

1. 使用错误凭据登录，页面显示中文 `邮箱或密码错误`。
2. 使用正确凭据登录，进入中文仪表盘并显示 `登录成功`。
3. 创建真实工单 `Chrome插件全链路验证-20260730-0352`。
4. 在工单详情添加公开评论：
   `Chrome 插件真实评论：页面提示、权限和事件链均需保持中文且可审计。`
5. 使用浏览器内 `File` 对象上传 `chronodesk-chrome-evidence.txt`，大小 47 B。
6. 页面显示附件 `待安全扫描`、内部可见性和 SHA-256；扫描完成前下载被禁用，
   符合安全策略。
7. 返回工单列表，确认新记录展示、单元格不换行且页面无全局横向溢出。
8. 删除该工单并完成关联测试数据清理。

本地文件路径上传曾被插件的文件访问权限阻止，因此改用浏览器内存中的真实
`File`/`DataTransfer` 完成上传；不是模拟后端成功，也没有绕过页面上传逻辑。

### 4.3 企业表格与左侧导航

工单列表实测：

```text
可见数据行：1
单行单元格：13
最大行高：43px
发生文字换行的单元格：0
页面 body 横向溢出：false
```

“工单信息”列：

```text
默认宽度：260px
键盘 ArrowRight 两次：276px
刷新后：276px（持久化成功）
双击分隔符：260px（恢复默认成功）
```

用户列表及其他主要列表也检查了可访问列宽分隔符、单行显示、容器横向滚动和
页面布局。表格超宽时只允许表格容器滚动，不得撑开应用壳层或遮住左侧功能项。

响应式实测覆盖：

- `820 × 360`：侧栏可滚动，所有功能项均可到达，页面无全局横向溢出。
- 浏览器实际可达到的最小视口 `500 × 320`：同样验证菜单可达与主体不遮挡。
- 测试结束恢复到 `1440 × 900`。

### 4.4 页面完整性

逐页检查的主要入口包括：

- 仪表盘、工单列表、工单创建/详情。
- 通知中心、用户管理。
- 自动化规则、自动化日志。
- 系统设置总入口与系统概览。
- 邮件设置、Webhook 设置。
- AI 智能体控制中心。
- 账号安全与可信设备。

中文按钮、表头、空状态、成功/失败反馈和确认提示均按当前业务语义检查。最终
已认证页面巡检基线：

```text
Console error：0
Console warning：0
Console issue：0
非预期失败网络请求：0
```

登录错误场景预期产生的认证拒绝不计为页面功能缺陷；最终基线在该场景结束并重新
登录后采集。

## 5. OpenAPI、OAuth、MCP 与 A2A

### 5.1 OpenAPI 3.2

当前机器契约统计：

| 项目 | 数量 |
| --- | ---: |
| Path Items | 34 |
| 路径操作 | 39 |
| Schemas | 87 |
| Webhooks | 1 |
| 唯一 `operationId` | 40 |

40 个 `operationId` = 39 个路径操作 + 1 个 Webhook 操作。契约测试还验证
OAuth scope、稳定错误码、幂等键、ETag/If-Match、操作回执和 Webhook Schema。

### 5.2 MCP `2026-07-28`

ChronoDesk 只支持 MCP `2026-07-28`，不保留旧 session、GET SSE、
`resources/subscribe` 或旧协议兼容分支。

仓库自动化覆盖：

- 当前协议发现与版本拒绝。
- Bearer token audience 和 scope。
- 13 个工具的 input/output Schema。
- 资源、资源模板与 `subscriptions/listen`。
- 工单读取/创建/更新/租约/评论/附件/历史和结构化错误。
- 旧传输方法与旧协议版本 fail closed。

MCP Inspector `2.0.0` 只能在本项目中作为**带认证的互操作冒烟工具**。本次使用
短期、精确 audience 的服务主体 token 和 `protocolEra: modern` 实际验证：

```text
protocolVersion: 2026-07-28
tools/list: 4 个当前工具，包含 ticket_list
ticket_list: isError=false，返回 structured result
```

临时配置权限为 0600，测试结束删除；服务主体随后停用，token 未进入命令行、报告
或仓库。

官方 `@modelcontextprotocol/conformance@0.2.0-alpha.10` 已固定版本执行
`list --server --spec-version 2026-07-28`，成功列出最新服务端场景。该工具当前
server CLI 无法为受保护的 `/mcp` 注入所需 Bearer/custom headers，因此只证明
测试工具识别当前规范，不证明场景已经执行；本报告不宣称完整 conformance 通过。

### 5.3 A2A `v1.0.1`

A2A 官方当前发布为 `v1.0.1`，线协议固定为 `1.0`。补丁发布号不参与
`A2A-Version` 协商。

仓库测试覆盖：

- `/.well-known/agent-card.json`、ETag 与 304。
- 5 个 Agent Skills，以及 streaming/push capability 声明。
- Task 与 Ticket 分离建模。
- Message、Task 查询/取消、`input-required`、状态历史、Artifact。
- SSE 事件游标和断线恢复。
- 授权撤销后 Task/Artifact 的查询、列表、重放与 SSE 均重新授权。
- A2A 标识 Unicode 长度、数据库宽度和并发执行安全。

官方 A2A TCK 当前不能为本项目的受保护端点直接注入认证，并包含针对 TCK fixture
的行为假设；因此只能对可执行的 Agent Card/无认证子集或通过受控授权适配的目标
子集做验证。除非完整官方命令、版本和原始结果可复核，否则不得写“完整 TCK 通过”。

本次固定官方 a2a-tck commit
`5996b79f9cefa6fc390980e383e358a66fb9e49e`，实际执行公开、无认证的
`tests/compatibility/agent_card` 子集：

```text
10 passed in 0.06s
transport agent_card: 10/10
```

该结果只适用于 Agent Card 子集。TCK 汇总中未执行的全局项目既不计失败，也不计
通过；本报告没有把 10/10 外推成完整 TCK 合规。

## 6. PostgreSQL、Redis 与容器

### 6.1 本地隔离环境

已完成：

- 干净 PostgreSQL/Redis 卷启动。
- 迁移和 seed 连续执行两次，第二次保持幂等。
- `/healthz` 返回 API、PostgreSQL、Redis、Agent control 均为 `ok`。
- 43 个模型的迁移/种子流程完成。
- A2A 7 个关联标识列通过 255 字符结构检查。
- Pytest 76 项和 Playwright 27 项在该服务上通过。
- 清空本项目一次性卷、按最终工作树重建后，再次连续执行两次 43/43 迁移与 seed；
  最终仅 1 个管理员、4 个默认分类，证明没有重复种子数据。
- 干净基线 Outbox `total=0`、`pending/processing/failed/dead=0`。

### 6.2 真实云 PostgreSQL / Redis

真实云环境只允许执行非破坏性、可重复的迁移、健康、凭据校验和集成测试；禁止
对共享云资源做停库、断网、压力、批量脏数据或破坏性故障注入。

真实云 PostgreSQL 已完成：

- 43/43 模型完整扫描完成；扫描后的旧 Actor 投影回填暴露出幂等重跑仍逐行
  `UPDATE` 的性能问题。
- 低效回填事务在受控中断后完整回滚，随后使用正式
  `-resume-from-model 43` 安全断点恢复；恢复流程和第二次幂等重跑均退出 0。
- 修复后已经规范化的 Actor 投影二次迁移为 0 次行更新，并有自动化断言；
  云端显式迁移阶段不再逼近超时。
- 每次完成都会执行 `ValidateRuntimeSchema`；7 个 A2A 标识列均通过 255 字符
  运行时结构门禁。
- `credential-maintain -validate-only` 通过。
- 独立 `:18081` 实例以云 PostgreSQL 和 Upstash Redis 启动，
  `/healthz` 返回 PostgreSQL、Redis、Agent control 全部 `ok`，随后优雅关闭。

真实 Upstash Redis 已完成：

- TCP/RESP `PING`：通过。
- REST/HTTPS `PING`：通过。
- 两种连接方式汇总：2/2。
- Redis Agent execution guard 真实集成测试：通过。
- Scheduler Redis lease 真实集成测试：通过。

云 Outbox 启动时恢复并投递了 8 条待处理记录，最终
`pending/processing/failed=0`。仍保留 1 条历史 `dead`：它来自 2026-02 的已停用
`E2E Webhook`，不是本轮新投递或活跃生产目标；未擅自删除历史审计记录。任何输出
只保存通过/失败、耗时和非敏感计数，不保存 DSN、endpoint、用户名、密码或 token。

### 6.3 容器与故障证据边界

已完成：

- Server 使用 `uid=100 chronodesk`，Web 使用 `uid=1000 node`，均非 root。
- 两个应用容器均为只读根文件系统、`cap_drop: ALL`、`no-new-privileges:true`，
  仅为 `/tmp`、Vite 缓存和附件卷保留明确可写挂载。
- 最终 Server 镜像不含 Go、Git、curl、GCC、make、cc 或 bash。
- Web 新增容器健康检查；Web 与 Server 均达到 `healthy`。
- `SIGTERM` 后 Server 退出码 0、无 panic/fatal，调度器和 HTTP 有界退出；
  重启后全部依赖恢复 `ok`。
- Docker 构建注入 version、commit、build date，并写入 OCI image labels；
  CI 使用 `GITHUB_SHA`，不再发布 `commit=unknown` 的候选镜像。

最终提交后仍需以提交对象执行 Gitleaks；干净一次性本地卷已确认 Outbox
`pending/processing/failed/dead=0`。

涉及停 PostgreSQL、超限压力、大量订阅或 10k 工单的用例只能在一次性环境执行，
不能对共享云服务注入。

## 7. 本轮测试发现并验证的关键修复

- PostgreSQL `TranslateError` 返回 `gorm.ErrDuplicatedKey` 时，幂等重放曾错误返回
  500；现仅识别目标唯一约束或等价数据库错误，重复请求返回首次操作回执。
- A2A 外部 Message/Context 标识可能超过旧 `varchar(64)`，且 Unicode 字符曾按
  字节判断；现统一为 255 字符契约、幂等数据库迁移和字符数边界测试。
- Python 长运行前缀在数据库字段截断后会让两个 Agent 夹具生成相同用户名；现使用
  固定长度哈希 run token 和独立 nonce。
- 通知查询测试仍使用旧 `limit` 参数；已切换为当前 `page_size` 契约。
- 自动化日志空状态最初让 E2E readiness 误判失败；现把中文
  `暂无自动化日志` 作为合法就绪状态。
- 企业表格文字换行、全局横向溢出和左侧菜单遮挡已通过统一单行/容器滚动/可调列宽
  能力修复，并在 Chrome 真实视口验证。
- 评论、附件、Agent 管理与系统设置的英文操作反馈已统一为中文并纳入浏览器回归。
- 人类评论和附件写入过去未统一执行并发控制；现强制强 ETag `If-Match`，缺失返回
  428、过期版本返回 `409 version_conflict`，前端会在连续内容操作间推进版本。
- 客户附件响应过去复用了内部模型；现改为窄 DTO，不暴露扫描详情、存储字段、
  哈希或内部 Actor 标识。
- A2A 幂等重放产物过去可能在撤销 `tickets:read` 后继续泄露 Ticket 快照；现保存
  类型化 Ticket 关联，并在 Get/List/重放时重新授权。
- 已规范化的 Actor 投影迁移过去会在每次启动逐行重写；现跳过等价投影，第二次迁移
  断言为 0 次 GORM 行更新。
- SMTP 上下文取消曾与 socket deadline 竞争而偶发返回 I/O timeout；现由取消监听
  直接触发 deadline，专项测试 100 次和 race 测试 25 次均通过。
- 删除确认框曾因焦点仍停留在背景元素而产生 `aria-hidden` 警告；现把初始焦点移入
  确认按钮，并由 Playwright 和 Chrome Console 共同回归。

## 8. 未由本次普通回归替代的风险验证

下列环境依赖或破坏性部分不能从普通回归的通过结果推断，明确记为部分覆盖或未执行：

| Case ID | 本轮状态 | 已有证据 | 未执行边界 | 后续责任 |
| --- | --- | --- | --- | --- |
| `AUTH-003`、`CNT-011`、`RES-003` | 部分自动化 | 唯一约束、CAS、事务回滚 Go 测试 | 两条真实连接的超时/上传故障注入 | 下次一次性预发布演练 |
| `EVT-005`、`EVT-006`、`EVT-009` | 部分自动化 | Outbox 重试/死信/回放和渠道单测 | 真实外部渠道耗尽及超长消息 | 下次渠道沙箱演练 |
| `MCP-020`、`PERF-003`、`SEC-006` | 部分自动化 | body、Schema、订阅边界测试 | 压缩炸弹和大规模订阅实压 | 下次隔离压力环境 |
| `PERF-001`–`PERF-004` | 未执行实压 | race、cursor、批次和连接逻辑测试 | 10k 工单、P95、Outbox 大积压 | 建立性能基线后执行 |
| `INF-010`、`RES-002`、`RES-006` | 部分自动化 | 依赖 fail-closed、Redis 恢复、迁移/密文校验 | 云 PostgreSQL 停库、脱敏备份恢复 | 仅在一次性恢复演练环境 |
| `SEC-008` | 部分自动化 | Cookie/安全头单测和本地浏览器检查 | 生产 TLS 终止层 HSTS/代理边界 | 部署环境上线前验证 |

这些边界不标记为通过，也未对共享云服务注入破坏。若部署模型或风险等级变化，项目
维护者必须在对应的一次性/预发布环境补齐，不得沿用本报告推断通过。

## 9. 最终合并门禁

只有以下条件全部满足，PR #3 才可 squash merge 到 `main`：

- [x] 最终源码重新运行 Go 全包、Race、Vet、Staticcheck、Govulncheck。
- [x] 最终镜像重新运行 Pytest 76 项和 Playwright 27 项。
- [x] OpenAPI、MCP、A2A 契约与 Inspector 互操作冒烟通过。
- [x] 真实云 PostgreSQL 与最终组合健康非破坏性复验通过；新 Redis 复验已通过。
- [x] Docker 非 root、优雅停机、Outbox 稳态和暂存区 Gitleaks 扫描通过。
- [x] 前端 Typecheck、ESLint、依赖门禁、Knip 和生产构建通过。
- [x] 236 条用例均有证据入口，未执行/部分覆盖边界已在本报告列明且未伪造通过。
- [ ] PR Checks、CodeQL 和依赖安全门禁全部成功。
- [x] PR diff 不包含 `.env`、凭据、token、原始日志、浏览器缓存或含敏感信息的
  自动生成报告；本脱敏人工审校报告属于版本化文档。

最终提交、CI、CodeQL 与合并记录由
[PR #3](https://github.com/seaworld008/chronodesk/pull/3) 持久化。本报告用于解释
覆盖范围和已取得的证据，不覆盖 GitHub 的最终状态。

## 10. 合并后验证

Squash merge 完成后拉取 `main`，以合并提交重新执行关键 Go 测试、前端构建、
Compose `/healthz` 和远端分支状态核对；该步骤是发布后的完整性验证，不作为一个
逻辑上不可能提前完成的合并前条件。
