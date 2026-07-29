"""Human REST ticket input, pagination, authorization and concurrency boundaries."""

from __future__ import annotations

import threading
from collections.abc import Mapping
from concurrent.futures import ThreadPoolExecutor

import pytest

from tests.utils import (
    APIClient,
    E2EResourceManager,
    HumanIdentity,
    assert_error_contract,
)

pytestmark = [pytest.mark.api, pytest.mark.integration]


def test_ticket_create_input_boundaries(
    e2e_manager: E2EResourceManager,
    human_identities: Mapping[str, HumanIdentity],
) -> None:
    """TKT-001/TKT-002: exact request limits and customer-controlled fields."""

    admin = human_identities["admin"]
    valid_base = {
        "description": f"{e2e_manager.prefix}valid description",
        "type": "request",
        "priority": "normal",
        "source": "api",
    }
    invalid_payloads = [
        valid_base,
        {
            "title": e2e_manager.unique("missing-description"),
            "type": "request",
            "priority": "normal",
            "source": "api",
        },
        {**valid_base, "title": e2e_manager.prefix + ("x" * 256)},
        {
            **valid_base,
            "title": e2e_manager.unique("description-too-long"),
            "description": "中" * 10001,
        },
        {
            **valid_base,
            "title": e2e_manager.unique("invalid-type"),
            "type": "unknown",
        },
        {
            **valid_base,
            "title": e2e_manager.unique("invalid-priority"),
            "priority": "blocker",
        },
    ]
    for payload in invalid_payloads:
        rejected = admin.api.post_json("/tickets", payload)
        if rejected.status_code == 201:
            unexpected_ticket = rejected.json().get("data", {})
            unexpected_id = unexpected_ticket.get("id")
            assert isinstance(unexpected_id, int) and unexpected_id > 0, (
                unexpected_ticket
            )
            cleanup = admin.api.delete(f"/tickets/{unexpected_id}")
            assert cleanup.status_code in (200, 204), cleanup.text
        assert_error_contract(rejected, 400)

    title = e2e_manager.prefix + ("T" * (255 - len(e2e_manager.prefix)))
    description = e2e_manager.prefix + ("D" * (10000 - len(e2e_manager.prefix)))
    maximum = admin.api.post_json(
        "/tickets",
        {
            **valid_base,
            "title": title,
            "description": description,
        },
    )
    assert maximum.status_code == 201, maximum.text
    maximum_ticket = maximum.json().get("data", {})
    assert maximum_ticket.get("title") == title
    assert maximum_ticket.get("description") == description
    assert maximum_ticket.get("version") == 1
    e2e_manager.track_ticket(maximum_ticket)

    customer = human_identities["customer_a"]
    forbidden_customer_fields = customer.api.post_json(
        "/tickets",
        {
            **valid_base,
            "title": e2e_manager.unique("customer-forbidden-fields"),
            "status": "in_progress",
            "assigned_to_id": human_identities["agent_a"].id,
        },
    )
    if forbidden_customer_fields.status_code == 201:
        unexpected_ticket = forbidden_customer_fields.json().get("data", {})
        unexpected_id = unexpected_ticket.get("id")
        assert isinstance(unexpected_id, int) and unexpected_id > 0, unexpected_ticket
        cleanup = admin.api.delete(f"/tickets/{unexpected_id}")
        assert cleanup.status_code in (200, 204), cleanup.text
    assert_error_contract(forbidden_customer_fields, 403)


def test_ticket_pagination_and_identifier_bounds(
    e2e_manager: E2EResourceManager,
    human_identities: Mapping[str, HumanIdentity],
) -> None:
    """TKT-006/TKT-024/SEC-005: cap pages and reject unsafe identifiers."""

    admin = human_identities["admin"]
    ticket = e2e_manager.create_ticket(admin, "pagination")

    oversized = admin.api.get_json(
        "/tickets",
        params={"page": -99, "page_size": 100000},
    )
    assert oversized.status_code == 200, oversized.text
    page = oversized.json().get("data", {})
    assert page.get("page") == 1
    assert page.get("page_size") == 100
    assert len(page.get("items", [])) <= 100
    assert ticket["id"] in {item.get("id") for item in page.get("items", [])}

    invalid_id = admin.api.get_json("/tickets/not-a-number")
    assert_error_contract(invalid_id, 400)
    overflow_id = admin.api.get_json("/tickets/18446744073709551616")
    assert_error_contract(overflow_id, 400)
    not_found = admin.api.get_json("/tickets/4294967295")
    assert_error_contract(not_found, 404)

    oversized_user_page = admin.api.get_json(
        "/admin/users",
        params={"page": 1, "page_size": 101},
    )
    assert_error_contract(oversized_user_page, 400)


def test_concurrent_human_updates_produce_version_conflict(
    api_client: APIClient,
    e2e_manager: E2EResourceManager,
    human_identities: Mapping[str, HumanIdentity],
) -> None:
    """TKT-009: two writers racing one version cannot both overwrite it."""

    admin = human_identities["admin"]
    ticket = e2e_manager.create_ticket(admin, "version-race")
    conflict_response = None

    for attempt in range(1, 7):
        etag = admin.api.ticket_etag(ticket["id"])
        barrier = threading.Barrier(2)

        def update(
            worker: str,
            current_barrier: threading.Barrier = barrier,
            current_attempt: int = attempt,
            current_etag: str = etag,
        ):
            client = api_client.with_auth(admin.access_token)
            try:
                current_barrier.wait(timeout=5)
                return client.put_ticket(
                    ticket["id"],
                    {
                        "description": (
                            e2e_manager.unique(f"race-{current_attempt}-{worker}")
                            + ("x" * 9000)
                        )
                    },
                    etag=current_etag,
                )
            finally:
                client.close()

        with ThreadPoolExecutor(max_workers=2) as executor:
            responses = list(executor.map(update, ("a", "b")))

        statuses = [response.status_code for response in responses]
        assert all(status in (200, 409) for status in statuses), [
            (response.status_code, response.text) for response in responses
        ]
        conflicts = [response for response in responses if response.status_code == 409]
        if conflicts:
            conflict_response = conflicts[0]
            assert statuses.count(200) == 1, statuses
            break

    assert conflict_response is not None, (
        "six synchronized update rounds produced no version conflict; "
        "the Human REST compare-and-swap guard was not observable"
    )
    assert_error_contract(
        conflict_response,
        409,
        machine_codes={"409", "version_conflict"},
    )
