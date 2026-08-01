"""Human role and ticket object-authorization matrix."""

from __future__ import annotations

import time
from collections.abc import Mapping
from urllib.parse import parse_qs
from uuid import UUID

import pytest

from tests.utils import (
    PLATFORM_ROLES,
    PROJECT_ROLES,
    APIClient,
    E2EResourceManager,
    HumanIdentity,
    assert_error_contract,
    strong_password,
)

pytestmark = [pytest.mark.api, pytest.mark.integration]


def _assert_strict_page_envelope(
    payload: object,
    *,
    expected_page: int = 1,
    expected_page_size: int = 25,
) -> list[dict[str, object]]:
    assert isinstance(payload, dict), payload
    assert set(payload) == {"code", "msg", "data"}, payload
    assert payload.get("code") == 0, payload
    assert isinstance(payload.get("msg"), str) and payload["msg"], payload

    page = payload.get("data")
    assert isinstance(page, dict), payload
    assert set(page) == {
        "items",
        "total",
        "page",
        "page_size",
        "total_pages",
    }, payload
    assert page.get("page") == expected_page, payload
    assert page.get("page_size") == expected_page_size, payload
    total = page.get("total")
    total_pages = page.get("total_pages")
    items = page.get("items")
    assert isinstance(total, int) and total >= 0, payload
    assert isinstance(total_pages, int) and total_pages >= 0, payload
    assert total_pages == ((total + expected_page_size - 1) // expected_page_size), (
        payload
    )
    assert isinstance(items, list) and len(items) <= expected_page_size, payload
    assert all(isinstance(item, dict) for item in items), payload
    return items


def test_platform_role_change_audit_identifies_actor_target_result_and_redacts_query(
    e2e_manager: E2EResourceManager,
    platform_admin_identity: HumanIdentity,
) -> None:
    """RBAC-012: platform-role governance has attributable, redacted audit."""

    admin = platform_admin_identity
    target = e2e_manager.create_platform_identity(
        "member",
        label="platform-role-audit-target",
    )
    target_path = f"/api/platform/users/{target.id}"
    updated = admin.api.put_json(
        (
            f"/platform/users/{target.id}"
            "?password=must-not-persist&token=must-not-persist&visible=evidence"
        ),
        {"platform_role": "security_auditor"},
    )
    assert updated.status_code == 200, updated.text
    updated_user = updated.json().get("data", {})
    assert updated_user.get("platform_role") == "security_auditor"
    assert "role" not in updated_user

    audit_item = None
    for _ in range(10):
        response = admin.api.get_json(
            "/platform/audit-logs",
            params={
                "method": "PUT",
                "path_prefix": target_path,
                "status": 200,
                "limit": 20,
            },
        )
        assert response.status_code == 200, response.text
        body = response.json()
        assert set(body) == {"code", "msg", "data"}, body
        assert body.get("code") == 0, body
        cursor_page = body.get("data")
        assert isinstance(cursor_page, dict), body
        assert set(cursor_page) == {"items", "next_cursor", "has_more"}, body
        items = cursor_page.get("items")
        assert isinstance(items, list) and len(items) <= 20, body
        assert isinstance(cursor_page.get("next_cursor"), str), body
        assert isinstance(cursor_page.get("has_more"), bool), body
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
    assert audit_item.get("platform_role") == "platform_admin"
    assert "role" not in audit_item
    assert audit_item.get("path") == target_path
    assert audit_item.get("action") == "platform.user.update"
    assert audit_item.get("action_code") == "platform.user.update"
    assert audit_item.get("resource_type") == "user"
    assert audit_item.get("resource_public_id") == str(target.id)
    assert audit_item.get("method") == "PUT"
    assert audit_item.get("status_code") == 200
    assert audit_item.get("result") == "success"
    assert isinstance(audit_item.get("created_at"), str) and audit_item["created_at"]
    assert isinstance(audit_item.get("masked_ip"), str) and audit_item["masked_ip"]
    assert isinstance(audit_item.get("latency_ms"), int)
    assert {"client_ip", "query", "user_agent", "notes"}.isdisjoint(audit_item)

    detail_response = admin.api.get_json(f"/platform/audit-logs/{audit_item['id']}")
    assert detail_response.status_code == 200, detail_response.text
    detail_body = detail_response.json()
    assert set(detail_body) == {"code", "msg", "data"}, detail_body
    assert detail_body.get("code") == 0, detail_body
    detail = detail_body.get("data")
    assert isinstance(detail, dict), detail_body
    assert detail.get("id") == audit_item["id"], detail
    assert detail.get("masked_ip") == audit_item["masked_ip"], detail
    assert "client_ip" not in detail
    assert isinstance(detail.get("user_agent"), str) and detail["user_agent"]
    assert isinstance(detail.get("notes"), str), detail

    query = parse_qs(detail.get("query", ""), keep_blank_values=True)
    assert query.get("password") == ["[已隐藏]"]
    assert query.get("token") == ["[已隐藏]"]
    assert query.get("visible") == ["evidence"]
    assert "must-not-persist" not in detail.get("query", "")


def test_platform_permission_matrix_never_grants_implicit_project_access(
    platform_identities: Mapping[str, HumanIdentity],
) -> None:
    """Four platform duties authorize only their explicitly declared surfaces."""

    assert set(platform_identities) == set(PLATFORM_ROLES)
    platform_expectations = {
        "platform_admin": {
            "/platform/users?page=1&page_size=1": 200,
            "/platform/configs?page=1": 200,
            "/platform/audit-logs?limit=1": 200,
        },
        "security_auditor": {
            "/platform/users?page=1&page_size=1": 403,
            "/platform/configs?page=1": 403,
            "/platform/audit-logs?limit=1": 200,
        },
        "emergency_operator": {
            "/platform/users?page=1&page_size=1": 403,
            "/platform/configs?page=1": 403,
            "/platform/audit-logs?limit=1": 403,
        },
        "member": {
            "/platform/users?page=1&page_size=1": 403,
            "/platform/configs?page=1": 403,
            "/platform/audit-logs?limit=1": 403,
        },
    }

    for platform_role, identity in platform_identities.items():
        assert identity.platform_role == platform_role
        assert identity.project_role is None

        projects = identity.api.get_json("/projects")
        assert projects.status_code == 200, projects.text
        assert _assert_strict_page_envelope(projects.json()) == [], projects.text

        context = identity.api.get_json(identity.api.project_path("context"))
        assert_error_contract(
            context,
            403,
            machine_codes={"project_access_revoked"},
        )
        tickets = identity.api.get_json(
            identity.api.project_path("tickets"),
            params={"page": 1, "page_size": 1},
        )
        assert_error_contract(
            tickets,
            403,
            machine_codes={"project_access_revoked"},
        )

        for path, expected_status in platform_expectations[platform_role].items():
            response = identity.api.get_json(path)
            if expected_status == 200:
                assert response.status_code == 200, f"{path}: {response.text}"
            else:
                assert_error_contract(
                    response,
                    expected_status,
                    machine_codes={
                        "access_denied",
                        "insufficient_permissions",
                    },
                )


def test_platform_project_inventory_requires_active_platform_admin_not_membership(
    e2e_manager: E2EResourceManager,
    platform_identities: Mapping[str, HumanIdentity],
) -> None:
    """A zero-Membership platform admin sees only the closed governance DTO."""

    administrator = platform_identities["platform_admin"]
    assert administrator.project_role is None

    membership_projects = administrator.api.get_json("/projects")
    assert membership_projects.status_code == 200, membership_projects.text
    assert _assert_strict_page_envelope(membership_projects.json()) == [], (
        membership_projects.text
    )

    response = administrator.api.get_json("/platform/projects")
    assert response.status_code == 200, response.text
    payload = response.json()
    projects = _assert_strict_page_envelope(payload)
    assert projects, payload

    expected_fields = {
        "public_id",
        "created_at",
        "updated_at",
        "key",
        "name",
        "description",
        "status",
        "business_unit",
    }
    for project in projects:
        assert isinstance(project, dict), project
        assert set(project) == expected_fields, project
        public_id = project.get("public_id")
        assert isinstance(public_id, str) and public_id
        parsed_public_id = UUID(public_id)
        assert parsed_public_id.version == 7, project
        assert str(parsed_public_id) == public_id, project
        assert isinstance(project.get("key"), str) and project["key"], project
        assert isinstance(project.get("name"), str), project
        assert isinstance(project.get("description"), str), project
        assert project.get("status") in {"active", "archived"}, project
        business_unit = project.get("business_unit")
        assert isinstance(business_unit, dict), project
        assert set(business_unit) == {
            "public_id",
            "key",
            "name",
            "description",
        }, project
        business_unit_public_id = business_unit.get("public_id")
        assert isinstance(business_unit_public_id, str), project
        assert UUID(business_unit_public_id).version == 7, project

    assert any(project.get("key") == e2e_manager.project_key for project in projects), (
        projects
    )

    legacy_role = administrator.api.get_json(
        "/platform/projects",
        params={"role": "admin"},
    )
    assert_error_contract(legacy_role, 400)

    for platform_role in ("security_auditor", "emergency_operator", "member"):
        identity = platform_identities[platform_role]
        direct = identity.api.get_json("/platform/projects")
        assert_error_contract(
            direct,
            403,
            machine_codes={"access_denied", "insufficient_permissions"},
        )
        smuggled = identity.api.get_json(
            "/platform/projects",
            params={"role": "admin"},
        )
        assert_error_contract(
            smuggled,
            403,
            machine_codes={"access_denied", "insufficient_permissions"},
        )


def test_project_permission_matrix_uses_only_active_membership(
    human_identities: Mapping[str, HumanIdentity],
) -> None:
    """All project personas are platform members with one explicit role."""

    expected_roles = {
        "admin": "project_admin",
        "supervisor": "manager",
        "agent_a": "agent",
        "agent_b": "agent",
        "customer_a": "requester",
        "customer_b": "requester",
        "observer": "observer",
    }
    assert set(expected_roles.values()) == set(PROJECT_ROLES)

    for name, expected_project_role in expected_roles.items():
        identity = human_identities[name]
        assert identity.platform_role == "member"
        assert identity.project_role == expected_project_role

        projects = identity.api.get_json("/projects")
        assert projects.status_code == 200, projects.text
        accesses = _assert_strict_page_envelope(projects.json())
        assert len(accesses) == 1, projects.text
        assert accesses[0].get("project_role") == expected_project_role
        assert "role" not in accesses[0]

        context = identity.api.get_json(identity.api.project_path("context"))
        assert context.status_code == 200, context.text
        project_access = context.json().get("data", {})
        assert project_access.get("project_role") == expected_project_role
        assert "role" not in project_access

        platform_users = identity.api.get_json(
            "/platform/users",
            params={"page": 1, "page_size": 1},
        )
        assert_error_contract(
            platform_users,
            403,
            machine_codes={"insufficient_permissions"},
        )

        memberships = identity.api.get_json(
            identity.api.project_path("memberships"),
        )
        if expected_project_role in {"project_admin", "manager"}:
            assert memberships.status_code == 200, memberships.text
        else:
            assert_error_contract(memberships, 403, machine_codes={"403"})

        agent_control = identity.api.get_json(
            identity.api.project_path("admin/agents/agent-control/overview"),
        )
        if expected_project_role == "project_admin":
            assert agent_control.status_code == 200, agent_control.text
        else:
            assert_error_contract(
                agent_control,
                403,
                machine_codes={"project_role_denied"},
            )


def test_unknown_roles_fail_closed(
    admin_api: APIClient,
    e2e_manager: E2EResourceManager,
    human_identities: Mapping[str, HumanIdentity],
) -> None:
    """Unknown platform and project role values are rejected."""

    invalid_username = f"e2e_{e2e_manager.run_id.replace('-', '')}_invalid_role"[:50]
    invalid_create = admin_api.post_json(
        "/platform/users",
        {
            "username": invalid_username,
            "email": (
                f"e2e+{e2e_manager.run_id.replace('-', '')}.invalid-role@example.com"
            ),
            "password": strong_password(),
            "platform_role": "operator",
        },
    )
    if invalid_create.status_code == 201:
        unexpected_user = invalid_create.json().get("data", {})
        unexpected_id = unexpected_user.get("id")
        assert isinstance(unexpected_id, int) and unexpected_id > 0, unexpected_user
        e2e_manager.track_user(unexpected_id, invalid_username)
        cleanup = admin_api.delete(f"/platform/users/{unexpected_id}")
        assert cleanup.status_code in (200, 204), cleanup.text
    assert_error_contract(invalid_create, 400)

    invalid_platform_update = admin_api.put_json(
        f"/platform/users/{human_identities['agent_a'].id}",
        {"platform_role": "superuser"},
    )
    assert_error_contract(invalid_platform_update, 400)

    invalid_membership = admin_api.post_json(
        admin_api.project_path("memberships"),
        {
            "user_id": human_identities["agent_a"].id,
            "role": "unknown",
            "expected_version": e2e_manager.membership_version(
                human_identities["agent_a"].id
            ),
        },
        retry=False,
    )
    if invalid_membership.status_code == 200:
        unexpected_membership = invalid_membership.json().get("data")
        unexpected_version = (
            unexpected_membership.get("version")
            if isinstance(unexpected_membership, dict)
            and unexpected_membership.get("user_id") == human_identities["agent_a"].id
            else None
        )
        if not isinstance(unexpected_version, int) or unexpected_version <= 0:
            unexpected_version = e2e_manager.refresh_membership_version(
                human_identities["agent_a"].id
            )
        else:
            e2e_manager.track_membership_version(
                human_identities["agent_a"].id,
                unexpected_version,
            )
        restored = admin_api.post_json(
            admin_api.project_path("memberships"),
            {
                "user_id": human_identities["agent_a"].id,
                "role": "agent",
                "expected_version": unexpected_version,
            },
            retry=False,
        )
        assert restored.status_code == 200, "非法项目角色意外写入后的恢复失败"
        restored_membership = restored.json().get("data")
        restored_version = (
            restored_membership.get("version")
            if isinstance(restored_membership, dict)
            and restored_membership.get("user_id") == human_identities["agent_a"].id
            and restored_membership.get("role") == "agent"
            else None
        )
        if not isinstance(restored_version, int) or restored_version <= 0:
            restored_version = e2e_manager.refresh_membership_version(
                human_identities["agent_a"].id
            )
        else:
            e2e_manager.track_membership_version(
                human_identities["agent_a"].id,
                restored_version,
            )
    assert_error_contract(invalid_membership, 400)

    unchanged_context = human_identities["agent_a"].api.get_json(
        human_identities["agent_a"].api.project_path("context"),
    )
    assert unchanged_context.status_code == 200, unchanged_context.text
    assert unchanged_context.json().get("data", {}).get("project_role") == "agent"


def test_revoked_membership_immediately_removes_project_access(
    admin_api: APIClient,
    e2e_manager: E2EResourceManager,
) -> None:
    """A valid platform session cannot retain project access after revocation."""

    identity = e2e_manager.create_project_identity(
        "observer",
        label="revoked-membership",
    )
    revoked = e2e_manager.revoke_project_membership(identity.id)
    assert revoked.status_code in (200, 204), revoked.text

    projects = identity.api.get_json("/projects")
    assert projects.status_code == 200, projects.text
    assert _assert_strict_page_envelope(projects.json()) == []

    context = identity.api.get_json(identity.api.project_path("context"))
    assert_error_contract(
        context,
        403,
        machine_codes={"project_access_revoked"},
    )
    tickets = identity.api.get_json(
        identity.api.project_path("tickets"),
        params={"page": 1, "page_size": 1},
    )
    assert_error_contract(
        tickets,
        403,
        machine_codes={"project_access_revoked"},
    )


def test_soft_deleted_human_identity_returns_stable_conflict(
    admin_api: APIClient,
    e2e_manager: E2EResourceManager,
) -> None:
    """RBAC-010: audit-retained usernames/emails remain unique and return 409."""

    retained = e2e_manager.create_platform_identity(
        "member",
        label="retained-identity",
    )
    deleted = admin_api.delete(f"/platform/users/{retained.id}")
    assert deleted.status_code == 200, deleted.text

    replacement_username = f"{retained.username[:36]}_replacement"
    response = admin_api.post_json(
        "/platform/users",
        {
            "username": replacement_username,
            "email": retained.email,
            "password": strong_password(),
            "platform_role": "member",
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
    observer = human_identities["observer"]

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

    observer_read = observer.api.get_json(
        e2e_manager.project_path(f"tickets/{ticket_a_id}")
    )
    assert observer_read.status_code == 200, observer_read.text
    observer_update = observer.api.put_ticket(
        ticket_a_id,
        {"priority": "urgent"},
    )
    assert_error_contract(
        observer_update,
        403,
        machine_codes={"ticket_access_denied"},
    )
    observer_create = observer.api.post_json(
        observer.api.project_path("tickets"),
        e2e_manager.ticket_create_payload(
            {
                "title": e2e_manager.unique("observer-create-denied"),
                "description": "Observer must remain read-only.",
                "type": "request",
                "priority": "normal",
                "source": "api",
            }
        ),
    )
    assert_error_contract(
        observer_create,
        403,
        machine_codes={"ticket_create_access_denied"},
    )

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
        params={
            "page": 1,
            "page_size": 100,
            "sort_by": "created_at",
            "sort_order": "desc",
        },
    )
    assert unassigned.status_code == 200, unassigned.text
    unassigned_body = unassigned.json()
    assert set(unassigned_body) == {
        "success",
        "data",
        "total",
        "page",
        "page_size",
        "total_pages",
    }, unassigned_body
    assert unassigned_body.get("success") is True, unassigned_body
    assert unassigned_body.get("page") == 1, unassigned_body
    assert unassigned_body.get("page_size") == 100, unassigned_body
    assert isinstance(unassigned_body.get("total"), int), unassigned_body
    assert isinstance(unassigned_body.get("total_pages"), int), unassigned_body
    unassigned_items = unassigned_body.get("data")
    assert isinstance(unassigned_items, list), unassigned_body
    assert len(unassigned_items) <= 100, unassigned_body
    assert ticket_b_id in {item.get("id") for item in unassigned_items}
