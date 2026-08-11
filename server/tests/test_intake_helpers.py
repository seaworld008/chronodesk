"""Unit guards for immutable project-intake payload preparation."""

from __future__ import annotations

import copy

import pytest

from tests.utils.human import E2EResourceManager
from tests.utils.intake import PublishedTicketIntake

pytestmark = pytest.mark.unit

REQUEST_ID = "019fb2bb-0000-7000-8000-000000000001"
INCIDENT_ID = "019fb2bb-0000-7000-8000-000000000002"
WORKFLOW_ID = "019fb2bb-0000-7000-8000-000000000003"
RELEASE_ID = "019fb2bb-0000-7000-8000-000000000004"


def _intake_data() -> dict[str, object]:
    return {
        "release_id": RELEASE_ID,
        "release_version": 3,
        "request_types": [
            {
                "id": INCIDENT_ID,
                "work_class": "incident",
                "status": "published",
            },
            {
                "id": REQUEST_ID,
                "work_class": "request",
                "status": "published",
            },
        ],
        "workflows": [
            {
                "id": WORKFLOW_ID,
                "status": "published",
            }
        ],
    }


def test_ticket_create_payload_selects_matching_published_versions() -> None:
    intake = PublishedTicketIntake.from_api_data(_intake_data())
    original = {"title": "API outage", "type": "incident"}
    snapshot = copy.deepcopy(original)

    prepared = intake.ticket_create_payload(original)

    assert original == snapshot
    assert prepared == {
        "title": "API outage",
        "type": "incident",
        "request_type_version_id": INCIDENT_ID,
        "workflow_version_id": WORKFLOW_ID,
    }


def test_ticket_create_payload_preserves_explicit_negative_test_versions() -> None:
    intake = PublishedTicketIntake.from_api_data(_intake_data())
    explicit_request_type = "019fb2bb-0000-7000-8000-000000000099"
    explicit_workflow = "019fb2bb-0000-7000-8000-000000000098"

    prepared = intake.ticket_create_payload(
        {
            "type": "unknown",
            "request_type_version_id": explicit_request_type,
            "workflow_version_id": explicit_workflow,
        },
        work_class="request",
    )

    assert prepared["request_type_version_id"] == explicit_request_type
    assert prepared["workflow_version_id"] == explicit_workflow
    assert prepared["type"] == "unknown"


def test_human_create_helper_rejects_status_override() -> None:
    manager = object.__new__(E2EResourceManager)

    with pytest.raises(AssertionError, match="已发布工作流"):
        manager.create_ticket(None, "invalid-status", status="open")


@pytest.mark.parametrize(
    ("path", "value"),
    [
        (("request_types", 0, "status"), "draft"),
        (("workflows", 0, "status"), "draft"),
        (("request_types", 0, "id"), "not-a-uuid"),
    ],
)
def test_published_intake_parser_rejects_untrusted_or_mutable_versions(
    path: tuple[str, int, str],
    value: str,
) -> None:
    data = _intake_data()
    collection = data[path[0]]
    assert isinstance(collection, list)
    row = collection[path[1]]
    assert isinstance(row, dict)
    row[path[2]] = value

    with pytest.raises(AssertionError):
        PublishedTicketIntake.from_api_data(data)
