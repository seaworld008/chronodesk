"""Automation rule management integration tests."""

from __future__ import annotations

import time
from typing import Dict, List

import pytest

from tests.utils import APIClient


@pytest.mark.api
@pytest.mark.integration
class TestAutomationRules:
    @pytest.fixture
    def rule_payload(self, admin_tokens: Dict[str, Dict[str, object]]) -> Dict[str, object]:
        user = admin_tokens.get("user", {})
        admin_id = user.get("id")
        assert admin_id, "Admin token payload 缺少 user.id"

        unique = int(time.time_ns())
        return {
            "name": f"Auto Rule {unique}",
            "description": "Created by automated pytest suite.",
            "rule_type": "assignment",
            "trigger_event": "ticket.created",
            "conditions": [
                {
                    "field": "priority",
                    "operator": "eq",
                    "value": "normal",
                    "logic_op": "and",
                }
            ],
            "actions": [
                {
                    "type": "assign",
                    "params": {
                        "user_id": admin_id,
                    },
                }
            ],
        }

    def test_rule_crud_flow(
        self,
        admin_api: APIClient,
        rule_payload: Dict[str, object],
    ) -> None:
        created_rule_id: int | None = None
        try:
            # Create rule
            create_resp = admin_api.post_json("/admin/automation/rules", rule_payload)
            assert create_resp.status_code == 201, create_resp.text
            create_body = create_resp.json()
            assert create_body.get("success") is True, create_body
            data = create_body.get("data", {})
            created_rule_id = data.get("id")
            assert created_rule_id, "创建规则未返回 ID"

            # List rules and ensure new rule present
            list_resp = admin_api.get_json("/admin/automation/rules")
            assert list_resp.status_code == 200, list_resp.text
            list_body = list_resp.json()
            assert list_body.get("success") is True, list_body
            list_data = list_body.get("data", {})
            rules: List[Dict[str, object]] = list_data.get("rules", [])
            assert any(rule.get("id") == created_rule_id for rule in rules), "规则列表未包含新建规则"

            # Fetch rule detail
            detail_resp = admin_api.get_json(f"/admin/automation/rules/{created_rule_id}")
            assert detail_resp.status_code == 200, detail_resp.text
            detail_body = detail_resp.json()
            assert detail_body.get("success") is True, detail_body
            detail_data = detail_body.get("data", {})
            assert detail_data.get("name") == rule_payload["name"]

            # Update rule (toggle active & priority comment)
            updated_payload = {
                **rule_payload,
                "description": "Updated via automated test.",
                "is_active": False,
            }
            update_resp = admin_api.put_json(
                f"/admin/automation/rules/{created_rule_id}",
                updated_payload,
            )
            assert update_resp.status_code == 200, update_resp.text
            update_body = update_resp.json()
            assert update_body.get("success") is True, update_body

            # Fetch stats (should default to zero counts)
            stats_resp = admin_api.get_json(f"/admin/automation/rules/{created_rule_id}/stats")
            assert stats_resp.status_code == 200, stats_resp.text
            stats_body = stats_resp.json()
            assert stats_body.get("success") is True, stats_body
            stats = stats_body.get("data", {})
            assert stats.get("rule_id") == created_rule_id
            assert "execution_count" in stats

            # Execution logs (likely empty but endpoint should succeed)
            logs_resp = admin_api.get_json(
                "/admin/automation/logs",
                params={"rule_id": created_rule_id, "page_size": 5},
            )
            assert logs_resp.status_code == 200, logs_resp.text
            logs_body = logs_resp.json()
            assert logs_body.get("success") is True, logs_body

        finally:
            if created_rule_id is not None:
                cleanup_resp = admin_api.delete(f"/admin/automation/rules/{created_rule_id}")
                if cleanup_resp.status_code == 200:
                    cleanup_body = cleanup_resp.json()
                    assert cleanup_body.get("success") is True, cleanup_body
