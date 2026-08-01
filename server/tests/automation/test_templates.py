"""Automation template integration tests."""

from __future__ import annotations

import time

import pytest

from tests.utils import APIClient


@pytest.mark.api
@pytest.mark.integration
class TestTemplates:
    @pytest.fixture
    def template_payload(
        self, admin_tokens: dict[str, dict[str, object]]
    ) -> dict[str, object]:
        user = admin_tokens.get("user", {})
        admin_id = user.get("id")
        assert admin_id, "Admin token payload 缺少 user.id"
        unique = int(time.time_ns())
        return {
            "name": f"Template {unique}",
            "description": "Fixture template for automated tests.",
            "category": "incident",
            "title_template": "Issue {unique}",
            "content_template": "Automated content",
            "default_type": "incident",
            "default_priority": "high",
            "default_status": "open",
            "assign_to_user_id": admin_id,
            "is_active": True,
            "custom_fields": [
                {
                    "name": "environment",
                    "type": "text",
                    "label": "Environment",
                    "required": False,
                }
            ],
        }

    def test_template_crud(
        self,
        admin_api: APIClient,
        template_payload: dict[str, object],
    ) -> None:
        created_id: int | None = None
        try:
            create_resp = admin_api.post_json(
                admin_api.project_path("admin/automation/templates"),
                template_payload,
            )
            assert create_resp.status_code == 201, create_resp.text
            create_body = create_resp.json()
            assert create_body.get("success") is True, create_body
            data = create_body.get("data", {})
            created_id = data.get("id")
            assert created_id, "模板创建未返回 ID"

            list_resp = admin_api.get_json(
                admin_api.project_path("admin/automation/templates"),
                params={"page": 1, "page_size": 50},
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
            templates = list_data["items"]
            assert isinstance(templates, list), list_data
            assert len(templates) <= 50, list_data
            assert any(tpl.get("id") == created_id for tpl in templates)

            detail_resp = admin_api.get_json(
                admin_api.project_path(f"admin/automation/templates/{created_id}")
            )
            assert detail_resp.status_code == 200, detail_resp.text
            detail_body = detail_resp.json()
            assert detail_body.get("success") is True, detail_body
            detail_data = detail_body.get("data", {})
            assert detail_data.get("name") == template_payload["name"]
        finally:
            if created_id is not None:
                delete_resp = admin_api.delete(
                    admin_api.project_path(f"admin/automation/templates/{created_id}")
                )
                if delete_resp.status_code == 200:
                    delete_body = delete_resp.json()
                    assert delete_body.get("success") is True, delete_body
