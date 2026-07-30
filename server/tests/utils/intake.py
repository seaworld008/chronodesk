"""Published project-configuration helpers for ticket intake tests.

Black-box tests must select the same immutable Request Type and Workflow
versions as production clients.  The helper intentionally discovers those
versions through the authenticated Human REST contract; it never reaches into
the database or recreates configuration-selection rules in a protocol test.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any
from uuid import UUID

from .api import APIClient
from .safety import response_diagnostic, safe_diagnostic


def _canonical_uuid(value: object, *, field: str) -> str:
    assert isinstance(value, str) and value, f"{field} 必须是非空 UUID"
    try:
        parsed = UUID(value)
    except (ValueError, AttributeError) as exc:
        raise AssertionError(f"{field} 不是有效 UUID") from exc
    canonical = str(parsed)
    assert value.lower() == canonical, f"{field} 必须使用规范 UUID 表示"
    return canonical


@dataclass(frozen=True)
class PublishedRequestType:
    """Minimum trusted projection needed to bind a Ticket create request."""

    id: str
    work_class: str


@dataclass(frozen=True)
class PublishedTicketIntake:
    """One immutable project intake release discovered through public REST."""

    release_id: str
    release_version: int
    request_types: tuple[PublishedRequestType, ...]
    workflow_ids: tuple[str, ...]

    @classmethod
    def from_api_data(cls, data: object) -> PublishedTicketIntake:
        assert isinstance(data, dict), (
            f"项目建单配置 data 必须为对象：{safe_diagnostic(data)}"
        )
        release_id = _canonical_uuid(data.get("release_id"), field="release_id")
        release_version = data.get("release_version")
        assert isinstance(release_version, int) and release_version > 0, (
            "项目建单配置缺少正整数 release_version"
        )

        raw_request_types = data.get("request_types")
        assert isinstance(raw_request_types, list) and raw_request_types, (
            "项目建单配置没有已发布请求类型"
        )
        request_types: list[PublishedRequestType] = []
        for index, raw_request_type in enumerate(raw_request_types):
            assert isinstance(raw_request_type, dict), (
                f"request_types[{index}] 必须为对象"
            )
            work_class = raw_request_type.get("work_class")
            assert isinstance(work_class, str) and work_class, (
                f"request_types[{index}] 缺少 work_class"
            )
            status = raw_request_type.get("status")
            assert status == "published", f"request_types[{index}] 不是已发布不可变版本"
            request_types.append(
                PublishedRequestType(
                    id=_canonical_uuid(
                        raw_request_type.get("id"),
                        field=f"request_types[{index}].id",
                    ),
                    work_class=work_class,
                )
            )

        raw_workflows = data.get("workflows")
        assert isinstance(raw_workflows, list) and raw_workflows, (
            "项目建单配置没有已发布工作流"
        )
        workflow_ids: list[str] = []
        for index, raw_workflow in enumerate(raw_workflows):
            assert isinstance(raw_workflow, dict), f"workflows[{index}] 必须为对象"
            status = raw_workflow.get("status")
            assert status == "published", f"workflows[{index}] 不是已发布不可变版本"
            workflow_ids.append(
                _canonical_uuid(
                    raw_workflow.get("id"),
                    field=f"workflows[{index}].id",
                )
            )

        return cls(
            release_id=release_id,
            release_version=release_version,
            request_types=tuple(request_types),
            workflow_ids=tuple(workflow_ids),
        )

    def ticket_create_payload(
        self,
        payload: dict[str, Any],
        *,
        work_class: str | None = None,
    ) -> dict[str, Any]:
        """Copy a payload and bind it to versions from this published release.

        Explicit version fields are preserved so negative tests can still send
        deliberately invalid or conflicting IDs.  ``work_class`` lets a
        validation test bind a valid Request Type before intentionally
        replacing the public ``type`` field with an invalid value.
        """

        selected_work_class = work_class or payload.get("type") or "request"
        assert isinstance(selected_work_class, str), (
            "建单测试 payload 的 type/work_class 必须为字符串"
        )
        request_type = next(
            (
                candidate
                for candidate in self.request_types
                if candidate.work_class == selected_work_class
            ),
            None,
        )
        assert request_type is not None, (
            f"当前已发布配置不包含 work_class={selected_work_class!r} 的请求类型"
        )
        prepared = {
            "request_type_version_id": request_type.id,
            "workflow_version_id": self.workflow_ids[0],
        }
        prepared.update(payload)
        return prepared


def load_published_ticket_intake(client: APIClient) -> PublishedTicketIntake:
    """Read and strictly validate the current project's published intake."""

    response = client.get_json(client.project_path("configuration/intake"))
    assert response.status_code == 200, response_diagnostic(response)
    try:
        envelope = response.json()
    except ValueError as exc:
        raise AssertionError(
            f"项目建单配置未返回 JSON：{response_diagnostic(response)}"
        ) from exc
    assert isinstance(envelope, dict), response_diagnostic(response)
    assert envelope.get("code") == 0, safe_diagnostic(envelope)
    return PublishedTicketIntake.from_api_data(envelope.get("data"))
