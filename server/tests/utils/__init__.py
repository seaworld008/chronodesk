"""Utility helpers for automated API tests."""

from .api import APIClient, APIError
from .assertions import assert_error_contract, assert_no_sensitive_fields
from .human import (
    PLATFORM_ROLES,
    PROJECT_ROLES,
    E2EResourceManager,
    HumanIdentity,
    assert_human_session_contract,
    strong_password,
)
from .intake import PublishedRequestType, PublishedTicketIntake
from .safety import (
    TestSafetyError,
    assert_local_ephemeral_target,
    response_diagnostic,
    safe_diagnostic,
    validate_test_target,
)

__all__ = [
    "PLATFORM_ROLES",
    "PROJECT_ROLES",
    "APIClient",
    "APIError",
    "E2EResourceManager",
    "HumanIdentity",
    "PublishedRequestType",
    "PublishedTicketIntake",
    "TestSafetyError",
    "assert_error_contract",
    "assert_human_session_contract",
    "assert_local_ephemeral_target",
    "assert_no_sensitive_fields",
    "response_diagnostic",
    "safe_diagnostic",
    "strong_password",
    "validate_test_target",
]
