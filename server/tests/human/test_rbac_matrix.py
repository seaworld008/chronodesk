"""Human role and ticket object-authorization matrix."""

from __future__ import annotations

import time
from collections.abc import Mapping
from urllib.parse import parse_qs

import pytest

from tests.utils import (
    HUMAN_ROLES,
    APIClient,
    E2EResourceManager,
    HumanIdentity,
    assert_error_contract,
    strong_password,
)

pytestmark = [pytest.mark.api, pytest.mark.integration]


def test_human_role_change_audit_identifies_actor_target_result_and_redacts_query(
    e2e_manager: E2EResourceManager,
    human_identities: Mapping[str, HumanIdentity],
) -> None:
    """RBAC-012: Human admin audit is attributable without Agent-only fields."""

    admin = human_identities["admin"]
    target = e2e_manager.create_user("agent", "role-audit-target")
    target_path = f"/api/admin/users/{target.id}"
    updated = admin.api.put_json(
        (
            f"/admin/users/{target.id}"
            "?password=must-not-persist&token=must-not-persist&visible=evidence"
        ),
        {"role": "supervisor"},
    )
    assert updated.status_code == 200, updated.text
    assert updated.json().get("data", {}).get("role") == "supervisor"

    audit_item = None
    for _ in range(10):
        response = admin.api.get_json(
            "/admin/audit-logs",
            params={
                "method": "PUT",
                "path": target_path,
                "status": 200,
                "page": 1,
                "limit": 20,
            },
        )
        assert response.status_code == 200, response.text
        items = response.json().get("data", {}).get("items", [])
        audit_item = next(
            (
                item
                for item in items
                if item.get("path") == target_path
                and item.get("method") == "PUT"
                and item.get("status_code") == 200
            ),
            None,
        )
        if audit_item is not None:
            break
        time.sleep(0.1)

    assert isinstance(audit_item, dict), "未找到 E2E 用户角色变更审计记录"
    assert audit_item.get("user_id") == admin.id
    assert audit_item.get("username") == admin.username
    assert audit_item.get("role") == "admin"
    assert audit_item.get("path") == target_path
    assert str(target.id) in audit_item.get("action", "")
    assert audit_item.get("method") == "PUT"
    assert audit_item.get("status_code") == 200
    assert audit_item.get("result") == "success"
    assert isinstance(audit_item.get("created_at"), str) and audit_item["created_at"]
    assert isinstance(audit_item.get("client_ip"), str) and audit_item["client_ip"]
    assert isinstance(audit_item.get("user_agent"), str) and audit_item["user_agent"]

    query = parse_qs(audit_item.get("query", ""), keep_blank_values=True)
    assert query.get("password") == ["[REDACTED]"]
    assert query.get("token") == ["[REDACTED]"]
    assert query.get("visible") == ["evidence"]
    assert "must-not-persist" not in audit_item.get("query", "")


def test_four_human_roles_and_admin_only_surfaces(
    admin_api: APIClient,
    e2e_manager: E2EResourceManager,
    human_identities: Mapping[str, HumanIdentity],
) -> None:
    """RBAC-008/RBAC-010: only the closed four-role enum reaches admin APIs."""

    observed_roles = {
        human_identities["admin"].role,
        human_identities["supervisor"].role,
        human_identities["agent_a"].role,
        human_identities["customer_a"].role,
    }
    assert observed_roles == set(HUMAN_ROLES)

    invalid_username = f"e2e_{e2e_manager.run_id.replace('-', '')}_invalid_role"[:50]
    invalid_create = admin_api.post_json(
        "/admin/users",
        {
            "username": invalid_username,
            "email": (
                f"e2e+{e2e_manager.run_id.replace('-', '')}.invalid-role@example.com"
            ),
            "password": "Aa1!invalid-roleZ",
            "role": "operator",
        },
    )
    if invalid_create.status_code == 201:
        unexpected_user = invalid_create.json().get("data", {})
        unexpected_id = unexpected_user.get("id")
        assert isinstance(unexpected_id, int) and unexpected_id > 0, unexpected_user
        e2e_manager.track_user(unexpected_id, invalid_username)
        cleanup = admin_api.delete(f"/admin/users/{unexpected_id}")
        assert cleanup.status_code in (200, 204), cleanup.text
    assert_error_contract(invalid_create, 400)

    invalid_update = admin_api.put_json(
        f"/admin/users/{human_identities['agent_a'].id}",
        {"role": "superuser"},
    )
    if invalid_update.status_code == 200:
        restored = admin_api.put_json(
            f"/admin/users/{human_identities['agent_a'].id}",
            {"role": "agent"},
        )
        assert restored.status_code == 200, restored.text
    assert_error_contract(invalid_update, 400)
    unchanged = admin_api.get_json(f"/admin/users/{human_identities['agent_a'].id}")
    assert unchanged.status_code == 200, unchanged.text
    assert unchanged.json().get("data", {}).get("role") == "agent"

    admin = human_identities["admin"].api
    for path in (
        "/admin/users?page=1&page_size=1",
        "/admin/configs?page=1",
        admin.project_path("admin/agents/agent-control/overview"),
    ):
        allowed = admin.get_json(path)
        assert allowed.status_code == 200, f"{path}: {allowed.text}"

    for identity_name in ("supervisor", "agent_a", "customer_a"):
        identity = human_identities[identity_name]
        for path in (
            "/admin/users?page=1&page_size=1",
            "/admin/configs?page=1",
            identity.api.project_path("admin/agents/agent-control/overview"),
        ):
            denied = identity.api.get_json(path)
            assert_error_contract(
                denied,
                403,
                machine_codes={"access_denied", "insufficient_permissions"},
            )


def test_soft_deleted_human_identity_returns_stable_conflict(
    admin_api: APIClient,
    e2e_manager: E2EResourceManager,
) -> None:
    """RBAC-010: audit-retained usernames/emails remain unique and return 409."""

    retained = e2e_manager.create_user("agent", "retained-identity")
    deleted = admin_api.delete(f"/admin/users/{retained.id}")
    assert deleted.status_code == 200, deleted.text

    replacement_username = f"{retained.username[:36]}_replacement"
    response = admin_api.post_json(
        "/admin/users",
        {
            "username": replacement_username,
            "email": retained.email,
            "password": strong_password(),
            "role": "agent",
            "first_name": "E2E",
            "last_name": "Replacement",
            "department": "QA Automation",
            "job_title": "Human REST Black-box",
        },
    )
    body = assert_error_contract(response, 409)
    assert "用户名或邮箱已被使用" in body.get("msg", "")
    assert "SQLSTATE" not in response.text
    assert "unique constraint" not in response.text


def test_ticket_object_permission_matrix(
    e2e_manager: E2EResourceManager,
    human_identities: Mapping[str, HumanIdentity],
) -> None:
    """RBAC-001..RBAC-007: enforce Human ticket visibility and write ownership."""

    admin = human_identities["admin"]
    supervisor = human_identities["supervisor"]
    agent_a = human_identities["agent_a"]
    agent_b = human_identities["agent_b"]
    customer_a = human_identities["customer_a"]
    customer_b = human_identities["customer_b"]

    ticket_a = e2e_manager.create_ticket(customer_a, "rbac-customer-a")
    ticket_b = e2e_manager.create_ticket(customer_b, "rbac-customer-b")
    ticket_a_id = ticket_a["id"]
    ticket_b_id = ticket_b["id"]

    admin_read = admin.api.get_json(e2e_manager.project_path(f"tickets/{ticket_a_id}"))
    assert admin_read.status_code == 200, admin_read.text
    admin_update = admin.api.put_ticket(
        ticket_a_id,
        {"internal_notes": e2e_manager.unique("admin-internal-note")},
    )
    assert admin_update.status_code == 200, admin_update.text

    supervisor_read = supervisor.api.get_json(
        e2e_manager.project_path(f"tickets/{ticket_a_id}")
    )
    assert supervisor_read.status_code == 200, supervisor_read.text
    assigned = supervisor.api.post_ticket_command(
        ticket_a_id,
        "assign",
        {
            "assigned_to_id": agent_a.id,
            "comment": e2e_manager.unique("supervisor-assignment"),
        },
    )
    assert assigned.status_code == 200, assigned.text

    own_agent_update = agent_a.api.put_ticket(
        ticket_a_id,
        {"priority": "high"},
    )
    assert own_agent_update.status_code == 200, own_agent_update.text

    other_agent_update = agent_b.api.put_ticket(
        ticket_a_id,
        {"priority": "urgent"},
    )
    assert_error_contract(
        other_agent_update,
        403,
        machine_codes={"ticket_access_denied"},
    )

    customer_own = customer_a.api.get_json(
        e2e_manager.project_path(f"tickets/{ticket_a_id}")
    )
    assert customer_own.status_code == 200, customer_own.text
    customer_ticket = customer_own.json().get("data", {})
    assert customer_ticket.get("id") == ticket_a_id
    assert customer_ticket.get("assigned_to_id") is None
    assert customer_ticket.get("assigned_to") is None
    assert not customer_ticket.get("attachments")
    assert not customer_ticket.get("agent_context")

    customer_cross = customer_a.api.get_json(
        e2e_manager.project_path(f"tickets/{ticket_b_id}")
    )
    assert_error_contract(
        customer_cross,
        403,
        machine_codes={"ticket_access_denied"},
    )
    assert str(ticket_b_id) not in customer_cross.text

    customer_workflow = customer_a.api.post_ticket_command(
        ticket_a_id,
        "status",
        {"status": "in_progress"},
    )
    assert_error_contract(
        customer_workflow,
        403,
        machine_codes={"ticket_access_denied"},
    )
    customer_history = customer_a.api.get_json(
        e2e_manager.project_path(f"tickets/{ticket_a_id}/history")
    )
    assert customer_history.status_code == 200, customer_history.text
    customer_history_items = customer_history.json().get("data", [])
    assert customer_history_items
    for item in customer_history_items:
        assert item.get("is_visible") is True
        assert "actor" not in item

    cross_history = customer_a.api.get_json(
        e2e_manager.project_path(f"tickets/{ticket_b_id}/history")
    )
    assert_error_contract(
        cross_history,
        403,
        machine_codes={"ticket_access_denied"},
    )

    customer_list = customer_a.api.get_json(
        e2e_manager.project_path("tickets"),
        params={"page_size": 100, "created_by": customer_b.id},
    )
    assert customer_list.status_code == 200, customer_list.text
    listed = customer_list.json().get("data", {}).get("items", [])
    assert ticket_a_id in {item.get("id") for item in listed}
    assert ticket_b_id not in {item.get("id") for item in listed}

    own_agent_read = agent_a.api.get_json(
        e2e_manager.project_path(f"tickets/{ticket_a_id}")
    )
    assert own_agent_read.status_code == 200, own_agent_read.text
    unassigned = agent_a.api.get_json(
        e2e_manager.project_path("tickets/unassigned"),
        params={"limit": 100},
    )
    assert unassigned.status_code == 200, unassigned.text
    assert ticket_b_id in {item.get("id") for item in unassigned.json().get("data", [])}
