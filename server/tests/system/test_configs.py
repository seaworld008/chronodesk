"""System configuration integration tests."""

from __future__ import annotations

import io
import json
import time
from typing import Any
from urllib.parse import quote

import pytest
import requests

from tests.utils import (
    APIClient,
    assert_local_ephemeral_target,
    response_diagnostic,
    safe_diagnostic,
)
from tests.utils.safety import redact_text

_RESTORABLE_CONFIG_FIELDS = {
    "key",
    "value",
    "value_type",
    "description",
    "category",
    "group",
    "default_value",
    "is_required",
    "is_active",
}


def _export_configs(admin_api: APIClient) -> bytes:
    response = admin_api.get_json(
        "/admin/configs/export",
        params={"format": "json"},
    )
    assert response.status_code == 200, response_diagnostic(response)
    assert response.headers.get("Content-Type", "").startswith("application/json"), (
        "配置导出必须返回 JSON"
    )
    return response.content


def _import_configs(
    admin_api: APIClient,
    snapshot: bytes,
    *,
    operation: str,
) -> None:
    response = admin_api.post_multipart(
        "/admin/configs/import",
        files={
            "file": (
                "configs.json",
                io.BytesIO(snapshot),
                "application/json",
            )
        },
    )
    assert response.status_code == 200, f"{operation}: {response_diagnostic(response)}"
    assert response.json().get("success") is True, response_diagnostic(response)


def _config_state(snapshot: bytes) -> dict[str, dict[str, Any]]:
    try:
        payload = json.loads(snapshot)
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise AssertionError("配置导出不是有效 UTF-8 JSON") from exc
    assert isinstance(payload, list), "配置导出顶层必须为数组"

    state: dict[str, dict[str, Any]] = {}
    for item in payload:
        assert isinstance(item, dict), "配置导出数组元素必须为对象"
        key = item.get("key")
        assert isinstance(key, str) and key, "配置导出项缺少 key"
        assert key not in state, f"配置导出存在重复 key：{key}"
        state[key] = {
            field: item.get(field)
            for field in _RESTORABLE_CONFIG_FIELDS
            if field in item
        }
    return state


def _delete_config(admin_api: APIClient, key: str) -> None:
    response = admin_api.delete(f"/admin/configs/{quote(key, safe='')}")
    assert response.status_code in (200, 404), response_diagnostic(response)
    if response.status_code == 200:
        assert response.json().get("success") is True, response_diagnostic(response)


def _restore_config_snapshot(
    admin_api: APIClient,
    original_snapshot: bytes,
    original_state: dict[str, dict[str, Any]],
) -> None:
    """Best-effort every restore phase, then fail if any phase was incomplete."""

    errors: list[str] = []
    try:
        current_state = _config_state(_export_configs(admin_api))
    except (AssertionError, RuntimeError, requests.RequestException, ValueError) as exc:
        errors.append(f"无法读取待恢复配置：{redact_text(exc)}")
    else:
        for extra_key in sorted(current_state.keys() - original_state.keys()):
            try:
                _delete_config(admin_api, extra_key)
            except (
                AssertionError,
                RuntimeError,
                requests.RequestException,
                ValueError,
            ) as exc:
                errors.append(
                    f"删除本轮新增配置 {extra_key!r} 失败：{redact_text(exc)}"
                )

    try:
        _import_configs(
            admin_api,
            original_snapshot,
            operation="恢复全局配置原快照",
        )
    except (AssertionError, RuntimeError, requests.RequestException, ValueError) as exc:
        errors.append(f"回灌原配置快照失败：{redact_text(exc)}")

    try:
        restored_state = _config_state(_export_configs(admin_api))
    except (AssertionError, RuntimeError, requests.RequestException, ValueError) as exc:
        errors.append(f"无法复核恢复后的配置：{redact_text(exc)}")
    else:
        mismatched_keys = sorted(
            key
            for key in original_state.keys() | restored_state.keys()
            if original_state.get(key) != restored_state.get(key)
        )
        if mismatched_keys:
            errors.append("恢复后仍不一致的配置 key：" + ", ".join(mismatched_keys))

    if errors:
        raise AssertionError("全局配置可靠恢复失败：\n" + "\n".join(errors))


@pytest.mark.api
@pytest.mark.integration
class TestSystemConfigs:
    @pytest.fixture
    def config_payload(self) -> dict[str, object]:
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
        api_base_url: str,
        config_payload: dict[str, object],
    ) -> None:
        assert_local_ephemeral_target(
            api_base_url,
            "导入、初始化和修改全局系统配置",
        )
        original_snapshot = _export_configs(admin_api)
        original_state = _config_state(original_snapshot)

        list_resp = admin_api.get_json("/admin/configs", params={"page": 1})
        assert list_resp.status_code == 200, response_diagnostic(list_resp)
        assert list_resp.json().get("success") is True, response_diagnostic(list_resp)

        try:
            create_resp = admin_api.post_json("/admin/configs", config_payload)
            assert create_resp.status_code == 201, response_diagnostic(create_resp)
            created = create_resp.json().get("data", {})
            created_key = created.get("key")
            assert created_key == config_payload["key"], safe_diagnostic(created)

            encoded_key = quote(created_key, safe="")
            detail_resp = admin_api.get_json(f"/admin/configs/{encoded_key}")
            assert detail_resp.status_code == 200, response_diagnostic(detail_resp)
            detail_body = detail_resp.json()
            assert detail_body.get("success") is True, response_diagnostic(detail_resp)
            assert detail_body.get("data", {}).get("value") == config_payload["value"]

            update_payload = {
                **config_payload,
                "value": "pytest-updated",
                "description": "Updated via automated test.",
            }
            update_resp = admin_api.put_json(
                f"/admin/configs/{encoded_key}", update_payload
            )
            assert update_resp.status_code == 200, response_diagnostic(update_resp)
            assert (
                update_resp.json().get("data", {}).get("value")
                == update_payload["value"]
            )

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
            assert batch_resp.status_code == 200, response_diagnostic(batch_resp)
            assert batch_resp.json().get("data", {}).get("updated_count") == 1

            detail_resp = admin_api.get_json(f"/admin/configs/{encoded_key}")
            assert detail_resp.status_code == 200, response_diagnostic(detail_resp)
            value = detail_resp.json().get("data", {}).get("value")
            if isinstance(value, str):
                assert value.lower() == "true"
            else:
                assert value is True

            policy_resp = admin_api.get_json("/admin/configs/security-policy")
            assert policy_resp.status_code == 200, response_diagnostic(policy_resp)
            assert "password_policy" in policy_resp.json().get("data", {})

            exported_payload = _export_configs(admin_api)
            _import_configs(
                admin_api,
                exported_payload,
                operation="回灌本轮导出配置",
            )

            init_resp = admin_api.post_json("/admin/configs/init", {})
            assert init_resp.status_code == 200, response_diagnostic(init_resp)
            assert init_resp.json().get("success") is True, response_diagnostic(
                init_resp
            )
        finally:
            _restore_config_snapshot(
                admin_api,
                original_snapshot,
                original_state,
            )
