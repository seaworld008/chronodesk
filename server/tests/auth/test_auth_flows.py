"""Authentication flow integration tests."""

from __future__ import annotations

import base64
import hashlib
import hmac
import time

import pytest

from tests.utils import APIClient, response_diagnostic, safe_diagnostic


def _generate_totp(secret: str, period: int = 30, digits: int = 6) -> str:
    """Generate TOTP code compatible with backend SimpleOTPService."""

    key = base64.b32decode(secret, casefold=True)
    counter = int(time.time()) // period
    msg = counter.to_bytes(8, "big")
    digest = hmac.new(key, msg, hashlib.sha1).digest()
    offset = digest[-1] & 0x0F
    code_int = int.from_bytes(digest[offset : offset + 4], "big") & 0x7FFFFFFF
    return str(code_int % (10**digits)).zfill(digits)


@pytest.mark.api
@pytest.mark.integration
class TestAuthenticationFlows:
    def _enable_otp(
        self,
        api_client: APIClient,
        access_token: str,
        password: str,
    ) -> tuple[str, list[str]]:
        authed_client = api_client.with_auth(access_token)
        try:
            enable_resp = authed_client.post_json(
                "/auth/enable-otp", {"password": password}
            )
            assert enable_resp.status_code == 200, response_diagnostic(enable_resp)
            enable_body = enable_resp.json()
            assert enable_body.get("success") is True, safe_diagnostic(enable_body)
            otp_data = enable_body.get("data", {})
            secret = otp_data.get("secret")
            assert secret, "启用OTP响应缺少密钥"
            backup_codes = otp_data.get("backup_codes") or []
            return secret, backup_codes
        finally:
            authed_client.close()

    def test_register_refresh_and_logout(
        self,
        api_client: APIClient,
        registered_user: dict[str, str],
    ) -> None:
        access_token = registered_user.get("access_token")
        refresh_token = registered_user.get("refresh_token")
        assert access_token and refresh_token, "注册响应缺少令牌"

        authed_client = api_client.with_auth(access_token)
        try:
            history_resp = authed_client.get_json(
                "/user/login-history", params={"page_size": 5}
            )
            assert history_resp.status_code == 200, response_diagnostic(history_resp)
            history_body = history_resp.json()
            assert history_body.get("code") == 0, safe_diagnostic(history_body)

            items = history_body.get("data", {}).get("items", [])
            assert items, "注册后应记录至少一条登录历史"
            first_entry = items[0]
            assert first_entry.get("login_status") == "success"
            assert first_entry.get("session_id"), "登录历史缺少会话 ID"
        finally:
            authed_client.close()

        refreshed = api_client.refresh(refresh_token)
        new_access_token = refreshed.get("access_token")
        new_refresh_token = refreshed.get("refresh_token")
        assert new_access_token and new_refresh_token, "刷新令牌接口未返回新的令牌"
        assert new_refresh_token != refresh_token, "刷新后应生成新的 refresh token"

        logout_body = api_client.logout(new_refresh_token)
        assert logout_body.get("success") is True

        failed_resp = api_client.post_json(
            "/auth/refresh", {"refresh_token": new_refresh_token}
        )
        assert failed_resp.status_code == 401, response_diagnostic(failed_resp)
        failed_body = failed_resp.json()
        assert failed_body.get("error") in {
            "invalid_token",
            "token_expired",
            "refresh_failed",
        }

        authed_after = api_client.with_auth(new_access_token)
        try:
            follow_resp = authed_after.get_json(
                "/user/login-history", params={"page_size": 5}
            )
            assert follow_resp.status_code == 401, response_diagnostic(follow_resp)
            follow_body = follow_resp.json()
            assert follow_body.get("error") == "session_revoked", safe_diagnostic(
                follow_body
            )
        finally:
            authed_after.close()

        # 刷新前签发的 access token 与新 token 共享同一个 sid，也必须一起失效。
        original_after = api_client.with_auth(access_token)
        try:
            original_resp = original_after.get_json(
                "/user/login-history", params={"page_size": 5}
            )
            assert original_resp.status_code == 401, response_diagnostic(original_resp)
            assert original_resp.json().get("error") == "session_revoked"
        finally:
            original_after.close()

    def test_logout_all_revokes_every_access_and_refresh_token(
        self,
        api_client: APIClient,
        registered_user: dict[str, str],
    ) -> None:
        first_access = registered_user.get("access_token")
        first_refresh = registered_user.get("refresh_token")
        assert first_access and first_refresh

        second_session = api_client.login(
            registered_user["email"],
            registered_user["password"],
        )
        second_access = second_session.get("access_token")
        second_refresh = second_session.get("refresh_token")
        assert second_access and second_refresh

        first_client = api_client.with_auth(first_access)
        try:
            logout_all = first_client.post_json("/auth/logout-all", {})
            assert logout_all.status_code == 200, response_diagnostic(logout_all)
            assert logout_all.json().get("success") is True
        finally:
            first_client.close()

        for access_token in (first_access, second_access):
            revoked_client = api_client.with_auth(access_token)
            try:
                response = revoked_client.get_json("/auth/me")
                assert response.status_code == 401, response_diagnostic(response)
                assert response.json().get("error") == "session_revoked"
            finally:
                revoked_client.close()

        for refresh_token in (first_refresh, second_refresh):
            response = api_client.post_json(
                "/auth/refresh",
                {"refresh_token": refresh_token},
            )
            assert response.status_code == 401, response_diagnostic(response)

    def test_otp_trusted_device_flow(
        self,
        api_client: APIClient,
        registered_user: dict[str, str],
    ) -> None:
        email = registered_user["email"]
        password = registered_user["password"]
        access_token = registered_user["access_token"]
        refresh_token = registered_user["refresh_token"]

        secret, _ = self._enable_otp(api_client, access_token, password)

        # 原刷新令牌应该继续可用，先显式登出便于后续验证
        api_client.logout(refresh_token)

        missing_otp_resp = api_client.post_json(
            "/auth/login",
            {
                "email": email,
                "password": password,
            },
        )
        assert missing_otp_resp.status_code == 400, response_diagnostic(
            missing_otp_resp
        )
        missing_body = missing_otp_resp.json()
        assert "OTP" in missing_body.get("msg", ""), safe_diagnostic(missing_body)

        otp_code = _generate_totp(secret)
        login_payload = {
            "email": email,
            "password": password,
            "otp_code": otp_code,
            "remember_device": True,
            "device_name": "pytest trusted device",
        }
        login_resp = api_client.post_json("/auth/login", login_payload)
        assert login_resp.status_code == 200, response_diagnostic(login_resp)
        login_body = login_resp.json()
        assert login_body.get("code") == 0, safe_diagnostic(login_body)
        login_data = login_body.get("data", {})
        assert "trusted_device_token" not in login_data, "登录响应不得暴露可信设备凭据"
        trusted_cookie = login_resp.cookies.get("chronodesk_trusted_device")
        if not trusted_cookie:
            pytest.fail("登录响应缺少 HttpOnly 可信设备 Cookie", pytrace=False)
        set_cookie = login_resp.headers.get("Set-Cookie", "")
        missing_cookie_attributes = [
            attribute
            for attribute in (
                "HttpOnly",
                "SameSite=Strict",
                "Path=/api/auth/login",
            )
            if attribute not in set_cookie
        ]
        assert not missing_cookie_attributes, (
            f"可信设备 Cookie 缺少安全属性：{', '.join(missing_cookie_attributes)}"
        )
        assert login_data.get("user", {}).get("otp_enabled") is True

        second_login_resp = api_client.post_json(
            "/auth/login",
            {
                "email": email,
                "password": password,
            },
        )
        assert second_login_resp.status_code == 200, response_diagnostic(
            second_login_resp
        )
        second_body = second_login_resp.json()
        assert second_body.get("code") == 0, safe_diagnostic(second_body)

        second_data = second_body.get("data", {})
        assert "trusted_device_token" not in second_data, "重复登录不应返回设备凭据"

        # 设备免OTP登录仍应提供新的令牌对，验证刷新立即可用
        new_refresh = second_data.get("refresh_token")
        assert new_refresh, "设备免OTP登录缺少刷新令牌"
        refreshed = api_client.refresh(new_refresh)
        assert refreshed.get("access_token"), "Trusted 登录 refresh 未返回访问令牌"

        # 销毁最新会话，避免污染
        api_client.logout(new_refresh)

    def test_login_failure_scenarios(
        self,
        api_client: APIClient,
        registered_user: dict[str, str],
    ) -> None:
        email = registered_user["email"]

        invalid_resp = api_client.post_json(
            "/auth/login",
            {
                "email": email,
                "password": "TotallyWrongPass!",
            },
        )
        assert invalid_resp.status_code == 401, response_diagnostic(invalid_resp)
        invalid_body = invalid_resp.json()
        assert invalid_body.get("msg") in {"邮箱或密码错误", "登录失败"}

        refresh_resp = api_client.post_json(
            "/auth/refresh",
            {"refresh_token": "deadbeef"},
        )
        assert refresh_resp.status_code == 401, response_diagnostic(refresh_resp)
        refresh_body = refresh_resp.json()
        assert refresh_body.get("error") in {
            "invalid_token",
            "refresh_failed",
            "token_expired",
        }

    def test_wrong_password_and_unknown_email_are_indistinguishable_and_limited(
        self,
        api_client: APIClient,
        registered_user: dict[str, str],
    ) -> None:
        """AUTH-005/006: prevent account enumeration and bound repeated failures."""

        wrong_password_payload = {
            "email": registered_user["email"],
            "password": "DefinitelyWrongPass!9",
        }
        unknown_email_payload = {
            "email": f"missing-{time.time_ns()}@example.com",
            "password": "DefinitelyWrongPass!9",
        }

        first_known = api_client.post_json("/auth/login", wrong_password_payload)
        unknown = api_client.post_json("/auth/login", unknown_email_payload)
        assert first_known.status_code == unknown.status_code == 401
        assert (
            first_known.json().get("msg")
            == unknown.json().get("msg")
            == ("邮箱或密码错误")
        )
        for body in (first_known.json(), unknown.json()):
            assert body.get("code") == 1
            assert body.get("data") is None
            serialized = str(body).lower()
            assert registered_user["email"].lower() not in serialized
            assert unknown_email_payload["email"].lower() not in serialized

        # The first known-account attempt above counts toward the configured
        # five-attempt window. The next four remain indistinguishable; the
        # following request must be rejected before password verification.
        for _ in range(4):
            repeated = api_client.post_json("/auth/login", wrong_password_payload)
            assert repeated.status_code == 401, response_diagnostic(repeated)
            assert repeated.json().get("msg") == "邮箱或密码错误"

        limited = api_client.post_json("/auth/login", wrong_password_payload)
        assert limited.status_code == 429, response_diagnostic(limited)
        limited_body = limited.json()
        assert limited_body.get("code") == 1
        assert limited_body.get("msg") == "登录失败次数过多，请稍后重试"
        assert limited_body.get("data") is None

    def test_trusted_device_revocation_requires_otp(
        self,
        api_client: APIClient,
        registered_user: dict[str, str],
    ) -> None:
        email = registered_user["email"]
        password = registered_user["password"]
        access_token = registered_user["access_token"]
        refresh_token = registered_user["refresh_token"]

        secret, _ = self._enable_otp(api_client, access_token, password)
        api_client.logout(refresh_token)

        otp_code = _generate_totp(secret)
        login_payload = {
            "email": email,
            "password": password,
            "otp_code": otp_code,
            "remember_device": True,
            "device_name": "pytest revoke device",
        }
        first_login_resp = api_client.post_json("/auth/login", login_payload)
        assert first_login_resp.status_code == 200, response_diagnostic(
            first_login_resp
        )
        first_data = first_login_resp.json()["data"]
        assert "trusted_device_token" not in first_data, "登录响应不得暴露可信设备凭据"
        assert first_login_resp.cookies.get("chronodesk_trusted_device")

        second_login_resp = api_client.post_json(
            "/auth/login",
            {
                "email": email,
                "password": password,
                "remember_device": True,
            },
        )
        assert second_login_resp.status_code == 200, response_diagnostic(
            second_login_resp
        )
        second_data = second_login_resp.json()["data"]
        new_access = second_data.get("access_token")
        new_refresh = second_data.get("refresh_token")
        assert new_access and new_refresh

        authed = api_client.with_auth(new_access)
        try:
            list_resp = authed.get_json("/user/trusted-devices")
            assert list_resp.status_code == 200, response_diagnostic(list_resp)
            list_body = list_resp.json()
            assert list_body.get("code") == 0, safe_diagnostic(list_body)
            devices = list_body.get("data", [])
            assert devices, "启用记住设备后应存在可信设备记录"
            device_id = devices[0]["id"]

            revoke_resp = authed.delete(f"/user/trusted-devices/{device_id}")
            assert revoke_resp.status_code == 200, response_diagnostic(revoke_resp)
            revoke_body = revoke_resp.json()
            assert revoke_body.get("code") == 0
        finally:
            authed.close()

        reuse_resp = api_client.post_json(
            "/auth/login",
            {
                "email": email,
                "password": password,
            },
        )
        assert reuse_resp.status_code in (400, 401), response_diagnostic(reuse_resp)
        reuse_body = reuse_resp.json()
        assert "OTP" in reuse_body.get("msg", "")

        recovery_login = api_client.post_json(
            "/auth/login",
            {
                "email": email,
                "password": password,
                "otp_code": _generate_totp(secret),
            },
        )
        assert recovery_login.status_code == 200, response_diagnostic(recovery_login)
        recovery_data = recovery_login.json()["data"]
        api_client.logout(recovery_data.get("refresh_token"))

    def test_backup_code_single_use(
        self,
        api_client: APIClient,
        registered_user: dict[str, str],
    ) -> None:
        email = registered_user["email"]
        password = registered_user["password"]
        access_token = registered_user["access_token"]
        refresh_token = registered_user["refresh_token"]

        secret, backup_codes = self._enable_otp(api_client, access_token, password)
        assert backup_codes, "启用OTP应返回备用码"
        backup_code = backup_codes[0]

        api_client.logout(refresh_token)

        backup_login_resp = api_client.post_json(
            "/auth/login",
            {
                "email": email,
                "password": password,
                "otp_code": backup_code,
            },
        )
        assert backup_login_resp.status_code == 200, response_diagnostic(
            backup_login_resp
        )
        backup_data = backup_login_resp.json()["data"]
        assert backup_data.get("user", {}).get("otp_enabled") is True

        api_client.logout(backup_data.get("refresh_token"))

        reuse_resp = api_client.post_json(
            "/auth/login",
            {
                "email": email,
                "password": password,
                "otp_code": backup_code,
            },
        )
        assert reuse_resp.status_code in (400, 401), response_diagnostic(reuse_resp)

        totp_resp = api_client.post_json(
            "/auth/login",
            {
                "email": email,
                "password": password,
                "otp_code": _generate_totp(secret),
            },
        )
        assert totp_resp.status_code == 200, response_diagnostic(totp_resp)
        api_client.logout(totp_resp.json()["data"].get("refresh_token"))

    def test_disable_otp_restores_password_only_login(
        self,
        api_client: APIClient,
        registered_user: dict[str, str],
    ) -> None:
        email = registered_user["email"]
        password = registered_user["password"]
        access_token = registered_user["access_token"]
        refresh_token = registered_user["refresh_token"]

        secret, _ = self._enable_otp(api_client, access_token, password)
        api_client.logout(refresh_token)

        # Without OTP now fails
        missing_resp = api_client.post_json(
            "/auth/login",
            {
                "email": email,
                "password": password,
            },
        )
        assert missing_resp.status_code == 400, response_diagnostic(missing_resp)

        login_resp = api_client.post_json(
            "/auth/login",
            {
                "email": email,
                "password": password,
                "otp_code": _generate_totp(secret),
            },
        )
        assert login_resp.status_code == 200, response_diagnostic(login_resp)
        login_data = login_resp.json()["data"]
        new_access = login_data.get("access_token")
        new_refresh = login_data.get("refresh_token")
        assert new_access and new_refresh

        authed = api_client.with_auth(new_access)
        try:
            disable_resp = authed.post_json("/auth/disable-otp", {"password": password})
            assert disable_resp.status_code == 200, response_diagnostic(disable_resp)
            disable_body = disable_resp.json()
            assert disable_body.get("success") is True
        finally:
            authed.close()

        api_client.logout(new_refresh)

        plain_login_resp = api_client.post_json(
            "/auth/login",
            {
                "email": email,
                "password": password,
            },
        )
        assert plain_login_resp.status_code == 200, response_diagnostic(
            plain_login_resp
        )
        plain_data = plain_login_resp.json()["data"]
        assert plain_data.get("user", {}).get("otp_enabled") is False
        api_client.logout(plain_data.get("refresh_token"))
