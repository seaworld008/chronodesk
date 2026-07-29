"""Cleanup configuration endpoints integration tests."""

from __future__ import annotations

import copy

import pytest

from tests.utils import (
    APIClient,
    assert_local_ephemeral_target,
    response_diagnostic,
    safe_diagnostic,
)


@pytest.mark.api
@pytest.mark.integration
class TestCleanupSettings:
    def test_cleanup_config_roundtrip(
        self,
        admin_api: APIClient,
        api_base_url: str,
    ) -> None:
        assert_local_ephemeral_target(
            api_base_url,
            "修改全局 cleanup 配置",
        )
        resp = admin_api.get_json("/admin/system/cleanup/config")
        assert resp.status_code == 200, response_diagnostic(resp)
        body = resp.json()
        assert body.get("success") is True, safe_diagnostic(body)
        config = body.get("data", {})
        assert config, "Expected cleanup config payload"

        original = copy.deepcopy(config)
        try:
            # Toggle cleanup_enabled if present, otherwise adjust max_records
            update_payload = copy.deepcopy(config)
            if "cleanup_enabled" in update_payload:
                update_payload["cleanup_enabled"] = not bool(
                    update_payload["cleanup_enabled"]
                )
            if "max_records_per_cleanup" in update_payload:
                value = update_payload["max_records_per_cleanup"] or 1000
                update_payload["max_records_per_cleanup"] = value + 1
            if "cleanup_schedule" in update_payload:
                update_payload["cleanup_schedule"] = "0 3 * * *"

            update_resp = admin_api.put_json(
                "/admin/system/cleanup/config", update_payload
            )
            assert update_resp.status_code == 200, response_diagnostic(update_resp)
            update_body = update_resp.json()
            assert update_body.get("success") is True, safe_diagnostic(update_body)

            # Logs endpoint (may be empty but should succeed)
            logs_resp = admin_api.get_json(
                "/admin/system/cleanup/logs",
                params={"limit": 5},
            )
            assert logs_resp.status_code == 200, response_diagnostic(logs_resp)
            logs_body = logs_resp.json()
            assert logs_body.get("success") is True, safe_diagnostic(logs_body)

            # Stats endpoint
            stats_resp = admin_api.get_json("/admin/system/cleanup/stats")
            assert stats_resp.status_code == 200, response_diagnostic(stats_resp)
            stats_body = stats_resp.json()
            assert stats_body.get("success") is True, safe_diagnostic(stats_body)
        finally:
            # Restore original configuration to avoid side effects
            restore_resp = None
            for _ in range(2):
                candidate = admin_api.put_json(
                    "/admin/system/cleanup/config",
                    original,
                )
                restore_resp = candidate
                try:
                    restored_successfully = (
                        candidate.status_code == 200
                        and candidate.json().get("success") is True
                    )
                except ValueError:
                    restored_successfully = False
                if restored_successfully:
                    break
            assert restore_resp is not None
            assert restore_resp.status_code == 200, response_diagnostic(restore_resp)
            assert restore_resp.json().get("success") is True, response_diagnostic(
                restore_resp
            )
            verify_resp = admin_api.get_json("/admin/system/cleanup/config")
            assert verify_resp.status_code == 200, response_diagnostic(verify_resp)
            restored = verify_resp.json().get("data")
            assert restored == original, (
                "cleanup 配置恢复后与原快照不一致："
                f"{safe_diagnostic({'expected': original, 'actual': restored})}"
            )
