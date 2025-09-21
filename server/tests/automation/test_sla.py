"""SLA configuration integration tests."""

from __future__ import annotations

import time
from typing import Dict

import pytest

from tests.utils import APIClient


@pytest.mark.api
@pytest.mark.integration
class TestSLAConfigs:
    @pytest.fixture
    def sla_payload(self) -> Dict[str, object]:
        unique = int(time.time_ns())
        return {
            "name": f"Standard SLA {unique}",
            "description": "Automated SLA config created via pytest.",
            "is_default": False,
            "response_time": 45,
            "resolution_time": 180,
            "working_hours": {
                "monday": {"start": "09:00", "end": "18:00"},
                "tuesday": {"start": "09:00", "end": "18:00"},
            },
            "escalation_rules": [
                {"trigger_minutes": 60, "action": "notify_admin", "notify_users": [1]}
            ],
        }

    def test_sla_create_list_detail_delete(
        self,
        admin_api: APIClient,
        sla_payload: Dict[str, object],
    ) -> None:
        created_id: int | None = None
        try:
            create_resp = admin_api.post_json("/admin/automation/sla", sla_payload)
            assert create_resp.status_code == 201, create_resp.text
            create_body = create_resp.json()
            assert create_body.get("success") is True, create_body
            created = create_body.get("data", {})
            created_id = created.get("id")
            assert created_id, "SLA 创建未返回 ID"

            list_resp = admin_api.get_json("/admin/automation/sla", params={"page_size": 50})
            assert list_resp.status_code == 200, list_resp.text
            list_body = list_resp.json()
            assert list_body.get("success") is True, list_body
            configs = list_body.get("data", {}).get("configs", [])
            assert any(cfg.get("id") == created_id for cfg in configs), "SLA 列表未包含新建配置"

        finally:
            if created_id is not None:
                delete_resp = admin_api.delete(f"/admin/automation/sla/{created_id}")
                if delete_resp.status_code == 200:
                    delete_body = delete_resp.json()
                    assert delete_body.get("success") is True, delete_body
