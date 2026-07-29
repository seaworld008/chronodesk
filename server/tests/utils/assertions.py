"""Assertions shared by Human REST black-box tests."""

from __future__ import annotations

import re
from collections.abc import Iterable
from typing import Any

import requests

from .safety import response_diagnostic, safe_diagnostic

_CHINESE_TEXT = re.compile(r"[\u3400-\u9fff]")
_FORBIDDEN_ERROR_KEYS = {
    "authorization",
    "backup_code",
    "backup_codes",
    "cookie",
    "database_url",
    "dsn",
    "password",
    "password_hash",
    "refresh_token",
    "access_token",
    "client_secret",
    "otp_secret",
    "storage_path",
    "stack",
    "stack_trace",
    "totp_seed",
    "totp_secret",
    "traceback",
}


def assert_error_contract(
    response: requests.Response,
    expected_status: int,
    *,
    machine_codes: Iterable[str] = (),
) -> dict[str, Any]:
    """Validate the stable minimum shared by existing Human REST errors.

    Human REST currently has several response envelopes. This assertion
    deliberately verifies their common public contract: exact HTTP status, a
    stable machine discriminator, Chinese operator feedback, JSON content, and
    no sensitive/debug fields.
    """

    assert response.status_code == expected_status, response_diagnostic(response)
    content_type = response.headers.get("Content-Type", "").split(";", 1)[0].strip()
    assert content_type in {"application/json", "application/problem+json"}, (
        f"unexpected Content-Type {content_type!r}"
    )
    body = response.json()
    assert isinstance(body, dict), safe_diagnostic(body)

    discriminators = [
        value
        for value in (body.get("error"), body.get("code"), body.get("msg"))
        if isinstance(value, (str, int))
    ]
    assert discriminators, safe_diagnostic(body)

    expected_codes = {str(code) for code in machine_codes}
    if expected_codes:
        observed = {str(value) for value in discriminators}
        assert observed & expected_codes, (
            f"expected one of {sorted(expected_codes)}, observed {sorted(observed)}: "
            f"{safe_diagnostic(body)}"
        )

    message_values = [
        value
        for value in (
            body.get("message"),
            body.get("detail"),
            body.get("msg"),
            body.get("data"),
            body.get("error"),
        )
        if isinstance(value, str)
    ]
    assert any(_CHINESE_TEXT.search(value) for value in message_values), (
        f"error response lacks Chinese operator feedback: {safe_diagnostic(body)}"
    )
    _assert_no_forbidden_keys(body)
    return body


def assert_no_sensitive_fields(value: Any) -> None:
    """Ensure a public response does not expose security or storage internals."""

    _assert_no_forbidden_keys(value)


def _assert_no_forbidden_keys(value: Any) -> None:
    if isinstance(value, dict):
        lowered = {str(key).lower() for key in value}
        leaked = lowered & _FORBIDDEN_ERROR_KEYS
        assert not leaked, (
            f"response exposed forbidden fields {sorted(leaked)}: "
            f"{safe_diagnostic(value)}"
        )
        for nested in value.values():
            _assert_no_forbidden_keys(nested)
    elif isinstance(value, list):
        for nested in value:
            _assert_no_forbidden_keys(nested)
