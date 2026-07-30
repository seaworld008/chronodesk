"""Lightweight API client utilities for integration tests."""

from __future__ import annotations

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

    def __post_init__(self) -> None:
        # remove trailing slash for consistency
        self.base_url = self.base_url.rstrip("/")
        if self.project_key is not None:
            self.bind_project(self.project_key)
        self.session = requests.Session()
        self.session.headers.update({"Content-Type": "application/json"})

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
    ) -> requests.Response:
        url = self._build_url(path)
        attempt = 0
        last_exc: Exception | None = None
        register_headers(self.session.headers)
        register_headers(headers)
        register_sensitive_values(json)
        register_sensitive_values(data)
        register_sensitive_values(params)

        while attempt < self.max_retries:
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
                    self.max_retries,
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
    ) -> requests.Response:
        return self.request("POST", path, json=payload, headers=headers)

    def get_json(
        self,
        path: str,
        *,
        headers: dict[str, str] | None = None,
        params: dict[str, Any] | None = None,
    ) -> requests.Response:
        return self.request("GET", path, headers=headers, params=params)

    def put_json(
        self,
        path: str,
        payload: dict[str, Any],
        *,
        headers: dict[str, str] | None = None,
    ) -> requests.Response:
        return self.request("PUT", path, json=payload, headers=headers)

    def delete(
        self,
        path: str,
        *,
        headers: dict[str, str] | None = None,
        expected_status: int | None = None,
    ) -> requests.Response:
        return self.request(
            "DELETE", path, headers=headers, expected_status=expected_status
        )

    def post_multipart(
        self,
        path: str,
        *,
        headers: Mapping[str, str] | None = None,
        fields: Mapping[str, Any] | None = None,
        files: Mapping[str, Any] | None = None,
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
        clone = APIClient(
            base_url=self.base_url,
            timeout=self.timeout,
            max_retries=self.max_retries,
            retry_delay=self.retry_delay,
            project_key=self.project_key,
        )
        clone.session.headers.update(self.session.headers)
        clone.session.headers["Authorization"] = f"Bearer {token}"
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
        return data["data"]

    def refresh(self, refresh_token: str) -> dict[str, Any]:
        response = self.post_json("/auth/refresh", {"refresh_token": refresh_token})
        if response.status_code != 200:
            raise APIError("Refresh token failed", response=response)
        return response.json().get("data", {})

    def register_user(self, payload: dict[str, Any]) -> dict[str, Any]:
        response = self.post_json("/auth/register", payload)
        if response.status_code not in (200, 201):
            raise APIError("Registration failed", response=response)

        data = response.json()
        if data.get("code") != 0 or "data" not in data:
            raise APIError("Unexpected registration payload", response=response)
        return data["data"]

    def logout(self, refresh_token: str | None = None) -> dict[str, Any]:
        payload: dict[str, Any] = {}
        if refresh_token:
            payload["refresh_token"] = refresh_token

        response = self.post_json("/auth/logout", payload)
        if response.status_code != 200:
            raise APIError("Logout failed", response=response)

        body = response.json()
        if not body.get("success"):
            raise APIError("Unexpected logout response", response=response)
        return body

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------
    def _build_url(self, path: str) -> str:
        if not path.startswith("/"):
            path = f"/{path}"
        return f"{self.base_url}{path}"
