"""Quick reply integration tests."""

from __future__ import annotations

import time
from typing import Dict

import pytest

from tests.utils import APIClient


@pytest.mark.api
@pytest.mark.integration
class TestQuickReplies:
    @pytest.fixture
    def quick_reply_payload(self) -> Dict[str, object]:
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
        quick_reply_payload: Dict[str, object],
    ) -> None:
        created_id: int | None = None
        try:
            create_resp = admin_api.post_json("/admin/automation/quick-replies", quick_reply_payload)
            assert create_resp.status_code == 201, create_resp.text
            create_body = create_resp.json()
            assert create_body.get("success") is True, create_body
            data = create_body.get("data", {})
            created_id = data.get("id")
            assert created_id, "快速回复创建未返回 ID"

            list_resp = admin_api.get_json(
                "/admin/automation/quick-replies",
                params={"page_size": 50, "is_public": True},
            )
            assert list_resp.status_code == 200, list_resp.text
            list_body = list_resp.json()
            assert list_body.get("success") is True, list_body
            replies = list_body.get("data", {}).get("replies", [])
            assert any(reply.get("id") == created_id for reply in replies)

            use_resp = admin_api.post_json(
                f"/admin/automation/quick-replies/{created_id}/use",
                {},
            )
            assert use_resp.status_code == 200, use_resp.text
            use_body = use_resp.json()
            assert use_body.get("success") is True, use_body
        finally:
            if created_id is not None:
                delete_resp = admin_api.delete(f"/admin/automation/quick-replies/{created_id}")
                if delete_resp.status_code == 200:
                    delete_body = delete_resp.json()
                    assert delete_body.get("success") is True, delete_body
