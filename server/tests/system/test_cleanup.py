"""Cleanup configuration endpoints integration tests."""

from __future__ import annotations

import copy

import pytest

from tests.utils import APIClient


@pytest.mark.api
@pytest.mark.integration
class TestCleanupSettings:
    def test_cleanup_config_roundtrip(self, admin_api: APIClient) -> None:
        resp = admin_api.get_json("/admin/system/cleanup/config")
        assert resp.status_code == 200, resp.text
        body = resp.json()
        assert body.get("success") is True, body
        config = body.get("data", {})
        assert config, "Expected cleanup config payload"

        original = copy.deepcopy(config)
        try:
            # Toggle cleanup_enabled if present, otherwise adjust max_records
            update_payload = copy.deepcopy(config)
            if "cleanup_enabled" in update_payload:
                update_payload["cleanup_enabled"] = not bool(update_payload["cleanup_enabled"])
            if "max_records_per_cleanup" in update_payload:
                value = update_payload["max_records_per_cleanup"] or 1000
                update_payload["max_records_per_cleanup"] = value + 1
            if "cleanup_schedule" in update_payload:
                update_payload["cleanup_schedule"] = "0 3 * * *"

            update_resp = admin_api.put_json("/admin/system/cleanup/config", update_payload)
            assert update_resp.status_code == 200, update_resp.text
            update_body = update_resp.json()
            assert update_body.get("success") is True, update_body

            # Logs endpoint (may be empty but should succeed)
            logs_resp = admin_api.get_json(
                "/admin/system/cleanup/logs",
                params={"limit": 5},
            )
            assert logs_resp.status_code == 200, logs_resp.text
            logs_body = logs_resp.json()
            assert logs_body.get("success") is True, logs_body

            # Stats endpoint
            stats_resp = admin_api.get_json("/admin/system/cleanup/stats")
            assert stats_resp.status_code == 200, stats_resp.text
            stats_body = stats_resp.json()
            assert stats_body.get("success") is True, stats_body
        finally:
            # Restore original configuration to avoid side effects
            restore_resp = admin_api.put_json("/admin/system/cleanup/config", original)
            assert restore_resp.status_code == 200, restore_resp.text
            assert restore_resp.json().get("success") is True
