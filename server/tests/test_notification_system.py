"""通知 API 的真实环境生命周期回归测试。

测试只创建一条带唯一标识的应用内通知，并在 fixture 的 ``finally`` 中
精确删除该记录。测试不会批量标记既有通知，也不会修改任何既有工单。
"""

from __future__ import annotations

import json
import time
from collections.abc import Iterator
from typing import Any

import pytest

from .utils import APIClient


@pytest.fixture
def notification_under_test(
    admin_api: APIClient,
    admin_tokens: dict[str, Any],
) -> Iterator[dict[str, Any]]:
    """创建一条仅属于当前管理员的通知，并保证测试结束后精确清理。"""

    user = admin_tokens.get("user")
    assert isinstance(user, dict), "管理员登录响应中的 user 应为对象"

    recipient_id = user.get("id")
    assert isinstance(recipient_id, int) and recipient_id > 0, (
        "管理员登录响应缺少有效用户 ID"
    )

    unread_response = admin_api.get_json(
        admin_api.project_path("notifications/unread-count")
    )
    assert unread_response.status_code == 200, "创建通知前应能查询未读数量"
    baseline_unread = unread_response.json().get("count")
    assert isinstance(baseline_unread, int) and baseline_unread >= 0, (
        "未读数量应为非负整数"
    )

    unique_suffix = str(time.time_ns())
    payload = {
        "type": "system_alert",
        "title": f"通知生命周期回归-{unique_suffix}",
        "content": f"该通知仅用于自动化回归，唯一标识：{unique_suffix}",
        "priority": "normal",
        "channel": "in_app",
        "recipient_id": recipient_id,
    }

    notification_id: int | None = None
    try:
        create_response = admin_api.post_json(
            admin_api.project_path("notifications"),
            payload,
        )
        assert create_response.status_code == 201, "管理员创建通知应返回 HTTP 201"

        created = create_response.json().get("data")
        assert isinstance(created, dict), "创建通知响应应包含 data 对象"
        raw_notification_id = created.get("id")
        assert isinstance(raw_notification_id, int) and raw_notification_id > 0, (
            "创建通知响应缺少有效通知 ID"
        )
        notification_id = raw_notification_id

        yield {
            "notification": created,
            "payload": payload,
            "baseline_unread": baseline_unread,
        }
    finally:
        if notification_id is not None:
            delete_response = admin_api.delete(
                admin_api.project_path(f"notifications/{notification_id}")
            )
            assert delete_response.status_code == 200, "管理员应能清理本测试创建的通知"
            assert delete_response.json().get("message") == "删除通知成功", (
                "清理通知应返回中文成功提示"
            )


def _list_notifications(
    admin_api: APIClient,
    *,
    title: str,
    is_read: bool | None = None,
    expected_count: int = 1,
) -> dict[str, Any]:
    """按唯一标题读取当前用户的通知列表。"""

    notification_filter: dict[str, Any] = {"q": title}
    if is_read is not None:
        notification_filter["is_read"] = is_read
    params: dict[str, Any] = {
        "page": 1,
        "page_size": 10,
        "filter": json.dumps(notification_filter, ensure_ascii=False),
    }

    response = admin_api.get_json(
        admin_api.project_path("notifications"),
        params=params,
    )
    assert response.status_code == 200, "当前用户读取通知列表应返回 HTTP 200"

    body = response.json()
    assert body.get("code") == 0, "通知列表响应 code 应为 0"
    assert body.get("msg") == "获取通知列表成功", "通知列表应返回中文成功提示"

    data = body.get("data")
    assert isinstance(data, dict), "通知列表响应应包含 data 对象"
    assert isinstance(data.get("total"), int), "通知列表 data.total 应为整数"
    assert isinstance(data.get("items"), list), "通知列表 data.items 应始终为数组"
    assert data["total"] == expected_count, (
        f"唯一标题过滤后的通知总数应为 {expected_count} 条"
    )
    assert len(data["items"]) == expected_count, (
        f"唯一标题过滤后的列表应返回 {expected_count} 条"
    )
    assert all(item.get("title") == title for item in data["items"]), (
        "通知列表不应混入不匹配的记录"
    )
    return data


@pytest.mark.integration
@pytest.mark.api
def test_notification_lifecycle_uses_current_contract(
    api_client: APIClient,
    admin_api: APIClient,
    admin_tokens: dict[str, Any],
    notification_under_test: dict[str, Any],
) -> None:
    """验证通知列表、未读计数、创建、读取、单条标记已读和管理员清理。"""

    unauthenticated_response = api_client.get_json(
        api_client.project_path("notifications"),
        params={"page": 1, "page_size": 1},
    )
    assert unauthenticated_response.status_code == 401, "未认证客户端不应读取通知列表"

    current_user = admin_tokens.get("user")
    assert isinstance(current_user, dict), "管理员令牌应包含当前用户信息"

    created = notification_under_test["notification"]
    payload = notification_under_test["payload"]
    notification_id = created["id"]

    assert created.get("type") == payload["type"], "创建响应中的通知类型应与请求一致"
    assert created.get("title") == payload["title"], "创建响应中的标题应与请求一致"
    assert created.get("content") == payload["content"], "创建响应中的内容应与请求一致"
    assert created.get("priority") == payload["priority"], (
        "创建响应中的优先级应与请求一致"
    )
    assert created.get("channel") == payload["channel"], "创建响应中的渠道应与请求一致"
    assert created.get("is_read") is False, "新创建的通知应为未读状态"

    unread_after_create_response = admin_api.get_json(
        admin_api.project_path("notifications/unread-count")
    )
    assert unread_after_create_response.status_code == 200, "当前用户应能查询未读数量"
    unread_after_create = unread_after_create_response.json().get("count")
    assert isinstance(unread_after_create, int), "未读数量应为整数"
    assert unread_after_create >= notification_under_test["baseline_unread"] + 1, (
        "创建未读通知后未读数量应增加"
    )

    unread_data = _list_notifications(admin_api, title=payload["title"], is_read=False)
    unread_items = unread_data["items"]
    matching_unread = [
        item for item in unread_items if item.get("id") == notification_id
    ]
    assert len(matching_unread) == 1, "当前用户的未读列表应包含本测试创建的通知"
    assert matching_unread[0].get("is_read") is False, "列表中的新通知应保持未读"

    mark_response = admin_api.put_json(
        admin_api.project_path(f"notifications/{notification_id}/read"),
        {},
    )
    assert mark_response.status_code == 200, "当前用户标记单条通知已读应返回 HTTP 200"
    assert mark_response.json().get("message") == "标记成功", (
        "标记已读应返回中文成功提示"
    )

    read_data = _list_notifications(admin_api, title=payload["title"], is_read=True)
    matching_read = [
        item for item in read_data["items"] if item.get("id") == notification_id
    ]
    assert len(matching_read) == 1, "当前用户的已读列表应包含本测试通知"
    assert matching_read[0].get("is_read") is True, "标记后的通知应为已读状态"
    assert matching_read[0].get("read_at"), "标记已读后应记录 read_at"

    unread_data_after_mark = _list_notifications(
        admin_api,
        title=payload["title"],
        is_read=False,
        expected_count=0,
    )
    assert all(
        item.get("id") != notification_id for item in unread_data_after_mark["items"]
    ), "标记已读后，该通知不应继续出现在未读列表"

    unread_after_mark_response = admin_api.get_json(
        admin_api.project_path("notifications/unread-count")
    )
    assert unread_after_mark_response.status_code == 200, "标记已读后仍应能查询未读数量"
    unread_after_mark = unread_after_mark_response.json().get("count")
    assert isinstance(unread_after_mark, int) and unread_after_mark >= 0, (
        "未读数量应保持为非负整数"
    )
    assert unread_after_mark <= unread_after_create - 1, (
        "标记该通知已读后未读数量应至少减少一条"
    )


@pytest.mark.integration
@pytest.mark.api
def test_legacy_global_notification_routes_are_removed(
    admin_api: APIClient,
) -> None:
    """破坏性升级后，不得保留任何隐式项目通知入口。"""

    legacy_list = admin_api.get_json(
        "/notifications",
        params={"page": 1, "page_size": 1},
    )
    assert legacy_list.status_code == 404, "旧全局 /notifications 必须直接不存在"

    # 空负载确保即使旧创建路由意外存在，也不会产生测试数据。
    legacy_admin_create = admin_api.post_json("/admin/notifications", {})
    assert legacy_admin_create.status_code == 404, (
        "旧全局 /admin/notifications 必须直接不存在"
    )
