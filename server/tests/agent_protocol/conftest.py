"""Fixtures shared by Agent REST, MCP, and A2A black-box tests."""

from __future__ import annotations

from collections.abc import Iterator

import pytest

from tests.utils.agent_protocol import (
    MINIMAL_AGENT_SCOPES,
    AgentAccessToken,
    AgentProtocolHarness,
    ServicePrincipalFixture,
)
from tests.utils.human import E2EResourceManager


@pytest.fixture(scope="session")
def protocol_harness(
    api_base_url: str,
    admin_tokens: dict[str, str],
    e2e_run_id: str,
    project_key: str,
    e2e_manager: E2EResourceManager,
) -> Iterator[AgentProtocolHarness]:
    admin_access_token = admin_tokens.get("access_token")
    if not isinstance(admin_access_token, str) or not admin_access_token:
        pytest.fail("管理员登录响应缺少 access_token，无法构造服务主体")
    harness = AgentProtocolHarness(
        api_base_url,
        admin_access_token,
        e2e_run_id,
        project_key,
        e2e_manager,
    )
    try:
        yield harness
    finally:
        harness.cleanup()


@pytest.fixture(scope="session")
def full_service_principal(
    protocol_harness: AgentProtocolHarness,
) -> ServicePrincipalFixture:
    principal = protocol_harness.create_principal(
        "protocol-full",
        MINIMAL_AGENT_SCOPES,
    )
    protocol_harness.create_policy(
        principal,
        label="allow-ticket-transition",
        effect="allow",
        scope="tickets:transition",
        action="ticket.transition",
        resource_type="ticket",
        resource_id="*",
        priority=200,
    )
    return principal


@pytest.fixture(scope="session")
def protected_resources(
    protocol_harness: AgentProtocolHarness,
) -> dict[str, str]:
    result: dict[str, str] = {}
    for name, suffix in {
        "api": "api/v2",
        "mcp": "mcp",
        "a2a": "a2a/v1",
    }.items():
        metadata = protocol_harness.protected_resource_metadata(suffix)
        resource = metadata.get("resource")
        assert isinstance(resource, str) and resource, (
            f"{name} Protected Resource Metadata 缺少 resource"
        )
        result[name] = resource
    return result


@pytest.fixture(scope="session")
def agent_tokens(
    protocol_harness: AgentProtocolHarness,
    full_service_principal: ServicePrincipalFixture,
    protected_resources: dict[str, str],
) -> dict[str, AgentAccessToken]:
    return {
        name: protocol_harness.exchange_token(
            full_service_principal,
            resource,
            MINIMAL_AGENT_SCOPES,
        )
        for name, resource in protected_resources.items()
    }
