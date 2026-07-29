"""Unit evidence for black-box target gates and report redaction."""

from __future__ import annotations

import html
import json
from pathlib import Path
from types import SimpleNamespace

import pytest
import requests

from tests.utils import safety

pytestmark = pytest.mark.unit


def test_redaction_covers_structured_headers_tokens_otp_passwords_and_dsns() -> None:
    secrets = {
        "access": "access-value-123456",
        "refresh": "refresh-value-123456",
        "totp": "JBSWY3DPEHPK3PXP",
        "backup": "backup-123456",
        "cookie": "cookie-value-123456",
        "authorization": "authorization-value-123456",
        "client": "client-value-123456",
        "password": "Password-value-123456!",
        "dsn": "postgresql://db-user:db-pass@example.invalid/chronodesk",
    }
    payload = {
        "access_token": secrets["access"],
        "refresh_token": secrets["refresh"],
        "totp_seed": secrets["totp"],
        "backup_codes": [secrets["backup"]],
        "Cookie": f"session={secrets['cookie']}",
        "Authorization": f"Bearer {secrets['authorization']}",
        "client_secret": secrets["client"],
        "password": secrets["password"],
        "database_dsn": secrets["dsn"],
    }
    safety.register_sensitive_values(payload)
    rendered = safety.safe_diagnostic(payload)
    free_form = safety.redact_text(
        f"Authorization: Bearer {secrets['authorization']}\n"
        f"Cookie: session={secrets['cookie']}\n"
        f"dsn={secrets['dsn']}"
    )

    assert all(secret not in rendered for secret in secrets.values())
    assert all(secret not in free_form for secret in secrets.values())
    assert rendered.count(safety.REDACTED) >= len(payload)
    assert "<redacted-dsn>" in free_form or safety.REDACTED in free_form


def test_response_and_pytest_report_redaction_remove_registered_runtime_values(
    tmp_path: Path,
) -> None:
    access_token = "runtime-access-token-123456"
    refresh_token = "runtime-refresh-token-123456"
    response = requests.Response()
    response.status_code = 401
    response.headers.update(
        {
            "Content-Type": "application/json",
            "Set-Cookie": "trusted=runtime-cookie-123456; HttpOnly",
        }
    )
    response._content = json.dumps(
        {
            "access_token": access_token,
            "refresh_token": refresh_token,
        }
    ).encode()

    diagnostic = safety.response_diagnostic(response)
    report = SimpleNamespace(
        longrepr=(
            "test_file.py",
            10,
            f"{access_token} Cookie: trusted=runtime-cookie-123456",
        ),
        sections=[
            (
                "Captured log",
                f"refresh_token={refresh_token}",
            )
        ],
        user_properties=[("Authorization", f"Bearer {access_token}")],
    )
    safety.sanitize_pytest_report(report)
    serialized_report = repr(report)
    html_report = tmp_path / "report.html"
    html_report.write_text(
        f"<html>{html.escape(access_token)} runtime-cookie-123456</html>",
        encoding="utf-8",
    )
    assert safety.scrub_html_report(html_report) is True
    scrubbed_html = html_report.read_text(encoding="utf-8")

    for secret in (
        access_token,
        refresh_token,
        "runtime-cookie-123456",
    ):
        assert secret not in diagnostic
        assert secret not in serialized_report
        assert secret not in scrubbed_html


def test_remote_target_requires_explicit_isolation_and_ownership_prefix() -> None:
    remote = "https://e2e.example.invalid/api"

    with pytest.raises(safety.TestSafetyError, match="拒绝对非回环目标"):
        safety.validate_test_target(remote, {})
    with pytest.raises(safety.TestSafetyError, match="拒绝对非回环目标"):
        safety.validate_test_target(
            remote,
            {safety.REMOTE_E2E_ENV: "1"},
        )
    with pytest.raises(safety.TestSafetyError, match="必须以 e2e- 开头"):
        safety.validate_test_target(
            remote,
            {
                safety.REMOTE_E2E_ENV: "1",
                safety.OWNERSHIP_PREFIX_ENV: "shared",
            },
        )

    target = safety.validate_test_target(
        remote,
        {
            safety.REMOTE_E2E_ENV: "1",
            safety.OWNERSHIP_PREFIX_ENV: "e2e-ci-owner-1234",
        },
    )
    assert target.is_loopback is False
    assert target.ownership_prefix == "e2e-ci-owner-1234"


def test_api_target_rejects_deployment_path_prefixes() -> None:
    with pytest.raises(safety.TestSafetyError, match="/api 根地址"):
        safety.validate_test_target("http://localhost:8081/prefix/api", {})

    target = safety.validate_test_target("http://localhost:8081/api/", {})
    assert target.base_url == "http://localhost:8081/api"


def test_global_mutation_requires_explicit_ephemeral_loopback() -> None:
    local = "http://127.0.0.1:8081/api"
    with pytest.raises(safety.TestSafetyError, match="仅回环一次性环境"):
        safety.assert_local_ephemeral_target(local, "修改全局配置", {})

    safety.assert_local_ephemeral_target(
        local,
        "修改全局配置",
        {safety.EPHEMERAL_E2E_ENV: "1"},
    )

    with pytest.raises(safety.TestSafetyError, match="仅回环一次性环境"):
        safety.assert_local_ephemeral_target(
            "https://e2e.example.invalid/api",
            "修改全局配置",
            {
                safety.REMOTE_E2E_ENV: "1",
                safety.OWNERSHIP_PREFIX_ENV: "e2e-ci-owner-1234",
                safety.EPHEMERAL_E2E_ENV: "1",
            },
        )
