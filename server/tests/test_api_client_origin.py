"""Unit evidence for browser-session Origin injection."""

from __future__ import annotations

from typing import Any

import pytest
import requests

from tests.utils.api import APIClient

pytestmark = pytest.mark.unit


def _response(status: int = 204) -> requests.Response:
    response = requests.Response()
    response.status_code = status
    response._content = b""
    response.headers["Content-Type"] = "application/json"
    return response


def test_browser_session_posts_use_configured_web_origin(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    client = APIClient(
        "https://api.example.test/api",
        browser_origin="https://web.example.test",
    )
    captured: list[dict[str, Any]] = []

    def capture_request(**kwargs: Any) -> requests.Response:
        captured.append(kwargs)
        return _response()

    monkeypatch.setattr(client.session, "request", capture_request)
    try:
        for path in (
            "/auth/register",
            "/auth/login",
            "/auth/refresh",
            "/auth/logout",
            "/auth/logout-all",
        ):
            client.request("POST", path, retry=False)
    finally:
        client.close()

    assert len(captured) == 5
    assert all(
        request["headers"] == {"Origin": "https://web.example.test"}
        for request in captured
    )
    assert all(
        request["headers"]["Origin"] != "https://api.example.test"
        for request in captured
    )


@pytest.mark.parametrize(
    ("headers", "expected"),
    [
        ({"Origin": "https://negative.example.test"}, "https://negative.example.test"),
        ({"origin": "null"}, "null"),
        ({"Origin": None}, None),
    ],
)
def test_explicit_browser_origin_header_overrides_automatic_origin(
    monkeypatch: pytest.MonkeyPatch,
    headers: dict[str, str | None],
    expected: str | None,
) -> None:
    client = APIClient(
        "http://localhost:8081/api",
        browser_origin="http://localhost:3000",
    )
    captured: list[dict[str, Any]] = []

    def capture_request(**kwargs: Any) -> requests.Response:
        captured.append(kwargs)
        return _response()

    monkeypatch.setattr(client.session, "request", capture_request)
    try:
        client.request("POST", "/auth/login", headers=headers, retry=False)
    finally:
        client.close()

    sent_headers = captured[0]["headers"]
    origin_key = next(key for key in sent_headers if key.lower() == "origin")
    assert sent_headers[origin_key] == expected
    assert len([key for key in sent_headers if key.lower() == "origin"]) == 1


@pytest.mark.parametrize(
    ("method", "path"),
    [
        ("GET", "/auth/login"),
        ("POST", "/auth/login-extra"),
        ("POST", "/platform/users"),
    ],
)
def test_non_browser_session_requests_do_not_gain_origin(
    monkeypatch: pytest.MonkeyPatch,
    method: str,
    path: str,
) -> None:
    client = APIClient(
        "http://localhost:8081/api",
        browser_origin="http://localhost:3000",
    )
    captured: list[dict[str, Any]] = []

    def capture_request(**kwargs: Any) -> requests.Response:
        captured.append(kwargs)
        return _response()

    monkeypatch.setattr(client.session, "request", capture_request)
    try:
        client.request(method, path, retry=False)
    finally:
        client.close()

    assert captured[0]["headers"] is None
