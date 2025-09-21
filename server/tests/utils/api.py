"""Lightweight API client utilities for integration tests."""

from __future__ import annotations

import logging
import os
import time
from dataclasses import dataclass
from typing import Any, Dict, Optional

import requests


logger = logging.getLogger(__name__)

DEFAULT_TIMEOUT = int(os.getenv("TEST_REQUEST_TIMEOUT", "15"))
DEFAULT_MAX_RETRIES = int(os.getenv("TEST_REQUEST_MAX_RETRIES", "3"))
DEFAULT_RETRY_DELAY = float(os.getenv("TEST_REQUEST_RETRY_DELAY", "1.0"))


class APIError(RuntimeError):
    """Simple exception wrapper for HTTP errors."""

    def __init__(self, message: str, response: Optional[requests.Response] = None) -> None:
        super().__init__(message)
        self.response = response


@dataclass
class APIClient:
    base_url: str
    timeout: int = DEFAULT_TIMEOUT
    max_retries: int = DEFAULT_MAX_RETRIES
    retry_delay: float = DEFAULT_RETRY_DELAY

    def __post_init__(self) -> None:
        # remove trailing slash for consistency
        self.base_url = self.base_url.rstrip("/")
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
        headers: Optional[Dict[str, str]] = None,
        json: Optional[Dict[str, Any]] = None,
        params: Optional[Dict[str, Any]] = None,
        expected_status: Optional[int] = None,
    ) -> requests.Response:
        url = self._build_url(path)
        attempt = 0
        last_exc: Optional[Exception] = None

        while attempt < self.max_retries:
            try:
                response = self.session.request(
                    method=method,
                    url=url,
                    headers=headers,
                    json=json,
                    params=params,
                    timeout=self.timeout,
                )

                if expected_status is not None and response.status_code != expected_status:
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

        raise APIError(f"Request {method} {url} failed after retries", response=None) from last_exc

    # ------------------------------------------------------------------
    # Convenience helpers
    # ------------------------------------------------------------------
    def post_json(self, path: str, payload: Dict[str, Any], *, headers: Optional[Dict[str, str]] = None) -> requests.Response:
        return self.request("POST", path, json=payload, headers=headers)

    def get_json(self, path: str, *, headers: Optional[Dict[str, str]] = None, params: Optional[Dict[str, Any]] = None) -> requests.Response:
        return self.request("GET", path, headers=headers, params=params)

    def put_json(self, path: str, payload: Dict[str, Any], *, headers: Optional[Dict[str, str]] = None) -> requests.Response:
        return self.request("PUT", path, json=payload, headers=headers)

    def delete(self, path: str, *, headers: Optional[Dict[str, str]] = None, expected_status: Optional[int] = None) -> requests.Response:
        return self.request("DELETE", path, headers=headers, expected_status=expected_status)

    def with_auth(self, token: str) -> "APIClient":
        clone = APIClient(
            base_url=self.base_url,
            timeout=self.timeout,
            max_retries=self.max_retries,
            retry_delay=self.retry_delay,
        )
        clone.session.headers.update(self.session.headers)
        clone.session.headers["Authorization"] = f"Bearer {token}"
        return clone

    def close(self) -> None:
        self.session.close()

    # ------------------------------------------------------------------
    # Authentication helpers
    # ------------------------------------------------------------------
    def login(self, email: str, password: str, **extra_fields: Any) -> Dict[str, Any]:
        payload: Dict[str, Any] = {
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

    def refresh(self, refresh_token: str) -> Dict[str, Any]:
        response = self.post_json("/auth/refresh", {"refresh_token": refresh_token})
        if response.status_code != 200:
            raise APIError("Refresh token failed", response=response)
        return response.json().get("data", {})

    def register_user(self, payload: Dict[str, Any]) -> Dict[str, Any]:
        response = self.post_json("/auth/register", payload)
        if response.status_code not in (200, 201):
            raise APIError("Registration failed", response=response)

        data = response.json()
        if data.get("code") != 0 or "data" not in data:
            raise APIError("Unexpected registration payload", response=response)
        return data["data"]

    def logout(self, refresh_token: Optional[str] = None) -> Dict[str, Any]:
        payload: Dict[str, Any] = {}
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
