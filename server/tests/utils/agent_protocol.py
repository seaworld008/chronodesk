"""Safe helpers for Agent REST, MCP, and A2A black-box tests.

The helpers deliberately use only public HTTP contracts. One-time client
secrets and access tokens remain in memory and are redacted from assertion
messages and dataclass representations.
"""

from __future__ import annotations

import re
import secrets
from collections.abc import Iterable, Mapping
from dataclasses import dataclass, field
from typing import Any
from urllib.parse import urljoin, urlsplit, urlunsplit

import requests

from .human import E2EResourceManager
from .safety import (
    register_headers,
    register_response_secrets,
    register_secret,
    register_sensitive_values,
    response_diagnostic,
)

MCP_PROTOCOL_VERSION = "2026-07-28"
A2A_PROTOCOL_VERSION = "1.0"
PROJECT_KEY_PATTERN = re.compile(r"^[A-Z][A-Z0-9_-]{0,31}$")

MINIMAL_AGENT_SCOPES = (
    "tickets:read",
    "tickets:create",
    "tickets:update",
    "tickets:assign",
    "tickets:transition",
    "comments:write",
    "attachments:read",
    "attachments:write",
    "knowledge:read",
    "knowledge:write",
    "events:subscribe",
    "tasks:manage",
)


def root_url_from_api_base(api_base_url: str) -> str:
    """Return the deployment root for a configured Human REST `/api` URL."""

    parsed = urlsplit(api_base_url.rstrip("/"))
    path = parsed.path.rstrip("/")
    if path != "/api":
        raise AssertionError(
            "TEST_API_BASE_URL 必须指向 ChronoDesk Human REST 的 /api 根路径"
        )
    root_path = path[: -len("/api")]
    return urlunsplit((parsed.scheme, parsed.netloc, root_path, "", "")).rstrip("/")


def assert_status(
    response: requests.Response,
    expected: int | Iterable[int],
    *,
    operation: str,
) -> None:
    statuses = {expected} if isinstance(expected, int) else set(expected)
    assert response.status_code in statuses, (
        f"{operation} 返回状态异常，期望 {sorted(statuses)}；"
        f"{response_diagnostic(response)}"
    )


def json_object(
    response: requests.Response,
    *,
    operation: str,
) -> dict[str, Any]:
    try:
        payload = response.json()
    except ValueError as exc:
        raise AssertionError(
            f"{operation} 未返回 JSON；{response_diagnostic(response)}"
        ) from exc
    assert isinstance(payload, dict), (
        f"{operation} 顶层响应必须为对象；{response_diagnostic(response)}"
    )
    return payload


def envelope_data(
    response: requests.Response,
    *,
    operation: str,
) -> Any:
    payload = json_object(response, operation=operation)
    assert "data" in payload, (
        f"{operation} 响应缺少 data；{response_diagnostic(response)}"
    )
    return payload["data"]


def page_envelope_data(
    response: requests.Response,
    *,
    operation: str,
    expected_page: int,
    expected_page_size: int,
) -> dict[str, Any]:
    """Validate one strict server-side page before a test consumes its rows."""

    data = envelope_data(response, operation=operation)
    assert isinstance(data, dict), f"{operation} data 必须为严格分页对象"
    items = data.get("items")
    total = data.get("total")
    page = data.get("page")
    page_size = data.get("page_size")
    total_pages = data.get("total_pages")
    assert isinstance(items, list), f"{operation} 缺少 items 列表"
    assert isinstance(total, int) and not isinstance(total, bool) and total >= 0, (
        f"{operation} total 必须为非负整数"
    )
    assert page == expected_page, f"{operation} page={page!r}，期望 {expected_page}"
    assert page_size == expected_page_size, (
        f"{operation} page_size={page_size!r}，期望 {expected_page_size}"
    )
    assert (
        isinstance(total_pages, int)
        and not isinstance(total_pages, bool)
        and total_pages >= 0
    ), f"{operation} total_pages 必须为非负整数"
    expected_total_pages = (
        (total + expected_page_size - 1) // expected_page_size if total else 0
    )
    assert total_pages == expected_total_pages, (
        f"{operation} total_pages 与 total/page_size 不一致"
    )
    assert len(items) <= expected_page_size, f"{operation} 单页条目数超过 page_size"
    assert len(items) <= total, f"{operation} items 条目数超过 total"
    if total_pages == 0:
        assert not items, f"{operation} 空目录不得返回 items"
    return data


def parse_etag(value: str) -> int:
    value = value.strip()
    assert value.startswith('"v') and value.endswith('"'), (
        f"资源 ETag 格式错误：{value!r}"
    )
    try:
        version = int(value[2:-1])
    except ValueError as exc:
        raise AssertionError(f"资源 ETag 版本不是整数：{value!r}") from exc
    assert version > 0, f"资源 ETag 版本必须大于 0：{value!r}"
    return version


@dataclass
class AgentAccessToken:
    resource: str
    project_key: str
    scopes: tuple[str, ...]
    access_token: str = field(repr=False)

    @property
    def authorization(self) -> str:
        return f"Bearer {self.access_token}"


@dataclass
class ServicePrincipalFixture:
    client_id: str
    name: str
    scopes: tuple[str, ...]
    resource_version: int
    client_secret: str = field(repr=False)
    policy_ids: list[str] = field(default_factory=list)


class AgentProtocolHarness:
    """Manage isolated machine identities and their protocol resources."""

    def __init__(
        self,
        api_base_url: str,
        admin_access_token: str,
        run_id: str,
        project_key: str,
        resource_manager: E2EResourceManager,
    ) -> None:
        if PROJECT_KEY_PATTERN.fullmatch(project_key) is None:
            raise AssertionError("Agent 测试项目键必须规范")
        self.root_url = root_url_from_api_base(api_base_url)
        self.run_id = run_id
        self.project_key = project_key
        self.prefix = f"E2E-{run_id}-"
        self._admin_access_token = admin_access_token
        self._resources = resource_manager
        self._session = requests.Session()
        self._session.headers.update({"Accept": "application/json"})
        self._principals: list[ServicePrincipalFixture] = []
        self._leases: list[str] = []

    def close(self) -> None:
        self._session.close()

    def unique(self, label: str) -> str:
        return f"{self.prefix}{label}-{secrets.token_hex(4)}"

    def idempotency_key(self, label: str) -> str:
        safe_label = "".join(
            character.lower() if character.isalnum() else "-" for character in label
        ).strip("-")
        return f"e2e-{self.run_id}-{safe_label}-{secrets.token_hex(4)}"[:128]

    def project_api_path(self, suffix: str = "") -> str:
        """Return an Agent REST v2 path bound to the configured project."""

        base = f"/api/v2/projects/{self.project_key}"
        normalized = suffix.strip("/")
        return f"{base}/{normalized}" if normalized else base

    def ticket_create_payload(
        self,
        payload: dict[str, Any],
        *,
        work_class: str | None = None,
    ) -> dict[str, Any]:
        """Bind machine intake to the project's published immutable versions."""

        return self._resources.ticket_create_payload(
            payload,
            work_class=work_class,
        )

    def agent_admin_path(self, suffix: str = "") -> str:
        """Return the Human REST Agent control path for the configured project."""

        base = f"/api/projects/{self.project_key}/admin/agents"
        normalized = suffix.strip("/")
        return f"{base}/{normalized}" if normalized else base

    def url(self, path_or_url: str) -> str:
        if path_or_url.startswith(("http://", "https://")):
            candidate = urlsplit(path_or_url)
            expected = urlsplit(self.root_url)
            if (
                candidate.scheme != expected.scheme
                or candidate.netloc != expected.netloc
            ):
                raise AssertionError("拒绝访问 ChronoDesk 测试目标以外的 URL")
            return path_or_url
        return urljoin(self.root_url + "/", path_or_url.lstrip("/"))

    def request(
        self,
        method: str,
        path_or_url: str,
        *,
        headers: Mapping[str, str] | None = None,
        json_body: Any | None = None,
        data: Mapping[str, str] | None = None,
        auth: tuple[str, str] | None = None,
        params: Mapping[str, Any] | None = None,
        stream: bool = False,
        timeout: int = 20,
    ) -> requests.Response:
        register_headers(self._session.headers)
        register_headers(headers)
        register_sensitive_values(json_body)
        register_sensitive_values(data)
        register_sensitive_values(params)
        if auth is not None:
            register_secret(auth[1])
        response = self._session.request(
            method,
            self.url(path_or_url),
            headers=dict(headers or {}),
            json=json_body,
            data=data,
            auth=auth,
            params=params,
            stream=stream,
            timeout=timeout,
        )
        register_response_secrets(response)
        return response

    def admin_request(
        self,
        method: str,
        path: str,
        *,
        json_body: Any | None = None,
        if_match: int | None = None,
        operation: str,
    ) -> requests.Response:
        headers = {
            "Authorization": f"Bearer {self._admin_access_token}",
            "Idempotency-Key": self.idempotency_key(operation),
        }
        if if_match is not None:
            headers["If-Match"] = f'"v{if_match}"'
        return self.request(
            method,
            path,
            headers=headers,
            json_body={} if json_body is None else json_body,
        )

    def admin_read_headers(self) -> dict[str, str]:
        """Return administrator authentication for read-only setup checks."""

        return {"Authorization": f"Bearer {self._admin_access_token}"}

    def overview(self) -> dict[str, Any]:
        """Return only bounded Agent control-plane metrics."""

        response = self.request(
            "GET",
            self.agent_admin_path("agent-control/overview"),
            headers=self.admin_read_headers(),
        )
        assert_status(response, 200, operation="读取 Agent 控制总览")
        data = envelope_data(response, operation="读取 Agent 控制总览")
        assert isinstance(data, dict), "Agent 控制总览 data 必须为对象"
        return data

    def _admin_directory_rows(
        self,
        suffix: str,
        *,
        sort_by: str,
        sort_order: str,
        operation: str,
        wanted_ids: set[str] | None = None,
    ) -> list[dict[str, Any]]:
        """Read a strict administrator directory without client-side slicing."""

        page_size = 100
        page = 1
        rows: list[dict[str, Any]] = []
        seen_ids: set[str] = set()
        while True:
            response = self.request(
                "GET",
                self.agent_admin_path(suffix),
                headers=self.admin_read_headers(),
                params={
                    "page": page,
                    "page_size": page_size,
                    "sort_by": sort_by,
                    "sort_order": sort_order,
                },
            )
            assert_status(response, 200, operation=operation)
            directory = page_envelope_data(
                response,
                operation=operation,
                expected_page=page,
                expected_page_size=page_size,
            )
            items = directory["items"]
            for item in items:
                assert isinstance(item, dict), f"{operation} items 必须只包含对象"
                item_id = item.get("id")
                assert isinstance(item_id, str) and item_id, (
                    f"{operation} 条目缺少不透明 id"
                )
                assert item_id not in seen_ids, (
                    f"{operation} 跨页出现重复条目 {item_id}"
                )
                seen_ids.add(item_id)
                rows.append(item)

            if wanted_ids is not None and wanted_ids.issubset(seen_ids):
                return rows
            total_pages = directory["total_pages"]
            if page >= total_pages:
                assert len(rows) == directory["total"], (
                    f"{operation} 遍历条目数与 total 不一致"
                )
                return rows
            assert items, f"{operation} 在末页前返回空 items"
            page += 1

    def principal_row(self, principal: ServicePrincipalFixture) -> dict[str, Any]:
        rows = self._admin_directory_rows(
            "service-principals",
            sort_by="created_at",
            sort_order="desc",
            operation="分页读取 Agent 服务主体",
            wanted_ids={principal.client_id},
        )
        row = next(
            (item for item in rows if item.get("id") == principal.client_id),
            None,
        )
        assert isinstance(row, dict), (
            f"Agent 服务主体分页目录中找不到 E2E 服务主体 {principal.client_id}"
        )
        assert row.get("client_id") == principal.client_id, (
            "服务主体 id/client_id 不一致"
        )
        version = row.get("resource_version")
        assert isinstance(version, int) and version > 0, (
            "服务主体缺少有效 resource_version"
        )
        principal.resource_version = version
        return row

    def policy_rows(
        self,
        principal: ServicePrincipalFixture,
    ) -> list[dict[str, Any]]:
        rows = self._admin_directory_rows(
            f"service-principals/{principal.client_id}/policies",
            sort_by="priority",
            sort_order="desc",
            operation="分页读取 E2E Agent 策略",
        )
        assert all(
            row.get("service_principal_id") == principal.client_id for row in rows
        ), "策略分页目录返回了其它服务主体的数据"
        return rows

    def create_principal(
        self,
        label: str,
        scopes: Iterable[str],
    ) -> ServicePrincipalFixture:
        normalized_scopes = tuple(dict.fromkeys(scopes))
        principal_name = self.unique(label)
        response = self.admin_request(
            "POST",
            self.agent_admin_path("service-principals"),
            json_body={
                "name": principal_name,
                "description": f"{self.prefix}Agent protocol black-box identity",
                "scopes": list(normalized_scopes),
                "rate_limit": 600,
                "concurrency_limit": 8,
            },
            operation=f"create-{label}",
        )
        assert_status(response, 201, operation="创建 E2E 服务主体")
        assert response.headers.get("Cache-Control") == "no-store", (
            "一次性凭据响应必须禁止缓存"
        )
        assert response.headers.get("Pragma") == "no-cache", (
            "一次性凭据响应必须包含 Pragma: no-cache"
        )
        payload = json_object(response, operation="创建 E2E 服务主体")
        data = payload.get("data")
        receipt = payload.get("receipt")
        assert isinstance(data, dict), "创建服务主体响应缺少 data"
        assert isinstance(receipt, dict), "创建服务主体响应缺少 receipt"
        client_id = data.get("client_id")
        client_secret = data.get("client_secret")
        version = receipt.get("resource_version")
        assert isinstance(client_id, str) and client_id, "服务主体响应缺少 client_id"
        assert isinstance(client_secret, str) and client_secret, (
            "服务主体响应缺少一次性 client_secret"
        )
        assert data.get("project_key") == self.project_key, (
            "服务主体初始项目 grant 与请求不一致"
        )
        assert isinstance(version, int) and version > 0, (
            "服务主体响应缺少 resource_version"
        )
        principal = ServicePrincipalFixture(
            client_id=client_id,
            name=principal_name,
            scopes=normalized_scopes,
            resource_version=version,
            client_secret=client_secret,
        )
        self._principals.append(principal)
        row = self.principal_row(principal)
        assert row.get("name") == principal.name
        assert principal.name.startswith(self.prefix), (
            "拒绝跟踪不属于当前 E2E 前缀的服务主体"
        )
        return principal

    def create_policy(
        self,
        principal: ServicePrincipalFixture,
        *,
        label: str,
        effect: str,
        scope: str,
        action: str = "",
        resource_type: str = "",
        resource_id: str = "",
        priority: int = 100,
    ) -> dict[str, Any]:
        current = self.principal_row(principal)
        response = self.admin_request(
            "POST",
            self.agent_admin_path(f"service-principals/{principal.client_id}/policies"),
            json_body={
                "name": self.unique(label),
                "effect": effect,
                "scope": scope,
                "action": action,
                "resource_type": resource_type,
                "resource_id": resource_id,
                "conditions": {},
                "priority": priority,
            },
            if_match=int(current["resource_version"]),
            operation=f"policy-{label}",
        )
        assert_status(response, 201, operation="创建 E2E Agent 策略")
        data = envelope_data(response, operation="创建 E2E Agent 策略")
        assert isinstance(data, dict), "策略创建响应 data 必须为对象"
        policy_id = data.get("id")
        assert isinstance(policy_id, str) and policy_id, "策略响应缺少 id"
        assert data.get("service_principal_id") == principal.client_id, (
            "策略未绑定目标服务主体"
        )
        principal.policy_ids.append(policy_id)
        parent_etag = response.headers.get("X-Parent-ETag")
        if parent_etag:
            principal.resource_version = parse_etag(parent_etag)
        else:
            self.principal_row(principal)
        return data

    def authorization_metadata(self) -> dict[str, Any]:
        response = self.request("GET", "/.well-known/oauth-authorization-server")
        assert_status(response, 200, operation="读取 OAuth 授权服务器发现")
        return json_object(response, operation="读取 OAuth 授权服务器发现")

    def protected_resource_metadata(self, suffix: str) -> dict[str, Any]:
        response = self.request(
            "GET", f"/.well-known/oauth-protected-resource/{suffix.lstrip('/')}"
        )
        assert_status(response, 200, operation=f"读取 {suffix} 资源发现")
        return json_object(response, operation=f"读取 {suffix} 资源发现")

    def exchange_token(
        self,
        principal: ServicePrincipalFixture,
        resource: str,
        scopes: Iterable[str] | None = None,
    ) -> AgentAccessToken:
        requested_scopes = tuple(scopes or principal.scopes)
        metadata = self.authorization_metadata()
        token_endpoint = metadata.get("token_endpoint")
        assert isinstance(token_endpoint, str) and token_endpoint, (
            "OAuth 授权服务器发现缺少 token_endpoint"
        )
        response = self.request(
            "POST",
            token_endpoint,
            data={
                "grant_type": "client_credentials",
                "project_key": self.project_key,
                "resource": resource,
                "scope": " ".join(requested_scopes),
            },
            auth=(principal.client_id, principal.client_secret),
        )
        assert_status(response, 200, operation="服务主体换取短期访问令牌")
        payload = json_object(response, operation="服务主体换取短期访问令牌")
        access_token = payload.get("access_token")
        assert isinstance(access_token, str) and access_token, (
            "OAuth token 响应缺少 access_token"
        )
        assert payload.get("token_type") == "Bearer", "OAuth token_type 必须为 Bearer"
        assert payload.get("resource") == resource, (
            "OAuth token resource/audience 不匹配"
        )
        assert payload.get("project_key") == self.project_key, (
            "OAuth token project_key 不匹配"
        )
        granted = tuple(str(payload.get("scope") or "").split())
        assert set(granted) == set(requested_scopes), (
            "OAuth 返回的 scope 与请求的最小权限不一致"
        )
        assert response.headers.get("Cache-Control") == "no-store", (
            "OAuth token 响应必须禁止缓存"
        )
        return AgentAccessToken(
            access_token=access_token,
            resource=resource,
            project_key=self.project_key,
            scopes=granted,
        )

    def rotate_credential(self, principal: ServicePrincipalFixture) -> None:
        current = self.principal_row(principal)
        response = self.admin_request(
            "POST",
            self.agent_admin_path(
                f"service-principals/{principal.client_id}/credentials/rotate"
            ),
            if_match=int(current["resource_version"]),
            operation="rotate-credential",
        )
        assert_status(response, 200, operation="轮换服务主体凭据")
        data = envelope_data(response, operation="轮换服务主体凭据")
        assert isinstance(data, dict), "轮换凭据响应 data 必须为对象"
        secret = data.get("client_secret")
        assert isinstance(secret, str) and secret, "轮换凭据未返回一次性 secret"
        principal.client_secret = secret
        principal.resource_version = parse_etag(response.headers.get("ETag", ""))

    def set_principal_status(
        self,
        principal: ServicePrincipalFixture,
        *,
        status: str,
        read_only: bool = False,
        emergency_disabled: bool = False,
    ) -> None:
        current = self.principal_row(principal)
        response = self.admin_request(
            "PUT",
            self.agent_admin_path(f"service-principals/{principal.client_id}/status"),
            json_body={
                "status": status,
                "read_only": read_only,
                "emergency_disabled": emergency_disabled,
            },
            if_match=int(current["resource_version"]),
            operation=f"principal-status-{status}",
        )
        assert_status(response, 200, operation=f"设置服务主体状态为 {status}")
        principal.resource_version = parse_etag(response.headers.get("ETag", ""))

    def track_ticket(self, ticket: dict[str, Any]) -> None:
        self._resources.track_ticket(ticket)

    def track_lease(self, lease_id: str) -> None:
        if lease_id and lease_id not in self._leases:
            self._leases.append(lease_id)

    def release_tracked_lease(self, lease_id: str) -> None:
        if lease_id in self._leases:
            self._leases.remove(lease_id)

    def cleanup(self) -> None:
        """Release leases, disable policies, and deactivate E2E principals."""

        errors: list[str] = []
        lease_rows: list[dict[str, Any]] = []
        if self._leases:
            try:
                lease_rows = self._admin_directory_rows(
                    "leases",
                    sort_by="expires_at",
                    sort_order="asc",
                    operation="分页读取 E2E Agent 租约用于清理",
                    wanted_ids=set(self._leases),
                )
            except (AssertionError, requests.RequestException) as exc:
                errors.append(f"读取 Agent 清理租约失败：{type(exc).__name__}")
        for lease_id in reversed(self._leases):
            row = next(
                (item for item in lease_rows if item.get("id") == lease_id),
                None,
            )
            if not isinstance(row, dict):
                continue
            version = row.get("resource_version")
            if not isinstance(version, int) or version <= 0:
                errors.append(f"租约 {lease_id} 缺少清理版本")
                continue
            try:
                response = self.admin_request(
                    "POST",
                    self.agent_admin_path(f"leases/{lease_id}/force-release"),
                    if_match=version,
                    operation="cleanup-force-release",
                )
                if response.status_code not in (200, 404, 409):
                    errors.append(
                        f"强制释放租约 {lease_id} 失败：HTTP {response.status_code}"
                    )
            except requests.RequestException as exc:
                errors.append(f"强制释放租约 {lease_id} 失败：{type(exc).__name__}")

        for principal in reversed(self._principals):
            try:
                policies = self.policy_rows(principal)
                tracked_policy_ids = set(principal.policy_ids)
                for policy in policies:
                    policy_id = policy.get("id")
                    if policy_id not in tracked_policy_ids or not policy.get(
                        "is_active"
                    ):
                        continue
                    version = policy.get("resource_version")
                    if not isinstance(version, int) or version <= 0:
                        errors.append(f"E2E 策略 {policy_id} 缺少清理版本")
                        continue
                    disabled = self.admin_request(
                        "DELETE",
                        self.agent_admin_path(
                            "service-principals/"
                            f"{principal.client_id}/policies/{policy_id}"
                        ),
                        if_match=version,
                        operation="cleanup-disable-policy",
                    )
                    if disabled.status_code not in (200, 404, 409):
                        errors.append(
                            f"停用 E2E 策略 {policy_id} 失败："
                            f"HTTP {disabled.status_code}"
                        )
                row = self.principal_row(principal)
                if (
                    row.get("status") != "inactive"
                    or row.get("read_only") is not False
                    or row.get("emergency_disabled") is not False
                ):
                    self.set_principal_status(
                        principal,
                        status="inactive",
                        read_only=False,
                        emergency_disabled=False,
                    )
            except (AssertionError, requests.RequestException) as exc:
                errors.append(
                    f"停用 E2E 服务主体 {principal.client_id} 失败："
                    f"{type(exc).__name__}"
                )

        self.close()
        if errors:
            raise AssertionError("Agent 协议 E2E 精确清理失败：\n" + "\n".join(errors))
