"""Pytest configuration & shared fixtures."""

from __future__ import annotations

import os
import secrets
import time
from typing import Dict

import pytest
import requests

from .utils import APIClient, APIError


def pytest_addoption(parser: pytest.Parser) -> None:
    parser.addoption(
        "--api-base-url",
        action="store",
        default=None,
        help="Target API base url (default: http://localhost:8081/api)",
    )


@pytest.fixture(scope="session")
def api_base_url(pytestconfig: pytest.Config) -> str:
    option = pytestconfig.getoption("--api-base-url")
    return option or os.getenv("TEST_API_BASE_URL", "http://localhost:8081/api")


@pytest.fixture(scope="session")
def api_client(api_base_url: str) -> APIClient:
    client = APIClient(api_base_url)
    yield client
    client.close()


@pytest.fixture(scope="session", autouse=True)
def _ensure_api_available(api_base_url: str) -> None:
    """Skip test session early if API 未启动."""
    health_url = os.getenv("TEST_HEALTHCHECK_URL")
    if not health_url:
        # 假设 api_base_url 以 /api 结尾
        base_host = api_base_url.rsplit("/api", 1)[0]
        health_url = f"{base_host}/health"

    try:
        response = requests.get(health_url, timeout=5)
    except requests.RequestException as exc:
        pytest.skip(f"API 未启动或无法连接: {exc}")
    else:
        if response.status_code >= 500:
            pytest.skip(f"API 健康检查失败: HTTP {response.status_code}")


@pytest.fixture(scope="session")
def admin_credentials() -> Dict[str, str]:
    return {
        "email": os.getenv("TEST_ADMIN_EMAIL", "manager@tickets.com"),
        "password": os.getenv("TEST_ADMIN_PASSWORD", "SecureTicket2025!@#$"),
    }


@pytest.fixture(scope="session")
def admin_tokens(api_client: APIClient, admin_credentials: Dict[str, str]) -> Dict[str, str]:
    try:
        payload = api_client.login(admin_credentials["email"], admin_credentials["password"])
    except APIError as exc:
        response = exc.response
        detail = response.text if response is not None else str(exc)
        pytest.skip(f"管理员登录失败，无法运行依赖测试: {detail}")
    return payload


@pytest.fixture(scope="session")
def admin_api(api_client: APIClient, admin_tokens: Dict[str, str]) -> APIClient:
    token = admin_tokens.get("access_token")
    if not token:
        pytest.skip("管理员登录响应缺少 access_token")

    authed_client = api_client.with_auth(token)
    yield authed_client
    authed_client.close()


@pytest.fixture
def registered_user(api_client: APIClient) -> Dict[str, str]:
    last_error: APIError | None = None
    for attempt in range(3):
        timestamp = int(time.time_ns())
        email = f"auth_test+{timestamp}@example.com"
        username = f"auth_user_{timestamp}"
        password = _generate_strong_password()

        payload = {
            "username": username,
            "email": email,
            "password": password,
            "confirm_password": password,
            "first_name": "Auth",
            "last_name": "Tester",
            "department": "QA Automation",
            "position": "Integration",
        }

        try:
            data = api_client.register_user(payload)
            break
        except APIError as exc:
            last_error = exc
            time.sleep(0.05)
    else:
        assert last_error is not None
        response = last_error.response
        detail = response.text if response is not None else str(last_error)
        pytest.fail(f"Failed to register test user: {detail}")

    return {
        "email": email,
        "password": password,
        "access_token": data.get("access_token"),
        "refresh_token": data.get("refresh_token"),
        "user": data.get("user", {}),
    }


def _generate_strong_password() -> str:
    """Generate a password satisfying policy without triple repeats."""

    while True:
        token = secrets.token_hex(6)
        password = f"Aa1!{token}Z"
        if all(password[i] != password[i + 1] or password[i] != password[i + 2] for i in range(len(password) - 2)):
            return password
