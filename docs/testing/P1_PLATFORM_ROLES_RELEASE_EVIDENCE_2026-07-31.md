# P1 平台角色与项目角色切换发布证据

- 执行日期：2026-07-31
- 候选分支：`codex/p1-platform-roles`
- 远端交付：[GitHub PR #8](https://github.com/seaworld008/chronodesk/pull/8)
- 范围：平台角色、项目 Membership、Human Web、Webhook、WebSocket、附件存储、
  OpenAPI、迁移与仓库文档

> 本报告不记录连接串、密码、Token、Cookie、Webhook secret 或其他凭据。
> GitHub PR 的最终 head、Checks 与合并记录是远端不可变证据；本文件只记录本地和
> 隔离环境实际执行结果。236 条 Case 的 `execution_record=not_recorded` 仍表示
> Manifest 只提供证据定位，不能被本报告批量改写为“全部执行通过”。

## 发布结论

候选代码已完成平台角色与项目角色分离：平台职责不再隐式授予项目访问，
`ProjectRole` 只来自 active Membership，项目作用域在请求和关键写入事务中实时
复核。Human Web 使用明确的 `project_access_revoked` 区分当前项目作用域失效与
普通角色/对象级 `403`，并且只在响应 Project Key 与当前选择一致时清理选择。

合并前新增评审项也已关闭：

- WebSocket 最终本地授权、完整帧写入和撤权/关闭具有明确线性化边界；慢
  `NextWriter` / `Write` 会被连接关闭主动中断，不会把多连接撤权拖成
  `N × writeWait`。
- 平台项目治理和项目成员列表使用共享企业表格，支持单行单元格、sticky 操作列、
  键盘调宽和持久化。
- `DEFAULT` 项目在 UI 和执行函数两层禁止归档。
- 本地附件存储使用 `os.Root` 约束读取、写入、重命名和删除，拒绝遍历、符号链接、
  FIFO 与目录交换逃逸。
- Agent lease replay 的资源 ID 使用原生位宽安全解析，拒绝零值、格式错误和溢出。

## 最终本地门禁

| 验证面 | 命令或环境 | 最终结果 |
| --- | --- | --- |
| 标准门禁 | `make verify` | 通过 |
| Go 竞态 | `make test-race` | 全包通过 |
| WebSocket 专项 | normal、`-race`、`vet`，关键竞态用例 `-count=100` | 通过 |
| Human/Agent 契约 | Redocly、Spectral、生成 freshness 与运行时契约测试 | 通过 |
| 安全扫描 | `govulncheck`、Web 生产依赖策略、TypeScript SDK audit | 可达漏洞 0；受控例外见下文 |
| Evidence Manifest | `validate_case_evidence_manifest.py` 与 `--self-test` | 236/236 可定位 |
| Python 黑盒收集 | `pytest --collect-only` | 91 项 |
| Web | TypeScript、ESLint、Vite 生产构建 | 通过，13,657 Modules |
| SDK | Go、Python、TypeScript 测试与构建 | 通过 |

`make verify` 在最终源码上从头执行。过程中门禁先后发现 Human OpenAPI 生成摘要、
企业表测试标题和黑盒错误码断言的同步漂移；修正后重新执行并通过，没有把失败轮次
当作最终证据。

## 全新容器与真实交互

使用当前工作树重新构建 Server 与 Web 镜像，在独立 Compose Project
`chronodesk-final-731` 和全新 Volume 中执行：

1. PostgreSQL 18、Redis 8、OpenSearch 3.5、Server 启动并通过健康检查。
2. `chronodesk-migrate -seed` 连续运行两次；每次均完成 87/87 Models，第二次保持
   幂等。
3. `chronodesk-credential-maintain -validate-only` 通过。
4. Python 真实 API smoke 在显式 ephemeral localhost 目标上
   **91/91 通过**。
5. 停止临时 OpenSearch 以适配 Docker Desktop 2 GB 内存上限，保持同一
   PostgreSQL、Redis 与 Server，启动当前 Web 镜像；完整 Playwright
   **77/77 通过**。

Playwright 覆盖 15 张企业表、五种项目职责、四种平台职责、平台治理、Membership
撤销、跨项目缓存隔离、普通 `403` 不误清理项目、`OTHER` 项目 revoked 不影响当前
项目、工单、评论、附件、Webhook、通知、自动化和可信设备。临时容器、Network 与
Volume 已删除；测试前暂停的既有 `chronodesk-p1fresh-29f3` PostgreSQL、Redis、
OpenSearch 和 Server 均恢复健康。

## 外部依赖与发布边界

- 当前 Upstash Redis 已通过 TCP/RESP 和 REST/HTTPS PING；本轮没有对共享云服务
  执行破坏性测试。
- 已配置的云 PostgreSQL 可连接，但仍是切换前 Schema：
  `credential-maintain -validate-only` 会因缺少 `previous_secret` 列失败。平台
  角色 cutover 会删除旧角色列，必须先按
  [数据库迁移手册](../operations/database-migrations.md)在备份或隔离克隆上演练，
  再由部署负责人显式执行；本轮没有把本地验证等同于云端已迁移。
- Web 生产依赖策略保留 React Router
  `GHSA-qwww-vcr4-c8h2` 例外。仓库未启用受影响的 unstable RSC API，复核期限为
  2026-09-30；这不是“没有安全公告”的声明。
- 本地干净容器结果不替代 GitHub PR 最终 head 的 Secret Scan、Dependency
  Security、CodeQL 和 Smoke/E2E Checks。只有这些 Checks 全部成功后才允许合并。

## 复核入口

- [全面测试用例](CHRONODESK_COMPREHENSIVE_TEST_CASES_2026-07-29.md)
- [Case Evidence Manifest](CASE_EVIDENCE_MANIFEST.tsv)
- [发布证据规程](RELEASE_EVIDENCE_PROCEDURES.md)
- [测试与质量控制指南](../testing_guide.md)
- [平台角色与项目角色分离 ADR](../adr/0008-separate-platform-and-project-roles.md)
- [数据库迁移手册](../operations/database-migrations.md)
