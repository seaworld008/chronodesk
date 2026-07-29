"""Safety gates and credential-safe diagnostics for black-box tests."""

from __future__ import annotations

import html
import json
import os
import re
import threading
from collections.abc import Mapping
from dataclasses import dataclass
from pathlib import Path
from typing import Any
from urllib.parse import quote, quote_plus, urlsplit, urlunsplit

import requests

DEFAULT_API_BASE_URL = "http://localhost:8081/api"
REMOTE_E2E_ENV = "CHRONODESK_ALLOW_REMOTE_E2E"
OWNERSHIP_PREFIX_ENV = "CHRONODESK_E2E_OWNERSHIP_PREFIX"
EPHEMERAL_E2E_ENV = "CHRONODESK_EPHEMERAL_E2E"
REDACTED = "<redacted>"

_OWNERSHIP_PREFIX = re.compile(r"e2e-[a-z0-9][a-z0-9._-]{4,58}[a-z0-9]")
_SENSITIVE_MARKERS = (
    "accesstoken",
    "refreshtoken",
    "authorization",
    "backupcode",
    "clientsecret",
    "confirm_password",
    "confirmpassword",
    "cookie",
    "credential",
    "databaseurl",
    "dsn",
    "newpassword",
    "otpauth",
    "otpsecret",
    "password",
    "redisurl",
    "secret",
    "setcookie",
    "totpseed",
    "totpsecret",
)
_SENSITIVE_EXACT_KEYS = {
    "token",
    "tokens",
}
_SENSITIVE_KEY_PATTERN = (
    r"(?:access[_-]?token|refresh[_-]?token|authorization|"
    r"backup[_-]?codes?|client[_-]?secret|confirm[_-]?password|"
    r"cookies?|credentials?|database[_-]?url|dsn|new[_-]?password|"
    r"otp[_-]?secret|password|redis[_-]?url|secret|set[_-]?cookie|"
    r"tokens?|totp[_-]?(?:seed|secret))"
)
_HEADER_PATTERN = re.compile(
    r"(?im)\b(?:authorization|proxy-authorization|cookie|set-cookie)"
    r"\s*:\s*[^\r\n]+"
)
_QUOTED_VALUE_PATTERN = re.compile(
    rf"(?P<prefix>['\"]?{_SENSITIVE_KEY_PATTERN}['\"]?\s*[:=]\s*)"
    r"(?P<quote>['\"])(?P<value>.*?)(?P=quote)",
    re.IGNORECASE,
)
_LIST_VALUE_PATTERN = re.compile(
    rf"(?P<prefix>['\"]?{_SENSITIVE_KEY_PATTERN}['\"]?\s*[:=]\s*)"
    r"\[[^\]]*]",
    re.IGNORECASE | re.DOTALL,
)
_UNQUOTED_VALUE_PATTERN = re.compile(
    rf"(?P<prefix>\b{_SENSITIVE_KEY_PATTERN}\b\s*[:=]\s*)"
    r"(?P<value>[^,\s;}})\]]+)",
    re.IGNORECASE,
)
_BEARER_PATTERN = re.compile(
    r"(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+",
)
_JWT_PATTERN = re.compile(
    r"\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}"
    r"(?:\.[A-Za-z0-9_-]{8,})?\b"
)
_DSN_PATTERN = re.compile(
    r"(?i)\b(?:postgres(?:ql)?|redis(?:s)?|mysql|mariadb|"
    r"mongodb(?:\+srv)?|amqp|amqps)://[^\s'\"<>]+"
)
_URL_USERINFO_PATTERN = re.compile(r"(?i)\b(?P<scheme>https?://)[^/\s:@]+:[^@/\s]+@")

_registered_secrets: set[str] = set()
_secret_lock = threading.RLock()


class TestSafetyError(RuntimeError):
    """Raised before a test can target an unsafe environment."""


@dataclass(frozen=True)
class TestTarget:
    """Validated test target metadata."""

    base_url: str
    origin: str
    is_loopback: bool
    ownership_prefix: str | None


def _normalized_key(key: Any) -> str:
    return re.sub(r"[^a-z0-9]", "", str(key).lower())


def is_sensitive_key(key: Any) -> bool:
    """Return whether a mapping/header key identifies credential material."""

    normalized = _normalized_key(key)
    return normalized in _SENSITIVE_EXACT_KEYS or any(
        marker.replace("_", "") in normalized for marker in _SENSITIVE_MARKERS
    )


def register_secret(value: Any) -> None:
    """Remember a runtime secret so assertion introspection can remove it."""

    if isinstance(value, bytes):
        value = value.decode("utf-8", errors="replace")
    if not isinstance(value, str):
        return
    candidate = value.strip()
    if len(candidate) < 4 or candidate == REDACTED:
        return
    with _secret_lock:
        _registered_secrets.add(candidate)


def _register_secret_leaves(value: Any) -> None:
    if isinstance(value, Mapping):
        for nested in value.values():
            _register_secret_leaves(nested)
    elif isinstance(value, (list, tuple, set)):
        for nested in value:
            _register_secret_leaves(nested)
    else:
        register_secret(value)


def register_sensitive_values(value: Any) -> None:
    """Register secrets found under sensitive keys without logging them."""

    if isinstance(value, Mapping):
        descriptor = value.get("key", value.get("name"))
        if descriptor is not None and is_sensitive_key(descriptor):
            for field_name in ("value", "default_value", "current_value"):
                if field_name in value:
                    _register_secret_leaves(value[field_name])
        for key, nested in value.items():
            if is_sensitive_key(key):
                _register_secret_leaves(nested)
            else:
                register_sensitive_values(nested)
    elif isinstance(value, (list, tuple)):
        for nested in value:
            register_sensitive_values(nested)


def register_headers(headers: Mapping[str, Any] | None) -> None:
    """Register Authorization/Cookie header values and individual credentials."""

    if not headers:
        return
    for key, raw_value in headers.items():
        if not is_sensitive_key(key) or raw_value is None:
            continue
        value = str(raw_value)
        register_secret(value)
        if _normalized_key(key) == "authorization":
            _, _, credential = value.partition(" ")
            register_secret(credential)
        if "cookie" in _normalized_key(key):
            for part in re.split(r"[;,]", value):
                _, separator, cookie_value = part.strip().partition("=")
                if separator:
                    register_secret(cookie_value)


def register_environment_secrets(
    environ: Mapping[str, str] | None = None,
) -> None:
    """Register credential-bearing environment variables for report scrubbing."""

    environment = os.environ if environ is None else environ
    register_sensitive_values(environment)


def register_response_secrets(response: requests.Response) -> None:
    """Register credentials returned by an HTTP response."""

    register_headers(response.headers)
    try:
        register_sensitive_values(response.json())
    except ValueError:
        # Text-only responses are scrubbed by patterns and previously registered
        # request secrets. Treating arbitrary response text as a secret would hide
        # all useful diagnostics.
        return


def redact_payload(value: Any) -> Any:
    """Return a recursively redacted diagnostic copy."""

    if isinstance(value, Mapping):
        result: dict[str, Any] = {}
        descriptor = value.get("key", value.get("name"))
        descriptor_is_sensitive = descriptor is not None and is_sensitive_key(
            descriptor
        )
        for key, nested in value.items():
            rendered_key = str(key)
            if is_sensitive_key(key) or (
                descriptor_is_sensitive
                and rendered_key in {"value", "default_value", "current_value"}
            ):
                result[rendered_key] = REDACTED
            else:
                result[rendered_key] = redact_payload(nested)
        return result
    if isinstance(value, list):
        return [redact_payload(nested) for nested in value]
    if isinstance(value, tuple):
        return tuple(redact_payload(nested) for nested in value)
    if isinstance(value, str):
        return redact_text(value)
    return value


def _redact_registered_secrets(text: str) -> str:
    with _secret_lock:
        secrets = sorted(_registered_secrets, key=len, reverse=True)
    for secret in secrets:
        variants = {
            secret,
            html.escape(secret),
            quote(secret, safe=""),
            quote_plus(secret, safe=""),
        }
        for variant in variants:
            if variant:
                text = text.replace(variant, REDACTED)
    return text


def redact_text(value: Any) -> str:
    """Redact secrets from free-form exception, log, and report text."""

    text = str(value)
    text = _redact_registered_secrets(text)
    text = _HEADER_PATTERN.sub(
        lambda match: match.group(0).split(":", 1)[0] + ": " + REDACTED, text
    )
    text = _LIST_VALUE_PATTERN.sub(
        lambda match: f"{match.group('prefix')}[{REDACTED!r}]",
        text,
    )
    text = _QUOTED_VALUE_PATTERN.sub(
        lambda match: (
            f"{match.group('prefix')}{match.group('quote')}"
            f"{REDACTED}{match.group('quote')}"
        ),
        text,
    )
    text = _UNQUOTED_VALUE_PATTERN.sub(
        lambda match: f"{match.group('prefix')}{REDACTED}",
        text,
    )
    text = _BEARER_PATTERN.sub(f"Bearer {REDACTED}", text)
    text = _JWT_PATTERN.sub(REDACTED, text)
    text = _DSN_PATTERN.sub("<redacted-dsn>", text)
    return _URL_USERINFO_PATTERN.sub(r"\g<scheme><redacted>@", text)


def scrub_html_report(path: str | Path) -> bool:
    """Remove every registered runtime secret from a generated HTML report."""

    report_path = Path(path)
    if not report_path.is_file():
        return False
    original = report_path.read_text(encoding="utf-8", errors="replace")
    scrubbed = _redact_registered_secrets(original)
    if scrubbed == original:
        return False
    report_path.write_text(scrubbed, encoding="utf-8")
    return True


def safe_diagnostic(value: Any, *, limit: int = 3000) -> str:
    """Render a bounded diagnostic without credential material."""

    safe = redact_payload(value)
    try:
        rendered = json.dumps(safe, ensure_ascii=False, default=str, sort_keys=True)
    except (TypeError, ValueError):
        rendered = repr(safe)
    return redact_text(rendered)[:limit]


def response_diagnostic(response: requests.Response, *, limit: int = 3000) -> str:
    """Build a bounded HTTP diagnostic with a minimal safe header allowlist."""

    register_response_secrets(response)
    try:
        payload: Any = response.json()
    except ValueError:
        payload = response.text[:limit]
    return (
        f"HTTP {response.status_code}; "
        f"content_type={response.headers.get('Content-Type', '')!r}; "
        f"body={safe_diagnostic(payload, limit=limit)}"
    )


def _validated_ownership_prefix(environment: Mapping[str, str]) -> str | None:
    prefix = environment.get(OWNERSHIP_PREFIX_ENV, "").strip().lower()
    if not prefix:
        return None
    if _OWNERSHIP_PREFIX.fullmatch(prefix) is None:
        raise TestSafetyError(
            f"{OWNERSHIP_PREFIX_ENV} 必须以 e2e- 开头，且为 10-64 位"
            "小写字母、数字、点、下划线或连字符"
        )
    return prefix


def validate_test_target(
    base_url: str,
    environ: Mapping[str, str] | None = None,
) -> TestTarget:
    """Fail closed unless a non-loopback target is explicitly isolated."""

    environment = os.environ if environ is None else environ
    try:
        parsed = urlsplit(base_url.strip())
        port = parsed.port
    except (AttributeError, ValueError) as exc:
        raise TestSafetyError("测试 API 地址无效") from exc
    if (
        parsed.scheme not in {"http", "https"}
        or not parsed.hostname
        or parsed.username is not None
        or parsed.password is not None
        or parsed.query
        or parsed.fragment
        or parsed.path.rstrip("/") != "/api"
    ):
        raise TestSafetyError(
            "TEST_API_BASE_URL 必须是无凭据、查询参数和片段的 http(s) /api 根地址"
        )

    hostname = parsed.hostname.lower()
    is_loopback = hostname in {"localhost", "127.0.0.1", "::1"}
    ownership_prefix = _validated_ownership_prefix(environment)
    if not is_loopback and (
        environment.get(REMOTE_E2E_ENV) != "1" or ownership_prefix is None
    ):
        raise TestSafetyError(
            f"拒绝对非回环目标 {parsed.scheme}://{hostname}"
            f"{f':{port}' if port is not None else ''} 运行写测试；"
            f"仅隔离测试环境可同时设置 {REMOTE_E2E_ENV}=1 与"
            f" {OWNERSHIP_PREFIX_ENV}=e2e-<唯一所有者>"
        )

    netloc = f"[{hostname}]" if ":" in hostname else hostname
    if port is not None:
        netloc = f"{netloc}:{port}"
    origin = urlunsplit((parsed.scheme, netloc, "", "", ""))
    normalized_path = parsed.path.rstrip("/")
    normalized_url = urlunsplit((parsed.scheme, netloc, normalized_path, "", ""))
    return TestTarget(
        base_url=normalized_url,
        origin=origin,
        is_loopback=is_loopback,
        ownership_prefix=ownership_prefix,
    )


def healthcheck_url_for(
    target: TestTarget,
    configured_url: str | None = None,
) -> str:
    """Return a same-origin health URL so test configuration cannot cause SSRF."""

    api = urlsplit(target.base_url)
    default_path = f"{api.path[: -len('/api')]}/healthz"
    candidate = configured_url or urlunsplit(
        (api.scheme, api.netloc, default_path, "", "")
    )
    try:
        parsed = urlsplit(candidate)
        port = parsed.port
    except ValueError as exc:
        raise TestSafetyError("TEST_HEALTHCHECK_URL 无效") from exc
    hostname = (parsed.hostname or "").lower()
    netloc = f"[{hostname}]" if ":" in hostname else hostname
    if port is not None:
        netloc = f"{netloc}:{port}"
    origin = urlunsplit((parsed.scheme, netloc, "", "", ""))
    if (
        origin != target.origin
        or parsed.username is not None
        or parsed.password is not None
        or parsed.query
        or parsed.fragment
    ):
        raise TestSafetyError(
            "TEST_HEALTHCHECK_URL 必须与 TEST_API_BASE_URL 同源且不含凭据、"
            "查询参数或片段"
        )
    return urlunsplit((parsed.scheme, netloc, parsed.path or "/healthz", "", ""))


def assert_local_ephemeral_target(
    base_url: str,
    operation: str,
    environ: Mapping[str, str] | None = None,
) -> None:
    """Allow global configuration mutation only on explicit disposable localhost."""

    environment = os.environ if environ is None else environ
    target = validate_test_target(base_url, environment)
    if not target.is_loopback or environment.get(EPHEMERAL_E2E_ENV) != "1":
        raise TestSafetyError(
            f"拒绝执行全局操作“{operation}”；仅回环一次性环境可设置"
            f" {EPHEMERAL_E2E_ENV}=1 后运行"
        )


def sanitize_pytest_report(report: Any) -> None:
    """Remove credentials from pytest console/HTML report fields in place."""

    longrepr = getattr(report, "longrepr", None)
    if isinstance(longrepr, tuple) and len(longrepr) == 3:
        report.longrepr = (longrepr[0], longrepr[1], redact_text(longrepr[2]))
    elif longrepr is not None:
        report.longrepr = redact_text(longrepr)

    sections = getattr(report, "sections", None)
    if sections is not None:
        report.sections = [
            (redact_text(name), redact_text(content)) for name, content in sections
        ]
    user_properties = getattr(report, "user_properties", None)
    if user_properties is not None:
        report.user_properties = [
            (redact_text(name), safe_diagnostic(value))
            for name, value in user_properties
        ]
    if hasattr(report, "wasxfail"):
        report.wasxfail = redact_text(report.wasxfail)
    if hasattr(report, "extras"):
        report.extras = redact_payload(report.extras)
