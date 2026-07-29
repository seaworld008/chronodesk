"""A2A 1.0 Agent Card, Task lifecycle, and SSE cursor black-box tests."""

from __future__ import annotations

import json
from typing import Any

import pytest
import requests

from tests.utils.agent_protocol import (
    A2A_PROTOCOL_VERSION,
    AgentAccessToken,
    AgentProtocolHarness,
    assert_status,
    envelope_data,
    json_object,
)

pytestmark = [pytest.mark.api, pytest.mark.integration]

EXPECTED_SKILLS = {
    "ticket-intake",
    "ticket-query",
    "ticket-work",
    "ticket-comment",
    "ticket-escalation",
}


def _a2a_rpc(
    harness: AgentProtocolHarness,
    token: AgentAccessToken | None,
    method: str,
    params: dict[str, Any],
    *,
    request_id: str,
    stream: bool = False,
    last_event_id: str | None = None,
) -> requests.Response:
    headers = {
        "Content-Type": "application/json",
        "Accept": "text/event-stream" if stream else "application/json",
        "A2A-Version": A2A_PROTOCOL_VERSION,
    }
    if token is not None:
        headers["Authorization"] = token.authorization
    if last_event_id is not None:
        headers["Last-Event-ID"] = last_event_id
    return harness.request(
        "POST",
        "/a2a/v1",
        headers=headers,
        json_body={
            "jsonrpc": "2.0",
            "id": request_id,
            "method": method,
            "params": params,
        },
        stream=stream,
    )


def _rpc_result(
    response: requests.Response,
    *,
    operation: str,
) -> Any:
    assert_status(response, 200, operation=operation)
    body = json_object(response, operation=operation)
    assert body.get("jsonrpc") == "2.0", body
    assert body.get("error") is None, body.get("error")
    assert "result" in body, body
    return body["result"]


def _create_agent_rest_ticket(
    harness: AgentProtocolHarness,
    api_token: AgentAccessToken,
    label: str,
) -> dict[str, Any]:
    response = harness.request(
        "POST",
        "/api/v1/tickets",
        headers={
            "Authorization": api_token.authorization,
            "Idempotency-Key": harness.idempotency_key(label),
        },
        json_body={
            "title": harness.unique(label),
            "description": f"{harness.prefix}A2A linked Ticket isolation",
            "type": "request",
            "priority": "normal",
            "tags": ["e2e", "a2a"],
        },
    )
    assert_status(response, 201, operation="为 A2A 创建关联工单")
    ticket = envelope_data(response, operation="为 A2A 创建关联工单")
    assert isinstance(ticket, dict)
    harness.track_ticket(ticket)
    return ticket


def _parse_sse(response: requests.Response) -> list[dict[str, Any]]:
    assert_status(response, 200, operation="读取 A2A SSE")
    assert response.headers.get("Content-Type", "").startswith("text/event-stream")
    assert response.headers.get("X-Accel-Buffering") == "no"

    events: list[dict[str, Any]] = []
    event_id: str | None = None
    data_lines: list[str] = []
    try:
        for raw_line in response.iter_lines(decode_unicode=True):
            if isinstance(raw_line, bytes):
                line = raw_line.decode("utf-8")
            else:
                line = raw_line
            if line == "":
                if data_lines:
                    payload = json.loads("\n".join(data_lines))
                    assert isinstance(payload, dict)
                    events.append({"id": event_id, "payload": payload})
                event_id = None
                data_lines = []
                continue
            if line.startswith(":"):
                continue
            if line.startswith("id:"):
                event_id = line.removeprefix("id:").strip()
            elif line.startswith("data:"):
                data_lines.append(line.removeprefix("data:").lstrip())
        if data_lines:
            payload = json.loads("\n".join(data_lines))
            assert isinstance(payload, dict)
            events.append({"id": event_id, "payload": payload})
    finally:
        response.close()
    assert events, "A2A SSE 未返回任何事件"
    return events


def _artifact_domain_result(task: dict[str, Any]) -> dict[str, Any]:
    artifacts = task.get("artifacts")
    assert isinstance(artifacts, list) and artifacts
    parts = artifacts[-1].get("parts")
    assert isinstance(parts, list) and parts
    data = parts[0].get("data")
    assert isinstance(data, dict)
    assert data.get("trust") == "untrusted-domain-content"
    result = data.get("result")
    assert isinstance(result, dict)
    return result


def test_agent_card_etag_authentication_and_current_capabilities(
    protocol_harness: AgentProtocolHarness,
    protected_resources: dict[str, str],
) -> None:
    """A2A-001..003: public discovery is cacheable; RPC remains OAuth-only."""

    first = protocol_harness.request(
        "GET",
        "/.well-known/agent-card.json",
    )
    assert_status(first, 200, operation="读取 A2A Agent Card")
    etag = first.headers.get("ETag")
    assert isinstance(etag, str) and etag
    assert first.headers.get("Last-Modified")
    assert "public" in first.headers.get("Cache-Control", "")
    card = json_object(first, operation="读取 A2A Agent Card")

    interfaces = card.get("supportedInterfaces")
    assert interfaces == [
        {
            "url": protected_resources["a2a"],
            "protocolBinding": "JSONRPC",
            "protocolVersion": A2A_PROTOCOL_VERSION,
        }
    ]
    assert card.get("capabilities", {}).get("streaming") is True
    assert card.get("capabilities", {}).get("pushNotifications") is True
    assert {skill.get("id") for skill in card.get("skills", [])} == (EXPECTED_SKILLS)
    assert set(card.get("securitySchemes", {})) == {"oauth2", "bearer"}
    oauth = card["securitySchemes"]["oauth2"]["oauth2SecurityScheme"]
    assert oauth.get("oauth2MetadataUrl", "").endswith(
        "/.well-known/oauth-authorization-server"
    )
    token_url = oauth.get("flows", {}).get("clientCredentials", {}).get("tokenUrl")
    assert isinstance(token_url, str) and token_url.endswith("/oauth/token")
    assert set(oauth.get("flows", {}).get("clientCredentials", {}).get("scopes", {}))

    conditional = protocol_harness.request(
        "GET",
        "/.well-known/agent-card.json",
        headers={"If-None-Match": etag},
    )
    assert_status(conditional, 304, operation="条件读取 A2A Agent Card")
    assert conditional.content == b""

    unauthenticated = _a2a_rpc(
        protocol_harness,
        None,
        "ListTasks",
        {"pageSize": 1},
        request_id="e2e-a2a-unauthenticated",
    )
    assert_status(unauthenticated, 401, operation="未认证 A2A ListTasks")
    problem = json_object(unauthenticated, operation="未认证 A2A ListTasks")
    assert problem.get("code") == "unauthorized"
    challenge = unauthenticated.headers.get("WWW-Authenticate", "")
    assert challenge.startswith("Bearer ")
    assert "resource_metadata=" in challenge


def test_a2a_ticket_intake_creates_queryable_task_and_ticket_artifact(
    protocol_harness: AgentProtocolHarness,
    agent_tokens: dict[str, AgentAccessToken],
) -> None:
    """A2A-004/A2A-005/A2A-006/A2A-010: Task and Ticket stay separate."""

    title = protocol_harness.unique("a2a-intake")
    params = {
        "message": {
            "messageId": protocol_harness.unique("a2a-intake-message"),
            "role": "ROLE_USER",
            "parts": [
                {
                    "data": {
                        "title": title,
                        "description": (f"{protocol_harness.prefix}A2A ticket intake"),
                        "type": "request",
                        "priority": "normal",
                        "tags": ["e2e", "a2a-intake"],
                    },
                    "mediaType": "application/json",
                }
            ],
            "metadata": {"skill": "ticket-intake"},
        },
        "metadata": {"testRun": protocol_harness.run_id},
    }
    response = _a2a_rpc(
        protocol_harness,
        agent_tokens["a2a"],
        "SendMessage",
        params,
        request_id="e2e-a2a-intake",
    )
    result = _rpc_result(response, operation="A2A SendMessage ticket-intake")
    task = result.get("task")
    assert isinstance(task, dict)
    assert isinstance(task.get("id"), str) and task["id"]
    assert isinstance(task.get("contextId"), str) and task["contextId"]
    assert task.get("status", {}).get("state") == "TASK_STATE_COMPLETED"
    assert not {
        "createdAt",
        "lastModified",
        "linkedTicketId",
        "statusHistory",
    } & set(task)

    domain_result = _artifact_domain_result(task)
    ticket = domain_result.get("ticket")
    receipt = domain_result.get("receipt")
    assert isinstance(ticket, dict) and isinstance(receipt, dict)
    assert ticket.get("title") == title
    assert ticket.get("source") == "agent"
    assert ticket.get("created_by_actor", {}).get("type") == ("service_principal")
    assert receipt.get("resource_id") == str(ticket.get("id"))
    protocol_harness.track_ticket(ticket)

    fetched = _a2a_rpc(
        protocol_harness,
        agent_tokens["a2a"],
        "GetTask",
        {"id": task["id"], "historyLength": 1},
        request_id="e2e-a2a-get-intake",
    )
    fetched_task = _rpc_result(fetched, operation="A2A GetTask")
    assert fetched_task.get("id") == task["id"]
    assert fetched_task.get("status", {}).get("state") == ("TASK_STATE_COMPLETED")
    assert len(fetched_task.get("history", [])) <= 1

    listed = _a2a_rpc(
        protocol_harness,
        agent_tokens["a2a"],
        "ListTasks",
        {
            "contextId": task["contextId"],
            "pageSize": 10,
            "includeArtifacts": True,
        },
        request_id="e2e-a2a-list-intake",
    )
    list_result = _rpc_result(listed, operation="A2A ListTasks")
    assert any(item.get("id") == task["id"] for item in list_result.get("tasks", []))


def test_a2a_input_required_does_not_mutate_ticket_and_sse_resumes_by_cursor(
    protocol_harness: AgentProtocolHarness,
    agent_tokens: dict[str, AgentAccessToken],
) -> None:
    """A2A-007/A2A-008/A2A-011: input-required is Task-only and replayable."""

    ticket = _create_agent_rest_ticket(
        protocol_harness,
        agent_tokens["api"],
        "a2a-input-required-ticket",
    )
    ticket_id = ticket["id"]
    original_status = ticket["status"]
    original_version = ticket["version"]

    send = _a2a_rpc(
        protocol_harness,
        agent_tokens["a2a"],
        "SendMessage",
        {
            "message": {
                "messageId": protocol_harness.unique("a2a-input-required-message"),
                "role": "ROLE_USER",
                "parts": [
                    {
                        "data": {"ticket_id": ticket_id},
                        "mediaType": "application/json",
                    }
                ],
                "metadata": {"skill": "ticket-comment"},
            },
            "metadata": {
                "com.chronodesk/linkedTicketId": ticket_id,
            },
        },
        request_id="e2e-a2a-input-required",
    )
    send_result = _rpc_result(send, operation="A2A 创建 input-required Task")
    task = send_result.get("task")
    assert isinstance(task, dict)
    assert task.get("status", {}).get("state") == ("TASK_STATE_INPUT_REQUIRED")
    status_message = task.get("status", {}).get("message")
    assert isinstance(status_message, dict)
    status_parts = status_message.get("parts")
    assert isinstance(status_parts, list) and status_parts
    status_data = status_parts[0].get("data")
    assert isinstance(status_data, dict)
    assert status_data.get("code") == "structured_input_required"
    assert {
        "ticket_id",
        "expected_version",
        "lease_id",
        "content",
    }.issubset(set(status_data.get("requiredFields", [])))

    unchanged = protocol_harness.request(
        "GET",
        f"/api/v1/tickets/{ticket_id}",
        headers={"Authorization": agent_tokens["api"].authorization},
    )
    assert_status(unchanged, 200, operation="input-required 后读取业务工单")
    unchanged_ticket = envelope_data(
        unchanged,
        operation="input-required 后读取业务工单",
    )
    assert unchanged_ticket.get("status") == original_status
    assert unchanged_ticket.get("version") == original_version
    assert original_status != "pending"

    first_stream = _a2a_rpc(
        protocol_harness,
        agent_tokens["a2a"],
        "SubscribeToTask",
        {"id": task["id"]},
        request_id="e2e-a2a-subscribe-first",
        stream=True,
    )
    first_events = _parse_sse(first_stream)
    first_cursors = [
        event["id"]
        for event in first_events
        if isinstance(event.get("id"), str) and event["id"]
    ]
    assert len(first_cursors) >= 3, (
        "首次 A2A 订阅应包含 submitted/working/input-required 的持久游标"
    )
    assert len(first_cursors) == len(set(first_cursors))

    resumed_stream = _a2a_rpc(
        protocol_harness,
        agent_tokens["a2a"],
        "SubscribeToTask",
        {"id": task["id"]},
        request_id="e2e-a2a-subscribe-resumed",
        stream=True,
        last_event_id=first_cursors[0],
    )
    resumed_events = _parse_sse(resumed_stream)
    resumed_cursors = [
        event["id"]
        for event in resumed_events
        if isinstance(event.get("id"), str) and event["id"]
    ]
    assert first_cursors[0] not in resumed_cursors
    assert resumed_cursors == first_cursors[1:]

    cancel = _a2a_rpc(
        protocol_harness,
        agent_tokens["a2a"],
        "CancelTask",
        {"id": task["id"]},
        request_id="e2e-a2a-cancel",
    )
    canceled_task = _rpc_result(cancel, operation="A2A CancelTask")
    assert canceled_task.get("id") == task["id"]
    assert canceled_task.get("status", {}).get("state") == ("TASK_STATE_CANCELED")

    fetched = _a2a_rpc(
        protocol_harness,
        agent_tokens["a2a"],
        "GetTask",
        {"id": task["id"], "historyLength": 10},
        request_id="e2e-a2a-get-canceled",
    )
    fetched_task = _rpc_result(fetched, operation="A2A 查询已取消 Task")
    assert fetched_task.get("status", {}).get("state") == ("TASK_STATE_CANCELED")

    still_unchanged = protocol_harness.request(
        "GET",
        f"/api/v1/tickets/{ticket_id}",
        headers={"Authorization": agent_tokens["api"].authorization},
    )
    assert_status(
        still_unchanged,
        200,
        operation="取消 A2A Task 后读取业务工单",
    )
    current_ticket = envelope_data(
        still_unchanged,
        operation="取消 A2A Task 后读取业务工单",
    )
    assert current_ticket.get("status") == original_status
    assert current_ticket.get("version") == original_version
