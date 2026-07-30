"""Cross-cutting Human REST error-contract tests.

The filename deliberately sorts last because the rate-limit scenario exhausts
one isolated user/route bucket for the remainder of its configured window.
"""

from __future__ import annotations

import os
from collections.abc import Mapping

import pytest

from ..utils import (
    APIClient,
    E2EResourceManager,
    HumanIdentity,
    assert_error_contract,
)

pytestmark = [pytest.mark.api, pytest.mark.integration]


def test_authentication_authorization_and_not_found_errors_are_machine_safe(
    api_client: APIClient,
    human_identities: Mapping[str, HumanIdentity],
) -> None:
    missing = api_client.get_json(api_client.project_path("tickets"))
    assert_error_contract(missing, 401, machine_codes={"missing_token"})

    malformed = api_client.get_json(
        api_client.project_path("tickets"),
        headers={"Authorization": "Token deliberately-invalid"},
    )
    assert_error_contract(
        malformed,
        401,
        machine_codes={"invalid_token_format"},
    )

    forged = api_client.get_json(
        api_client.project_path("tickets"),
        headers={"Authorization": "Bearer aaa.bbb.ccc"},
    )
    assert_error_contract(
        forged,
        401,
        machine_codes={"invalid_token"},
    )

    forbidden = human_identities["customer_a"].api.get_json("/admin/users")
    assert_error_contract(forbidden, 403)

    customer_api = human_identities["customer_a"].api
    absent = customer_api.get_json(customer_api.project_path("tickets/4294967295"))
    assert_error_contract(absent, 404)


def test_suspended_account_invalidates_an_existing_access_token(
    admin_api: APIClient,
    human_identities: Mapping[str, HumanIdentity],
) -> None:
    identity = human_identities["customer_a"]

    suspended = admin_api.put_json(
        f"/admin/users/{identity.id}",
        {"status": "suspended"},
    )
    assert suspended.status_code == 200, suspended.text

    try:
        rejected = identity.api.get_json("/auth/me")
        assert_error_contract(
            rejected,
            401,
            machine_codes={"account_inactive"},
        )
    finally:
        restored = admin_api.put_json(
            f"/admin/users/{identity.id}",
            {"status": "active"},
        )
        assert restored.status_code == 200, restored.text

    accepted_again = identity.api.get_json("/auth/me")
    assert accepted_again.status_code == 200, accepted_again.text


def test_deleted_account_invalidates_an_existing_access_token(
    admin_api: APIClient,
    e2e_manager: E2EResourceManager,
) -> None:
    identity = e2e_manager.create_user("customer", "deleted-token")

    deleted = admin_api.delete(f"/admin/users/{identity.id}")
    assert deleted.status_code in (200, 204), deleted.text

    rejected = identity.api.get_json("/auth/me")
    assert_error_contract(
        rejected,
        401,
        machine_codes={"invalid_token"},
    )


@pytest.mark.slow
def test_authenticated_rate_limit_is_isolated_and_returns_retry_contract(
    human_identities: Mapping[str, HumanIdentity],
) -> None:
    # This E2E customer is only reused after all functional scenarios have
    # completed; exhausting its /auth/me bucket cannot affect other users or
    # routes.
    identity = human_identities["customer_b"]

    first = identity.api.get_json("/auth/me")
    assert first.status_code == 200, first.text
    limit_header = first.headers.get("RateLimit-Limit")
    remaining_header = first.headers.get("RateLimit-Remaining")
    reset_header = first.headers.get("RateLimit-Reset")
    assert limit_header and limit_header.isdigit(), first.headers
    assert remaining_header and remaining_header.isdigit(), first.headers
    assert reset_header and reset_header.isdigit(), first.headers

    limit = int(limit_header)
    remaining = int(remaining_header)
    ceiling = int(os.getenv("TEST_RATE_LIMIT_EXHAUSTION_CEILING", "1000"))
    assert 0 <= remaining < limit
    assert remaining <= ceiling, (
        f"当前限流桶尚余 {remaining} 次请求，超过安全压测上限 {ceiling}；"
        "请在隔离测试环境降低 RATE_LIMIT_REQUESTS，或显式提高 "
        "TEST_RATE_LIMIT_EXHAUSTION_CEILING"
    )

    rate_limited = None
    for _ in range(remaining + 1):
        response = identity.api.get_json("/auth/me")
        if response.status_code == 429:
            rate_limited = response
            break
        assert response.status_code == 200, response.text

    assert rate_limited is not None, (
        f"在声明剩余配额 {remaining} 次后仍未返回 429，"
        "RateLimit-Remaining 与实际限流行为不一致"
    )
    assert_error_contract(
        rate_limited,
        429,
        machine_codes={"rate_limit_exceeded", "RATE_LIMIT_EXCEEDED"},
    )
    assert rate_limited.headers.get("Retry-After", "").isdigit()
    assert rate_limited.headers.get("RateLimit-Limit") == limit_header
    assert rate_limited.headers.get("RateLimit-Remaining") == "0"
    assert rate_limited.headers.get("RateLimit-Reset", "").isdigit()
