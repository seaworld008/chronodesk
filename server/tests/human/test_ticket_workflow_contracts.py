"""Release-gate contracts for Human ticket workflow edge cases."""

from __future__ import annotations

import json
import time
from collections.abc import Mapping
from typing import Any

import pytest

from tests.utils import E2EResourceManager, HumanIdentity, assert_error_contract

pytestmark = [pytest.mark.api, pytest.mark.integration]


def test_ticket_list_combines_status_priority_type_source_assignee_and_search(
    e2e_manager: E2EResourceManager,
    human_identities: Mapping[str, HumanIdentity],
) -> None:
    """TKT-021: direct and JSON source filters compose with other dimensions."""

    admin = human_identities["admin"]
    assignee = human_identities["agent_a"]
    marker = e2e_manager.unique("combined-filter")
    matching = e2e_manager.create_ticket(
        admin,
        "filter-match",
        title=f"{marker}-api-high",
        priority="high",
        type="incident",
        source="api",
    )
    wrong_source = e2e_manager.create_ticket(
        admin,
        "filter-wrong-source",
        title=f"{marker}-web-high",
        priority="high",
        type="incident",
        source="web",
    )
    wrong_priority = e2e_manager.create_ticket(
        admin,
        "filter-wrong-priority",
        title=f"{marker}-api-normal",
        priority="normal",
        type="incident",
        source="api",
    )
    for ticket in (matching, wrong_source, wrong_priority):
        assigned = admin.api.post_ticket_command(
            ticket["id"],
            "assign",
            {
                "assigned_to_id": assignee.id,
                "comment": e2e_manager.unique("filter-assignment"),
            },
        )
        assert assigned.status_code == 200, assigned.text

    common = {
        "status": "open",
        "priority": "high",
        "type": "incident",
        "source": "api",
        "assigned_to": assignee.id,
        "search": marker,
        "page_size": 100,
    }
    direct = admin.api.get_json(
        e2e_manager.project_path("tickets"),
        params=common,
    )
    assert direct.status_code == 200, direct.text
    direct_ids = {
        item.get("id") for item in direct.json().get("data", {}).get("items", [])
    }
    assert direct_ids == {matching["id"]}

    json_filter = admin.api.get_json(
        e2e_manager.project_path("tickets"),
        params={
            "filter": json.dumps(
                {
                    "q": marker,
                    "status": ["open"],
                    "priority": ["high"],
                    "type": "incident",
                    "source": "api",
                },
                ensure_ascii=False,
            ),
            "assigned_to": assignee.id,
            "page_size": 100,
        },
    )
    assert json_filter.status_code == 200, json_filter.text
    json_ids = {
        item.get("id") for item in json_filter.json().get("data", {}).get("items", [])
    }
    assert json_ids == {matching["id"]}

    excluded = admin.api.get_json(
        e2e_manager.project_path("tickets"),
        params={**common, "source": "email"},
    )
    assert excluded.status_code == 200, excluded.text
    assert excluded.json().get("data", {}).get("items", []) == []


def _ticket_notifications(
    identity: HumanIdentity,
    ticket_id: int,
) -> list[dict[str, Any]]:
    response = identity.api.get_json(
        identity.api.project_path("notifications"),
        params={
            "page_size": 100,
            "filter": f'{{"related_ticket_id":{ticket_id}}}',
        },
    )
    assert response.status_code == 200, response.text
    items = response.json().get("data", {}).get("items", [])
    assert isinstance(items, list)
    return [
        item
        for item in items
        if (item.get("related_ticket") or {}).get("id") == ticket_id
        or item.get("related_ticket_id") == ticket_id
    ]


def _wait_for_new_assignment_notification(
    identity: HumanIdentity,
    ticket_id: int,
    existing_ids: set[int],
) -> dict[str, Any]:
    for _ in range(20):
        matching = [
            item
            for item in _ticket_notifications(identity, ticket_id)
            if item.get("id") not in existing_ids
            and item.get("type") == "ticket_assigned"
        ]
        if matching:
            return matching[0]
        time.sleep(0.5)
    pytest.fail(f"工单 {ticket_id} 转移后未向新处理人投递 ticket_assigned 通知")


def test_invalid_transition_is_a_versioned_conflict_with_allowed_next_states(
    e2e_manager: E2EResourceManager,
    human_identities: Mapping[str, HumanIdentity],
) -> None:
    """TKT-014: illegal transitions are explicit conflicts without mutation."""

    admin = human_identities["admin"]
    ticket = e2e_manager.create_ticket(admin, "invalid-transition")
    rejected = admin.api.post_ticket_command(
        ticket["id"],
        "status",
        {"status": "closed", "comment": e2e_manager.unique("illegal-close")},
    )
    assert_error_contract(
        rejected,
        409,
        machine_codes={"invalid_status_transition"},
    )
    body = rejected.json()
    details = body.get("details")
    assert isinstance(details, dict), body
    allowed = details.get("allowed_next_statuses")
    # The published bootstrap workflow requires work to start before it can
    # wait or resolve.  This assertion deliberately follows that immutable
    # workflow version instead of the removed global lifecycle projection.
    assert set(allowed or []) == {"in_progress", "cancelled"}

    unchanged = admin.api.get_json(e2e_manager.project_path(f"tickets/{ticket['id']}"))
    assert unchanged.status_code == 200, unchanged.text
    unchanged_ticket = unchanged.json().get("data", {})
    assert unchanged_ticket.get("status") == "open"
    assert unchanged_ticket.get("version") == ticket["version"]
    assert unchanged.headers.get("ETag") == f'"v{ticket["version"]}"'


def test_transfer_updates_assignee_history_event_and_recipient_notification(
    e2e_manager: E2EResourceManager,
    human_identities: Mapping[str, HumanIdentity],
) -> None:
    """TKT-017: transfer preserves reason, event linkage and recipient delivery."""

    supervisor = human_identities["supervisor"]
    first_agent = human_identities["agent_a"]
    second_agent = human_identities["agent_b"]
    customer = human_identities["customer_a"]
    ticket = e2e_manager.create_ticket(customer, "transfer")

    assigned = supervisor.api.post_ticket_command(
        ticket["id"],
        "assign",
        {
            "assigned_to_id": first_agent.id,
            "comment": e2e_manager.unique("initial-assignment"),
        },
    )
    assert assigned.status_code == 200, assigned.text
    existing_ids = {
        item["id"]
        for item in _ticket_notifications(second_agent, ticket["id"])
        if isinstance(item.get("id"), int)
    }

    reason = e2e_manager.unique("capacity-rebalance")
    comment = e2e_manager.unique("transfer-comment")
    transferred = supervisor.api.post_ticket_command(
        ticket["id"],
        "transfer",
        {
            "assigned_to_id": second_agent.id,
            "department": "QA Automation",
            "transfer_reason": reason,
            "comment": comment,
        },
    )
    assert transferred.status_code == 200, transferred.text
    transfer_data = transferred.json().get("data", {})
    assert transfer_data.get("assigned_to_id") == second_agent.id
    assert transfer_data.get("assigned_to", {}).get("id") == second_agent.id
    assert (
        transfer_data.get("version")
        == assigned.json().get("data", {}).get("version") + 1
    )
    assert transferred.headers.get("ETag") == f'"v{transfer_data["version"]}"'

    history_response = supervisor.api.get_json(
        e2e_manager.project_path(f"tickets/{ticket['id']}/history")
    )
    assert history_response.status_code == 200, history_response.text
    transfer_history = [
        item
        for item in history_response.json().get("data", [])
        if item.get("action") == "transfer"
    ]
    assert len(transfer_history) == 1, transfer_history
    history = transfer_history[0]
    assert reason in history.get("description", "")
    assert comment in history.get("description", "")
    assert history.get("new_value") == str(second_agent.id)
    assert isinstance(history.get("event_id"), str) and history["event_id"]
    assert history.get("resource_version") == transfer_data["version"]

    notification = _wait_for_new_assignment_notification(
        second_agent,
        ticket["id"],
        existing_ids,
    )
    assert notification.get("recipient", {}).get("id") == second_agent.id


def test_bulk_delete_reports_authorization_and_missing_items_without_silence(
    e2e_manager: E2EResourceManager,
    human_identities: Mapping[str, HumanIdentity],
) -> None:
    """TKT-019: bulk delete has an explicit all-denied and mixed-result contract."""

    admin = human_identities["admin"]
    customer = human_identities["customer_a"]
    owned = e2e_manager.create_ticket(customer, "bulk-delete-owned")
    other = e2e_manager.create_ticket(admin, "bulk-delete-other")

    removed_legacy_shape = admin.api.request(
        "DELETE",
        e2e_manager.project_path("tickets/bulk-delete"),
        json={"ids": [other["id"]]},
    )
    assert_error_contract(
        removed_legacy_shape,
        400,
        machine_codes={"invalid_request"},
    )
    still_present = admin.api.get_json(
        e2e_manager.project_path(f"tickets/{other['id']}")
    )
    assert still_present.status_code == 200, still_present.text

    denied = customer.api.request(
        "DELETE",
        e2e_manager.project_path("tickets/bulk-delete"),
        json={
            "tickets": [
                {"id": owned["id"], "version": owned["version"]},
                {"id": other["id"], "version": other["version"]},
            ]
        },
    )
    assert_error_contract(denied, 403)

    missing_id = 4_294_967_295
    mixed = admin.api.request(
        "DELETE",
        e2e_manager.project_path("tickets/bulk-delete"),
        json={
            "tickets": [
                {"id": other["id"], "version": other["version"]},
                {"id": missing_id, "version": 1},
            ]
        },
    )
    assert mixed.status_code == 200, mixed.text
    body = mixed.json()
    assert body.get("code") == 0, body
    assert body.get("msg") == "部分工单删除失败"
    result = body.get("data")
    assert isinstance(result, dict), body
    assert result.get("deleted_ids") == [other["id"]]
    assert result.get("deleted_tickets") == [
        {"id": other["id"], "version": other["version"] + 1}
    ]
    assert result.get("failed_ids") == [missing_id]
    reasons = result.get("failed_reasons")
    assert isinstance(reasons, dict)
    assert str(missing_id) in reasons and reasons[str(missing_id)]
