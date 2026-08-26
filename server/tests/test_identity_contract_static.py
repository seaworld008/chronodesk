"""Static contracts for the Human platform/project identity split."""

from __future__ import annotations

import base64
import inspect
import json
from typing import Any, cast

import pytest

from tests.utils import (
    PLATFORM_ROLES,
    PROJECT_ROLES,
    APIClient,
    E2EResourceManager,
    HumanIdentity,
    assert_human_session_contract,
)

pytestmark = pytest.mark.unit


def _jwt(claims: dict[str, Any]) -> str:
    def encode(value: dict[str, Any]) -> str:
        raw = json.dumps(value, separators=(",", ":")).encode("utf-8")
        return base64.urlsafe_b64encode(raw).decode("ascii").rstrip("=")

    return f"{encode({'alg': 'none', 'typ': 'JWT'})}.{encode(claims)}.signature"


def _identity(
    *,
    platform_role: str,
    project_role: str | None,
) -> HumanIdentity:
    return HumanIdentity(
        id=1,
        platform_role=platform_role,
        project_role=project_role,
        username="static-contract",
        email="static-contract@example.com",
        password="not-a-real-secret",
        access_token="not-a-real-token",
        api=cast(APIClient, object()),
    )


def test_role_sets_are_closed_and_disjoint() -> None:
    assert PLATFORM_ROLES == (
        "platform_admin",
        "security_auditor",
        "emergency_operator",
        "member",
    )
    assert PROJECT_ROLES == (
        "project_admin",
        "manager",
        "agent",
        "requester",
        "observer",
    )
    assert set(PLATFORM_ROLES).isdisjoint(PROJECT_ROLES)


def test_project_identity_factory_requires_explicit_project_role() -> None:
    parameters = inspect.signature(
        E2EResourceManager.create_project_identity
    ).parameters
    assert tuple(parameters) == ("self", "project_role", "platform_role", "label")
    assert parameters["project_role"].default is inspect.Parameter.empty
    assert parameters["platform_role"].default == "member"
    assert parameters["label"].kind is inspect.Parameter.KEYWORD_ONLY


def test_human_identity_rejects_unknown_roles() -> None:
    member = _identity(platform_role="member", project_role="observer")
    assert member.platform_role == "member"
    assert member.project_role == "observer"

    with pytest.raises(ValueError, match="unsupported platform role"):
        _identity(platform_role="unknown", project_role=None)
    with pytest.raises(ValueError, match="unsupported project role"):
        _identity(platform_role="member", project_role="unknown")


def test_auth_and_jwt_contract_contains_only_platform_role() -> None:
    valid_claims = {
        "user_id": 1,
        "platform_role": "member",
        "type": "access",
        "sid": "static-session",
    }
    session = {
        "user": {
            "id": 1,
            "username": "static-contract",
            "platform_role": "member",
        },
        "access_token": _jwt(valid_claims),
    }
    assert_human_session_contract(
        session,
        expected_platform_role="member",
    )
