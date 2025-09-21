"""Authentication flow integration tests."""

from __future__ import annotations

import base64
import hashlib
import hmac
import time
from typing import Dict

import pytest

from tests.utils import APIClient


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
            enable_resp = authed_client.post_json("/auth/enable-otp", {"password": password})
            assert enable_resp.status_code == 200, enable_resp.text
            enable_body = enable_resp.json()
            assert enable_body.get("success") is True, enable_body
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
        registered_user: Dict[str, str],
    ) -> None:
        access_token = registered_user.get("access_token")
        refresh_token = registered_user.get("refresh_token")
        assert access_token and refresh_token, "注册响应缺少令牌"

        authed_client = api_client.with_auth(access_token)
        try:
            history_resp = authed_client.get_json("/user/login-history", params={"page_size": 5})
            assert history_resp.status_code == 200, history_resp.text
            history_body = history_resp.json()
            assert history_body.get("code") == 0, history_body

            items = history_body.get("data", {}).get("items", [])
            assert items, "注册后应记录至少一条登录历史"
            first_entry = items[0]
            assert first_entry.get("login_status") == "success"
            session_id = first_entry.get("session_id")
        finally:
            authed_client.close()

        refreshed = api_client.refresh(refresh_token)
        new_access_token = refreshed.get("access_token")
        new_refresh_token = refreshed.get("refresh_token")
        assert new_access_token and new_refresh_token, "刷新令牌接口未返回新的令牌"
        assert new_refresh_token != refresh_token, "刷新后应生成新的 refresh token"

        logout_body = api_client.logout(new_refresh_token)
        assert logout_body.get("success") is True

        failed_resp = api_client.post_json("/auth/refresh", {"refresh_token": new_refresh_token})
        assert failed_resp.status_code == 401, failed_resp.text
        failed_body = failed_resp.json()
        assert failed_body.get("error") in {"invalid_token", "token_expired", "refresh_failed"}

        authed_after = api_client.with_auth(new_access_token)
        try:
            follow_resp = authed_after.get_json(
                "/user/login-history",
                params={"session_id": session_id} if session_id else None,
            )
            assert follow_resp.status_code == 200, follow_resp.text
            follow_body = follow_resp.json()
            assert follow_body.get("code") == 0, follow_body

            follow_items = follow_body.get("data", {}).get("items", [])
            if follow_items:
                assert follow_items[0].get("is_active") is False
        finally:
            authed_after.close()

    def test_otp_trusted_device_flow(
        self,
        api_client: APIClient,
        registered_user: Dict[str, str],
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
        assert missing_otp_resp.status_code == 400, missing_otp_resp.text
        missing_body = missing_otp_resp.json()
        assert "OTP" in missing_body.get("msg", ""), missing_body

        otp_code = _generate_totp(secret)
        login_payload = {
            "email": email,
            "password": password,
            "otp_code": otp_code,
            "remember_device": True,
            "device_name": "pytest trusted device",
        }
        login_resp = api_client.post_json("/auth/login", login_payload)
        assert login_resp.status_code == 200, login_resp.text
        login_body = login_resp.json()
        assert login_body.get("code") == 0, login_body
        login_data = login_body.get("data", {})
        trusted_token = login_data.get("trusted_device_token")
        assert trusted_token, "登录响应缺少 trusted_device_token"
        assert login_data.get("user", {}).get("otp_enabled") is True

        second_login_resp = api_client.post_json(
            "/auth/login",
            {
                "email": email,
                "password": password,
                "device_token": trusted_token,
            },
        )
        assert second_login_resp.status_code == 200, second_login_resp.text
        second_body = second_login_resp.json()
        assert second_body.get("code") == 0, second_body

        second_data = second_body.get("data", {})
        assert second_data.get("trusted_device_token") is None, "重复登录不应返回新的设备令牌"

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
        registered_user: Dict[str, str],
    ) -> None:
        email = registered_user["email"]

        invalid_resp = api_client.post_json(
            "/auth/login",
            {
                "email": email,
                "password": "TotallyWrongPass!",
            },
        )
        assert invalid_resp.status_code == 401, invalid_resp.text
        invalid_body = invalid_resp.json()
        assert invalid_body.get("msg") in {"Invalid email or password", "Login failed"}

        refresh_resp = api_client.post_json(
            "/auth/refresh",
            {"refresh_token": "deadbeef"},
        )
        assert refresh_resp.status_code == 401, refresh_resp.text
        refresh_body = refresh_resp.json()
        assert refresh_body.get("error") in {"invalid_token", "refresh_failed", "token_expired"}

    def test_trusted_device_revocation_requires_otp(
        self,
        api_client: APIClient,
        registered_user: Dict[str, str],
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
        assert first_login_resp.status_code == 200, first_login_resp.text
        first_data = first_login_resp.json()["data"]
        trusted_token = first_data.get("trusted_device_token")
        assert trusted_token, "首次验证码登录应返回 trusted_device_token"

        second_login_resp = api_client.post_json(
            "/auth/login",
            {
                "email": email,
                "password": password,
                "device_token": trusted_token,
            },
        )
        assert second_login_resp.status_code == 200, second_login_resp.text
        second_data = second_login_resp.json()["data"]
        new_access = second_data.get("access_token")
        new_refresh = second_data.get("refresh_token")
        assert new_access and new_refresh

        authed = api_client.with_auth(new_access)
        try:
            list_resp = authed.get_json("/user/trusted-devices")
            assert list_resp.status_code == 200, list_resp.text
            list_body = list_resp.json()
            assert list_body.get("code") == 0, list_body
            devices = list_body.get("data", [])
            assert devices, "启用记住设备后应存在可信设备记录"
            device_id = devices[0]["id"]

            revoke_resp = authed.delete(f"/user/trusted-devices/{device_id}")
            assert revoke_resp.status_code == 200, revoke_resp.text
            revoke_body = revoke_resp.json()
            assert revoke_body.get("code") == 0
        finally:
            authed.close()

        reuse_resp = api_client.post_json(
            "/auth/login",
            {
                "email": email,
                "password": password,
                "device_token": trusted_token,
            },
        )
        assert reuse_resp.status_code in (400, 401), reuse_resp.text
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
        assert recovery_login.status_code == 200, recovery_login.text
        recovery_data = recovery_login.json()["data"]
        api_client.logout(recovery_data.get("refresh_token"))

    def test_backup_code_single_use(
        self,
        api_client: APIClient,
        registered_user: Dict[str, str],
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
        assert backup_login_resp.status_code == 200, backup_login_resp.text
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
        assert reuse_resp.status_code in (400, 401), reuse_resp.text

        totp_resp = api_client.post_json(
            "/auth/login",
            {
                "email": email,
                "password": password,
                "otp_code": _generate_totp(secret),
            },
        )
        assert totp_resp.status_code == 200, totp_resp.text
        api_client.logout(totp_resp.json()["data"].get("refresh_token"))

    def test_disable_otp_restores_password_only_login(
        self,
        api_client: APIClient,
        registered_user: Dict[str, str],
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
        assert missing_resp.status_code == 400, missing_resp.text

        login_resp = api_client.post_json(
            "/auth/login",
            {
                "email": email,
                "password": password,
                "otp_code": _generate_totp(secret),
            },
        )
        assert login_resp.status_code == 200, login_resp.text
        login_data = login_resp.json()["data"]
        new_access = login_data.get("access_token")
        new_refresh = login_data.get("refresh_token")
        assert new_access and new_refresh

        authed = api_client.with_auth(new_access)
        try:
            disable_resp = authed.post_json("/auth/disable-otp", {"password": password})
            assert disable_resp.status_code == 200, disable_resp.text
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
        assert plain_login_resp.status_code == 200, plain_login_resp.text
        plain_data = plain_login_resp.json()["data"]
        assert plain_data.get("user", {}).get("otp_enabled") is False
        api_client.logout(plain_data.get("refresh_token"))
