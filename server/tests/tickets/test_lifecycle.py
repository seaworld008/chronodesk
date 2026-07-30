"""Ticket lifecycle integration tests.

Covers create -> update -> assign -> status changes -> notifications.
"""

from __future__ import annotations

import time
from collections.abc import Iterator
from typing import Any

import pytest

from tests.utils import APIClient, E2EResourceManager


@pytest.mark.api
@pytest.mark.integration
class TestTicketLifecycle:
    @pytest.fixture(scope="class")
    @classmethod
    def ticket_payload(cls) -> dict[str, object]:
        unique_suffix = int(time.time())
        return {
            "title": f"Auto Test Ticket {unique_suffix}",
            "description": "Automated test ticket created via pytest.",
            "type": "request",
            "priority": "normal",
            "source": "api",
        }

    def _fetch_notification_ids(self, client: APIClient, limit: int = 50) -> set[int]:
        response = client.get_json(
            client.project_path("notifications"),
            params={"page": 1, "page_size": limit},
        )
        assert response.status_code == 200, response.text
        payload = response.json()
        assert payload.get("code") == 0, payload
        items = payload.get("data", {}).get("items", [])
        return {item["id"] for item in items}

    def _wait_for_ticket_notifications(
        self,
        client: APIClient,
        ticket_id: int,
        *,
        expected_counts: dict[str, int],
        attempts: int = 45,
        delay: float = 1.0,
    ) -> list[dict[str, Any]]:
        """Poll the recipient's inbox until every expected Outbox item arrives."""

        for _ in range(attempts):
            response = client.get_json(
                client.project_path("notifications"),
                params={
                    "page": 1,
                    "page_size": 50,
                },
            )
            assert response.status_code == 200, response.text
            payload = response.json()
            assert payload.get("code") == 0, payload

            items = payload.get("data", {}).get("items", [])
            linked: list[dict[str, Any]] = []
            for item in items:
                related_ticket = item.get("related_ticket") or {}
                related_ticket_id = item.get("related_ticket_id")
                if (
                    related_ticket.get("id") == ticket_id
                    or related_ticket_id == ticket_id
                ):
                    linked.append(item)

            if all(
                sum(1 for item in linked if item.get("type") == notification_type)
                >= count
                for notification_type, count in expected_counts.items()
            ):
                return linked

            time.sleep(delay)

        pytest.fail(
            f"未在通知列表中收到工单 {ticket_id} 的全部异步通知：{expected_counts}"
        )

    def _fetch_ticket(self, client: APIClient, ticket_id: int) -> dict[str, object]:
        response = client.get_json(client.project_path(f"tickets/{ticket_id}"))
        assert response.status_code == 200, response.text
        payload = response.json()
        assert payload.get("code") == 0, payload
        return payload["data"]

    @pytest.fixture
    def secondary_agent(
        self,
        e2e_manager: E2EResourceManager,
    ) -> Iterator[dict[str, Any]]:
        """Create an isolated assignee and authenticate as that exact recipient."""

        identity = e2e_manager.create_project_identity(
            "agent",
            label="ticket-lifecycle-agent",
        )
        agent: dict[str, Any] = {
            "id": identity.id,
            "platform_role": identity.platform_role,
            "project_role": identity.project_role,
            "api": identity.api,
        }
        try:
            yield agent
        finally:
            for notification_id in agent.get("_notification_ids", []):
                e2e_manager.track_notification(notification_id)

    @pytest.fixture
    def created_ticket_ids(
        self,
        admin_api: APIClient,
        secondary_agent: dict[str, Any],
    ) -> Iterator[list[int]]:
        """Always remove test tickets before the temporary assignee is deleted."""

        del secondary_agent
        ticket_ids: list[int] = []
        try:
            yield ticket_ids
        finally:
            for ticket_id in reversed(ticket_ids):
                cleanup = admin_api.delete_ticket(ticket_id)
                assert cleanup.status_code in (200, 204, 404), cleanup.text

    def test_full_lifecycle(
        self,
        admin_api: APIClient,
        ticket_payload: dict[str, object],
        secondary_agent: dict[str, Any],
        created_ticket_ids: list[int],
        e2e_manager: E2EResourceManager,
    ) -> None:
        agent_api = secondary_agent["api"]
        existing_agent_notifications = self._fetch_notification_ids(
            agent_api, limit=100
        )

        # 1. Create ticket
        configured_ticket_payload = e2e_manager.ticket_create_payload(ticket_payload)
        create_resp = admin_api.post_json(
            admin_api.project_path("tickets"),
            configured_ticket_payload,
        )
        assert create_resp.status_code in (200, 201), create_resp.text
        create_body = create_resp.json()
        assert create_body.get("code") == 0, create_body
        ticket = create_body["data"]
        ticket_id = ticket["id"]
        created_ticket_ids.append(ticket_id)

        # Ensure title matches request
        assert ticket["title"] == ticket_payload["title"]
        assert ticket["status"] == "open"

        # 2. Update ticket meta data
        update_payload = {
            "description": "Updated description via automated test.",
            "priority": "high",
        }
        update_resp = admin_api.put_ticket(ticket_id, update_payload)
        assert update_resp.status_code in (200, 201), update_resp.text
        update_body = update_resp.json()
        assert update_body.get("code") == 0, update_body
        updated_ticket = update_body["data"]
        assert updated_ticket["priority"] == "high"
        assert "Updated description" in updated_ticket["description"]

        # 3. Assign to automation agent for triage
        agent_id = secondary_agent["id"]
        assign_comment = "Assigning to automation agent for triage"
        assign_payload = {"assigned_to_id": agent_id, "comment": assign_comment}
        assign_resp = admin_api.post_ticket_command(
            ticket_id,
            "assign",
            assign_payload,
        )
        assert assign_resp.status_code == 200, assign_resp.text
        assign_body = assign_resp.json()
        assert assign_body.get("success") is True, assign_body
        # Reload ticket to confirm assignment persisted
        assigned_ticket = self._fetch_ticket(admin_api, ticket_id)
        assigned_user = assigned_ticket.get("assigned_to")
        assert assigned_user, "Assigned ticket should expose assignee"
        assert assigned_user.get("id") == agent_id

        # 4. Move to in_progress with comment
        progress_comment = "Work started"
        progress_payload = {"status": "in_progress", "comment": progress_comment}
        progress_resp = admin_api.post_ticket_command(
            ticket_id,
            "status",
            progress_payload,
        )
        assert progress_resp.status_code == 200, progress_resp.text
        progress_body = progress_resp.json()
        assert progress_body.get("success") is True, progress_body
        assert progress_body.get("data", {}).get("status") == "in_progress"

        # 5. Resolve ticket with resolution notes
        resolution_comment = "Issue fixed"
        resolve_payload = {
            "status": "resolved",
            "comment": resolution_comment,
            "resolution_notes": "Automated resolution notes",
        }
        resolve_resp = admin_api.post_ticket_command(
            ticket_id,
            "status",
            resolve_payload,
        )
        assert resolve_resp.status_code == 200, resolve_resp.text
        resolve_body = resolve_resp.json()
        assert resolve_body.get("success") is True, resolve_body
        assert resolve_body.get("data", {}).get("status") == "resolved"

        # 6. Fetch ticket to ensure history/comments updated
        reloaded = self._fetch_ticket(admin_api, ticket_id)
        assert reloaded["status"] == "resolved"

        history_resp = admin_api.get_json(
            admin_api.project_path(f"tickets/{ticket_id}/history")
        )
        assert history_resp.status_code == 200, history_resp.text
        history_body = history_resp.json()
        # workflow handler returns {success: bool, data: [...]}
        assert history_body.get("success") is True, history_body
        history_events: list[dict[str, object]] = history_body.get("data", [])
        actions = {event.get("action") for event in history_events}
        assert {"assign", "status_change"}.issubset(actions), "缺少关键工单历史记录"

        assign_history = next(
            (event for event in history_events if event.get("action") == "assign"), None
        )
        assert assign_history is not None, "分配历史缺失"
        assert assign_comment in assign_history.get("description", ""), (
            "分配历史未包含备注"
        )

        status_descriptions = [
            event.get("description", "")
            for event in history_events
            if event.get("action") == "status_change"
        ]
        assert any(progress_comment in desc for desc in status_descriptions), (
            "进度状态历史未包含备注"
        )
        assert any(resolution_comment in desc for desc in status_descriptions), (
            "解决状态历史未包含备注"
        )
        assert any(
            "Automated resolution notes" in desc for desc in status_descriptions
        ), "解决历史未包含解决方案"

        # 7. Verify the asynchronous Outbox delivery through the actual recipient.
        # Project notifications remain recipient-scoped, so an administrator's
        # self-service list must never inspect another user's inbox.
        delivered_notifications = self._wait_for_ticket_notifications(
            agent_api,
            ticket_id,
            expected_counts={
                "ticket_assigned": 1,
                "ticket_status_changed": 2,
            },
        )
        secondary_agent["_notification_ids"] = [
            notification["id"] for notification in delivered_notifications
        ]
        assignment_notification = next(
            item
            for item in delivered_notifications
            if item.get("type") == "ticket_assigned"
        )
        assert assignment_notification["id"] not in existing_agent_notifications
        assert assignment_notification.get("recipient", {}).get("id") == agent_id

        status_notifications = [
            item
            for item in delivered_notifications
            if item.get("type") == "ticket_status_changed"
        ]
        assert len(status_notifications) >= 2
        assert all(
            item.get("recipient", {}).get("id") == agent_id
            for item in status_notifications
        )

        admin_notification_ids = self._fetch_notification_ids(admin_api, limit=100)
        assert assignment_notification["id"] not in admin_notification_ids
        assert all(
            notification["id"] not in admin_notification_ids
            for notification in status_notifications
        )

        # 8. Cleanup - delete ticket
        delete_resp = admin_api.delete_ticket(ticket_id)
        assert delete_resp.status_code in (200, 204), delete_resp.text
        if delete_resp.status_code == 200:
            delete_body = delete_resp.json()
            assert delete_body.get("code") == 0, delete_body
        created_ticket_ids.remove(ticket_id)
