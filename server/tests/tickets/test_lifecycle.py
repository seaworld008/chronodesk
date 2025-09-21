"""Ticket lifecycle integration tests.

Covers create -> update -> assign -> status changes -> notifications.
"""

from __future__ import annotations

import time
from typing import Any, Dict, List, Set

import pytest

from tests.utils import APIClient


@pytest.mark.api
@pytest.mark.integration
class TestTicketLifecycle:
    @pytest.fixture(scope="class")
    def ticket_payload(self) -> Dict[str, object]:
        unique_suffix = int(time.time())
        return {
            "title": f"Auto Test Ticket {unique_suffix}",
            "description": "Automated test ticket created via pytest.",
            "type": "request",
            "priority": "normal",
            "source": "api",
        }

    def _fetch_notification_ids(self, client: APIClient, limit: int = 50) -> Set[int]:
        response = client.get_json("/notifications", params={"limit": limit})
        assert response.status_code == 200, response.text
        payload = response.json()
        assert payload.get("code") == 0, payload
        items = payload.get("data", {}).get("items", [])
        return {item["id"] for item in items}

    def _wait_for_ticket_notification(
        self,
        client: APIClient,
        ticket_id: int,
        *,
        expected_type: str | None = None,
        attempts: int = 5,
        delay: float = 1.0,
    ) -> Dict[str, Any]:
        """Poll notifications until we find one referencing the ticket."""

        for _ in range(attempts):
            response = client.get_json("/notifications", params={"limit": 50})
            assert response.status_code == 200, response.text
            payload = response.json()
            assert payload.get("code") == 0, payload

            items = payload.get("data", {}).get("items", [])
            for item in items:
                if expected_type and item.get("type") != expected_type:
                    continue

                related_ticket = item.get("related_ticket") or {}
                if related_ticket and related_ticket.get("id") == ticket_id:
                    return item

                related_ticket_id = item.get("related_ticket_id")
                if related_ticket_id == ticket_id:
                    return item

            time.sleep(delay)

        pytest.fail(f"未在通知列表中找到与工单 {ticket_id} 相关的通知 ({expected_type or 'any'})")

    def _fetch_ticket(self, client: APIClient, ticket_id: int) -> Dict[str, object]:
        response = client.get_json(f"/tickets/{ticket_id}")
        assert response.status_code == 200, response.text
        payload = response.json()
        assert payload.get("code") == 0, payload
        return payload["data"]

    @pytest.fixture(scope="class")
    def secondary_agent(self, admin_api: APIClient) -> Dict[str, Any]:
        response = admin_api.get_json(
            "/admin/users",
            params={
                "role": "agent",
                "page_size": 1,
                "order_by": "id",
                "order": "asc",
            },
        )
        assert response.status_code == 200, response.text
        body = response.json()
        assert body.get("code") == 0, body
        items: List[Dict[str, Any]] = body.get("data", {}).get("items", [])
        assert items, "缺少可用的客服/技术支持账号供分配"
        return items[0]

    def test_full_lifecycle(
        self,
        admin_api: APIClient,
        admin_tokens: Dict[str, object],
        ticket_payload: Dict[str, object],
        secondary_agent: Dict[str, Any],
    ) -> None:
        # Baseline notifications for diff check
        existing_notifications = self._fetch_notification_ids(admin_api, limit=100)

        # 1. Create ticket
        create_resp = admin_api.post_json("/tickets", ticket_payload)
        assert create_resp.status_code in (200, 201), create_resp.text
        create_body = create_resp.json()
        assert create_body.get("code") == 0, create_body
        ticket = create_body["data"]
        ticket_id = ticket["id"]

        # Ensure title matches request
        assert ticket["title"] == ticket_payload["title"]
        assert ticket["status"] == "open"

        # 2. Update ticket meta data
        update_payload = {
            "description": "Updated description via automated test.",
            "priority": "high",
        }
        update_resp = admin_api.put_json(f"/tickets/{ticket_id}", update_payload)
        assert update_resp.status_code in (200, 201), update_resp.text
        update_body = update_resp.json()
        assert update_body.get("code") == 0, update_body
        updated_ticket = update_body["data"]
        assert updated_ticket["priority"] == "high"
        assert "Updated description" in updated_ticket["description"]

        admin_user = admin_tokens.get("user", {})
        admin_id = admin_user.get("id")
        assert admin_id, "Admin login payload缺少 user.id"

        # 3. Assign to automation agent for triage
        agent_id = secondary_agent["id"]
        assign_comment = "Assigning to automation agent for triage"
        assign_payload = {"assigned_to_id": agent_id, "comment": assign_comment}
        assign_resp = admin_api.post_json(f"/tickets/{ticket_id}/assign", assign_payload)
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
        progress_resp = admin_api.post_json(f"/tickets/{ticket_id}/status", progress_payload)
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
        resolve_resp = admin_api.post_json(f"/tickets/{ticket_id}/status", resolve_payload)
        assert resolve_resp.status_code == 200, resolve_resp.text
        resolve_body = resolve_resp.json()
        assert resolve_body.get("success") is True, resolve_body
        assert resolve_body.get("data", {}).get("status") == "resolved"

        # 6. Fetch ticket to ensure history/comments updated
        reloaded = self._fetch_ticket(admin_api, ticket_id)
        assert reloaded["status"] == "resolved"

        history_resp = admin_api.get_json(f"/tickets/{ticket_id}/history")
        assert history_resp.status_code == 200, history_resp.text
        history_body = history_resp.json()
        # workflow handler returns {success: bool, data: [...]}
        assert history_body.get("success") is True, history_body
        history_events: List[Dict[str, object]] = history_body.get("data", [])
        actions = {event.get("action") for event in history_events}
        assert {"assign", "status_change"}.issubset(actions), "缺少关键工单历史记录"

        assign_history = next((event for event in history_events if event.get("action") == "assign"), None)
        assert assign_history is not None, "分配历史缺失"
        assert assign_comment in assign_history.get("description", ""), "分配历史未包含备注"

        status_descriptions = [event.get("description", "") for event in history_events if event.get("action") == "status_change"]
        assert any(progress_comment in desc for desc in status_descriptions), "进度状态历史未包含备注"
        assert any(resolution_comment in desc for desc in status_descriptions), "解决状态历史未包含备注"
        assert any("Automated resolution notes" in desc for desc in status_descriptions), "解决历史未包含解决方案"

        # 7. Verify notifications
        # 获取通知并确认与工单相关的通知存在
        latest_notifications = self._fetch_notification_ids(admin_api, limit=100)
        assert latest_notifications - existing_notifications, "未检测到新的通知记录"

        notif_resp = admin_api.get_json("/notifications", params={"limit": 50})
        assert notif_resp.status_code == 200, notif_resp.text
        notif_body = notif_resp.json()
        assert notif_body.get("code") == 0, notif_body
        items: List[Dict[str, object]] = notif_body.get("data", {}).get("items", [])
        linked = [
            item
            for item in items
            if (item.get("related_ticket") and item["related_ticket"]["id"] == ticket_id)
            or (item.get("related_id") == ticket_id)
        ]
        assert linked, "Expected at least one notification referencing the ticket"

        agent_notifications = [
            item
            for item in items
            if item.get("type") == "ticket_assigned"
            and item.get("recipient", {}).get("id") == agent_id
            and (
                (item.get("related_ticket") and item["related_ticket"].get("id") == ticket_id)
                or item.get("related_ticket_id") == ticket_id
            )
        ]
        assert agent_notifications, "工单分配后未在通知列表中找到针对代理用户的通知"

        # 8. Cleanup - delete ticket
        delete_resp = admin_api.delete(f"/tickets/{ticket_id}")
        if delete_resp.status_code in (200, 204):
            if delete_resp.status_code == 200:
                delete_body = delete_resp.json()
                assert delete_body.get("code") == 0, delete_body
        else:
            # 当前系统对关联通知存在外键限制，允许在通知保留时清理失败
            assert "violates foreign key" in delete_resp.text
