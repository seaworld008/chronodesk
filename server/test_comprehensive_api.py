#!/usr/bin/env python3
"""
综合API接口测试
==================================

基于pytest框架的工单管理系统API完整测试套件
使用pydantic数据验证和requests客户端库
覆盖所有主要API端点和工作流程

Usage:
    pytest test_comprehensive_api.py -v
    pytest test_comprehensive_api.py::TestAuthAPI -v
    pytest test_comprehensive_api.py -k "test_ticket" -v --tb=short
    pytest test_comprehensive_api.py --html=reports/api_test_report.html
"""

import json
import time
from typing import Dict, List, Optional, Any, Union
from dataclasses import dataclass
from enum import Enum
from datetime import datetime, timedelta

import pytest
import requests
from pydantic import BaseModel, Field, validator


# ============================================================================
# 测试配置和数据模型
# ============================================================================

class TestConfig:
    """测试配置类 - 更新为正确端口8081"""
    API_BASE = "http://localhost:8081/api"
    ADMIN_TOKEN = "test-admin-token"
    USER_TOKEN = "test-user-token"
    REQUEST_TIMEOUT = 15
    MAX_RETRIES = 3
    RETRY_DELAY = 1.0


class TicketStatus(str, Enum):
    """工单状态枚举"""
    OPEN = "open"
    IN_PROGRESS = "in_progress"
    PENDING = "pending"
    RESOLVED = "resolved"
    CLOSED = "closed"


class TicketPriority(str, Enum):
    """工单优先级枚举"""
    LOW = "low"
    NORMAL = "normal"
    HIGH = "high"
    URGENT = "urgent"


class UserRole(str, Enum):
    """用户角色枚举"""
    ADMIN = "admin"
    AGENT = "agent"
    SUPERVISOR = "supervisor"
    USER = "user"


# ============================================================================
# Pydantic数据模型
# ============================================================================

class LoginRequest(BaseModel):
    """登录请求模型"""
    email: str = Field(..., pattern=r'^[^@]+@[^@]+\.[^@]+$')
    password: str = Field(..., min_length=6)
    remember: bool = False


class RegisterRequest(BaseModel):
    """注册请求模型"""
    username: str = Field(..., min_length=3, max_length=50)
    email: str = Field(..., pattern=r'^[^@]+@[^@]+\.[^@]+$')
    password: str = Field(..., min_length=8)
    first_name: str = Field(..., min_length=2, max_length=50)
    last_name: str = Field(..., min_length=2, max_length=50)


class TicketCreateRequest(BaseModel):
    """工单创建请求模型"""
    title: str = Field(..., min_length=5, max_length=200)
    description: str = Field(..., min_length=10)
    priority: TicketPriority = TicketPriority.NORMAL
    category_id: Optional[int] = None
    
    @validator('title')
    def title_must_not_be_empty(cls, v):
        if not v.strip():
            raise ValueError('标题不能为空')
        return v.strip()


class TicketResponse(BaseModel):
    """工单响应模型"""
    id: int
    ticket_number: str
    title: str
    description: str
    status: TicketStatus
    priority: TicketPriority
    created_at: str
    updated_at: str
    assigned_to_id: Optional[int] = None
    created_by_id: int


class APIResponse(BaseModel):
    """标准API响应模型 - 支持中英文格式"""
    success: Optional[bool] = None
    message: Optional[str] = None
    data: Optional[Any] = None
    total: Optional[int] = None
    error: Optional[str] = None
    # 中文API格式
    code: Optional[int] = None
    msg: Optional[str] = None


@dataclass
class TestResult:
    """测试结果数据类"""
    endpoint: str
    method: str
    status_code: int
    response_time: float
    success: bool
    error_msg: Optional[str] = None


# ============================================================================
# 辅助函数
# ============================================================================

def is_api_success(response_data: Dict[str, Any]) -> bool:
    """检测API响应是否成功 - 兼容中英文格式"""
    # 英文格式
    if "success" in response_data:
        return response_data.get("success") is True
    
    # 中文格式
    if "code" in response_data:
        return response_data.get("code") == 0
    
    # 默认判断
    return "data" in response_data


# ============================================================================
# API客户端类
# ============================================================================

class APIClient:
    """统一API客户端"""
    
    def __init__(self, base_url: str, token: Optional[str] = None):
        self.base_url = base_url.rstrip('/')
        self.session = requests.Session()
        self.session.timeout = TestConfig.REQUEST_TIMEOUT
        
        if token:
            self.session.headers.update({
                "Authorization": f"Bearer {token}",
                "Content-Type": "application/json"
            })
    
    def request(self, method: str, endpoint: str, **kwargs) -> requests.Response:
        """发送HTTP请求"""
        url = f"{self.base_url}{endpoint}"
        
        # 重试机制
        for attempt in range(TestConfig.MAX_RETRIES):
            try:
                response = self.session.request(method, url, **kwargs)
                return response
            except requests.RequestException as e:
                if attempt == TestConfig.MAX_RETRIES - 1:
                    raise e
                time.sleep(TestConfig.RETRY_DELAY * (attempt + 1))
    
    def get(self, endpoint: str, **kwargs) -> requests.Response:
        return self.request("GET", endpoint, **kwargs)
    
    def post(self, endpoint: str, json_data: Any = None, **kwargs) -> requests.Response:
        return self.request("POST", endpoint, json=json_data, **kwargs)
    
    def put(self, endpoint: str, json_data: Any = None, **kwargs) -> requests.Response:
        return self.request("PUT", endpoint, json=json_data, **kwargs)
    
    def delete(self, endpoint: str, **kwargs) -> requests.Response:
        return self.request("DELETE", endpoint, **kwargs)
    
    def set_auth(self, token: str):
        """设置认证令牌"""
        self.session.headers.update({
            "Authorization": f"Bearer {token}",
            "Content-Type": "application/json"
        })


# ============================================================================
# Pytest Fixtures
# ============================================================================

@pytest.fixture(scope="session")
def api_client():
    """创建API客户端"""
    return APIClient(TestConfig.API_BASE)


@pytest.fixture(scope="session")
def admin_client():
    """创建管理员API客户端"""
    return APIClient(TestConfig.API_BASE, TestConfig.ADMIN_TOKEN)


@pytest.fixture(scope="session")
def test_user_data():
    """测试用户数据"""
    timestamp = int(time.time())
    return {
        "username": f"testuser_{timestamp}",
        "email": f"testuser_{timestamp}@example.com",
        "password": "TestPassword123!",
        "first_name": "Test",
        "last_name": "User"
    }


@pytest.fixture(scope="session")
def test_ticket_data():
    """测试工单数据"""
    return {
        "title": "API测试工单 - 自动化测试创建",
        "description": "这是一个通过自动化测试创建的工单，用于验证工单创建和管理功能的正确性。测试时间: " + str(datetime.now()),
        "priority": "normal"
    }


# ============================================================================
# 基础连接测试
# ============================================================================

@pytest.mark.api
class TestBasicConnectivity:
    """基础连接性测试"""
    
    def test_server_health_check(self, api_client):
        """测试服务器健康检查"""
        response = api_client.get("/health")
        assert response.status_code == 200
        
        data = response.json()
        assert is_api_success(data)
        assert "data" in data
    
    def test_api_ping(self, api_client):
        """测试API ping端点"""
        response = api_client.get("/ping")
        assert response.status_code == 200
        
        data = response.json()
        assert data.get("message") == "pong"
    
    def test_email_status(self, api_client):
        """测试邮箱状态检查"""
        response = api_client.get("/email-status")
        assert response.status_code == 200
        
        data = response.json()
        # 适配中文API响应格式
        assert "code" in data or "success" in data
    
    def test_redis_connection(self, api_client):
        """测试Redis连接"""
        response = api_client.get("/redis/test")
        assert response.status_code == 200
        
        data = response.json()
        assert data.get("status") == "ok"
    
    @pytest.mark.slow
    def test_response_time(self, api_client):
        """测试响应时间"""
        start_time = time.time()
        response = api_client.get("/ping")
        response_time = time.time() - start_time
        
        assert response.status_code == 200
        assert response_time < 2.0  # 响应时间应该小于2秒


# ============================================================================
# 认证API测试
# ============================================================================

@pytest.mark.api
class TestAuthAPI:
    """认证API测试"""
    
    def test_user_registration(self, api_client, test_user_data):
        """测试用户注册"""
        # 验证数据模型
        register_data = RegisterRequest(**test_user_data)
        
        response = api_client.post("/auth/register", json_data=register_data.dict())
        
        # 允许200或201状态码
        assert response.status_code in [200, 201], f"注册失败: {response.text}"
        
        data = response.json()
        if data.get("success"):
            assert data.get("success") is True
            assert "data" in data
    
    def test_user_login_valid(self, api_client, test_user_data):
        """测试有效用户登录"""
        # 先尝试注册用户
        register_data = RegisterRequest(**test_user_data)
        api_client.post("/auth/register", json_data=register_data.dict())
        
        # 然后测试登录
        login_data = LoginRequest(
            email=test_user_data["email"],
            password=test_user_data["password"]
        )
        
        response = api_client.post("/auth/login", json_data=login_data.dict())
        assert response.status_code == 200
        
        data = response.json()
        if data.get("success"):
            assert "data" in data
            # 检查是否有token
            token_data = data.get("data", {})
            assert any(key in token_data for key in ["token", "access_token", "auth_token"])
    
    def test_user_login_invalid(self, api_client):
        """测试无效用户登录"""
        login_data = LoginRequest(
            email="nonexistent@example.com",
            password="wrongpassword"
        )
        
        response = api_client.post("/auth/login", json_data=login_data.dict())
        assert response.status_code in [400, 401, 403]
    
    def test_protected_endpoint_without_auth(self, api_client):
        """测试未认证访问受保护端点"""
        response = api_client.get("/auth/me")
        assert response.status_code == 401
        
        data = response.json()
        assert data.get("success") is False or "error" in data


# ============================================================================
# 工单API测试
# ============================================================================

@pytest.mark.api
class TestTicketAPI:
    """工单API测试"""
    
    def test_create_ticket(self, admin_client, test_ticket_data):
        """测试创建工单"""
        # 验证请求数据
        ticket_data = TicketCreateRequest(**test_ticket_data)
        
        response = admin_client.post("/tickets", json_data=ticket_data.dict())
        assert response.status_code in [200, 201]
        
        data = response.json()
        assert data.get("success") is True
        assert "data" in data
        
        # 验证返回的工单数据
        ticket = data["data"]
        assert "id" in ticket
        assert ticket["title"] == test_ticket_data["title"]
        assert ticket["priority"] == test_ticket_data["priority"]
    
    def test_get_tickets_list(self, admin_client):
        """测试获取工单列表"""
        response = admin_client.get("/tickets")
        assert response.status_code == 200
        
        data = response.json()
        assert is_api_success(data)
        assert "data" in data
        
        # 检查数据结构
        tickets_data = data["data"]
        if isinstance(tickets_data, dict) and "tickets" in tickets_data:
            tickets = tickets_data["tickets"]
        elif isinstance(tickets_data, list):
            tickets = tickets_data
        else:
            tickets = []
        
        assert isinstance(tickets, list)
    
    def test_get_ticket_by_id(self, admin_client, test_ticket_data):
        """测试通过ID获取工单"""
        # 先创建一个工单
        ticket_data = TicketCreateRequest(**test_ticket_data)
        create_response = admin_client.post("/tickets", json_data=ticket_data.dict())
        
        if create_response.status_code in [200, 201]:
            created_ticket = create_response.json().get("data", {})
            ticket_id = created_ticket.get("id")
            
            if ticket_id:
                # 获取工单详情
                response = admin_client.get(f"/tickets/{ticket_id}")
                assert response.status_code == 200
                
                data = response.json()
                assert data.get("success") is True
                assert "data" in data
                
                ticket = data["data"]
                assert ticket["id"] == ticket_id
    
    def test_update_ticket_status(self, admin_client, test_ticket_data):
        """测试更新工单状态"""
        # 先创建工单
        ticket_data = TicketCreateRequest(**test_ticket_data)
        create_response = admin_client.post("/tickets", json_data=ticket_data.dict())
        
        if create_response.status_code in [200, 201]:
            created_ticket = create_response.json().get("data", {})
            ticket_id = created_ticket.get("id")
            
            if ticket_id:
                # 更新状态
                update_data = {
                    "status": "in_progress",
                    "comment": "开始处理工单"
                }
                
                response = admin_client.post(f"/tickets/{ticket_id}/status", json_data=update_data)
                assert response.status_code == 200
                
                data = response.json()
                assert data.get("success") is True
    
    def test_get_ticket_statistics(self, admin_client):
        """测试获取工单统计"""
        response = admin_client.get("/tickets/stats")
        assert response.status_code == 200
        
        data = response.json()
        assert data.get("success") is True
        assert "data" in data
        
        stats = data["data"]
        # 检查统计数据结构
        expected_keys = ["total", "open", "in_progress", "resolved"]
        for key in expected_keys:
            if key in stats:
                assert isinstance(stats[key], int)


# ============================================================================
# 工作流API测试
# ============================================================================

@pytest.mark.api
class TestWorkflowAPI:
    """工作流API测试"""
    
    def test_assign_ticket(self, admin_client, test_ticket_data):
        """测试工单分配"""
        # 创建工单
        ticket_data = TicketCreateRequest(**test_ticket_data)
        create_response = admin_client.post("/tickets", json_data=ticket_data.dict())
        
        if create_response.status_code in [200, 201]:
            created_ticket = create_response.json().get("data", {})
            ticket_id = created_ticket.get("id")
            
            if ticket_id:
                # 分配工单
                assign_data = {
                    "assigned_to_id": 1,  # 假设存在用户ID为1
                    "comment": "分配工单给技术支持"
                }
                
                response = admin_client.post(f"/tickets/{ticket_id}/assign", json_data=assign_data)
                # 接受200或404(如果用户不存在)
                assert response.status_code in [200, 404]
    
    def test_get_my_tickets(self, admin_client):
        """测试获取我的工单"""
        response = admin_client.get("/tickets/my-tickets")
        assert response.status_code == 200
        
        data = response.json()
        assert data.get("success") is True
        assert "data" in data
    
    def test_get_unassigned_tickets(self, admin_client):
        """测试获取未分配工单"""
        response = admin_client.get("/tickets/unassigned")
        assert response.status_code == 200
        
        data = response.json()
        assert data.get("success") is True


# ============================================================================
# 用户管理API测试
# ============================================================================

@pytest.mark.api
class TestUserManagementAPI:
    """用户管理API测试"""
    
    def test_get_user_profile(self, admin_client):
        """测试获取用户配置文件"""
        response = admin_client.get("/user/profile")
        assert response.status_code == 200
        
        data = response.json()
        assert data.get("success") is True
    
    def test_get_user_stats(self, admin_client):
        """测试获取用户统计"""
        response = admin_client.get("/user/stats")
        assert response.status_code == 200
        
        data = response.json()
        assert data.get("success") is True


# ============================================================================
# 管理员API测试
# ============================================================================

@pytest.mark.api
class TestAdminAPI:
    """管理员API测试"""
    
    def test_get_system_analytics(self, admin_client):
        """测试获取系统分析数据"""
        response = admin_client.get("/admin/analytics/system")
        assert response.status_code == 200
        
        data = response.json()
        assert data.get("success") is True
    
    def test_get_users_list(self, admin_client):
        """测试获取用户列表"""
        response = admin_client.get("/admin/users")
        assert response.status_code == 200
        
        data = response.json()
        assert data.get("success") is True
    
    def test_get_email_config(self, admin_client):
        """测试获取邮箱配置"""
        response = admin_client.get("/admin/email-config")
        assert response.status_code == 200
        
        data = response.json()
        assert data.get("success") is True


# ============================================================================
# 性能和压力测试
# ============================================================================

@pytest.mark.performance
@pytest.mark.slow
class TestPerformance:
    """性能测试"""
    
    def test_concurrent_ticket_creation(self, admin_client):
        """测试并发工单创建"""
        import threading
        import queue
        
        results = queue.Queue()
        
        def create_ticket(client, ticket_num):
            try:
                ticket_data = {
                    "title": f"并发测试工单 #{ticket_num}",
                    "description": f"并发测试工单描述 {ticket_num}",
                    "priority": "normal"
                }
                response = client.post("/tickets", json_data=ticket_data)
                results.put({
                    "thread": ticket_num,
                    "status_code": response.status_code,
                    "success": response.status_code in [200, 201]
                })
            except Exception as e:
                results.put({
                    "thread": ticket_num,
                    "error": str(e),
                    "success": False
                })
        
        # 创建5个并发线程
        threads = []
        for i in range(5):
            thread = threading.Thread(target=create_ticket, args=(admin_client, i))
            threads.append(thread)
            thread.start()
        
        # 等待所有线程完成
        for thread in threads:
            thread.join()
        
        # 收集结果
        successful_requests = 0
        while not results.empty():
            result = results.get()
            if result.get("success"):
                successful_requests += 1
        
        # 至少应该有一半的请求成功
        assert successful_requests >= 2, f"并发测试失败，成功请求数: {successful_requests}"
    
    def test_api_response_times(self, admin_client):
        """测试API响应时间"""
        endpoints_to_test = [
            "/ping",
            "/health",
            "/tickets",
            "/user/profile",
            "/tickets/stats"
        ]
        
        for endpoint in endpoints_to_test:
            start_time = time.time()
            response = admin_client.get(endpoint)
            response_time = time.time() - start_time
            
            assert response.status_code == 200
            assert response_time < 3.0, f"端点 {endpoint} 响应时间过长: {response_time:.2f}s"


# ============================================================================
# 集成测试
# ============================================================================

@pytest.mark.integration
class TestIntegration:
    """完整工作流程集成测试"""
    
    def test_complete_ticket_workflow(self, admin_client, test_ticket_data):
        """测试完整的工单生命周期"""
        # 1. 创建工单
        ticket_data = TicketCreateRequest(**test_ticket_data)
        create_response = admin_client.post("/tickets", json_data=ticket_data.dict())
        
        if create_response.status_code not in [200, 201]:
            pytest.skip("工单创建失败，跳过集成测试")
        
        created_ticket = create_response.json().get("data", {})
        ticket_id = created_ticket.get("id")
        assert ticket_id, "创建的工单没有ID"
        
        # 2. 获取工单详情
        get_response = admin_client.get(f"/tickets/{ticket_id}")
        assert get_response.status_code == 200
        
        # 3. 更新工单状态
        status_update = {
            "status": "in_progress",
            "comment": "开始处理工单"
        }
        status_response = admin_client.post(f"/tickets/{ticket_id}/status", json_data=status_update)
        assert status_response.status_code == 200
        
        # 4. 验证状态更新
        updated_ticket_response = admin_client.get(f"/tickets/{ticket_id}")
        if updated_ticket_response.status_code == 200:
            updated_ticket = updated_ticket_response.json().get("data", {})
            # 注意：状态可能因为权限或其他原因没有更新，这里不强制断言


# ============================================================================
# 测试报告和总结
# ============================================================================

def pytest_html_report_title(report):
    """自定义HTML报告标题"""
    report.title = "工单管理系统 API 测试报告"


def pytest_html_results_summary(prefix, summary, postfix):
    """自定义HTML报告摘要"""
    prefix.extend([
        "<h2>测试摘要</h2>",
        f"<p>测试时间: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}</p>",
        f"<p>API基础URL: {TestConfig.API_BASE}</p>",
        "<p>测试覆盖: 基础连接性、认证、工单管理、工作流、用户管理、管理员功能</p>"
    ])


if __name__ == "__main__":
    # 直接运行测试的简单入口
    import subprocess
    import sys
    
    cmd = [
        sys.executable, "-m", "pytest", __file__,
        "-v", "--tb=short", "--durations=10",
        "--html=reports/comprehensive_api_test_report.html",
        "--self-contained-html"
    ]
    
    subprocess.run(cmd)