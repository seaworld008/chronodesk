"""Pytest configuration & shared fixtures."""

from __future__ import annotations

import os
import secrets
import time
from collections.abc import Iterator, Mapping
from typing import Any

import pytest
import requests

from .utils import (
    PLATFORM_ROLES,
    PROJECT_ROLES,
    APIClient,
    APIError,
    E2EResourceManager,
    HumanIdentity,
    assert_human_session_contract,
)
from .utils.api import validate_project_key
from .utils.human import (
    active_project_membership_version,
    cleanup_project_membership,
)
from .utils.safety import (
    TestSafetyError,
    TestTarget,
    healthcheck_url_for,
    redact_text,
    register_environment_secrets,
    response_diagnostic,
    safe_diagnostic,
    sanitize_pytest_report,
    scrub_html_report,
    validate_test_target,
)

_TEST_TARGET = pytest.StashKey[TestTarget]()
_COLLECTED_ITEMS = pytest.StashKey[list[pytest.Item]]()


def pytest_addoption(parser: pytest.Parser) -> None:
    parser.addoption(
        "--api-base-url",
        action="store",
        default=None,
        help="Target API base url (default: http://localhost:8081/api)",
    )


def pytest_configure(config: pytest.Config) -> None:
    """Validate the target before collection can execute imported test code."""

    register_environment_secrets()
    configured = config.getoption("--api-base-url") or os.getenv(
        "TEST_API_BASE_URL", "http://localhost:8081/api"
    )
    try:
        config.stash[_TEST_TARGET] = validate_test_target(configured)
    except TestSafetyError as exc:
        raise pytest.UsageError(redact_text(exc)) from exc


def pytest_collection_modifyitems(
    config: pytest.Config,
    items: list[pytest.Item],
) -> None:
    config.stash[_COLLECTED_ITEMS] = items


@pytest.hookimpl(hookwrapper=True, tryfirst=True)
def pytest_runtest_makereport(
    item: pytest.Item,
    call: pytest.CallInfo[None],
) -> Iterator[None]:
    """Scrub the final report consumed by terminals and pytest-html."""

    del item, call
    outcome = yield
    sanitize_pytest_report(outcome.get_result())


@pytest.hookimpl(tryfirst=True)
def pytest_collectreport(
    report: pytest.CollectReport,
) -> None:
    """Scrub import/collection failures before reporters receive them."""

    sanitize_pytest_report(report)


def pytest_assertrepr_compare(
    config: pytest.Config,
    op: str,
    left: Any,
    right: Any,
) -> list[str] | None:
    """Override comparison output only when credential redaction was required."""

    del config
    left_repr = repr(left)
    right_repr = repr(right)
    safe_left = redact_text(left_repr)
    safe_right = redact_text(right_repr)
    if safe_left == left_repr and safe_right == right_repr:
        return None
    return [
        f"credential-safe comparison failed ({op})",
        f"left: {safe_left}",
        f"right: {safe_right}",
    ]


def pytest_unconfigure(config: pytest.Config) -> None:
    """Scrub the completed pytest-html file after every reporter has written it."""

    html_path = config.getoption("htmlpath", default=None)
    if html_path:
        scrub_html_report(html_path)


@pytest.fixture(scope="session")
def api_base_url(pytestconfig: pytest.Config) -> str:
    return pytestconfig.stash[_TEST_TARGET].base_url


@pytest.fixture(scope="session")
def bootstrap_api_client(api_base_url: str) -> APIClient:
    client = APIClient(api_base_url)
    yield client
    client.close()


@pytest.fixture(scope="session", autouse=True)
def _ensure_api_available(
    pytestconfig: pytest.Config,
    api_base_url: str,
) -> None:
    """Fail closed when the real API or either required dependency is absent."""

    items = pytestconfig.stash.get(_COLLECTED_ITEMS, [])
    if items and all(item.get_closest_marker("unit") is not None for item in items):
        return

    target = pytestconfig.stash[_TEST_TARGET]
    try:
        health_url = healthcheck_url_for(
            target,
            os.getenv("TEST_HEALTHCHECK_URL"),
        )
    except TestSafetyError as exc:
        pytest.fail(redact_text(exc), pytrace=False)

    try:
        response = requests.get(health_url, timeout=5)
    except requests.RequestException as exc:
        pytest.fail(f"API 未启动或无法连接: {redact_text(exc)}", pytrace=False)
    if response.status_code != 200:
        pytest.fail(
            f"API 健康检查失败: {response_diagnostic(response)}",
            pytrace=False,
        )
    try:
        payload = response.json()
    except ValueError as exc:
        pytest.fail(
            f"API 健康检查返回非 JSON 响应: {redact_text(exc)}",
            pytrace=False,
        )
    dependencies = payload.get("dependencies", {})
    if (
        payload.get("status") != "ok"
        or dependencies.get("postgresql") != "ok"
        or dependencies.get("redis") != "ok"
    ):
        pytest.fail(f"API 依赖未就绪: {safe_diagnostic(payload)}", pytrace=False)


@pytest.fixture(scope="session")
def admin_credentials() -> dict[str, str]:
    return {
        "email": os.getenv("TEST_ADMIN_EMAIL", "admin@example.com"),
        "password": os.getenv("TEST_ADMIN_PASSWORD", "Admin123!"),
    }


@pytest.fixture(scope="session")
def admin_tokens(
    bootstrap_api_client: APIClient,
    admin_credentials: dict[str, str],
) -> dict[str, object]:
    try:
        payload = bootstrap_api_client.login(
            admin_credentials["email"], admin_credentials["password"]
        )
    except APIError as exc:
        response = exc.response
        detail = (
            response_diagnostic(response) if response is not None else redact_text(exc)
        )
        pytest.fail(f"管理员登录失败，无法运行依赖测试: {detail}")
    try:
        assert_human_session_contract(
            payload,
            expected_platform_role="platform_admin",
        )
    except AssertionError:
        pytest.fail(
            "平台管理员 Auth/JWT 必须只使用 platform_role："
            f"{safe_diagnostic(payload.get('user'))}"
        )
    return payload


@pytest.fixture(scope="session")
def admin_api(api_client: APIClient, admin_tokens: dict[str, object]) -> APIClient:
    token = admin_tokens.get("access_token")
    if not isinstance(token, str) or not token:
        pytest.fail("管理员登录响应缺少 access_token")

    authed_client = api_client.with_auth(token)
    yield authed_client
    authed_client.close()


@pytest.fixture(scope="session")
def project_key(
    bootstrap_api_client: APIClient,
    admin_tokens: dict[str, object],
) -> str:
    token = admin_tokens.get("access_token")
    if not isinstance(token, str) or not token:
        pytest.fail("管理员登录响应缺少 access_token，无法发现项目")
    discovery_client = bootstrap_api_client.with_auth(token)
    try:
        response = discovery_client.get_json("/projects")
        if response.status_code != 200:
            pytest.fail(f"项目发现失败：{response_diagnostic(response)}")
        try:
            payload = response.json()
        except ValueError as exc:
            pytest.fail(f"项目发现返回非 JSON：{redact_text(exc)}")
    finally:
        discovery_client.close()
    directory = payload.get("data") if isinstance(payload, dict) else None
    if (
        not isinstance(directory, dict)
        or not isinstance(directory.get("items"), list)
        or directory.get("page") != 1
        or directory.get("page_size") != 25
        or not isinstance(directory.get("total"), int)
        or not isinstance(directory.get("total_pages"), int)
    ):
        pytest.fail(f"项目发现缺少严格分页 data：{safe_diagnostic(payload)}")
    rows = directory["items"]
    active_accesses = [
        row
        for row in rows
        if isinstance(row, dict)
        and isinstance(row.get("project"), dict)
        and row["project"].get("status") == "active"
    ]
    if len(active_accesses) != 1:
        pytest.fail(f"隔离测试要求唯一 active 项目，实际 {len(active_accesses)} 个")
    selected_access = active_accesses[0]
    selected_role = selected_access.get("project_role")
    if selected_role not in PROJECT_ROLES or "role" in selected_access:
        pytest.fail(
            f"项目发现必须只返回合法 project_role：{safe_diagnostic(selected_access)}"
        )
    selected_project = selected_access["project"]
    selected = selected_project.get("key")
    if not isinstance(selected, str) or not selected:
        pytest.fail(f"active 项目缺少 key：{safe_diagnostic(selected_project)}")
    try:
        validate_project_key(selected)
    except AssertionError as exc:
        pytest.fail(str(exc))
    return selected


@pytest.fixture(scope="session")
def api_client(api_base_url: str, project_key: str) -> APIClient:
    client = APIClient(api_base_url, project_key=project_key)
    yield client
    client.close()


@pytest.fixture(scope="session")
def e2e_run_id(pytestconfig: pytest.Config) -> str:
    target = pytestconfig.stash[_TEST_TARGET]
    ownership = target.ownership_prefix or "e2e-local"
    return f"{ownership}-{int(time.time())}-{secrets.token_hex(4)}"


@pytest.fixture(scope="session")
def e2e_manager(
    api_client: APIClient,
    admin_api: APIClient,
    e2e_run_id: str,
    project_key: str,
) -> Iterator[E2EResourceManager]:
    manager = E2EResourceManager(api_client, admin_api, e2e_run_id, project_key)
    try:
        yield manager
    finally:
        manager.cleanup()


@pytest.fixture(scope="session")
def platform_admin_identity(
    admin_api: APIClient,
    admin_credentials: dict[str, str],
    admin_tokens: dict[str, object],
) -> HumanIdentity:
    admin_user = admin_tokens.get("user")
    if not isinstance(admin_user, dict):
        pytest.fail("管理员登录响应缺少 user")
    admin_id = admin_user.get("id")
    admin_username = admin_user.get("username")
    admin_email = admin_user.get("email", admin_credentials["email"])
    admin_access_token = admin_tokens.get("access_token")
    admin_refresh_token = admin_tokens.get("refresh_token")
    if (
        not isinstance(admin_id, int)
        or admin_id <= 0
        or not isinstance(admin_username, str)
        or not isinstance(admin_email, str)
        or not isinstance(admin_access_token, str)
        or not isinstance(admin_refresh_token, str)
    ):
        pytest.fail("管理员登录响应缺少完整身份或令牌字段")

    context = admin_api.get_json(admin_api.project_path("context"))
    if context.status_code != 200:
        pytest.fail(
            f"测试平台管理员缺少显式项目 Membership：{response_diagnostic(context)}"
        )
    project_access = context.json().get("data")
    if (
        not isinstance(project_access, dict)
        or project_access.get("project_role") != "project_admin"
        or "role" in project_access
    ):
        pytest.fail(
            "测试平台管理员项目上下文必须显式为 project_admin："
            f"{safe_diagnostic(project_access)}"
        )

    return HumanIdentity(
        id=admin_id,
        platform_role="platform_admin",
        project_role="project_admin",
        username=admin_username,
        email=admin_email,
        password=admin_credentials["password"],
        access_token=admin_access_token,
        refresh_token=admin_refresh_token,
        api=admin_api,
    )


@pytest.fixture(scope="session")
def human_identities(
    e2e_manager: E2EResourceManager,
) -> Mapping[str, HumanIdentity]:
    """Project personas; names are labels, authority comes from Membership."""

    return {
        "admin": e2e_manager.create_project_identity(
            "project_admin",
            label="project-admin",
        ),
        "supervisor": e2e_manager.create_project_identity(
            "manager",
            label="manager",
        ),
        "agent_a": e2e_manager.create_project_identity(
            "agent",
            label="agent-a",
        ),
        "agent_b": e2e_manager.create_project_identity(
            "agent",
            label="agent-b",
        ),
        "customer_a": e2e_manager.create_project_identity(
            "requester",
            label="requester-a",
        ),
        "customer_b": e2e_manager.create_project_identity(
            "requester",
            label="requester-b",
        ),
        "observer": e2e_manager.create_project_identity(
            "observer",
            label="observer",
        ),
    }


@pytest.fixture(scope="session")
def platform_identities(
    e2e_manager: E2EResourceManager,
) -> Mapping[str, HumanIdentity]:
    """Platform personas intentionally have no ProjectMembership."""

    return {
        platform_role: e2e_manager.create_platform_identity(
            platform_role,
            label=f"platform-{platform_role}",
        )
        for platform_role in PLATFORM_ROLES
    }


@pytest.fixture
def registered_user(
    api_client: APIClient, admin_api: APIClient
) -> Iterator[dict[str, object]]:
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
        detail = (
            response_diagnostic(response)
            if response is not None
            else redact_text(last_error)
        )
        pytest.fail(f"Failed to register test user: {detail}")

    registered = {
        "email": email,
        "password": password,
        "access_token": data.get("access_token"),
        "refresh_token": data.get("refresh_token"),
        "user": data.get("user", {}),
    }
    registered_identity = registered["user"]
    registered_id = (
        registered_identity.get("id") if isinstance(registered_identity, dict) else None
    )
    if not isinstance(registered_id, int) or registered_id <= 0:
        pytest.fail("注册响应缺少 user.id，无法授权测试项目")
    if (
        not isinstance(registered_identity, dict)
        or registered_identity.get("platform_role") != "member"
        or "role" in registered_identity
    ):
        pytest.fail(
            "注册身份必须只返回 platform_role=member："
            f"{safe_diagnostic(registered_identity)}"
        )
    try:
        assert_human_session_contract(data, expected_platform_role="member")
    except AssertionError:
        pytest.fail(
            "注册 Auth/JWT 必须只使用 platform_role=member："
            f"{safe_diagnostic(registered_identity)}"
        )
    membership_version: int | None = None
    try:
        try:
            membership = admin_api.post_json(
                admin_api.project_path("memberships"),
                {
                    "user_id": registered_id,
                    "role": "requester",
                    "expected_version": 0,
                },
                retry=False,
            )
        except APIError:
            try:
                membership_version = active_project_membership_version(
                    admin_api,
                    registered_id,
                    "requester",
                )
            except AssertionError as reconcile_exc:
                pytest.fail(
                    "注册用户项目授权结果不确定，且未能确认提交结果："
                    f"{safe_diagnostic(reconcile_exc)}",
                    pytrace=False,
                )
        else:
            if membership.status_code != 200:
                pytest.fail(f"注册用户项目授权失败：{response_diagnostic(membership)}")
            try:
                membership_data = membership.json().get("data")
            except ValueError:
                membership_data = None
            membership_version = (
                membership_data.get("version")
                if isinstance(membership_data, dict)
                and membership_data.get("user_id") == registered_id
                and membership_data.get("role") == "requester"
                else None
            )
            if not isinstance(membership_version, int) or membership_version <= 0:
                try:
                    membership_version = active_project_membership_version(
                        admin_api,
                        registered_id,
                        "requester",
                    )
                except AssertionError as exc:
                    pytest.fail(
                        "注册用户项目授权响应缺少有效版本，且对账失败："
                        f"{safe_diagnostic(exc)}",
                        pytrace=False,
                    )
        yield registered
    finally:
        cleanup_errors: list[str] = []
        membership_cleanup_error = cleanup_project_membership(
            admin_api,
            registered_id,
            membership_version,
        )
        if membership_cleanup_error is not None:
            cleanup_errors.append(
                f"清理注册测试用户 {registered_id} 的项目成员失败："
                f"{membership_cleanup_error}"
            )
        user = registered.get("user", {})
        user_id = user.get("id") if isinstance(user, dict) else None
        if user_id:
            response = admin_api.delete(f"/platform/users/{user_id}")
            if response.status_code not in (200, 204, 404):
                cleanup_errors.append(
                    f"Failed to clean up registered test user {user_id}: "
                    f"{response_diagnostic(response)}"
                )
        if cleanup_errors:
            pytest.fail("\n".join(cleanup_errors))


def _generate_strong_password() -> str:
    """Generate a password satisfying policy without triple repeats."""

    while True:
        token = secrets.token_hex(6)
        password = f"Aa1!{token}Z"
        if all(
            password[i] != password[i + 1] or password[i] != password[i + 2]
            for i in range(len(password) - 2)
        ):
            return password
