# Agent REST、MCP 与 A2A 黑盒用例映射

本目录依据
`docs/testing/CHRONODESK_COMPREHENSIVE_TEST_CASES_2026-07-29.md`
通过真实 HTTP 验证运行中的 ChronoDesk。测试不导入 Go 实现、不直连数据库，
也不会用 mock 替代 PostgreSQL、Redis、OAuth 或协议入口。

## OAuth、服务主体和 Agent REST

| 用例 ID | Pytest | 真实黑盒断言 |
|---|---|---|
| AGT-001、AGT-002、AGT-006 | `test_oauth_and_agent_rest.py::test_admin_provisions_e2e_principal_and_explicit_policy` | 人类管理员创建独立 E2E 服务主体；一次性凭据响应禁止缓存；总览不回显 secret；显式策略绑定 scope/action/resource |
| AGT-003、AGT-005、AGT-008 | `test_oauth_and_agent_rest.py::test_client_credentials_rotation_and_deactivation_invalidate_tokens` | `client_credentials` 最小 scope 换 token；越 scope 创建返回 403；轮换后旧 token 立即 401；停用后新 token 立即 401 |
| AGT-025 | `test_oauth_and_agent_rest.py::test_oauth_discovery_advertises_three_exact_resources` | AS discovery 与 API/MCP/A2A 三个 Protected Resource Metadata 的 issuer、resource、scope、缓存和 audience 隔离 |
| AGT-012、AGT-013 | `test_oauth_and_agent_rest.py::test_agent_rest_ticket_idempotency_lease_version_and_event_cursor` | capabilities、cursor 列表、读取 ETag、Agent Actor 和写回执 |
| TKT-003、AGT-013 | 同上 | 相同 `Idempotency-Key` 重放返回原回执且列表只有一个工单 |
| AGT-015、AGT-017、AGT-018 | 同上 | claim、heartbeat、release 的 lease ID、到期时间、ticket version 和回执 |
| TKT-008、AGT-020 | 同上 | 有效 lease 搭配错误版本返回 `409 version_conflict`，工单 version/priority 不变 |
| EVT-014、EVT-018 | 同上 | CloudEvents 1.0 必需字段；不透明 cursor 恢复无重复 |
| TKT-023、TKT-024 | `test_oauth_and_agent_rest.py::test_agent_ticket_cursor_first_middle_tail_empty_and_invalid_limits` | 三个 E2E 工单按 limit=1 完整遍历首/中/尾页且无重复漏项；空页稳定；超大、零、负、非数字 limit 与畸形 cursor 均为 `400 invalid_request` |

## MCP `2026-07-28`

| 用例 ID | Pytest | 真实黑盒断言 |
|---|---|---|
| MCP-001、MCP-017 | `test_mcp_2026_07_28.py::test_mcp_server_discover_advertises_latest_only` | `server/discover` 仅声明 `2026-07-28`、stateless、complete 结果和当前 OAuth 扩展 |
| AGT-004、MCP-003、MCP-004、MCP-015 | `test_mcp_2026_07_28.py::test_mcp_requires_audience_token_and_rejects_legacy_transport` | 缺 token 与错误 audience 拒绝；GET/DELETE 仅返回 405；带请求 ID 的旧 `initialize`、`resources/subscribe` 均返回 `-32601` |
| MCP-003、MCP-004 | `test_mcp_2026_07_28.py::test_mcp_rejects_client_notifications_over_streamable_http` | 无请求 ID 的 `notifications/initialized`、`notifications/cancelled` 先按 2026-07-28 Streamable HTTP 的“客户端通知未定义”规则返回 `-32600`；HTTP 取消只能关闭响应流，不能进入旧通知分发 |
| MCP-002、MCP-005、MCP-006 | `test_mcp_2026_07_28.py::test_mcp_rejects_old_version_or_missing_request_contract` | 旧/缺失版本、缺失/不一致 `Mcp-Method`、缺少每请求 `_meta` 精确错误码 |
| MCP-007、MCP-008、MCP-009、MCP-010 | `test_mcp_2026_07_28.py::test_mcp_tools_list_and_ticket_get_return_schema_bound_structured_result` | 13 个工具稳定排序；严格 input/output Schema、风险/scope/幂等元数据；`Mcp-Name` 校验；`ticket_get` structured result；非法类型和外部 URL 字段无副作用拒绝 |

## A2A `1.0`

| 用例 ID | Pytest | 真实黑盒断言 |
|---|---|---|
| A2A-001、A2A-002、A2A-003 | `test_a2a_v1.py::test_agent_card_etag_authentication_and_current_capabilities` | Agent Card 的唯一 1.0 Interface、五项 skills、OAuth、stream/push、ETag/304；未认证 RPC 为 401 |
| A2A-004、A2A-005、A2A-006、A2A-010 | `test_a2a_v1.py::test_a2a_ticket_intake_creates_queryable_task_and_ticket_artifact` | `SendMessage` 执行 `ticket-intake`，Task 与 Ticket 分离；Artifact 含工单快照和回执；Get/List Task 可查询 |
| A2A-007、A2A-008 | `test_a2a_v1.py::test_a2a_input_required_does_not_mutate_ticket_and_sse_resumes_by_cursor` | 缺结构化字段只将 Task 置为 `TASK_STATE_INPUT_REQUIRED`；关联 Ticket 不变为 pending、version 不增加；Task 可独立取消 |
| A2A-011、RES-004 | 同上 | `SubscribeToTask` 返回持久事件 cursor；`Last-Event-ID` 只恢复其后的事件，无重复 |

## 数据隔离和清理

- 所有服务主体名称、策略名称、工单标题、消息 ID 和幂等键使用唯一
  `E2E-<run-id>-` 标识。
- one-time secret 与 access token 只保存在内存；dataclass `repr` 和失败响应诊断均脱敏。
- fixture teardown 先通过管理员控制面强制释放仍有效的 E2E lease，再停用 E2E
  策略并将 E2E 服务主体设为 `inactive`。
- 测试工单交由现有 `E2EResourceManager` 在重新读取并验证标题前缀后逐个删除；
  不执行模糊批量清理，不触碰既有云数据。
- 当前 A2A 服务端没有 Task 删除接口；测试创建的 Task 只会进入
  `TASK_STATE_COMPLETED` 或 `TASK_STATE_CANCELED` 终态。

## 执行

仅验证导入和收集（不代表真实环境通过）：

```bash
cd server
python3 -m compileall -q tests/agent_protocol tests/utils/agent_protocol.py
python3 -m pytest tests/agent_protocol --collect-only -q
```

完整真实 HTTP 执行：

```bash
cd server
python3 -m pytest tests/agent_protocol -v
```
