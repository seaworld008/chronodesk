"""System configuration integration tests."""

from __future__ import annotations

import io
import json
import time
from typing import Dict

import pytest
import requests

from tests.utils import APIClient


@pytest.mark.api
@pytest.mark.integration
class TestSystemConfigs:
    @pytest.fixture
    def config_payload(self) -> Dict[str, object]:
        unique = int(time.time_ns())
        return {
            "key": f"system.test.{unique}",
            "value": "pytest-value",
            "value_type": "string",
            "description": "Configuration created via automated system test.",
            "category": "system",
            "group": "pytest",
        }

    def test_config_operations(
        self,
        admin_api: APIClient,
        config_payload: Dict[str, object],
    ) -> None:
        created_key: str | None = None

        list_resp = admin_api.get_json("/admin/configs", params={"page": 1})
        assert list_resp.status_code == 200, list_resp.text
        assert list_resp.json().get("success") is True

        try:
            create_resp = admin_api.post_json("/admin/configs", config_payload)
            assert create_resp.status_code == 201, create_resp.text
            created = create_resp.json().get("data", {})
            created_key = created.get("key")
            assert created_key == config_payload["key"]

            detail_resp = admin_api.get_json(f"/admin/configs/{created_key}")
            detail_body = detail_resp.json()
            assert detail_body.get("success") is True
            assert detail_body.get("data", {}).get("value") == config_payload["value"]

            update_payload = {
                **config_payload,
                "value": "pytest-updated",
                "description": "Updated via automated test.",
            }
            update_resp = admin_api.put_json(f"/admin/configs/{created_key}", update_payload)
            assert update_resp.status_code == 200, update_resp.text
            assert update_resp.json().get("data", {}).get("value") == update_payload["value"]

            batch_payload = [
                {
                    "key": created_key,
                    "value": "true",
                    "value_type": "bool",
                    "description": "Batch updated flag",
                    "category": "system",
                    "group": "pytest",
                }
            ]
            batch_resp = admin_api.put_json("/admin/configs/batch", batch_payload)
            assert batch_resp.status_code == 200, batch_resp.text
            assert batch_resp.json().get("data", {}).get("updated_count") == 1

            detail_resp = admin_api.get_json(f"/admin/configs/{created_key}")
            value = detail_resp.json().get("data", {}).get("value")
            if isinstance(value, str):
                assert value.lower() == "true"
            else:
                assert value is True

            policy_resp = admin_api.get_json("/admin/configs/security-policy")
            assert policy_resp.status_code == 200, policy_resp.text
            assert "password_policy" in policy_resp.json().get("data", {})

            clear_resp = admin_api.post_json("/admin/configs/cache/clear", {})
            assert clear_resp.status_code == 200, clear_resp.text
            stats_resp = admin_api.get_json("/admin/configs/cache/stats")
            assert stats_resp.status_code == 200, stats_resp.text
            assert stats_resp.json().get("success") is True

            export_resp = admin_api.session.get(
                admin_api._build_url("/admin/configs/export"),
                params={"format": "json"},
            )
            assert export_resp.status_code == 200, export_resp.text
            exported_payload = export_resp.content

            files = {"file": ("configs.json", io.BytesIO(exported_payload), "application/json")}
            auth_header = admin_api.session.headers.get("Authorization")
            import_resp = requests.post(
                admin_api._build_url("/admin/configs/import"),
                headers={"Authorization": auth_header} if auth_header else None,
                files=files,
                timeout=30,
            )
            assert import_resp.status_code == 200, import_resp.text
            assert import_resp.json().get("success") is True

            init_resp = admin_api.post_json("/admin/configs/init", {})
            assert init_resp.status_code == 200, init_resp.text

        finally:
            if created_key is not None:
                delete_resp = admin_api.delete(f"/admin/configs/{created_key}")
                if delete_resp.status_code == 200:
                    assert delete_resp.json().get("success") is True
            admin_api.post_json("/admin/configs/cache/clear", {})
