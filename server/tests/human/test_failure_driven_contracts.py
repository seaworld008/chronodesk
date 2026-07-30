"""Failure-driven Human REST contracts for known authorization/API gaps."""

from __future__ import annotations

import json
from collections.abc import Mapping
from typing import Any

import pytest

from tests.utils import (
    E2EResourceManager,
    HumanIdentity,
    assert_error_contract,
)

pytestmark = [pytest.mark.api, pytest.mark.integration]


def test_agent_full_queue_read_remains_read_only_under_current_human_policy(
    e2e_manager: E2EResourceManager,
    human_identities: Mapping[str, HumanIdentity],
) -> None:
    """RBAC-003/004: full queue read is intentional; cross-owner writes are not.

    The current Human UI policy explicitly permits ticket list/show/read for
    agents (`web/src/lib/authProvider.ts`) and limits mutations to unassigned or
    self-assigned records (`web/src/admin/tickets/ticketAccess.ts`). The
    repository's current full test report documents the same read-only
    behavior, so this test must not invent a 403 read contract.
    """

    supervisor = human_identities["supervisor"]
    assigned_agent = human_identities["agent_a"]
    observing_agent = human_identities["agent_b"]
    customer = human_identities["customer_a"]
    ticket = e2e_manager.create_ticket(customer, "agent-read-only-queue")
    ticket_id = ticket["id"]

    assigned = supervisor.api.post_ticket_command(
        ticket_id,
        "assign",
        {
            "assigned_to_id": assigned_agent.id,
            "comment": e2e_manager.unique("agent-read-policy-assignment"),
        },
    )
    assert assigned.status_code == 200, assigned.text

    detail = observing_agent.api.get_json(
        e2e_manager.project_path(f"tickets/{ticket_id}")
    )
    assert detail.status_code == 200, detail.text
    assert detail.json().get("data", {}).get("id") == ticket_id

    queue = observing_agent.api.get_json(
        e2e_manager.project_path("tickets"),
        params={"search": ticket["title"], "page_size": 100},
    )
    assert queue.status_code == 200, queue.text
    assert ticket_id in {
        item.get("id") for item in queue.json().get("data", {}).get("items", [])
    }

    update = observing_agent.api.put_ticket(
        ticket_id,
        {"priority": "urgent"},
    )
    assert_error_contract(
        update,
        403,
        machine_codes={"ticket_access_denied"},
    )
    transition = observing_agent.api.post_ticket_command(
        ticket_id,
        "status",
        {"status": "in_progress"},
    )
    assert_error_contract(
        transition,
        403,
        machine_codes={"ticket_access_denied"},
    )
    comment = observing_agent.api.post_json(
        e2e_manager.project_path(f"tickets/{ticket_id}/comments"),
        {
            "content": e2e_manager.unique("cross-owner-comment"),
            "type": "public",
        },
    )
    assert_error_contract(
        comment,
        403,
        machine_codes={"ticket_access_denied"},
    )


def test_customer_history_redacts_assignment_and_internal_changes(
    e2e_manager: E2EResourceManager,
    human_identities: Mapping[str, HumanIdentity],
) -> None:
    """TKT-027: Customer history must not bypass the ticket DTO redaction."""

    supervisor = human_identities["supervisor"]
    agent = human_identities["agent_a"]
    customer = human_identities["customer_a"]
    ticket = e2e_manager.create_ticket(customer, "customer-history-redaction")
    ticket_id = ticket["id"]

    assigned = supervisor.api.post_ticket_command(
        ticket_id,
        "assign",
        {
            "assigned_to_id": agent.id,
            "comment": e2e_manager.unique("history-assignment"),
        },
    )
    assert assigned.status_code == 200, assigned.text
    internal = agent.api.post_json(
        e2e_manager.project_path(f"tickets/{ticket_id}/comments"),
        {
            "content": e2e_manager.unique("history-internal-comment"),
            "type": "internal",
        },
        headers={"If-Match": agent.api.ticket_etag(ticket_id)},
    )
    assert internal.status_code == 201, internal.text
    internal_comment_id = internal.json().get("data", {}).get("id")
    assert isinstance(internal_comment_id, int) and internal_comment_id > 0

    privileged_history = supervisor.api.get_json(
        e2e_manager.project_path(f"tickets/{ticket_id}/history")
    )
    assert privileged_history.status_code == 200, privileged_history.text
    privileged_items = privileged_history.json().get("data", [])
    assert any(
        item.get("field_name") == "assigned_to_id" for item in privileged_items
    ), privileged_items
    assert internal_comment_id in {item.get("comment_id") for item in privileged_items}

    customer_history = customer.api.get_json(
        e2e_manager.project_path(f"tickets/{ticket_id}/history")
    )
    assert customer_history.status_code == 200, customer_history.text
    customer_items = customer_history.json().get("data", [])
    assert customer_items
    assert all("actor" not in item for item in customer_items)
    assert all(
        item.get("field_name") not in {"assigned_to_id", "internal_notes"}
        for item in customer_items
    )
    assert internal_comment_id not in {
        item.get("comment_id") for item in customer_items
    }


@pytest.mark.parametrize(
    "blank_field",
    ("title", "description"),
    ids=("blank-title", "blank-description"),
)
def test_blank_ticket_text_is_chinese_400_without_persisted_row(
    blank_field: str,
    e2e_manager: E2EResourceManager,
    human_identities: Mapping[str, HumanIdentity],
) -> None:
    """TKT-002: whitespace-only required fields are invalid, not empty records."""

    admin = human_identities["admin"]
    marker = e2e_manager.unique(f"{blank_field}-must-not-persist")
    payload = e2e_manager.ticket_create_payload(
        {
            "title": marker,
            "description": marker,
            "type": "request",
            "priority": "normal",
            "source": "api",
        }
    )
    payload[blank_field] = " \t\n "
    rejected = admin.api.post_json(e2e_manager.project_path("tickets"), payload)

    lookup = admin.api.get_json(
        e2e_manager.project_path("tickets"),
        params={"search": marker, "page_size": 100},
    )
    assert lookup.status_code == 200, lookup.text
    matching = [
        item
        for item in lookup.json().get("data", {}).get("items", [])
        if marker in str(item.get("title", ""))
        or marker in str(item.get("description", ""))
    ]
    for item in matching:
        ticket_id = item.get("id")
        assert isinstance(ticket_id, int) and ticket_id > 0, item
        cleanup = admin.api.delete_ticket(ticket_id)
        assert cleanup.status_code in (200, 204), cleanup.text

    assert rejected.status_code == 400 and not matching, (
        f"{blank_field} 纯空白请求 status={rejected.status_code}, "
        f"persisted_ids={[item.get('id') for item in matching]}, "
        f"body={rejected.text}"
    )
    assert_error_contract(rejected, 400)


@pytest.mark.parametrize(
    ("case_name", "payload"),
    (
        ("empty", {"content": "", "type": "public"}),
        ("blank", {"content": " \t\n ", "type": "public"}),
        ("too-long", {"content": "评" * 10001, "type": "public"}),
        (
            "content-type",
            {"content": "合法正文", "content_type": "text/html", "type": "public"},
        ),
        ("comment-type", {"content": "合法正文", "type": "private"}),
    ),
    ids=("empty", "blank", "too-long", "invalid-content-type", "invalid-type"),
)
def test_invalid_comment_errors_are_chinese_without_validator_details(
    case_name: str,
    payload: dict[str, Any],
    e2e_manager: E2EResourceManager,
    human_identities: Mapping[str, HumanIdentity],
) -> None:
    """CNT-004: malformed comments expose a Chinese public error only."""

    admin = human_identities["admin"]
    ticket = e2e_manager.create_ticket(admin, f"invalid-comment-{case_name}")
    rejected = admin.api.post_json(
        e2e_manager.project_path(f"tickets/{ticket['id']}/comments"),
        payload,
        headers={"If-Match": admin.api.ticket_etag(ticket["id"])},
    )

    comments = admin.api.get_json(
        e2e_manager.project_path(f"tickets/{ticket['id']}/comments")
    )
    assert comments.status_code == 200, comments.text
    assert comments.json().get("data", []) == [], comments.text

    body = assert_error_contract(
        rejected,
        400,
        machine_codes={"invalid_request", "validation_error"},
    )
    public_text = json.dumps(body, ensure_ascii=False).lower()
    for internal_fragment in (
        "key: '",
        "field validation",
        "failed on the",
        "comment content must",
        "native comments support",
        "invalid comment type",
    ):
        assert internal_fragment not in public_text, (
            f"{case_name} 泄露校验实现细节 {internal_fragment!r}: {body}"
        )


def test_human_ticket_detail_returns_strong_etag(
    e2e_manager: E2EResourceManager,
    human_identities: Mapping[str, HumanIdentity],
) -> None:
    """TKT-005: Human detail exposes the version as a strong ETag."""

    admin = human_identities["admin"]
    ticket = e2e_manager.create_ticket(admin, "human-get-etag")
    response = admin.api.get_json(e2e_manager.project_path(f"tickets/{ticket['id']}"))

    assert response.status_code == 200, response.text
    version = response.json().get("data", {}).get("version")
    assert version == 1, response.text
    assert response.headers.get("ETag") == '"v1"', response.headers


@pytest.mark.parametrize(
    ("case_name", "etag", "expected_status"),
    (
        ("missing", None, 428),
        ("malformed", "not-a-strong-etag", 409),
        ("stale", '"v0"', 409),
        ("current", '"v1"', 200),
    ),
    ids=("missing", "malformed", "stale", "current"),
)
def test_human_ticket_put_enforces_if_match(
    case_name: str,
    etag: str | None,
    expected_status: int,
    e2e_manager: E2EResourceManager,
    human_identities: Mapping[str, HumanIdentity],
) -> None:
    """TKT-007/008: PUT requires the current strong resource validator."""

    admin = human_identities["admin"]
    ticket = e2e_manager.create_ticket(admin, f"human-put-if-match-{case_name}")
    headers = {} if etag is None else {"If-Match": etag}
    response = admin.api.put_json(
        e2e_manager.project_path(f"tickets/{ticket['id']}"),
        {"priority": "high"},
        headers=headers,
    )

    if expected_status == 200:
        assert response.status_code == 200, response.text
        assert response.json().get("data", {}).get("version") == 2, response.text
        assert response.headers.get("ETag") == '"v2"', response.headers
        return
    assert_error_contract(
        response,
        expected_status,
        machine_codes={
            "precondition_required",
            "version_conflict",
        },
    )


@pytest.mark.parametrize(
    ("case_name", "etag", "expected_status"),
    (
        ("missing", None, 428),
        ("malformed", "not-a-strong-etag", 409),
        ("stale", '"v0"', 409),
        ("current", '"v1"', 200),
    ),
    ids=("missing", "malformed", "stale", "current"),
)
def test_human_ticket_workflow_enforces_if_match(
    case_name: str,
    etag: str | None,
    expected_status: int,
    e2e_manager: E2EResourceManager,
    human_identities: Mapping[str, HumanIdentity],
) -> None:
    """TKT-007/008: workflow commands use the same validator contract."""

    admin = human_identities["admin"]
    ticket = e2e_manager.create_ticket(
        admin,
        f"human-workflow-if-match-{case_name}",
    )
    headers = {} if etag is None else {"If-Match": etag}
    response = admin.api.post_json(
        e2e_manager.project_path(f"tickets/{ticket['id']}/status"),
        {"status": "in_progress"},
        headers=headers,
    )

    if expected_status == 200:
        assert response.status_code == 200, response.text
        assert response.json().get("data", {}).get("version") == 2, response.text
        assert response.headers.get("ETag") == '"v2"', response.headers
        return
    assert_error_contract(
        response,
        expected_status,
        machine_codes={
            "precondition_required",
            "version_conflict",
        },
    )
