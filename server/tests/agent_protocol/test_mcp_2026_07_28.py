"""MCP 2026-07-28-only real HTTP contract tests."""

from __future__ import annotations

import json
from typing import Any

import pytest
import requests

from tests.utils.agent_protocol import (
    MCP_PROTOCOL_VERSION,
    AgentAccessToken,
    AgentProtocolHarness,
    assert_status,
    json_object,
)

pytestmark = [pytest.mark.api, pytest.mark.integration]

EXPECTED_TOOLS = (
    "action_check",
    "ticket_add_comment",
    "ticket_assign",
    "ticket_attach_file",
    "ticket_claim",
    "ticket_create",
    "ticket_get",
    "ticket_heartbeat",
    "ticket_history",
    "ticket_list",
    "ticket_release",
    "ticket_transition",
    "ticket_update",
)


def _modern_meta() -> dict[str, Any]:
    return {
        "io.modelcontextprotocol/protocolVersion": MCP_PROTOCOL_VERSION,
        "io.modelcontextprotocol/clientCapabilities": {
            "extensions": {
                "io.modelcontextprotocol/oauth-client-credentials": {},
            }
        },
        "io.modelcontextprotocol/clientInfo": {
            "name": "chronodesk-pytest-client",
            "version": "1.0.0",
        },
    }


def _mcp_post(
    harness: AgentProtocolHarness,
    token: AgentAccessToken | None,
    *,
    method: str,
    params: dict[str, Any] | None = None,
    name: str | None = None,
    protocol_version: str | None = MCP_PROTOCOL_VERSION,
    method_header: str | None = "",
    include_meta: bool = True,
    request_id: str = "e2e-mcp-request",
    extra_headers: dict[str, str] | None = None,
) -> requests.Response:
    request_params = dict(params or {})
    if include_meta:
        request_params.setdefault("_meta", _modern_meta())
    headers = {
        "Accept": "application/json, text/event-stream",
        "Content-Type": "application/json",
    }
    if token is not None:
        headers["Authorization"] = token.authorization
    if protocol_version is not None:
        headers["MCP-Protocol-Version"] = protocol_version
    if method_header == "":
        headers["Mcp-Method"] = method
    elif method_header is not None:
        headers["Mcp-Method"] = method_header
    if name is not None:
        headers["Mcp-Name"] = name
    headers.update(extra_headers or {})
    return harness.request(
        "POST",
        "/mcp",
        headers=headers,
        json_body={
            "jsonrpc": "2.0",
            "id": request_id,
            "method": method,
            "params": request_params,
        },
    )


def _rpc_error(
    response: requests.Response,
    *,
    status: int,
    code: int,
    operation: str,
) -> dict[str, Any]:
    assert_status(response, status, operation=operation)
    body = json_object(response, operation=operation)
    error = body.get("error")
    assert isinstance(error, dict), f"{operation} 缺少 JSON-RPC error"
    assert error.get("code") == code, error
    assert isinstance(error.get("message"), str) and error["message"], error
    return error


def _create_ticket_for_mcp(
    harness: AgentProtocolHarness,
    api_token: AgentAccessToken,
) -> dict[str, Any]:
    response = harness.request(
        "POST",
        "/api/v1/tickets",
        headers={
            "Authorization": api_token.authorization,
            "Idempotency-Key": harness.idempotency_key("mcp-ticket"),
        },
        json_body={
            "title": harness.unique("mcp-ticket"),
            "description": f"{harness.prefix}MCP ticket_get structured result",
            "type": "request",
            "priority": "normal",
            "tags": ["e2e", "mcp"],
        },
    )
    assert_status(response, 201, operation="为 MCP 创建 E2E 工单")
    ticket = json_object(response, operation="为 MCP 创建 E2E 工单").get("data")
    assert isinstance(ticket, dict)
    harness.track_ticket(ticket)
    return ticket


def test_mcp_server_discover_advertises_latest_only(
    protocol_harness: AgentProtocolHarness,
    agent_tokens: dict[str, AgentAccessToken],
) -> None:
    """MCP-001/MCP-017: discover exposes only the current stateless contract."""

    response = _mcp_post(
        protocol_harness,
        agent_tokens["mcp"],
        method="server/discover",
    )
    assert_status(response, 200, operation="MCP server/discover")
    assert "Mcp-Session-Id" not in response.headers
    body = json_object(response, operation="MCP server/discover")
    assert body.get("jsonrpc") == "2.0"
    result = body.get("result")
    assert isinstance(result, dict)
    assert result.get("supportedVersions") == [MCP_PROTOCOL_VERSION]
    assert result.get("resultType") == "complete"
    assert result.get("cacheScope") == "private"
    assert result.get("ttlMs") == 300000
    capabilities = result.get("capabilities")
    assert isinstance(capabilities, dict)
    extensions = capabilities.get("extensions")
    assert extensions == {"io.modelcontextprotocol/oauth-client-credentials": {}}
    server_info = result.get("_meta", {}).get("io.modelcontextprotocol/serverInfo")
    assert isinstance(server_info, dict)
    assert server_info.get("name") == "chronodesk"


def test_mcp_requires_audience_token_and_rejects_legacy_transport(
    protocol_harness: AgentProtocolHarness,
    agent_tokens: dict[str, AgentAccessToken],
) -> None:
    """AGT-004/MCP-003/MCP-004: auth and transport never fall back."""

    unauthenticated = _mcp_post(
        protocol_harness,
        None,
        method="server/discover",
    )
    assert_status(
        unauthenticated,
        401,
        operation="未认证 MCP server/discover",
    )
    challenge = unauthenticated.headers.get("WWW-Authenticate", "")
    assert challenge.startswith("Bearer ")
    assert "resource_metadata=" in challenge

    wrong_audience = _mcp_post(
        protocol_harness,
        agent_tokens["api"],
        method="server/discover",
    )
    assert_status(
        wrong_audience,
        401,
        operation="使用 Agent REST audience token 调用 MCP",
    )

    for method in ("GET", "DELETE"):
        response = protocol_harness.request(method, "/mcp")
        assert_status(
            response,
            405,
            operation=f"旧 MCP {method} 传输",
        )
        assert response.headers.get("Allow") == "POST"

    removed_headers = _mcp_post(
        protocol_harness,
        agent_tokens["mcp"],
        method="server/discover",
        extra_headers={
            "Mcp-Session-Id": "removed-session",
            "Last-Event-ID": "removed-cursor",
        },
    )
    assert_status(
        removed_headers,
        200,
        operation="已移除的 MCP 传输 Header 无协议语义",
    )
    assert "Mcp-Session-Id" not in removed_headers.headers
    assert "Last-Event-ID" not in removed_headers.headers

    obsolete_methods = {
        "initialize": {
            "protocolVersion": MCP_PROTOCOL_VERSION,
            "capabilities": {},
            "clientInfo": {"name": "legacy-client", "version": "1"},
        },
        "resources/subscribe": {"uri": "ticket://capabilities"},
    }
    for obsolete_method, params in obsolete_methods.items():
        obsolete = _mcp_post(
            protocol_harness,
            agent_tokens["mcp"],
            method=obsolete_method,
            params=params,
        )
        _rpc_error(
            obsolete,
            status=404,
            code=-32601,
            operation=f"旧 MCP {obsolete_method}",
        )


@pytest.mark.parametrize(
    "notification_method",
    ("notifications/initialized", "notifications/cancelled"),
)
def test_mcp_rejects_client_notifications_over_streamable_http(
    protocol_harness: AgentProtocolHarness,
    agent_tokens: dict[str, AgentAccessToken],
    notification_method: str,
) -> None:
    """MCP-004: any id-less client notification is an invalid HTTP request.

    A request carrying an obsolete method *with* an id reaches method
    resolution and returns ``-32601`` (covered above). Omitting the id makes it
    a client notification, which MCP 2026-07-28 Streamable HTTP does not define,
    so the stricter transport-level ``-32600`` applies before method dispatch.
    """

    response = protocol_harness.request(
        "POST",
        "/mcp",
        headers={
            "Authorization": agent_tokens["mcp"].authorization,
            "Accept": "application/json, text/event-stream",
            "Content-Type": "application/json",
            "MCP-Protocol-Version": MCP_PROTOCOL_VERSION,
            "Mcp-Method": notification_method,
        },
        json_body={
            "jsonrpc": "2.0",
            "method": notification_method,
            "params": {
                "_meta": _modern_meta(),
            },
        },
    )
    _rpc_error(
        response,
        status=400,
        code=-32600,
        operation="Streamable HTTP 客户端通知",
    )


@pytest.mark.parametrize(
    ("label", "kwargs", "expected_code"),
    [
        (
            "旧协议版本",
            {"protocol_version": "2025-11-25"},
            -32022,
        ),
        (
            "缺少版本 Header",
            {"protocol_version": None},
            -32020,
        ),
        (
            "缺少方法 Header",
            {"method_header": None},
            -32020,
        ),
        (
            "方法 Header 与 body 不一致",
            {"method_header": "resources/list"},
            -32020,
        ),
        (
            "缺少每请求 _meta",
            {"include_meta": False},
            -32602,
        ),
    ],
)
def test_mcp_rejects_old_version_or_missing_request_contract(
    protocol_harness: AgentProtocolHarness,
    agent_tokens: dict[str, AgentAccessToken],
    label: str,
    kwargs: dict[str, Any],
    expected_code: int,
) -> None:
    """MCP-002/MCP-005/MCP-006: headers and per-request metadata are strict."""

    response = _mcp_post(
        protocol_harness,
        agent_tokens["mcp"],
        method="tools/list",
        **kwargs,
    )
    _rpc_error(
        response,
        status=400,
        code=expected_code,
        operation=f"MCP {label}",
    )


def test_mcp_tools_list_and_ticket_get_return_schema_bound_structured_result(
    protocol_harness: AgentProtocolHarness,
    agent_tokens: dict[str, AgentAccessToken],
) -> None:
    """MCP-007..010: discovery and calls use strict name/schema/result contracts."""

    list_response = _mcp_post(
        protocol_harness,
        agent_tokens["mcp"],
        method="tools/list",
    )
    assert_status(list_response, 200, operation="MCP tools/list")
    list_body = json_object(list_response, operation="MCP tools/list")
    result = list_body.get("result")
    assert isinstance(result, dict)
    assert result.get("resultType") == "complete"
    assert result.get("cacheScope") == "private"
    assert result.get("ttlMs") == 0
    tools = result.get("tools")
    assert isinstance(tools, list)
    names = tuple(tool.get("name") for tool in tools)
    assert names == EXPECTED_TOOLS

    for tool in tools:
        assert tool.get("inputSchema", {}).get("type") == "object"
        assert tool.get("inputSchema", {}).get("additionalProperties") is False
        assert tool.get("outputSchema", {}).get("type") == "object"
        assert tool.get("outputSchema", {}).get("additionalProperties") is False
        annotations = tool.get("annotations")
        assert isinstance(annotations, dict)
        for annotation in (
            "readOnlyHint",
            "destructiveHint",
            "idempotentHint",
            "openWorldHint",
        ):
            assert isinstance(annotations.get(annotation), bool)
        metadata = tool.get("_meta")
        assert isinstance(metadata, dict)
        assert isinstance(metadata.get("com.chronodesk/required-scopes"), list)
        assert isinstance(metadata.get("com.chronodesk/idempotency-required"), bool)
        assert metadata.get("com.chronodesk/no-external-fetch") is True

    ticket = _create_ticket_for_mcp(
        protocol_harness,
        agent_tokens["api"],
    )
    ticket_id = ticket["id"]

    missing_name = _mcp_post(
        protocol_harness,
        agent_tokens["mcp"],
        method="tools/call",
        params={"name": "ticket_get", "arguments": {"ticket_id": ticket_id}},
    )
    _rpc_error(
        missing_name,
        status=400,
        code=-32020,
        operation="MCP tools/call 缺少 Mcp-Name",
    )

    mismatched_name = _mcp_post(
        protocol_harness,
        agent_tokens["mcp"],
        method="tools/call",
        name="ticket_list",
        params={"name": "ticket_get", "arguments": {"ticket_id": ticket_id}},
    )
    _rpc_error(
        mismatched_name,
        status=400,
        code=-32020,
        operation="MCP tools/call 名称不一致",
    )

    call_response = _mcp_post(
        protocol_harness,
        agent_tokens["mcp"],
        method="tools/call",
        name="ticket_get",
        params={"name": "ticket_get", "arguments": {"ticket_id": ticket_id}},
    )
    assert_status(call_response, 200, operation="MCP ticket_get")
    call_body = json_object(call_response, operation="MCP ticket_get")
    call_result = call_body.get("result")
    assert isinstance(call_result, dict)
    assert call_result.get("resultType") == "complete"
    # MCP omits optional default-valued fields in a successful ToolResult.
    assert call_result.get("isError", False) is False
    structured = call_result.get("structuredContent")
    assert isinstance(structured, dict)
    assert structured.get("ok") is True
    returned_ticket = structured.get("data", {}).get("ticket")
    assert isinstance(returned_ticket, dict)
    assert returned_ticket.get("id") == ticket_id
    assert returned_ticket.get("title") == ticket["title"]
    assert returned_ticket.get("version") == ticket["version"]
    assert call_result.get("_meta", {}).get("com.chronodesk/trust") == ("untrusted")
    content = call_result.get("content")
    assert isinstance(content, list) and len(content) == 1
    encoded = content[0].get("text")
    assert isinstance(encoded, str)
    assert json.loads(encoded) == structured

    invalid_arguments = _mcp_post(
        protocol_harness,
        agent_tokens["mcp"],
        method="tools/call",
        name="ticket_get",
        params={
            "name": "ticket_get",
            "arguments": {
                "ticket_id": str(ticket_id),
                "external_url": "https://example.invalid/untrusted",
            },
        },
    )
    _rpc_error(
        invalid_arguments,
        status=400,
        code=-32602,
        operation="MCP ticket_get 非法参数",
    )
