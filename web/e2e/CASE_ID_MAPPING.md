# Playwright 用例与验收 Case ID 映射

本目录的真实数据统一使用单次进程唯一的 `E2E-<run-id>-`（用户名与邮箱按后端约束使用
`e2e_<run-id>_`）。每次创建成功后立即登记服务端返回的精确资源 ID，清理阶段只处理
登记 ID；禁止扫描并删除所有 `E2E-`、`e2e_` 或 `test_` 前缀数据。

破坏性用例默认只允许 `localhost`、`127.0.0.1` 或 `::1`。非回环隔离测试环境必须由
操作者同时设置 `CHRONODESK_ALLOW_REMOTE_E2E=1` 和格式受限的
`CHRONODESK_E2E_OWNERSHIP_PREFIX=e2e-<唯一所有者>`；全局配置与全局控制写入只允许
回环一次性环境并要求 `CHRONODESK_EPHEMERAL_E2E=1`。API helper 和浏览器请求路由均会
执行 fail-closed 守卫。

| Playwright 文件 | 覆盖 Case ID | 真实交互与断言 | 清理/恢复 |
|---|---|---|---|
| `00-site-health.spec.ts` | UI-002～UI-004、UI-013～UI-020、UI-023、UI-025、UI-030、P1-PLATFORM-AUDIT | 全站 13 个一级页面巡航（含只读平台审计）；侧栏覆盖 1440/1280/1024/820 CSS 视口、短高度，以及物理宽高减半的 200% 等效重排；桌面与临时抽屉中的全部菜单都须可滚动到达并通过命中测试；`route.abort` 和英文 Problem 响应只允许显示中文 Toast/Alert；收集 console error/warn、pageerror、失败请求与 HTTP 4xx/5xx | 只读 |
| `00-users.spec.ts` | RBAC-010、RBAC-011、UI-013 | 通过 `/api/platform/users` 页面创建并编辑 platform_admin/security_auditor/emergency_operator/member 四种平台角色；仅在真实环境只有一个活跃平台管理员时尝试降级并验证中文 409 | 按本轮登记的用户 ID 删除 |
| `platform-user-dto.spec.ts` | P1 Human AdminUser DTO | 浏览器真实填写创建/编辑表单；严格 Mock 解码器拒绝 Human OpenAPI DTO 之外的字段，并分别以 201/200 响应；断言创建不泄漏状态、偏好、验证及确认密码，编辑不回传只读服务端字段 | Mock 后端，无持久数据 |
| `project-scoped-admin.spec.ts` | P1 平台/项目权限矩阵 | 四个平台角色在无 Membership 时不产生项目作用域请求；member + project_admin 不获得平台能力；安全审计只读、紧急运维无隐式入口；同浏览器账号切换不泄漏项目缓存；Membership 撤销后旧请求 403；五种 ProjectRole 的菜单与创建/编辑/删除按钮矩阵 | Mock 后端，无持久数据 |
| `project-resource-isolation.spec.ts` | P1 项目资源与会话缓存隔离 | A/B 项目同 numeric ticket ID，撤销 A 后当前业务 URL fail closed 且绝不请求/修改 B；双向切换 project_admin/observer 后清 permissions、query 与资源缓存；direct `apiFetch` 401 清全部会话/项目缓存并跳登录 | Mock 后端，无持久数据 |
| `project-membership.spec.ts` | P1 Project Membership UI | project_admin 通过项目作用域接口查看五种 ProjectRole、逐一授予、变更与撤销；manager/agent/requester/observer 均无菜单，直接路由也在请求前拒绝 | Mock 后端，无持久数据 |
| `01-enterprise-tables.spec.ts` | UI-004～UI-007、UI-026、UI-028、UI-029 | 枚举全部 15 张企业表（含系统配置、Webhook、工单历史与 Agent 五表）；每表必须有真实数据行，并用文本 Range 矩形和行高验证实际单行；全部表按独立浏览器上下文逐一验证键盘列宽持久化和双击复位持久化；820px 横滚后 9 张 sticky 右操作列仍贴边、可命中，按钮/链接有可访问名称且通过 trial click；平台用户详情不得隐式投影项目工单 | 精确登记并删除本轮工单、通知、规则、Webhook；先强制释放登记租约，再停用登记服务主体及策略 |
| `agent-control.spec.ts` | AGT-001、AGT-002、AGT-008～AGT-012、UI-020、UI-021 | 控制面指标与七类独立清单；每个标签只请求自己的严格页码/游标端点；局部错误重试；项目切换取消旧请求并清除旧行；创建服务主体；一次性凭据；轮换；allow/deny 策略；单体熔断；停用；全局只读与紧急停止 | 每个用例保存原始全局开关快照，以最新版本 `If-Match` CAS 并在 `finally` 恢复；只停用登记 ID 的主体及其策略 |
| `webhook-settings.spec.ts` | EVT-002、EVT-007、EVT-008、EVT-010、UI-019 | 页面创建、编辑 canonical `ticket.transitioned` 与 resolved/closed 谓词；密钥不回显；异步 switch 与操作按钮有名称；中文站内确认对话框后测试 `.invalid` 保留域名并验证安全拒绝；API 核对 canonical 失败日志 | 删除 E2E Webhook 配置 |
| `trusted-devices.spec.ts` | AUTH-019、AUTH-022、UI-023 | UI 登录创建可信设备；Cookie 为 HttpOnly/Strict 且 localStorage 无凭据；在具名设备 region 内通过中文站内确认对话框撤销 | 按登录后登记的精确设备 ID 撤销，不假定账号没有其他设备 |
| `ticket-content.spec.ts` | CNT-001、CNT-002、CNT-006、CNT-009、UI-011 | 页面添加公开/内部评论；上传内存小文件；待扫描禁止下载；管理员标记 clean；浏览器真实下载 | 删除 E2E 工单，由附件清理 Outbox 回收对象 |
| `automation-rules.spec.ts` | AUT-001、UI-015 | 页面创建 canonical CloudEvent 自动化规则 | 删除 E2E 规则 |
| `email-settings.spec.ts` | UI-018 | 通过 `/api/platform/email-config` 修改可恢复字段并验证中文反馈，不写 SMTP 密码；非本地且邮件已启用时拒绝触发真实 SMTP | 修改前快照、比较测试后状态、`finally` 精确恢复；并发变化时拒绝覆盖 |
| `notifications.spec.ts` | EVT-011、UI-014 | 创建并搜索真实站内通知 | 按登记通知 ID 删除 |
| `notification-role-contract.spec.ts` | P1 通知创建职责边界 | 空通知项目中 project_admin/manager 可从列表进入创建页，严格只提交 type/title/content/priority/channel/recipient_id 六字段；agent/requester/observer 无创建按钮且直接路由在请求前拒绝 | Mock 后端，无持久数据 |
| `system-settings.spec.ts` | UI-017 | 通过 `/api/platform/configs` 修改安全配置并刷新验证 | 修改前快照、比较测试后值、`finally` 精确恢复；并发变化时拒绝覆盖 |
| `ticket-workflow.spec.ts` | RBAC-007、RBAC-008、TKT-001、TKT-010、TKT-013、TKT-015、TKT-018、UI-008～UI-010 | 为本轮创建 platform_role=member 的临时账号，再分别显式授予 agent/requester Membership；覆盖人类工单全流程、请求人通知筛选和 Agent 控制路由防护 | 先撤销 Membership，再按登记 ID 删除工单、通知和临时账号 |
| `ticket-role-contract.spec.ts` | P1 项目工作流与 requester 最小权限 | 页面执行 assign/transfer/escalate/status，逐项断言只请求 `/api/projects/{key}/tickets/...` 且携带版本前置条件；requester 编辑不发送 status/assignee/internal_notes，详情不显示工作流或内部内容，公开评论与附件仍可写 | Mock 后端，无持久数据 |
| `tickets.spec.ts` | AUTH-004、UI-001、UI-004、UI-008、UI-009 | UI 登录、列表、创建入口、按本轮精确标题打开详情与导航，禁止用“第一行详情”产生假绿 | 按登记工单 ID 删除 |

## 收集与执行

```bash
cd web
npm run test:e2e:list
npm run typecheck
npm run lint

# 根任务启动 PostgreSQL、Redis、API 与 Web 后执行
npm run test:e2e
```

高风险 Agent 控制测试不会把全局状态强制归零：无论成功、失败或异常退出后的
Playwright `finally`/清理阶段，都会读取最新资源版本并恢复为测试前布尔快照。

平台配置接口目前没有服务端 `ETag`/`If-Match`，因此系统设置与邮件设置采用
“测试前快照 + 恢复前精确值比较 + finally”的客户端防覆盖策略；它能拒绝已观察到的
并发变化，但不能提供原子 CAS。真正的跨进程 CAS 仍需生产接口增加资源版本契约。
