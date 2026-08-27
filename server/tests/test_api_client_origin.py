"""Unit evidence for browser-session Origin injection."""

from __future__ import annotations

import base64
import json
from typing import Any

import pytest
import requests

from tests.utils.api import APIClient, APIError

pytestmark = pytest.mark.unit


def _response(status: int = 204, payload: Any | None = None) -> requests.Response:
    response = requests.Response()
    response.status_code = status
    response.headers["Content-Type"] = "application/json"
    response._content = b"" if payload is None else json.dumps(payload).encode("utf-8")
    return response


def _access_token(session_id: str) -> str:
    payload = base64.urlsafe_b64encode(
        json.dumps({"sid": session_id}).encode("utf-8")
    ).rstrip(b"=")
    return f"header.{payload.decode('ascii')}.signature"


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


@pytest.mark.parametrize(
    ("path", "payload"),
    [
        (
            "/auth/login",
            {"code": 0, "data": {"access_token": _access_token("login-session")}},
        ),
        (
            "/auth/register",
            {
                "code": 0,
                "data": {"access_token": _access_token("registration-session")},
            },
        ),
        (
            "/auth/refresh",
            {
                "success": True,
                "data": {"access_token": _access_token("refresh-session")},
            },
        ),
    ],
)
def test_direct_successful_browser_session_response_commits_session_id(
    monkeypatch: pytest.MonkeyPatch,
    path: str,
    payload: dict[str, Any],
) -> None:
    client = APIClient("http://localhost:8081/api")
    monkeypatch.setattr(
        client.session,
        "request",
        lambda **_kwargs: _response(200, payload),
    )
    try:
        client.post_json(path, {})
        session_id = payload["data"]["access_token"]
        assert client._human_session_id == APIClient._access_token_session_id(
            session_id
        )
    finally:
        client.close()


def test_direct_login_can_use_logout_helper_with_committed_session(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    client = APIClient("http://localhost:8081/api")
    captured: list[dict[str, Any]] = []
    responses = iter(
        (
            _response(
                200,
                {
                    "code": 0,
                    "data": {"access_token": _access_token("direct-login-session")},
                },
            ),
            _response(200, {"success": True}),
        )
    )

    def capture_request(**kwargs: Any) -> requests.Response:
        captured.append(kwargs)
        return next(responses)

    monkeypatch.setattr(client.session, "request", capture_request)
    try:
        client.post_json(
            "/auth/login",
            {
                "email": "browser-user@example.test",
                "password": "test-password-sentinel",
            },
        )
        client.logout()
    finally:
        client.close()

    assert captured[1]["headers"] == {
        "Origin": "http://localhost:3000",
        "X-Chronodesk-Session-ID": "direct-login-session",
    }
    assert client._human_session_id is None


@pytest.mark.parametrize(
    ("status", "payload"),
    [
        (
            401,
            {"code": 0, "data": {"access_token": _access_token("failed-session")}},
        ),
        (
            200,
            {"code": 1, "data": {"access_token": _access_token("failed-envelope")}},
        ),
        (200, {"code": 0, "data": "not-a-session"}),
    ],
)
def test_failed_or_malformed_login_response_does_not_replace_session(
    monkeypatch: pytest.MonkeyPatch,
    status: int,
    payload: dict[str, Any],
) -> None:
    client = APIClient("http://localhost:8081/api")
    client._human_session_id = "existing-session"
    monkeypatch.setattr(
        client.session,
        "request",
        lambda **_kwargs: _response(status, payload),
    )
    try:
        client.post_json("/auth/login", {})
        assert client._human_session_id == "existing-session"
    finally:
        client.close()


def test_invalid_success_token_fails_closed_without_replacing_session(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    client = APIClient("http://localhost:8081/api")
    client._human_session_id = "existing-session"
    monkeypatch.setattr(
        client.session,
        "request",
        lambda **_kwargs: _response(
            200,
            {
                "code": 0,
                "data": {"access_token": "malformed-session-sentinel"},
            },
        ),
    )
    try:
        with pytest.raises(APIError, match="stable session"):
            client.post_json("/auth/login", {})
        assert client._human_session_id == "existing-session"
    finally:
        client.close()


@pytest.mark.parametrize(
    ("path", "status", "payload", "expected"),
    [
        ("/auth/logout", 200, {"success": True}, None),
        ("/auth/logout-all", 200, {"success": True}, None),
        ("/auth/logout", 401, {"success": True}, "existing-session"),
        ("/auth/logout", 200, {"success": False}, "existing-session"),
        ("/auth/logout", 200, {"unexpected": True}, "existing-session"),
    ],
)
def test_logout_response_clears_only_a_committed_success(
    monkeypatch: pytest.MonkeyPatch,
    path: str,
    status: int,
    payload: dict[str, Any],
    expected: str | None,
) -> None:
    client = APIClient("http://localhost:8081/api")
    client._human_session_id = "existing-session"
    monkeypatch.setattr(
        client.session,
        "request",
        lambda **_kwargs: _response(status, payload),
    )
    try:
        client.post_json(path, {})
        assert client._human_session_id == expected
    finally:
        client.close()
