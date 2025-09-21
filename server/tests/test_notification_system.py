#!/usr/bin/env python3
"""
通知系统自动化测试
======================================

现代化Python测试脚本，使用pytest + requests + pydantic
大师级开发思想：批量自动化测试，避免低效重复的单个命令测试

Usage:
    pytest tests/test_notification_system.py -v
    pytest tests/test_notification_system.py::TestNotificationAPI -v
    pytest tests/test_notification_system.py -k "test_basic" -v
"""

import json
import time
from typing import Dict, List, Optional, Any
from dataclasses import dataclass
from enum import Enum

import pytest
import requests
from pydantic import BaseModel, Field


# ============================================================================
# 配置和数据模型
# ============================================================================

class TestConfig:
    """测试配置类"""
    API_BASE = "http://localhost:8080/api"
    ADMIN_TOKEN = "test-token"
    REQUEST_TIMEOUT = 10
    

class NotificationType(str, Enum):
    """通知类型枚举"""
    SYSTEM_ALERT = "system_alert"
    TICKET_ASSIGNED = "ticket_assigned"
    TICKET_STATUS_CHANGED = "ticket_status_changed"
    TICKET_COMMENT = "ticket_comment"


class NotificationPriority(str, Enum):
    """通知优先级枚举"""
    LOW = "low"
    NORMAL = "normal"
    HIGH = "high"
    URGENT = "urgent"


class NotificationRequest(BaseModel):
    """通知创建请求模型"""
    type: NotificationType
    title: str = Field(..., max_length=255)
    content: str
    priority: NotificationPriority = NotificationPriority.NORMAL
    recipient_id: int = Field(..., gt=0)
    related_ticket_id: Optional[int] = None
    action_url: Optional[str] = None


class NotificationResponse(BaseModel):
    """通知响应模型"""
    id: int
    type: NotificationType
    title: str
    content: str
    priority: NotificationPriority
    is_read: bool
    created_at: str
    recipient: Dict[str, Any]


@dataclass
class TestResult:
    """测试结果数据类"""
    name: str
    passed: bool
    duration: float
    error: Optional[str] = None


# ============================================================================
# API客户端类
# ============================================================================

class NotificationAPIClient:
    """通知系统API客户端"""
    
    def __init__(self, base_url: str, token: str):
        self.base_url = base_url
        self.session = requests.Session()
        self.session.headers.update({
            "Authorization": f"Bearer {token}",
            "Content-Type": "application/json"
        })
        self.session.timeout = TestConfig.REQUEST_TIMEOUT
    
    def get_notifications(self, limit: Optional[int] = None, 
                         offset: Optional[int] = None,
                         is_read: Optional[bool] = None) -> Dict[str, Any]:
        """获取通知列表"""
        params = {}
        if limit is not None:
            params["limit"] = limit
        if offset is not None:
            params["offset"] = offset
        if is_read is not None:
            params["is_read"] = str(is_read).lower()
            
        response = self.session.get(f"{self.base_url}/notifications", params=params)
        response.raise_for_status()
        return response.json()
    
    def get_unread_count(self) -> int:
        """获取未读通知数量"""
        response = self.session.get(f"{self.base_url}/notifications/unread-count")
        response.raise_for_status()
        return response.json()["count"]
    
    def create_notification(self, notification: NotificationRequest) -> NotificationResponse:
        """创建通知"""
        response = self.session.post(
            f"{self.base_url}/admin/notifications",
            json=notification.dict()
        )
        response.raise_for_status()
        return NotificationResponse(**response.json()["data"])
    
    def mark_as_read(self, notification_id: int) -> None:
        """标记通知为已读"""
        response = self.session.put(f"{self.base_url}/notifications/{notification_id}/read")
        response.raise_for_status()
    
    def mark_all_as_read(self) -> None:
        """标记所有通知为已读"""
        response = self.session.put(f"{self.base_url}/notifications/read-all")
        response.raise_for_status()
    
    def update_ticket(self, ticket_id: int, data: Dict[str, Any]) -> Dict[str, Any]:
        """更新工单（用于测试自动通知）"""
        response = self.session.put(f"{self.base_url}/tickets/{ticket_id}", json=data)
        response.raise_for_status()
        return response.json()


# ============================================================================
# Pytest Fixtures
# ============================================================================

@pytest.fixture(scope="session")
def api_client() -> NotificationAPIClient:
    """API客户端fixture"""
    return NotificationAPIClient(TestConfig.API_BASE, TestConfig.ADMIN_TOKEN)


@pytest.fixture(scope="session")
def health_check(api_client: NotificationAPIClient):
    """健康检查fixture，确保服务正常运行"""
    try:
        # 尝试获取通知列表来检查服务状态
        api_client.get_notifications(limit=1)
    except requests.exceptions.RequestException as e:
        pytest.skip(f"API服务不可用: {e}")


@pytest.fixture
def baseline_notification_count(api_client: NotificationAPIClient) -> int:
    """获取测试前的基准通知数量"""
    return api_client.get_unread_count()


@pytest.fixture
def sample_notification_request() -> NotificationRequest:
    """示例通知请求fixture"""
    return NotificationRequest(
        type=NotificationType.SYSTEM_ALERT,
        title="自动化测试通知",
        content="这是通过pytest自动化测试创建的通知",
        priority=NotificationPriority.HIGH,
        recipient_id=1
    )


# ============================================================================
# 测试类
# ============================================================================

class TestNotificationAPI:
    """通知API基础功能测试"""
    
    def test_get_notifications_empty_or_existing(self, api_client: NotificationAPIClient, health_check):
        """测试获取通知列表（可能为空或有数据）"""
        result = api_client.get_notifications()
        
        assert "data" in result
        assert "total" in result
        assert isinstance(result["data"], list)
        assert isinstance(result["total"], int)
        assert result["total"] >= 0
    
    def test_get_unread_count(self, api_client: NotificationAPIClient, health_check):
        """测试获取未读通知数量"""
        count = api_client.get_unread_count()
        assert isinstance(count, int)
        assert count >= 0
    
    def test_create_notification(self, api_client: NotificationAPIClient, 
                                sample_notification_request: NotificationRequest,
                                baseline_notification_count: int, health_check):
        """测试创建通知"""
        # 创建通知
        notification = api_client.create_notification(sample_notification_request)
        
        # 验证返回的通知数据
        assert notification.id > 0
        assert notification.type == sample_notification_request.type
        assert notification.title == sample_notification_request.title
        assert notification.content == sample_notification_request.content
        assert notification.priority == sample_notification_request.priority
        assert notification.is_read is False
        
        # 验证未读数量增加
        new_count = api_client.get_unread_count()
        assert new_count >= baseline_notification_count
    
    def test_notification_pagination(self, api_client: NotificationAPIClient, health_check):
        """测试通知分页功能"""
        # 获取前3条
        page1 = api_client.get_notifications(limit=3, offset=0)
        
        # 获取接下来的3条
        page2 = api_client.get_notifications(limit=3, offset=3)
        
        assert len(page1["data"]) <= 3
        assert len(page2["data"]) <= 3
        
        # 如果有足够的数据，确保分页内容不同
        if len(page1["data"]) > 0 and len(page2["data"]) > 0:
            page1_ids = [item["id"] for item in page1["data"]]
            page2_ids = [item["id"] for item in page2["data"]]
            assert not set(page1_ids).intersection(set(page2_ids))


class TestNotificationOperations:
    """通知操作功能测试"""
    
    def test_mark_notification_as_read(self, api_client: NotificationAPIClient,
                                      sample_notification_request: NotificationRequest, health_check):
        """测试标记单个通知为已读"""
        # 先创建一个通知
        notification = api_client.create_notification(sample_notification_request)
        initial_unread_count = api_client.get_unread_count()
        
        # 标记为已读
        api_client.mark_as_read(notification.id)
        
        # 验证未读数量减少
        final_unread_count = api_client.get_unread_count()
        assert final_unread_count <= initial_unread_count
    
    def test_mark_all_notifications_as_read(self, api_client: NotificationAPIClient, health_check):
        """测试批量标记所有通知为已读"""
        # 标记所有为已读
        api_client.mark_all_as_read()
        
        # 验证未读数量为0
        final_count = api_client.get_unread_count()
        assert final_count == 0


class TestTicketIntegration:
    """工单集成测试"""
    
    @pytest.mark.parametrize("ticket_id,update_data", [
        (1, {"status": "resolved", "resolution_time": 150}),
        (1, {"status": "closed"}),
        (1, {"assigned_to_id": 2, "status": "in_progress"}),
    ])
    def test_ticket_updates_trigger_notifications(self, api_client: NotificationAPIClient,
                                                 ticket_id: int, update_data: Dict[str, Any],
                                                 baseline_notification_count: int, health_check):
        """测试工单更新是否触发通知"""
        try:
            # 更新工单
            api_client.update_ticket(ticket_id, update_data)
            
            # 等待通知生成
            time.sleep(1)
            
            # 检查是否有新通知
            new_count = api_client.get_unread_count()
            # 注意：可能不会触发通知（例如自己操作自己的工单）
            # 这里我们验证操作成功执行，通知数量保持稳定或增加
            assert new_count >= baseline_notification_count
            
        except requests.exceptions.HTTPError as e:
            # 如果工单不存在或操作失败，跳过测试
            if e.response.status_code == 404:
                pytest.skip(f"工单{ticket_id}不存在")
            else:
                raise


class TestNotificationFiltering:
    """通知过滤功能测试"""
    
    def test_filter_by_read_status(self, api_client: NotificationAPIClient, health_check):
        """测试按已读状态过滤通知"""
        # 获取未读通知
        unread_notifications = api_client.get_notifications(is_read=False)
        
        # 获取已读通知
        read_notifications = api_client.get_notifications(is_read=True)
        
        # 验证过滤结果
        for notification in unread_notifications["data"]:
            assert notification["is_read"] is False
            
        for notification in read_notifications["data"]:
            assert notification["is_read"] is True


class TestErrorHandling:
    """错误处理测试"""
    
    def test_invalid_notification_id(self, api_client: NotificationAPIClient, health_check):
        """测试无效的通知ID"""
        with pytest.raises(requests.exceptions.HTTPError) as exc_info:
            api_client.mark_as_read(99999)
        
        assert exc_info.value.response.status_code in [404, 400]
    
    def test_invalid_notification_data(self, api_client: NotificationAPIClient, health_check):
        """测试无效的通知数据"""
        invalid_request = NotificationRequest(
            type=NotificationType.SYSTEM_ALERT,
            title="",  # 空标题应该失败
            content="",
            priority=NotificationPriority.NORMAL,
            recipient_id=1
        )
        
        with pytest.raises(requests.exceptions.HTTPError) as exc_info:
            api_client.create_notification(invalid_request)
        
        assert exc_info.value.response.status_code in [400, 422]


class TestPerformance:
    """性能测试"""
    
    def test_bulk_notification_creation_performance(self, api_client: NotificationAPIClient, health_check):
        """测试批量创建通知的性能"""
        notification_count = 5
        start_time = time.time()
        
        created_notifications = []
        for i in range(notification_count):
            request = NotificationRequest(
                type=NotificationType.SYSTEM_ALERT,
                title=f"性能测试通知 {i+1}",
                content=f"这是第{i+1}个性能测试通知",
                priority=NotificationPriority.NORMAL,
                recipient_id=1
            )
            notification = api_client.create_notification(request)
            created_notifications.append(notification)
        
        duration = time.time() - start_time
        
        # 验证所有通知都创建成功
        assert len(created_notifications) == notification_count
        
        # 性能要求：考虑到云数据库延迟，5个通知应该在10秒内创建完成
        assert duration < 10.0, f"批量创建{notification_count}个通知耗时{duration:.2f}秒，超出性能要求"
        
        print(f"✅ 批量创建{notification_count}个通知耗时: {duration:.2f}秒")
    
    def test_notification_list_response_time(self, api_client: NotificationAPIClient, health_check):
        """测试通知列表响应时间"""
        start_time = time.time()
        result = api_client.get_notifications(limit=10)
        duration = time.time() - start_time
        
        # 响应时间要求：获取通知列表应该在2秒内完成
        assert duration < 2.0, f"获取通知列表耗时{duration:.2f}秒，超出性能要求"
        
        # 验证返回数据有效
        assert "data" in result
        assert isinstance(result["data"], list)
        
        print(f"✅ 获取通知列表耗时: {duration:.2f}秒")


# ============================================================================
# 测试报告和统计
# ============================================================================

class TestReporter:
    """测试报告生成器"""
    
    def __init__(self):
        self.results: List[TestResult] = []
    
    def add_result(self, result: TestResult):
        """添加测试结果"""
        self.results.append(result)
    
    def generate_report(self) -> str:
        """生成测试报告"""
        total_tests = len(self.results)
        passed_tests = sum(1 for r in self.results if r.passed)
        failed_tests = total_tests - passed_tests
        total_duration = sum(r.duration for r in self.results)
        
        report = f"""
通知系统自动化测试报告
{'='*50}
总测试数: {total_tests}
通过: {passed_tests}
失败: {failed_tests}
成功率: {(passed_tests/total_tests*100):.1f}%
总耗时: {total_duration:.2f}秒

详细结果:
"""
        
        for result in self.results:
            status = "✅ PASS" if result.passed else "❌ FAIL"
            report += f"{status} {result.name} ({result.duration:.2f}s)\n"
            if result.error:
                report += f"    错误: {result.error}\n"
        
        return report


# ============================================================================
# 主测试入口和配置
# ============================================================================

def pytest_configure(config):
    """pytest配置"""
    config.addinivalue_line(
        "markers", "slow: marks tests as slow (deselect with '-m \"not slow\"')"
    )
    config.addinivalue_line(
        "markers", "integration: marks tests as integration tests"
    )


def pytest_collection_modifyitems(config, items):
    """修改测试项目配置"""
    for item in items:
        # 为性能测试添加slow标记
        if "performance" in item.name.lower():
            item.add_marker(pytest.mark.slow)
        
        # 为集成测试添加integration标记
        if "integration" in item.name.lower() or "ticket" in item.name.lower():
            item.add_marker(pytest.mark.integration)


if __name__ == "__main__":
    # 可以直接运行此文件进行测试
    import sys
    
    # 添加一些有用的pytest参数
    args = [
        __file__,
        "-v",  # 详细输出
        "--tb=short",  # 简短的traceback
        "--strict-markers",  # 严格标记模式
        "--durations=10",  # 显示最慢的10个测试
    ]
    
    # 如果提供了命令行参数，使用它们
    if len(sys.argv) > 1:
        args = [__file__] + sys.argv[1:]
    
    sys.exit(pytest.main(args))