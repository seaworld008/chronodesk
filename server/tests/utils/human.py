"""Safe factories for Human REST black-box scenarios."""

from __future__ import annotations

import base64
import hashlib
import json
import secrets
import time
from dataclasses import dataclass, field, replace
from typing import Any

from .api import APIClient
from .intake import PublishedTicketIntake, load_published_ticket_intake
from .safety import (
    register_secret,
    response_diagnostic,
    safe_diagnostic,
)

PLATFORM_ROLES = (
    "platform_admin",
    "security_auditor",
    "emergency_operator",
    "member",
)
PROJECT_ROLES = (
    "project_admin",
    "manager",
    "agent",
    "requester",
    "observer",
)


def assert_human_session_contract(
    session: dict[str, Any],
    *,
    expected_platform_role: str,
) -> None:
    """Assert Auth/JWT identity uses only the closed platform-role claim."""

    assert expected_platform_role in PLATFORM_ROLES
    user = session.get("user")
    assert isinstance(user, dict), safe_diagnostic(session)
    assert user.get("platform_role") == expected_platform_role, safe_diagnostic(user)
    assert "role" not in user, safe_diagnostic(user)
    assert "project_role" not in user, safe_diagnostic(user)

    for token_name in ("access_token", "refresh_token"):
        token = session.get(token_name)
        assert isinstance(token, str) and token
        segments = token.split(".")
        assert len(segments) == 3, f"{token_name} is not a compact JWT"
        encoded_payload = segments[1]
        padding = "=" * (-len(encoded_payload) % 4)
        try:
            claims = json.loads(
                base64.urlsafe_b64decode(encoded_payload + padding).decode("utf-8")
            )
        except (UnicodeDecodeError, ValueError) as exc:
            raise AssertionError(f"{token_name} payload is not valid JSON") from exc
        assert isinstance(claims, dict)
        assert claims.get("platform_role") == expected_platform_role, safe_diagnostic(
            claims
        )
        assert "role" not in claims, safe_diagnostic(claims)
        assert "project_role" not in claims, safe_diagnostic(claims)


_WEAK_PASSWORD_PATTERNS = (
    "password",
    "123456",
    "123456789",
    "qwerty",
    "abc123",
    "password123",
    "admin",
    "letmein",
    "welcome",
    "monkey",
    "1234567890",
    "qwertyuiop",
    "asdfghjkl",
    "zxcvbnm",
)


def _has_sequential_ascii(value: str, length: int = 4) -> bool:
    """Mirror the backend's ascending/descending byte-sequence rejection."""

    for start in range(len(value) - length + 1):
        codepoint = ord(value[start])
        window = value[start : start + length]
        if all(
            ord(character) == codepoint + offset
            for offset, character in enumerate(window)
        ):
            return True
        if all(
            ord(character) == codepoint - offset
            for offset, character in enumerate(window)
        ):
            return True
    return False


def strong_password() -> str:
    """Return a unique password accepted by the production policy."""

    while True:
        password = f"Aa1!{secrets.token_hex(10)}Z"
        no_repeating_triple = all(
            password[index] != password[index + 1]
            or password[index] != password[index + 2]
            for index in range(len(password) - 2)
        )
        lowered = password.lower()
        if (
            no_repeating_triple
            and not _has_sequential_ascii(password)
            and not any(pattern in lowered for pattern in _WEAK_PASSWORD_PATTERNS)
        ):
            register_secret(password)
            return password


@dataclass(frozen=True)
class HumanIdentity:
    id: int
    platform_role: str
    project_role: str | None
    username: str
    email: str
    password: str = field(repr=False)
    access_token: str = field(repr=False)
    refresh_token: str = field(repr=False)
    api: APIClient = field(repr=False)

    def __post_init__(self) -> None:
        if self.platform_role not in PLATFORM_ROLES:
            raise ValueError(f"unsupported platform role {self.platform_role!r}")
        if self.project_role is not None and self.project_role not in PROJECT_ROLES:
            raise ValueError(f"unsupported project role {self.project_role!r}")


class E2EResourceManager:
    """Create and precisely clean resources owned by one black-box run."""

    def __init__(
        self,
        api_client: APIClient,
        admin_api: APIClient,
        run_id: str,
        project_key: str,
    ) -> None:
        if (
            api_client.project_key != project_key
            or admin_api.project_key != project_key
        ):
            raise AssertionError("E2E 资源管理器要求统一的显式项目绑定")
        self.api_client = api_client
        self.admin_api = admin_api
        self.run_id = run_id
        self.project_key = project_key
        self.prefix = f"E2E-{run_id}-"
        # Usernames are capped at 50 characters.  Keeping an arbitrarily long
        # ownership prefix at the front used to truncate both the role label and
        # nonce, so e.g. agent-a and agent-b could collapse to the same username.
        # A stable run token preserves ownership while leaving room for the
        # per-identity label and a cryptographically random suffix.
        self._user_run_token = hashlib.sha256(run_id.encode("utf-8")).hexdigest()[:12]
        self._users: list[tuple[int, str]] = []
        self._memberships: list[int] = []
        self._tickets: list[tuple[int, str]] = []
        self._notifications: list[int] = []
        self._clients: list[APIClient] = []
        self._published_ticket_intake: PublishedTicketIntake | None = None

    def unique(self, label: str) -> str:
        return f"{self.prefix}{label}-{time.time_ns()}-{secrets.token_hex(2)}"

    def project_path(self, suffix: str) -> str:
        return self.admin_api.project_path(suffix)

    def ticket_create_payload(
        self,
        payload: dict[str, Any],
        *,
        work_class: str | None = None,
    ) -> dict[str, Any]:
        """Bind a Ticket create payload to this project's published release."""

        if self._published_ticket_intake is None:
            self._published_ticket_intake = load_published_ticket_intake(self.admin_api)
        return self._published_ticket_intake.ticket_create_payload(
            payload,
            work_class=work_class,
        )

    def create_platform_identity(
        self,
        platform_role: str,
        *,
        label: str,
    ) -> HumanIdentity:
        """Create one platform user without granting any project access."""

        assert platform_role in PLATFORM_ROLES, (
            f"unsupported platform role {platform_role!r}"
        )
        safe_label = "".join(
            character if character.isalnum() else "_" for character in label.lower()
        )
        compact_label = safe_label[:16] or "identity"
        nonce = secrets.token_hex(6)
        username = f"e2e_{self._user_run_token}_{compact_label}_{nonce}"
        email = f"e2e+{self._user_run_token}.{compact_label}.{nonce}@example.com"
        password = strong_password()
        # DisplayName is capped at 100 characters.  Remote ownership prefixes
        # may be intentionally verbose, so use the stable run token here too
        # and keep the time/nonce suffix intact instead of truncating it away.
        display_name = (
            f"E2E-{self._user_run_token}-{compact_label}-"
            f"{time.time_ns()}-{secrets.token_hex(2)}"
        )
        response = self.admin_api.post_json(
            "/platform/users",
            {
                "username": username,
                "email": email,
                "password": password,
                "first_name": "E2E",
                "last_name": label,
                "display_name": display_name,
                "platform_role": platform_role,
                "department": "QA Automation",
                "job_title": "Human REST Black-box",
            },
        )
        assert response.status_code == 201, response_diagnostic(response)
        body = response.json()
        assert body.get("code") == 0, safe_diagnostic(body)
        created = body.get("data")
        assert isinstance(created, dict), safe_diagnostic(body)

        user_id = created.get("id")
        assert isinstance(user_id, int) and user_id > 0, safe_diagnostic(body)
        assert created.get("platform_role") == platform_role, safe_diagnostic(body)
        assert "role" not in created, safe_diagnostic(body)
        self._users.append((user_id, username))

        verification = self.admin_api.put_json(
            f"/platform/users/{user_id}",
            {
                "display_name": display_name,
                "email_verified": True,
            },
        )
        assert verification.status_code == 200, response_diagnostic(verification)

        tokens = self.api_client.login(email, password)
        assert_human_session_contract(
            tokens,
            expected_platform_role=platform_role,
        )
        access_token = tokens.get("access_token")
        refresh_token = tokens.get("refresh_token")
        assert isinstance(access_token, str) and access_token, safe_diagnostic(tokens)
        assert isinstance(refresh_token, str) and refresh_token, safe_diagnostic(tokens)
        api = self.api_client.with_auth(access_token)
        self._clients.append(api)
        return HumanIdentity(
            id=user_id,
            platform_role=platform_role,
            project_role=None,
            username=username,
            email=email,
            password=password,
            access_token=access_token,
            refresh_token=refresh_token,
            api=api,
        )

    def create_project_identity(
        self,
        project_role: str,
        platform_role: str = "member",
        *,
        label: str | None = None,
    ) -> HumanIdentity:
        """Create a user, then explicitly grant one project Membership."""

        assert project_role in PROJECT_ROLES, (
            f"unsupported project role {project_role!r}"
        )
        identity = self.create_platform_identity(
            platform_role,
            label=label or project_role,
        )
        membership = self.admin_api.post_json(
            self.project_path("memberships"),
            {
                "user_id": identity.id,
                "role": project_role,
            },
        )
        assert membership.status_code == 200, response_diagnostic(membership)
        self._memberships.append(identity.id)
        granted = membership.json().get("data")
        assert isinstance(granted, dict), response_diagnostic(membership)
        assert granted.get("user_id") == identity.id, safe_diagnostic(granted)
        assert granted.get("role") == project_role, safe_diagnostic(granted)

        context = identity.api.get_json(identity.api.project_path("context"))
        assert context.status_code == 200, response_diagnostic(context)
        access = context.json().get("data")
        assert isinstance(access, dict), response_diagnostic(context)
        assert access.get("project_role") == project_role, safe_diagnostic(access)
        assert "role" not in access, safe_diagnostic(access)
        return replace(
            identity,
            project_role=project_role,
        )

    def create_ticket(
        self,
        identity: HumanIdentity,
        label: str,
        **overrides: Any,
    ) -> dict[str, Any]:
        payload: dict[str, Any] = {
            "title": self.unique(f"{label}-ticket"),
            "description": f"{self.prefix}Human REST black-box ticket",
            "type": "request",
            "priority": "normal",
            "source": "api",
        }
        payload.update(overrides)
        payload = self.ticket_create_payload(payload)
        response = identity.api.post_json(
            identity.api.project_path("tickets"),
            payload,
        )
        assert response.status_code == 201, response_diagnostic(response)
        body = response.json()
        assert body.get("code") == 0, safe_diagnostic(body)
        ticket = body.get("data")
        assert isinstance(ticket, dict), safe_diagnostic(body)
        ticket_id = ticket.get("id")
        title = ticket.get("title")
        assert isinstance(ticket_id, int) and ticket_id > 0, safe_diagnostic(body)
        assert isinstance(title, str) and title.startswith(self.prefix), (
            safe_diagnostic(body)
        )
        self._tickets.append((ticket_id, title))
        return ticket

    def track_ticket(self, ticket: dict[str, Any]) -> None:
        ticket_id = ticket.get("id")
        title = ticket.get("title")
        assert isinstance(ticket_id, int) and ticket_id > 0, safe_diagnostic(ticket)
        assert isinstance(title, str) and title.startswith(self.prefix), (
            safe_diagnostic(ticket)
        )
        if all(existing_id != ticket_id for existing_id, _ in self._tickets):
            self._tickets.append((ticket_id, title))

    def track_user(self, user_id: int, username: str) -> None:
        """Track an unexpectedly accepted negative-test user for safe teardown."""

        assert isinstance(user_id, int) and user_id > 0
        assert username.startswith("e2e_")
        if all(existing_id != user_id for existing_id, _ in self._users):
            self._users.append((user_id, username))

    def track_notification(self, notification_id: int) -> None:
        """Track a notification created asynchronously for precise cleanup."""

        assert isinstance(notification_id, int) and notification_id > 0
        if notification_id not in self._notifications:
            self._notifications.append(notification_id)

    def create_notification(
        self,
        recipient_id: int,
        label: str,
    ) -> dict[str, Any]:
        title = self.unique(f"{label}-notification")
        response = self.admin_api.post_json(
            self.project_path("notifications"),
            {
                "type": "system_alert",
                "title": title,
                "content": f"{self.prefix}notification isolation",
                "priority": "normal",
                "channel": "in_app",
                "recipient_id": recipient_id,
            },
        )
        assert response.status_code == 201, response_diagnostic(response)
        notification = response.json().get("data")
        assert isinstance(notification, dict), response_diagnostic(response)
        notification_id = notification.get("id")
        assert isinstance(notification_id, int) and notification_id > 0, (
            safe_diagnostic(notification)
        )
        self._notifications.append(notification_id)
        return notification

    def cleanup(self) -> None:
        errors: list[str] = []

        for ticket_id, expected_title in reversed(self._tickets):
            detail = self.admin_api.get_json(
                self.admin_api.project_path(f"tickets/{ticket_id}")
            )
            if detail.status_code == 404:
                continue
            if detail.status_code != 200:
                errors.append(
                    f"inspect ticket {ticket_id}: {response_diagnostic(detail)}"
                )
                continue
            ticket = detail.json().get("data", {})
            actual_title = ticket.get("title") if isinstance(ticket, dict) else None
            if not isinstance(actual_title, str) or not actual_title.startswith(
                self.prefix
            ):
                errors.append(
                    f"refused to delete unowned ticket {ticket_id}: "
                    f"expected={expected_title!r} actual={actual_title!r}"
                )
                continue
            etag = detail.headers.get("ETag")
            if (
                not isinstance(etag, str)
                or not etag.startswith('"v')
                or not etag.endswith('"')
            ):
                errors.append(
                    f"inspect ticket {ticket_id}: response lacks a strong ETag"
                )
                continue
            deleted = self.admin_api.delete_ticket(ticket_id, etag=etag)
            if deleted.status_code not in (200, 204, 404):
                errors.append(
                    f"delete ticket {ticket_id}: {response_diagnostic(deleted)}"
                )

        for notification_id in reversed(self._notifications):
            deleted = self.admin_api.delete(
                self.project_path(f"notifications/{notification_id}")
            )
            if deleted.status_code not in (200, 204, 404):
                errors.append(
                    f"delete notification {notification_id}: "
                    f"{response_diagnostic(deleted)}"
                )

        for client in self._clients:
            client.close()

        for user_id in reversed(self._memberships):
            membership = self.admin_api.delete(
                self.project_path(f"memberships/{user_id}")
            )
            if membership.status_code not in (200, 204, 404):
                errors.append(
                    f"revoke project membership for user {user_id}: "
                    f"{response_diagnostic(membership)}"
                )

        for user_id, expected_username in reversed(self._users):
            detail = self.admin_api.get_json(f"/platform/users/{user_id}")
            if detail.status_code == 404:
                continue
            if detail.status_code != 200:
                errors.append(f"inspect user {user_id}: {response_diagnostic(detail)}")
                continue
            user = detail.json().get("data", {})
            actual_username = user.get("username") if isinstance(user, dict) else None
            if actual_username != expected_username or not expected_username.startswith(
                "e2e_"
            ):
                errors.append(
                    f"refused to delete unowned user {user_id}: "
                    f"expected={expected_username!r} actual={actual_username!r}"
                )
                continue
            deleted = self.admin_api.delete(f"/platform/users/{user_id}")
            if deleted.status_code not in (200, 204, 404):
                errors.append(f"delete user {user_id}: {response_diagnostic(deleted)}")

        if errors:
            raise AssertionError("E2E 精确清理失败:\n" + "\n".join(errors))
