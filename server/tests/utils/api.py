"""Lightweight API client utilities for integration tests."""

from __future__ import annotations

import base64
import binascii
import json
import logging
import os
import re
import time
from collections.abc import Mapping
from dataclasses import dataclass
from typing import Any

import requests

from .safety import (
    register_headers,
    register_response_secrets,
    register_secret,
    register_sensitive_values,
    response_diagnostic,
)

logger = logging.getLogger(__name__)

DEFAULT_TIMEOUT = int(os.getenv("TEST_REQUEST_TIMEOUT", "15"))
DEFAULT_MAX_RETRIES = int(os.getenv("TEST_REQUEST_MAX_RETRIES", "3"))
DEFAULT_RETRY_DELAY = float(os.getenv("TEST_REQUEST_RETRY_DELAY", "1.0"))
DEFAULT_BROWSER_ORIGIN = os.getenv("TEST_WEB_ORIGIN", "http://localhost:3000")
PROJECT_KEY_PATTERN = re.compile(r"^[A-Z][A-Z0-9_-]{0,31}$")
PROJECT_PATH_SEGMENT_PATTERN = re.compile(r"^[A-Za-z0-9._:-]+$")


def validate_project_key(project_key: str) -> str:
    if PROJECT_KEY_PATTERN.fullmatch(project_key) is None:
        raise AssertionError("项目键格式无效")
    return project_key


class APIError(RuntimeError):
    """Simple exception wrapper for HTTP errors."""

    def __init__(self, message: str, response: requests.Response | None = None) -> None:
        super().__init__(message)
        self.response = response


@dataclass
class APIClient:
    base_url: str
    timeout: int = DEFAULT_TIMEOUT
    max_retries: int = DEFAULT_MAX_RETRIES
    retry_delay: float = DEFAULT_RETRY_DELAY
    project_key: str | None = None
    browser_origin: str = DEFAULT_BROWSER_ORIGIN

    def __post_init__(self) -> None:
        # remove trailing slash for consistency
        self.base_url = self.base_url.rstrip("/")
        if self.project_key is not None:
            self.bind_project(self.project_key)
        self.session = requests.Session()
        self.session.headers.update({"Content-Type": "application/json"})
        self._human_session_id: str | None = None

    # ------------------------------------------------------------------
    # Core request helper
    # ------------------------------------------------------------------
    def request(
        self,
        method: str,
        path: str,
        *,
        headers: Mapping[str, str | None] | None = None,
        json: Any | None = None,
        params: dict[str, Any] | None = None,
        data: Mapping[str, Any] | None = None,
        files: Mapping[str, Any] | None = None,
        expected_status: int | None = None,
        retry: bool = True,
    ) -> requests.Response:
        url = self._build_url(path)
        attempt = 0
        max_attempts = self.max_retries if retry else 1
        last_exc: Exception | None = None
        register_headers(self.session.headers)
        register_headers(headers)
        register_sensitive_values(json)
        register_sensitive_values(data)
        register_sensitive_values(params)

        while attempt < max_attempts:
            try:
                response = self.session.request(
                    method=method,
                    url=url,
                    headers=headers,
                    json=json,
                    params=params,
                    data=data,
                    files=files,
                    timeout=self.timeout,
                )
                register_response_secrets(response)

                if (
                    expected_status is not None
                    and response.status_code != expected_status
                ):
                    raise APIError(
                        f"Unexpected status {response.status_code} (expected {expected_status})",
                        response=response,
                    )
                return response
            except (requests.ConnectionError, requests.Timeout) as exc:
                last_exc = exc
                attempt += 1
                logger.warning(
                    "Request %s %s failed (attempt %s/%s): %s",
                    method,
                    url,
                    attempt,
                    max_attempts,
                    exc,
                )
                time.sleep(self.retry_delay)

        raise APIError(
            f"Request {method} {url} failed after retries", response=None
        ) from last_exc

    # ------------------------------------------------------------------
    # Convenience helpers
    # ------------------------------------------------------------------
    def post_json(
        self,
        path: str,
        payload: dict[str, Any],
        *,
        headers: dict[str, str] | None = None,
        retry: bool = True,
    ) -> requests.Response:
        return self.request(
            "POST",
            path,
            json=payload,
            headers=headers,
            retry=retry,
        )

    def get_json(
        self,
        path: str,
        *,
        headers: dict[str, str] | None = None,
        params: dict[str, Any] | None = None,
        retry: bool = True,
    ) -> requests.Response:
        return self.request(
            "GET",
            path,
            headers=headers,
            params=params,
            retry=retry,
        )

    def put_json(
        self,
        path: str,
        payload: dict[str, Any],
        *,
        headers: dict[str, str] | None = None,
        retry: bool = True,
    ) -> requests.Response:
        return self.request(
            "PUT",
            path,
            json=payload,
            headers=headers,
            retry=retry,
        )

    def delete(
        self,
        path: str,
        *,
        headers: dict[str, str] | None = None,
        params: dict[str, Any] | None = None,
        expected_status: int | None = None,
        retry: bool = True,
    ) -> requests.Response:
        return self.request(
            "DELETE",
            path,
            headers=headers,
            params=params,
            expected_status=expected_status,
            retry=retry,
        )

    def post_multipart(
        self,
        path: str,
        *,
        headers: Mapping[str, str] | None = None,
        fields: Mapping[str, Any] | None = None,
        files: Mapping[str, Any] | None = None,
        retry: bool = True,
    ) -> requests.Response:
        # A request-level None removes the JSON Content-Type inherited from the
        # session so requests can generate the multipart boundary safely.
        request_headers: dict[str, str | None] = {"Content-Type": None}
        request_headers.update(headers or {})
        return self.request(
            "POST",
            path,
            headers=request_headers,
            data=fields,
            files=files,
            retry=retry,
        )

    def bind_project(self, project_key: str) -> None:
        """Bind project-scoped Human REST helpers to one canonical project."""

        self.project_key = validate_project_key(project_key)

    def project_path(self, suffix: str) -> str:
        """Return a Human REST path under the client's explicit project."""

        project_key = self.project_key
        if project_key is None:
            raise AssertionError("APIClient 尚未绑定项目")
        normalized = suffix.strip("/")
        segments = normalized.split("/")
        if not normalized or any(
            segment in {".", ".."}
            or PROJECT_PATH_SEGMENT_PATTERN.fullmatch(segment) is None
            for segment in segments
        ):
            raise AssertionError("项目资源路径包含非法段")
        return f"/projects/{project_key}/{normalized}"

    def ticket_etag(self, ticket_id: int) -> str:
        assert isinstance(ticket_id, int) and ticket_id > 0
        response = self.get_json(self.project_path(f"tickets/{ticket_id}"))
        assert response.status_code == 200, response_diagnostic(response)
        etag = response.headers.get("ETag")
        assert isinstance(etag, str) and etag.startswith('"v') and etag.endswith('"'), (
            f"ticket {ticket_id} response lacks a strong ETag"
        )
        return etag

    def put_ticket(
        self,
        ticket_id: int,
        payload: dict[str, Any],
        *,
        etag: str | None = None,
    ) -> requests.Response:
        validator = etag or self.ticket_etag(ticket_id)
        return self.put_json(
            self.project_path(f"tickets/{ticket_id}"),
            payload,
            headers={"If-Match": validator},
        )

    def post_ticket_command(
        self,
        ticket_id: int,
        command: str,
        payload: dict[str, Any],
        *,
        etag: str | None = None,
    ) -> requests.Response:
        assert command in {"assign", "transfer", "escalate", "status"}
        validator = etag or self.ticket_etag(ticket_id)
        return self.post_json(
            self.project_path(f"tickets/{ticket_id}/{command}"),
            payload,
            headers={"If-Match": validator},
        )

    def delete_ticket(
        self,
        ticket_id: int,
        *,
        etag: str | None = None,
    ) -> requests.Response:
        assert isinstance(ticket_id, int) and ticket_id > 0
        validator = etag or self.ticket_etag(ticket_id)
        return self.delete(
            self.project_path(f"tickets/{ticket_id}"),
            headers={"If-Match": validator},
        )

    def with_auth(self, token: str) -> APIClient:
        register_secret(token)
        clone = self.clone()
        clone.session.headers["Authorization"] = f"Bearer {token}"
        clone._human_session_id = self._access_token_session_id(token)
        return clone

    def clone(self, *, include_cookies: bool = True) -> APIClient:
        clone = APIClient(
            base_url=self.base_url,
            timeout=self.timeout,
            max_retries=self.max_retries,
            retry_delay=self.retry_delay,
            project_key=self.project_key,
            browser_origin=self.browser_origin,
        )
        clone.session.headers.update(self.session.headers)
        if include_cookies:
            clone.session.cookies.update(self.session.cookies)
        clone._human_session_id = self._human_session_id
        return clone

    def close(self) -> None:
        self.session.close()

    # ------------------------------------------------------------------
    # Authentication helpers
    # ------------------------------------------------------------------
    def login(self, email: str, password: str, **extra_fields: Any) -> dict[str, Any]:
        payload: dict[str, Any] = {
            "email": email,
            "password": password,
        }
        payload.update(extra_fields)

        response = self.post_json("/auth/login", payload)
        if response.status_code != 200:
            raise APIError("Login failed", response=response)

        data = response.json()
        if data.get("code") != 0 or "data" not in data:
            raise APIError("Unexpected login response payload", response=response)
        result = data["data"]
        self._remember_human_session(result)
        return result

    def refresh(self) -> dict[str, Any]:
        response = self.request(
            "POST",
            "/auth/refresh",
            headers={"Origin": self.browser_origin},
        )
        if response.status_code != 200:
            raise APIError("Refresh token failed", response=response)
        result = response.json().get("data", {})
        self._remember_human_session(result)
        return result

    def register_user(self, payload: dict[str, Any]) -> dict[str, Any]:
        response = self.post_json("/auth/register", payload)
        if response.status_code not in (200, 201):
            raise APIError("Registration failed", response=response)

        data = response.json()
        if data.get("code") != 0 or "data" not in data:
            raise APIError("Unexpected registration payload", response=response)
        result = data["data"]
        self._remember_human_session(result)
        return result

    def logout(self) -> dict[str, Any]:
        if self._human_session_id is None:
            raise APIError("Logout requires a committed human session")
        response = self.request(
            "POST",
            "/auth/logout",
            headers={
                "Origin": self.browser_origin,
                "X-Chronodesk-Session-ID": self._human_session_id,
            },
        )
        if response.status_code != 200:
            raise APIError("Logout failed", response=response)

        body = response.json()
        if not body.get("success"):
            raise APIError("Unexpected logout response", response=response)
        self._human_session_id = None
        return body

    def _remember_human_session(self, data: Any) -> None:
        if not isinstance(data, Mapping):
            return
        access_token = data.get("access_token")
        if not isinstance(access_token, str) or not access_token:
            return
        self._human_session_id = self._access_token_session_id(access_token)

    @staticmethod
    def _access_token_session_id(token: str) -> str:
        parts = token.split(".")
        if len(parts) != 3:
            raise APIError("Human access token does not contain a stable session")
        try:
            encoded = parts[1] + "=" * (-len(parts[1]) % 4)
            payload = json.loads(
                base64.urlsafe_b64decode(encoded.encode("ascii")).decode("utf-8")
            )
        except (
            ValueError,
            UnicodeError,
            json.JSONDecodeError,
            binascii.Error,
        ) as exc:
            raise APIError(
                "Human access token does not contain a stable session"
            ) from exc
        session_id = payload.get("sid") if isinstance(payload, dict) else None
        if not isinstance(session_id, str) or not session_id or len(session_id) > 128:
            raise APIError("Human access token does not contain a stable session")
        return session_id

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------
    def _build_url(self, path: str) -> str:
        if not path.startswith("/"):
            path = f"/{path}"
        return f"{self.base_url}{path}"
