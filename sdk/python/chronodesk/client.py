"""Dependency-free client for the project-scoped ChronoDesk Agent REST API."""

from __future__ import annotations

import base64
import ipaddress
import json
import math
import re
import ssl
from dataclasses import dataclass
from enum import StrEnum
from typing import Any
from urllib import error, parse, request

_PROJECT_KEY = re.compile(r"^[A-Z][A-Z0-9]{1,15}$")
_MAX_RESPONSE_BYTES = 4 << 20


class Audience(StrEnum):
    """One exact OAuth protected resource."""

    API = "api"
    MCP = "mcp"
    A2A = "a2a"


@dataclass(frozen=True, slots=True)
class ClientCredentials:
    """OAuth client credentials for one explicit audience."""

    client_id: str
    client_secret: str
    audience: Audience
    scopes: tuple[str, ...] = ()


@dataclass(frozen=True, slots=True)
class TokenResponse:
    """A project- and audience-bound OAuth response."""

    access_token: str
    token_type: str
    expires_in: int
    scope: str
    resource: str
    project_key: str


class APIError(RuntimeError):
    """A safe representation of a non-success ChronoDesk response."""

    def __init__(
        self,
        status: int,
        code: str = "",
        *,
        retryable: bool = False,
        request_id: str = "",
    ) -> None:
        self.status = status
        self.code = code
        self.retryable = retryable
        self.request_id = request_id
        label = code or "http_error"
        super().__init__(f"ChronoDesk API {status}: {label}")


class _RejectRedirects(request.HTTPRedirectHandler):
    def redirect_request(
        self,
        req: request.Request,
        fp: Any,
        code: int,
        msg: str,
        headers: Any,
        newurl: str,
    ) -> None:
        del req, fp, code, msg, headers, newurl


class ChronoDeskClient:
    """A client permanently bound to one ChronoDesk project."""

    def __init__(
        self,
        base_url: str,
        project_key: str,
        *,
        access_token: str | None = None,
        timeout: float = 30.0,
        ssl_context: ssl.SSLContext | None = None,
    ) -> None:
        self._base_url = _validate_base_url(base_url)
        if not _PROJECT_KEY.fullmatch(project_key):
            raise ValueError("project_key must match ^[A-Z][A-Z0-9]{1,15}$")
        if not math.isfinite(timeout) or timeout <= 0 or timeout > 300:
            raise ValueError("timeout must be greater than 0 and at most 300 seconds")
        if access_token is not None and not access_token.strip():
            raise ValueError("access_token cannot be blank")
        self.project_key = project_key
        self._access_token = access_token.strip() if access_token else None
        self._timeout = timeout
        self._ssl_context = ssl_context
        handlers: list[Any] = [_RejectRedirects()]
        if ssl_context is not None:
            handlers.append(request.HTTPSHandler(context=ssl_context))
        self._opener = request.build_opener(*handlers)

    def with_access_token(self, access_token: str) -> ChronoDeskClient:
        """Return a new authenticated client for the same project."""

        return ChronoDeskClient(
            self._base_url,
            self.project_key,
            access_token=access_token,
            timeout=self._timeout,
            ssl_context=self._ssl_context,
        )

    def exchange_client_credentials(
        self,
        credentials: ClientCredentials,
    ) -> TokenResponse:
        """Exchange credentials without including the secret in errors."""

        if not credentials.client_id.strip() or not credentials.client_secret:
            raise ValueError("client_id and client_secret are required")
        if not isinstance(credentials.audience, Audience):
            raise TypeError("audience must be explicitly Audience.API, MCP, or A2A")
        resource = self._resource(credentials.audience)
        form: dict[str, str] = {
            "grant_type": "client_credentials",
            "project_key": self.project_key,
            "resource": resource,
        }
        scopes = tuple(dict.fromkeys(" ".join(credentials.scopes).split()))
        if scopes:
            form["scope"] = " ".join(scopes)
        basic = base64.b64encode(
            f"{credentials.client_id.strip()}:{credentials.client_secret}".encode()
        ).decode("ascii")
        http_request = request.Request(
            self._endpoint("/oauth/token"),
            data=parse.urlencode(form).encode(),
            method="POST",
            headers={
                "Accept": "application/json",
                "Authorization": f"Basic {basic}",
                "Content-Type": "application/x-www-form-urlencoded",
                "User-Agent": "chronodesk-python/0.1",
            },
        )
        try:
            payload = self._execute(http_request)
        finally:
            http_request.remove_header("Authorization")
            basic = ""
        access_token = payload.get("access_token")
        token_type = payload.get("token_type")
        expires_in = payload.get("expires_in")
        scope = payload.get("scope")
        response_resource = payload.get("resource")
        response_project = payload.get("project_key")
        if (
            not isinstance(access_token, str)
            or not isinstance(token_type, str)
            or not isinstance(expires_in, int)
            or isinstance(expires_in, bool)
            or not isinstance(scope, str)
            or not isinstance(response_resource, str)
            or not isinstance(response_project, str)
        ):
            raise TypeError("OAuth response is malformed")
        token = TokenResponse(
            access_token=access_token,
            token_type=token_type,
            expires_in=expires_in,
            scope=scope,
            resource=response_resource,
            project_key=response_project,
        )
        if (
            not token.access_token
            or token.token_type != "Bearer"
            or token.expires_in <= 0
            or token.expires_in > 3600
            or token.project_key != self.project_key
            or token.resource != resource
        ):
            raise RuntimeError("OAuth response violates project or audience binding")
        return token

    def capabilities(self) -> dict[str, Any]:
        """Read the supported contract for this project."""

        envelope = self._agent_get("/capabilities")
        data = envelope.get("data")
        if not isinstance(data, dict):
            raise TypeError("capabilities response is malformed")
        scopes = data.get("scopes_supported")
        concurrency = data.get("concurrency")
        oauth_metadata = data.get("oauth_metadata")
        if (
            data.get("api_version") != "v2"
            or data.get("mcp_version") != "2026-07-28"
            or data.get("a2a_version") != "1.0"
            or not data.get("openapi")
            or not data.get("asyncapi")
            or data.get("mcp_endpoint") != "/mcp"
            or data.get("a2a_endpoint") != "/a2a/v1"
            or data.get("agent_card") != "/.well-known/agent-card.json"
            or not isinstance(oauth_metadata, dict)
            or oauth_metadata.get("api")
            != "/.well-known/oauth-protected-resource/api/v2"
            or oauth_metadata.get("mcp") != "/.well-known/oauth-protected-resource/mcp"
            or oauth_metadata.get("a2a")
            != "/.well-known/oauth-protected-resource/a2a/v1"
            or not isinstance(scopes, list)
            or "tickets:read" not in scopes
            or not isinstance(concurrency, dict)
            or concurrency.get("optimistic_version") is not True
            or concurrency.get("ticket_leases") is not True
            or concurrency.get("idempotency_keys") is not True
        ):
            raise RuntimeError(
                "capabilities response violates the supported protocol versions"
            )
        return envelope

    def list_tickets(
        self,
        *,
        cursor: str | None = None,
        limit: int | None = None,
        status: str | None = None,
        priority: str | None = None,
        search: str | None = None,
    ) -> dict[str, Any]:
        """List tickets only inside this client's project."""

        query: dict[str, str] = {}
        if cursor:
            query["cursor"] = cursor
        if limit is not None:
            if limit < 1 or limit > 100:
                raise ValueError("limit must be between 1 and 100")
            query["limit"] = str(limit)
        if status:
            query["status"] = status
        if priority:
            query["priority"] = priority
        if search:
            query["search"] = search
        suffix = "/tickets"
        if query:
            suffix += "?" + parse.urlencode(query)
        envelope = self._agent_get(suffix)
        if not isinstance(envelope.get("data"), list):
            raise TypeError("ticket list response is malformed")
        return envelope

    def _agent_get(self, suffix: str) -> dict[str, Any]:
        if not self._access_token:
            raise ValueError("API audience access_token is required")
        result = self._execute(
            request.Request(
                self._agent_endpoint(suffix),
                method="GET",
                headers={
                    "Accept": "application/json",
                    "Authorization": f"Bearer {self._access_token}",
                    "User-Agent": "chronodesk-python/0.1",
                },
            )
        )
        if not isinstance(result, dict):
            raise TypeError("ChronoDesk response is malformed")
        return result

    def _execute(self, http_request: request.Request) -> dict[str, Any]:
        try:
            with self._opener.open(http_request, timeout=self._timeout) as response:
                body = response.read(_MAX_RESPONSE_BYTES + 1)
                if len(body) > _MAX_RESPONSE_BYTES:
                    raise RuntimeError("ChronoDesk response exceeds 4 MiB")
        except error.HTTPError as exc:
            body = exc.read(_MAX_RESPONSE_BYTES)
            problem = _decode_object(body)
            raise APIError(
                exc.code,
                str(problem.get("code", problem.get("error", ""))),
                retryable=bool(problem.get("retryable", False)),
                request_id=str(problem.get("request_id", "")),
            ) from None
        except error.URLError as exc:
            raise RuntimeError(f"ChronoDesk request failed: {exc.reason}") from None
        result = _decode_object(body)
        return result

    def _resource(self, audience: Audience) -> str:
        resources = {
            Audience.API: "/api/v2",
            Audience.MCP: "/mcp",
            Audience.A2A: "/a2a/v1",
        }
        try:
            return self._endpoint(resources[audience])
        except KeyError as exc:
            raise ValueError(
                "audience must be explicitly Audience.API, MCP, or A2A"
            ) from exc

    def _endpoint(self, path: str) -> str:
        return f"{self._base_url}/{path.lstrip('/')}"

    def _agent_endpoint(self, suffix: str) -> str:
        path, separator, query = suffix.partition("?")
        target = self._endpoint(
            f"/api/v2/projects/{parse.quote(self.project_key, safe='')}/"
            f"{path.lstrip('/')}"
        )
        return target + (separator + query if separator else "")


def _validate_base_url(raw: str) -> str:
    parsed = parse.urlsplit(raw.strip())
    if (
        parsed.scheme not in {"http", "https"}
        or not parsed.netloc
        or parsed.username is not None
        or parsed.password is not None
        or parsed.path not in {"", "/"}
        or parsed.query
        or parsed.fragment
    ):
        raise ValueError(
            "base_url must be an http(s) origin without path, credentials, query, "
            "or fragment"
        )
    if parsed.scheme == "http" and not _loopback_hostname(parsed.hostname or ""):
        raise ValueError("non-loopback base_url must use HTTPS")
    return parse.urlunsplit((parsed.scheme, parsed.netloc, "", "", ""))


def _loopback_hostname(hostname: str) -> bool:
    normalized = hostname.rstrip(".").lower()
    if normalized == "localhost":
        return True
    try:
        return ipaddress.ip_address(normalized).is_loopback
    except ValueError:
        return False


def _decode_object(body: bytes) -> dict[str, Any]:
    try:
        value = json.loads(body)
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise RuntimeError("ChronoDesk returned invalid JSON") from exc
    if not isinstance(value, dict):
        raise TypeError("ChronoDesk JSON response must be an object")
    return value
