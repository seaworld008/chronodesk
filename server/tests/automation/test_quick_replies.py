"""Quick reply integration tests."""

from __future__ import annotations

import time

import pytest

from tests.utils import APIClient


@pytest.mark.api
@pytest.mark.integration
class TestQuickReplies:
    @pytest.fixture
    def quick_reply_payload(self) -> dict[str, object]:
        unique = int(time.time_ns())
        return {
            "name": f"Quick Reply {unique}",
            "category": "general",
            "content": "Automated quick reply body.",
            "tags": "auto,pytest",
            "is_public": True,
        }

    def test_quick_reply_flow(
        self,
        admin_api: APIClient,
        quick_reply_payload: dict[str, object],
    ) -> None:
        created_id: int | None = None
        try:
            create_resp = admin_api.post_json(
                admin_api.project_path("admin/automation/quick-replies"),
                quick_reply_payload,
            )
            assert create_resp.status_code == 201, create_resp.text
            create_body = create_resp.json()
            assert create_body.get("success") is True, create_body
            data = create_body.get("data", {})
            created_id = data.get("id")
            assert created_id, "快速回复创建未返回 ID"

            list_resp = admin_api.get_json(
                admin_api.project_path("admin/automation/quick-replies"),
                params={"page": 1, "page_size": 50, "is_public": "true"},
            )
            assert list_resp.status_code == 200, list_resp.text
            list_body = list_resp.json()
            assert list_body.get("success") is True, list_body
            assert set(list_body) == {"success", "message", "data"}, list_body
            list_data = list_body["data"]
            assert isinstance(list_data, dict), list_body
            assert set(list_data) == {
                "items",
                "total",
                "page",
                "page_size",
                "total_pages",
            }, list_data
            assert list_data["page"] == 1, list_data
            assert list_data["page_size"] == 50, list_data
            assert isinstance(list_data["total"], int), list_data
            assert isinstance(list_data["total_pages"], int), list_data
            replies = list_data["items"]
            assert isinstance(replies, list), list_data
            assert len(replies) <= 50, list_data
            assert any(reply.get("id") == created_id for reply in replies)

            use_resp = admin_api.post_json(
                admin_api.project_path(
                    f"admin/automation/quick-replies/{created_id}/use"
                ),
                {},
            )
            assert use_resp.status_code == 200, use_resp.text
            use_body = use_resp.json()
            assert use_body.get("success") is True, use_body
        finally:
            if created_id is not None:
                delete_resp = admin_api.delete(
                    admin_api.project_path(
                        f"admin/automation/quick-replies/{created_id}"
                    )
                )
                if delete_resp.status_code == 200:
                    delete_body = delete_resp.json()
                    assert delete_body.get("success") is True, delete_body
