# Human REST 黑盒用例映射

本文件只说明 `server/tests/human` 通过真实 HTTP 直接断言的行为。跨数据库事务、
CloudEvent、Outbox、Chrome 与故障注入证据统一以
[`CASE_EVIDENCE_MANIFEST.tsv`](../../docs/testing/CASE_EVIDENCE_MANIFEST.tsv)
为准，不能因为相邻接口返回 2xx 就宣称整条用例已经证明。

状态含义：

- **HTTP 直接证据**：该测试完整断言本表描述的 HTTP 行为；
- **复合证据**：HTTP 只证明传输/对象权限，完整 Case 还绑定 manifest 中列出的
  Go、Playwright 或发布规程；
- **发布规程**：共享云环境不适合自动构造，必须执行明确、可留痕的发布门禁。

## 角色、账号与对象级授权

| 用例 ID | 证据状态 | Pytest / 其他 locator | 精确断言边界 |
|---|---|---|---|
| AUTH-005、AUTH-006 | HTTP 直接证据 | `auth/test_auth_flows.py::test_wrong_password_and_unknown_email_are_indistinguishable_and_limited` | 错误密码与未知邮箱首个响应相同且不回显账号；已存在账号连续五次失败后下一次为中文 429。 |
| AUTH-007 | 复合证据 | `human/test_zz_error_contract.py::test_authentication_authorization_and_not_found_errors_are_machine_safe` | 缺 token、错误认证头和伪造签名为中文 JSON 401；过期、错误 audience 等由 manifest 中 Go 测试证明。 |
| RBAC-001 | 复合证据 | `human/test_rbac_matrix.py::test_ticket_object_permission_matrix` | Admin 可读、可更新任意 E2E 工单；Actor/Event 审计另有领域及发布证据。 |
| RBAC-002 | 复合证据 | 同上 | Supervisor 可读队列并分配；完整升级链与系统管理拒绝分别由其他证据证明。 |
| RBAC-003 | HTTP 直接证据（当前策略） | `human/test_failure_driven_contracts.py::test_agent_full_queue_read_remains_read_only_under_current_human_policy` | Human Agent 可只读完整队列；只有未分配/自己处理的工单可写，测试不虚构跨处理人读取 403。 |
| RBAC-004 | HTTP 直接证据 | 同上及 `human/test_rbac_matrix.py::test_ticket_object_permission_matrix` | 非处理 Agent 的更新、流转和评论均为 403。 |
| RBAC-005 | HTTP 直接证据 | `human/test_rbac_matrix.py::test_ticket_object_permission_matrix` | Customer 读取本人工单，处理人、内部上下文与附件投影被裁剪。 |
| RBAC-006 | HTTP 直接证据 | 同上 | Customer 跨客户读取拒绝，响应不回显目标 ID。 |
| RBAC-007 | HTTP 直接证据 | `human/test_content_and_notifications.py::test_public_and_internal_comment_permissions` | Customer 可写公开评论，internal/system 均为 403。 |
| RBAC-008 | HTTP 直接证据 | `human/test_rbac_matrix.py::test_four_human_roles_and_admin_only_surfaces` | Supervisor、Agent、Customer 不能访问用户、系统配置和 Agent 控制面。 |
| RBAC-009 | HTTP 直接证据 | `human/test_zz_error_contract.py::{test_suspended_account_invalidates_an_existing_access_token,test_deleted_account_invalidates_an_existing_access_token}` | 停用和删除后，已签发 access token 立即为 401。 |
| RBAC-010 | 复合证据 | `human/test_rbac_matrix.py::test_four_human_roles_and_admin_only_surfaces`、`human/test_rbac_matrix.py::test_soft_deleted_human_identity_returns_stable_conflict` | 创建/更新未知角色为 400；审计保留身份复用为中文 409；数据库 CHECK 由 manifest 中 Go 测试证明。 |
| RBAC-011 | 复合证据 | `internal/services/admin_user_service_role_test.go::TestAdminUserServiceUpdatePreservesLastActiveAdmin`、`web/e2e/00-users.spec.ts` | 服务层与浏览器共同验证最后活跃管理员不能降级；不破坏共享云管理员来构造黑盒前置条件。 |
| RBAC-012 | HTTP 直接证据 | `human/test_rbac_matrix.py::test_human_role_change_audit_identifies_actor_target_result_and_redacts_query` | Human 角色变更审计包含操作者、目标路径/ID、方法、结果、时间和来源；password/token query 被脱敏。该接口不冒充 Agent diff/event 审计。 |

## 工单输入、并发、生命周期与列表

| 用例 ID | 证据状态 | Pytest | 精确断言边界 |
|---|---|---|---|
| TKT-001 | 复合证据 | `human/test_ticket_boundaries.py::test_ticket_create_input_boundaries` | 合法边界创建且 `version=1`；Actor/Event/Outbox 由领域测试证明。 |
| TKT-002 | HTTP 直接证据 | 同上及 `human/test_failure_driven_contracts.py::test_blank_ticket_text_is_chinese_400_without_persisted_row` | 缺字段、超长、非法枚举、纯空白为中文 400，并以唯一 marker 确认无持久化行。 |
| TKT-005 | HTTP 直接证据 | `human/test_failure_driven_contracts.py::test_human_ticket_detail_returns_strong_etag` | GET 返回与 `version` 一致的强 `ETag: "v<number>"`。 |
| TKT-006 | HTTP 直接证据 | `human/test_ticket_boundaries.py::test_ticket_pagination_and_identifier_bounds`、RBAC matrix | 不存在、越权、非法及溢出 ID 均稳定处理。 |
| TKT-007、TKT-008 | HTTP 直接证据 | `human/test_failure_driven_contracts.py::{test_human_ticket_put_enforces_if_match,test_human_ticket_workflow_enforces_if_match}` | 正确 If-Match 成功并递增版本；缺失为 428，畸形/陈旧为 409。 |
| TKT-009 | HTTP 直接证据 | `human/test_ticket_boundaries.py::test_concurrent_human_updates_produce_version_conflict` | 两个同步 Human 写方恰有一个成功、另一个 `409 version_conflict`。 |
| TKT-014 | HTTP 发布门禁 | `human/test_ticket_workflow_contracts.py::test_invalid_transition_is_a_versioned_conflict_with_allowed_next_states` | 非法流转必须为 `409 invalid_status_transition`，列出允许下一状态且资源完全不变。 |
| TKT-017 | 复合证据 | `human/test_ticket_workflow_contracts.py::test_transfer_updates_assignee_history_event_and_recipient_notification` | 转移后的新处理人、原因、版本、真实 event_id/resource_version、历史和新收件人通知一致。 |
| TKT-019 | HTTP 直接证据 | `human/test_ticket_workflow_contracts.py::test_bulk_delete_reports_authorization_and_missing_items_without_silence` | 旧 `ids` shape 已拒绝；当前 `tickets:[{id,version}]` 前置条件下，非特权整批 403，管理员混合存在/不存在 ID 时显式返回 deleted_tickets/failed/reasons。 |
| TKT-021 | 复合证据 | `human/test_ticket_workflow_contracts.py::test_ticket_list_combines_status_priority_type_source_assignee_and_search` | direct query 与 JSON filter 都组合断言 status/priority/type/source/assignee/search，另有错误 source 排除样本；更多 SLA 特殊列表由 Go 证据覆盖。 |
| TKT-023、TKT-024 | HTTP 直接证据 | `agent_protocol/test_oauth_and_agent_rest.py::test_agent_ticket_cursor_first_middle_tail_empty_and_invalid_limits` | Agent REST 首/中/尾/空页无重复漏项；超大、零、负、非数字 limit 和畸形 cursor 为 400。 |
| TKT-027 | 复合证据 | `human/test_failure_driven_contracts.py::test_customer_history_redacts_assignment_and_internal_changes` | Customer 历史裁剪 actor、分配与内部评论；真实事件关联及迁移由 Go 测试证明。 |
| SEC-005 | HTTP 直接证据 | `human/test_ticket_boundaries.py::test_ticket_pagination_and_identifier_bounds` | 非数字、uint64 溢出和 32 位边界 ID 不溢出、不超分配。 |

旧 `tickets/test_lifecycle.py::test_full_lifecycle` 只证明创建、普通更新、分配、
`open → in_progress → resolved`、历史、收件人通知和删除。它不再被描述为升级、
转移、关闭/重开、批量或 cursor 的完整证据。

## 评论、附件、通知与错误

| 用例 ID | 证据状态 | Pytest | 精确断言边界 |
|---|---|---|---|
| CNT-001 | 复合证据 | `human/test_content_and_notifications.py::test_public_and_internal_comment_permissions` | 公开评论创建并读取；版本/Event/Outbox 原子性由领域测试证明。 |
| CNT-002、CNT-003 | HTTP 直接证据 | 同上 | 内部评论只向处理角色返回；Customer 写 internal/system 为 403。 |
| CNT-004 | HTTP 直接证据 | 同上及 `human/test_failure_driven_contracts.py::test_invalid_comment_errors_are_chinese_without_validator_details` | 空、纯空白、超长、非法类型/content-type 为中文 400；HTML/提示注入仅作为文本。 |
| CNT-006 | 复合证据 | `human/test_content_and_notifications.py::test_attachment_rejection_name_safety_and_download_authorization` | 合法小文件返回 SHA-256、大小与 pending；事件一致性由 Go 测试证明。 |
| CNT-007 | 发布规程 | `docs/testing/RELEASE_EVIDENCE_PROCEDURES.md::CNT-007` | 黑盒现有测试只证明缺失/空文件；超大、危险扩展及 MIME/魔数欺骗按发布规程执行，不能计作现有 Pytest 通过。 |
| CNT-008 | HTTP 直接证据 | attachment test | `../../` 文件名安全归一化，响应不含路径分隔符。 |
| CNT-009 | 复合证据 | attachment test、`web/e2e/ticket-content.spec.ts` | pending 禁止下载，浏览器验证 clean 下载；infected/error 状态由领域或发布证据证明。 |
| CNT-010 | HTTP 直接证据 | attachment test | 跨用户下载拒绝，猜测 ID 为 404。 |
| CNT-014 | 复合证据 | attachment test、共享 `assert_no_sensitive_fields` | API 响应扫描存储路径和凭据字段；日志扫描由安全流水线证明。 |
| EVT-011 | 复合证据 | `human/test_content_and_notifications.py::test_notifications_are_strictly_recipient_scoped` | 两用户列表隔离、未读状态保持；全部已读与 UI 同步另有证据。 |
| EVT-012 | HTTP 直接证据 | 同上 | 伪造 recipient 过滤不能越权，修改他人通知为 403。 |

共享 `utils/assertions.py::assert_error_contract` 对触发到的 4xx 统一验证状态、
JSON、机器判别字段、中文反馈和敏感字段；不能把“某个 401 已通过”外推成所有错误
分支已通过。429 测试只耗尽本轮专用用户/路由桶。

## 数据隔离与执行

`utils/human.py::E2EResourceManager` 为每次 session 生成唯一
`E2E-<run-id>-` 前缀。清理前重新读取并验证所有权，只删除明确跟踪的本轮资源。

```bash
cd server
python3 -m pytest tests/human tests/auth tests/agent_protocol -v

# 只验证收集和导入，不代表真实 API 已通过
python3 -m pytest tests/human tests/auth tests/agent_protocol --collect-only -q
python3 -m compileall -q tests
python3 tests/validate_case_evidence_manifest.py
```
