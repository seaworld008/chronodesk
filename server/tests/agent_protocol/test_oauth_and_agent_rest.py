"""OAuth identity lifecycle and Agent REST real-HTTP contracts."""

from __future__ import annotations

import json
from typing import Any

import pytest

from tests.utils.agent_protocol import (
    MINIMAL_AGENT_SCOPES,
    AgentAccessToken,
    AgentProtocolHarness,
    ServicePrincipalFixture,
    assert_status,
    envelope_data,
    json_object,
)

pytestmark = [pytest.mark.api, pytest.mark.integration]


def _bearer(token: AgentAccessToken) -> dict[str, str]:
    return {"Authorization": token.authorization}


def _assert_no_secret_fields(value: Any) -> None:
    if isinstance(value, dict):
        lowered = {str(key).lower() for key in value}
        forbidden = {
            "access_token",
            "client_secret",
            "password",
            "refresh_token",
            "secret_hash",
        }
        assert not lowered & forbidden, (
            f"持久读取接口暴露敏感字段：{sorted(lowered & forbidden)}"
        )
        for nested in value.values():
            _assert_no_secret_fields(nested)
    elif isinstance(value, list):
        for nested in value:
            _assert_no_secret_fields(nested)


def _assert_problem(
    response: Any,
    *,
    status: int,
    code: str,
    operation: str,
) -> dict[str, Any]:
    assert_status(response, status, operation=operation)
    body = json_object(response, operation=operation)
    assert body.get("status") == status, body
    assert body.get("code") == code, body
    assert isinstance(body.get("request_id"), str) and body["request_id"], body
    assert "application/problem+json" in response.headers.get("Content-Type", ""), (
        response.headers
    )
    _assert_no_secret_fields(body)
    return body


def test_oauth_discovery_advertises_three_exact_resources(
    protocol_harness: AgentProtocolHarness,
) -> None:
    """AGT-025: PRM/AS discovery declares exact issuer, audience and scopes."""

    authorization_response = protocol_harness.request(
        "GET", "/.well-known/oauth-authorization-server"
    )
    assert_status(
        authorization_response,
        200,
        operation="读取 OAuth 授权服务器元数据",
    )
    assert "public" in authorization_response.headers.get("Cache-Control", "")
    authorization = json_object(
        authorization_response,
        operation="读取 OAuth 授权服务器元数据",
    )
    issuer = authorization.get("issuer")
    token_endpoint = authorization.get("token_endpoint")
    assert isinstance(issuer, str) and issuer.startswith(("http://", "https://"))
    assert isinstance(token_endpoint, str) and token_endpoint == (
        issuer.rstrip("/") + "/oauth/token"
    )
    assert authorization.get("grant_types_supported") == ["client_credentials"]
    assert {
        "client_secret_basic",
        "client_secret_post",
    }.issubset(set(authorization.get("token_endpoint_auth_methods_supported", [])))
    assert set(MINIMAL_AGENT_SCOPES).issubset(
        set(authorization.get("scopes_supported", []))
    )

    observed_resources: set[str] = set()
    expected_names = {
        "api/v1": "ChronoDesk Agent REST API",
        "mcp": "ChronoDesk MCP",
        "a2a/v1": "ChronoDesk A2A",
    }
    for suffix, resource_name in expected_names.items():
        response = protocol_harness.request(
            "GET",
            f"/.well-known/oauth-protected-resource/{suffix}",
        )
        assert_status(response, 200, operation=f"读取 {suffix} 资源元数据")
        assert "public" in response.headers.get("Cache-Control", "")
        metadata = json_object(response, operation=f"读取 {suffix} 资源元数据")
        resource = metadata.get("resource")
        assert isinstance(resource, str) and resource.startswith(
            ("http://", "https://")
        )
        assert resource not in observed_resources, "三个协议 audience 必须相互隔离"
        observed_resources.add(resource)
        assert metadata.get("authorization_servers") == [issuer]
        assert metadata.get("bearer_methods_supported") == ["header"]
        assert metadata.get("resource_name") == resource_name
        assert set(metadata.get("scopes_supported", [])) == set(MINIMAL_AGENT_SCOPES)

    assert len(observed_resources) == 3


def test_admin_provisions_e2e_principal_and_explicit_policy(
    protocol_harness: AgentProtocolHarness,
    full_service_principal: ServicePrincipalFixture,
) -> None:
    """AGT-001/AGT-002/AGT-006: machine identity is independent and auditable."""

    row = protocol_harness.principal_row(full_service_principal)
    assert row.get("name", "").startswith(protocol_harness.prefix)
    assert row.get("status") == "active"
    assert row.get("client_id") == full_service_principal.client_id
    assert set(row.get("scopes", [])) == set(MINIMAL_AGENT_SCOPES)
    assert row.get("rate_limit") == 600
    assert row.get("concurrency_limit") == 8
    assert "compatibility_user_id" not in row
    _assert_no_secret_fields(row)

    response = protocol_harness.request(
        "GET",
        (
            "/api/v1/admin/service-principals/"
            f"{full_service_principal.client_id}/policies"
        ),
        headers=protocol_harness.admin_read_headers(),
    )
    assert_status(response, 200, operation="读取 E2E Agent 策略")
    policies = envelope_data(response, operation="读取 E2E Agent 策略")
    assert isinstance(policies, list)
    policy = next(
        (
            item
            for item in policies
            if isinstance(item, dict)
            and item.get("id") in full_service_principal.policy_ids
        ),
        None,
    )
    assert isinstance(policy, dict), "未找到 fixture 创建的显式 allow 策略"
    assert policy.get("effect") == "allow"
    assert policy.get("scope") == "tickets:transition"
    assert policy.get("action") == "ticket.transition"
    assert policy.get("resource_type") == "ticket"
    assert policy.get("resource_id") == "*"
    assert policy.get("is_active") is True
    _assert_no_secret_fields(policies)


def test_client_credentials_rotation_and_deactivation_invalidate_tokens(
    protocol_harness: AgentProtocolHarness,
    protected_resources: dict[str, str],
) -> None:
    """AGT-003/AGT-005/AGT-008: scope, rotation and deactivation fail closed."""

    principal = protocol_harness.create_principal(
        "credential-lifecycle",
        ("tickets:read",),
    )
    protocol_harness.create_policy(
        principal,
        label="allow-read",
        effect="allow",
        scope="tickets:read",
        action="ticket.read",
        resource_type="ticket",
        resource_id="*",
    )
    api_resource = protected_resources["api"]

    first = protocol_harness.exchange_token(
        principal,
        api_resource,
        ("tickets:read",),
    )
    visible = protocol_harness.request(
        "GET",
        "/api/v1/tickets",
        headers=_bearer(first),
        params={"limit": 1},
    )
    assert_status(visible, 200, operation="只读服务主体查询工单")

    denied_create = protocol_harness.request(
        "POST",
        "/api/v1/tickets",
        headers={
            **_bearer(first),
            "Idempotency-Key": protocol_harness.idempotency_key(
                "read-token-create-denied"
            ),
        },
        json_body={
            "title": protocol_harness.unique("must-not-create"),
            "description": f"{protocol_harness.prefix}insufficient scope",
            "type": "request",
            "priority": "normal",
        },
    )
    _assert_problem(
        denied_create,
        status=403,
        code="insufficient_scope",
        operation="只读 token 尝试创建工单",
    )

    protocol_harness.rotate_credential(principal)
    stale_after_rotation = protocol_harness.request(
        "GET",
        "/api/v1/tickets",
        headers=_bearer(first),
        params={"limit": 1},
    )
    _assert_problem(
        stale_after_rotation,
        status=401,
        code="unauthorized",
        operation="轮换后使用旧 access token",
    )
    assert 'error="invalid_token"' in stale_after_rotation.headers.get(
        "WWW-Authenticate", ""
    )

    rotated = protocol_harness.exchange_token(
        principal,
        api_resource,
        ("tickets:read",),
    )
    valid_after_rotation = protocol_harness.request(
        "GET",
        "/api/v1/tickets",
        headers=_bearer(rotated),
        params={"limit": 1},
    )
    assert_status(valid_after_rotation, 200, operation="轮换后使用新 access token")

    protocol_harness.set_principal_status(principal, status="inactive")
    stale_after_deactivation = protocol_harness.request(
        "GET",
        "/api/v1/tickets",
        headers=_bearer(rotated),
        params={"limit": 1},
    )
    _assert_problem(
        stale_after_deactivation,
        status=401,
        code="unauthorized",
        operation="停用服务主体后使用现有 access token",
    )
    assert protocol_harness.principal_row(principal).get("status") == "inactive"


def test_agent_rest_ticket_idempotency_lease_version_and_event_cursor(
    protocol_harness: AgentProtocolHarness,
    agent_tokens: dict[str, AgentAccessToken],
    protected_resources: dict[str, str],
) -> None:
    """AGT-012..020/EVT-018: complete Agent REST coordination contract."""

    token = agent_tokens["api"]
    headers = _bearer(token)

    capabilities_response = protocol_harness.request("GET", "/api/v1/capabilities")
    assert_status(capabilities_response, 200, operation="读取 Agent REST capabilities")
    capabilities = envelope_data(
        capabilities_response,
        operation="读取 Agent REST capabilities",
    )
    assert capabilities.get("api_version") == "v1"
    assert capabilities.get("mcp_version") == "2026-07-28"
    assert capabilities.get("a2a_version") == "1.0"
    assert capabilities.get("concurrency") == {
        "optimistic_version": True,
        "ticket_leases": True,
        "idempotency_keys": True,
    }
    assert set(capabilities.get("scopes_supported", [])) == set(MINIMAL_AGENT_SCOPES)
    assert protected_resources["api"].endswith("/api/v1")

    title = protocol_harness.unique("agent-rest-ticket")
    create_key = protocol_harness.idempotency_key("agent-rest-create")
    create_payload = {
        "title": title,
        "description": (
            f"{protocol_harness.prefix}Agent REST idempotency and lease contract"
        ),
        "type": "request",
        "priority": "normal",
        "tags": ["e2e", "agent-rest"],
        "agent_context": {
            "goal": "验证机器接口的幂等、租约和版本边界",
            "constraints": ["只操作当前 E2E 工单"],
            "acceptance_criteria": ["冲突写入不覆盖数据"],
            "missing_information": [],
            "related_resources": [],
        },
    }
    create_headers = {**headers, "Idempotency-Key": create_key}
    first_create = protocol_harness.request(
        "POST",
        "/api/v1/tickets",
        headers=create_headers,
        json_body=create_payload,
    )
    assert_status(first_create, 201, operation="Agent REST 创建工单")
    first_body = json_object(first_create, operation="Agent REST 创建工单")
    ticket = first_body.get("data")
    receipt = first_body.get("receipt")
    assert isinstance(ticket, dict) and isinstance(receipt, dict)
    ticket_id = ticket.get("id")
    version = ticket.get("version")
    assert isinstance(ticket_id, int) and ticket_id > 0
    assert isinstance(version, int) and version == 1
    assert ticket.get("title") == title
    assert ticket.get("source") == "agent"
    assert ticket.get("created_by_actor", {}).get("type") == ("service_principal")
    assert receipt.get("resource_id") == str(ticket_id)
    assert receipt.get("resource_version") == version
    assert all(
        isinstance(receipt.get(field), str) and receipt[field]
        for field in ("operation_id", "event_id", "policy_decision_id")
    )
    protocol_harness.track_ticket(ticket)

    replay = protocol_harness.request(
        "POST",
        "/api/v1/tickets",
        headers=create_headers,
        json_body=create_payload,
    )
    assert_status(replay, 201, operation="Agent REST 重放相同幂等创建")
    replay_body = json_object(replay, operation="Agent REST 重放相同幂等创建")
    assert replay_body.get("receipt") == receipt
    assert replay_body.get("data", {}).get("id") == ticket_id
    assert replay.headers.get("ETag") == first_create.headers.get("ETag")

    listed = protocol_harness.request(
        "GET",
        "/api/v1/tickets",
        headers=headers,
        params={"search": title, "limit": 10},
    )
    assert_status(listed, 200, operation="Agent REST 查询工单列表")
    list_body = json_object(listed, operation="Agent REST 查询工单列表")
    items = list_body.get("data")
    assert isinstance(items, list)
    matching = [item for item in items if item.get("id") == ticket_id]
    assert len(matching) == 1, "同一 Idempotency-Key 不得创建两个工单"

    detail = protocol_harness.request(
        "GET",
        f"/api/v1/tickets/{ticket_id}",
        headers=headers,
    )
    assert_status(detail, 200, operation="Agent REST 读取工单")
    assert detail.headers.get("ETag") == f'"v{version}"'
    assert (
        envelope_data(detail, operation="Agent REST 读取工单").get("version") == version
    )

    claim = protocol_harness.request(
        "POST",
        f"/api/v1/tickets/{ticket_id}/claim",
        headers={
            **headers,
            "If-Match": f'"v{version}"',
            "Idempotency-Key": protocol_harness.idempotency_key("claim"),
        },
        json_body={"ttl_seconds": 90},
    )
    assert_status(claim, 200, operation="Agent REST claim 工单")
    claim_body = json_object(claim, operation="Agent REST claim 工单")
    lease = claim_body.get("data")
    assert isinstance(lease, dict)
    lease_id = lease.get("lease_id")
    assert isinstance(lease_id, str) and lease_id
    assert lease.get("ticket_id") == ticket_id
    assert lease.get("ticket_version") == version
    assert isinstance(lease.get("expires_at"), str) and lease["expires_at"]
    assert claim_body.get("receipt", {}).get("resource_version") == version
    protocol_harness.track_lease(lease_id)

    heartbeat = protocol_harness.request(
        "POST",
        f"/api/v1/leases/{lease_id}/heartbeat",
        headers={
            **headers,
            "If-Match": f'"v{version}"',
            "Idempotency-Key": protocol_harness.idempotency_key("heartbeat"),
        },
        json_body={"ttl_seconds": 120},
    )
    assert_status(heartbeat, 200, operation="Agent REST heartbeat 租约")
    heartbeat_data = envelope_data(
        heartbeat,
        operation="Agent REST heartbeat 租约",
    )
    assert heartbeat_data.get("lease_id") == lease_id
    assert heartbeat_data.get("ticket_version") == version

    stale_update = protocol_harness.request(
        "PATCH",
        f"/api/v1/tickets/{ticket_id}",
        headers={
            **headers,
            "If-Match": f'"v{version + 1}"',
            "X-Ticket-Lease": lease_id,
            "Idempotency-Key": protocol_harness.idempotency_key("stale-version-update"),
        },
        json_body={"priority": "high"},
    )
    _assert_problem(
        stale_update,
        status=409,
        code="version_conflict",
        operation="Agent REST 使用过期资源版本更新",
    )

    unchanged = protocol_harness.request(
        "GET",
        f"/api/v1/tickets/{ticket_id}",
        headers=headers,
    )
    assert_status(unchanged, 200, operation="冲突后重新读取 Agent 工单")
    unchanged_ticket = envelope_data(
        unchanged,
        operation="冲突后重新读取 Agent 工单",
    )
    assert unchanged_ticket.get("version") == version
    assert unchanged_ticket.get("priority") == "normal"

    release_key = protocol_harness.idempotency_key("release")
    release = protocol_harness.request(
        "DELETE",
        f"/api/v1/leases/{lease_id}",
        headers={**headers, "Idempotency-Key": release_key},
    )
    assert_status(release, 200, operation="Agent REST release 租约")
    released_data = envelope_data(release, operation="Agent REST release 租约")
    assert released_data.get("lease_id") == lease_id
    assert released_data.get("released_at")
    protocol_harness.release_tracked_lease(lease_id)

    events_page_one = protocol_harness.request(
        "GET",
        "/api/v1/events",
        headers=headers,
        params={"limit": 1},
    )
    assert_status(events_page_one, 200, operation="读取 Agent 事件第一页")
    page_one = json_object(events_page_one, operation="读取 Agent 事件第一页")
    event_items = page_one.get("data")
    meta = page_one.get("meta")
    assert isinstance(event_items, list) and len(event_items) == 1
    assert isinstance(meta, dict)
    cursor = meta.get("next_cursor")
    assert meta.get("has_more") is True
    assert isinstance(cursor, str) and cursor
    event = event_items[0]
    for field in (
        "specversion",
        "id",
        "source",
        "type",
        "time",
        "dataschema",
        "actortype",
        "actorid",
        "resourceversion",
        "data",
    ):
        assert field in event, f"CloudEvent 缺少 {field}"
    assert event["specversion"] == "1.0"

    events_page_two = protocol_harness.request(
        "GET",
        "/api/v1/events",
        headers=headers,
        params={"limit": 1, "cursor": cursor},
    )
    assert_status(events_page_two, 200, operation="使用 cursor 恢复 Agent 事件")
    page_two = json_object(events_page_two, operation="使用 cursor 恢复 Agent 事件")
    second_items = page_two.get("data")
    assert isinstance(second_items, list) and len(second_items) <= 1
    assert {item.get("id") for item in event_items}.isdisjoint(
        {item.get("id") for item in second_items}
    )

    serialized = json.dumps(
        [first_body, replay_body, claim_body, page_one, page_two],
        ensure_ascii=False,
    )
    assert "client_secret" not in serialized
    assert "access_token" not in serialized


def test_agent_ticket_cursor_first_middle_tail_empty_and_invalid_limits(
    protocol_harness: AgentProtocolHarness,
    agent_tokens: dict[str, AgentAccessToken],
) -> None:
    """TKT-023/024: cursor pages are bounded, stable and reject malformed input."""

    token = agent_tokens["api"]
    headers = _bearer(token)
    marker = protocol_harness.unique("cursor-window")
    created_ids: set[int] = set()
    for index in range(3):
        response = protocol_harness.request(
            "POST",
            "/api/v1/tickets",
            headers={
                **headers,
                "Idempotency-Key": protocol_harness.idempotency_key(
                    f"cursor-ticket-{index}"
                ),
            },
            json_body={
                "title": f"{marker}-{index}",
                "description": f"{protocol_harness.prefix}cursor page {index}",
                "type": "request",
                "priority": "normal",
            },
        )
        assert_status(response, 201, operation=f"创建 cursor 工单 {index}")
        ticket = envelope_data(response, operation=f"创建 cursor 工单 {index}")
        assert isinstance(ticket, dict)
        protocol_harness.track_ticket(ticket)
        created_ids.add(ticket["id"])

    observed: list[int] = []
    cursor: str | None = None
    page_count = 0
    while True:
        params: dict[str, Any] = {"search": marker, "limit": 1}
        if cursor is not None:
            params["cursor"] = cursor
        response = protocol_harness.request(
            "GET",
            "/api/v1/tickets",
            headers=headers,
            params=params,
        )
        assert_status(response, 200, operation=f"读取 cursor 页 {page_count + 1}")
        page = json_object(response, operation=f"读取 cursor 页 {page_count + 1}")
        items = page.get("data")
        meta = page.get("meta")
        assert isinstance(items, list) and len(items) == 1, page
        assert isinstance(meta, dict), page
        item_id = items[0].get("id")
        assert isinstance(item_id, int) and item_id in created_ids
        assert item_id not in observed, "cursor 翻页返回了重复工单"
        observed.append(item_id)
        page_count += 1
        if not meta.get("has_more"):
            assert "next_cursor" not in meta
            break
        cursor = meta.get("next_cursor")
        assert isinstance(cursor, str) and cursor
        assert page_count < 10, "cursor 翻页未收敛"

    assert set(observed) == created_ids
    assert page_count == 3

    empty = protocol_harness.request(
        "GET",
        "/api/v1/tickets",
        headers=headers,
        params={
            "search": protocol_harness.unique("cursor-empty"),
            "limit": 1,
        },
    )
    assert_status(empty, 200, operation="读取 cursor 空页")
    empty_body = json_object(empty, operation="读取 cursor 空页")
    assert empty_body.get("data") == []
    assert empty_body.get("meta", {}).get("has_more") in (None, False)
    assert "next_cursor" not in empty_body.get("meta", {})

    for label, params in (
        ("超大 limit", {"limit": 101}),
        ("零 limit", {"limit": 0}),
        ("负 limit", {"limit": -1}),
        ("非数字 limit", {"limit": "NaN"}),
        ("伪造 cursor", {"cursor": "not-an-opaque-cursor"}),
    ):
        rejected = protocol_harness.request(
            "GET",
            "/api/v1/tickets",
            headers=headers,
            params=params,
        )
        _assert_problem(
            rejected,
            status=400,
            code="invalid_request",
            operation=f"Agent REST {label}",
        )
