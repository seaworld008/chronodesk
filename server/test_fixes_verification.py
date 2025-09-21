#!/usr/bin/env python3
"""
工单管理系统修复验证测试
=====================================

验证前端数据流修复的专业测试脚本，确保所有功能正常工作
主要测试:
1. 工单管理 - 数据加载和分页
2. 通知中心 - 分页功能修复
3. 用户管理 - CRUD操作和错误处理
4. 邮箱重复错误提示

运行方式: python test_fixes_verification.py
"""

import json
import time
import random
import string
from typing import Dict, List, Any
from dataclasses import dataclass

import requests
from requests.adapters import HTTPAdapter
from requests.packages.urllib3.util.retry import Retry


@dataclass 
class TestResult:
    name: str
    passed: bool
    duration: float
    error: str = None
    details: str = None


class FixVerificationTester:
    """修复验证测试类"""
    
    def __init__(self):
        self.base_url = "http://localhost:8081/api"
        self.session = self._create_session()
        self.results: List[TestResult] = []
        
        # 测试用的认证token
        self.token = "test-token-for-verification"
        self.session.headers.update({
            "Authorization": f"Bearer {self.token}",
            "Content-Type": "application/json"
        })
    
    def _create_session(self) -> requests.Session:
        """创建带重试机制的HTTP会话"""
        session = requests.Session()
        
        # 配置重试策略
        retry_strategy = Retry(
            total=3,
            backoff_factor=1,
            status_forcelist=[429, 500, 502, 503, 504],
        )
        
        adapter = HTTPAdapter(max_retries=retry_strategy)
        session.mount("http://", adapter)
        session.mount("https://", adapter)
        
        return session
    
    def _random_string(self, length: int = 8) -> str:
        """生成随机字符串"""
        return ''.join(random.choices(string.ascii_letters + string.digits, k=length))
    
    def _log_test_start(self, test_name: str):
        """记录测试开始"""
        print(f"\n🔄 开始测试: {test_name}")
        print("-" * 60)
    
    def _log_test_result(self, result: TestResult):
        """记录测试结果"""
        status = "✅ PASS" if result.passed else "❌ FAIL"
        print(f"{status} {result.name} ({result.duration:.2f}s)")
        
        if result.details:
            print(f"   详情: {result.details}")
        
        if result.error:
            print(f"   错误: {result.error}")
    
    def test_ticket_pagination_fix(self) -> TestResult:
        """测试工单分页修复"""
        start_time = time.time()
        
        try:
            # 测试不同分页参数
            test_cases = [
                {"page": 1, "page_size": 5},
                {"page": 1, "page_size": 10}, 
                {"page": 2, "page_size": 5},
            ]
            
            results = []
            for params in test_cases:
                response = self.session.get(f"{self.base_url}/tickets", params=params)
                
                # 检查响应状态
                if response.status_code != 200:
                    return TestResult(
                        name="工单分页修复",
                        passed=False,
                        duration=time.time() - start_time,
                        error=f"HTTP {response.status_code}: {response.text}"
                    )
                
                data = response.json()
                
                # 验证响应格式
                if "data" not in data:
                    return TestResult(
                        name="工单分页修复", 
                        passed=False,
                        duration=time.time() - start_time,
                        error="响应缺少data字段"
                    )
                
                results.append({
                    "params": params,
                    "total": data.get("total", 0),
                    "count": len(data.get("data", []))
                })
            
            return TestResult(
                name="工单分页修复",
                passed=True,
                duration=time.time() - start_time,
                details=f"测试了{len(results)}种分页参数，所有响应正常"
            )
            
        except Exception as e:
            return TestResult(
                name="工单分页修复",
                passed=False,
                duration=time.time() - start_time,
                error=str(e)
            )
    
    def test_notification_pagination_fix(self) -> TestResult:
        """测试通知分页修复"""
        start_time = time.time()
        
        try:
            # 测试通知分页
            test_cases = [
                {"page": 1, "page_size": 5},
                {"page": 1, "page_size": 10},
            ]
            
            for params in test_cases:
                response = self.session.get(f"{self.base_url}/notifications", params=params)
                
                if response.status_code != 200:
                    return TestResult(
                        name="通知分页修复",
                        passed=False,
                        duration=time.time() - start_time,
                        error=f"HTTP {response.status_code}: {response.text}"
                    )
                
                data = response.json()
                
                # 验证分页数据结构
                if "data" not in data:
                    return TestResult(
                        name="通知分页修复",
                        passed=False,
                        duration=time.time() - start_time,
                        error="通知响应缺少data字段"
                    )
            
            return TestResult(
                name="通知分页修复",
                passed=True,
                duration=time.time() - start_time,
                details="通知分页参数传递正常，响应格式正确"
            )
            
        except Exception as e:
            return TestResult(
                name="通知分页修复",
                passed=False,
                duration=time.time() - start_time,
                error=str(e)
            )
    
    def test_user_list_pagination_fix(self) -> TestResult:
        """测试用户列表分页修复"""
        start_time = time.time()
        
        try:
            # 测试用户列表分页
            params = {"page": 1, "page_size": 10}
            response = self.session.get(f"{self.base_url}/admin/users", params=params)
            
            if response.status_code != 200:
                return TestResult(
                    name="用户列表分页修复",
                    passed=False,
                    duration=time.time() - start_time,
                    error=f"HTTP {response.status_code}: {response.text}"
                )
            
            data = response.json()
            
            # 验证用户列表数据结构
            if "data" not in data:
                return TestResult(
                    name="用户列表分页修复", 
                    passed=False,
                    duration=time.time() - start_time,
                    error="用户列表响应缺少data字段"
                )
            
            return TestResult(
                name="用户列表分页修复",
                passed=True,
                duration=time.time() - start_time,
                details=f"用户列表获取成功，共{data.get('total', 0)}个用户"
            )
            
        except Exception as e:
            return TestResult(
                name="用户列表分页修复",
                passed=False,
                duration=time.time() - start_time,
                error=str(e)
            )
    
    def test_email_duplicate_error_handling(self) -> TestResult:
        """测试邮箱重复错误处理"""
        start_time = time.time()
        
        try:
            # 先创建一个测试用户
            random_suffix = self._random_string(6)
            test_email = f"test_{random_suffix}@example.com"
            
            user_data = {
                "username": f"testuser_{random_suffix}",
                "email": test_email,
                "password": "testpass123",
                "role": "customer",
                "status": "active",
                "first_name": "测试",
                "last_name": "用户"
            }
            
            # 第一次创建 - 应该成功
            response = self.session.post(f"{self.base_url}/admin/users", json=user_data)
            
            if response.status_code == 201:
                first_create_success = True
                created_user_data = response.json()
            else:
                first_create_success = False
                print(f"首次创建用户失败: {response.status_code} - {response.text}")
            
            # 第二次创建相同邮箱 - 应该返回错误
            duplicate_user_data = user_data.copy()
            duplicate_user_data["username"] = f"testuser2_{random_suffix}"  # 不同用户名，相同邮箱
            
            duplicate_response = self.session.post(f"{self.base_url}/admin/users", json=duplicate_user_data)
            
            # 检查是否正确返回冲突错误
            if duplicate_response.status_code == 409:
                error_data = duplicate_response.json()
                error_message = error_data.get("msg", "")
                
                # 验证错误信息是否包含邮箱相关内容
                email_error_detected = "email" in error_message.lower() or "邮箱" in error_message
                
                return TestResult(
                    name="邮箱重复错误处理",
                    passed=email_error_detected,
                    duration=time.time() - start_time,
                    details=f"正确返回409错误，错误信息: {error_message}",
                    error=None if email_error_detected else "错误信息中未明确指出邮箱重复"
                )
            else:
                return TestResult(
                    name="邮箱重复错误处理",
                    passed=False,
                    duration=time.time() - start_time,
                    error=f"期望HTTP 409，实际收到{duplicate_response.status_code}: {duplicate_response.text}"
                )
                
        except Exception as e:
            return TestResult(
                name="邮箱重复错误处理",
                passed=False,
                duration=time.time() - start_time,
                error=str(e)
            )
    
    def test_api_connectivity(self) -> TestResult:
        """测试API连接性"""
        start_time = time.time()
        
        try:
            # 测试健康检查端点
            response = self.session.get(f"http://localhost:8081/healthz")
            
            if response.status_code == 200:
                health_data = response.json()
                return TestResult(
                    name="API连接性测试",
                    passed=True,
                    duration=time.time() - start_time,
                    details=f"服务健康: {health_data.get('message', 'OK')}"
                )
            else:
                return TestResult(
                    name="API连接性测试",
                    passed=False,
                    duration=time.time() - start_time,
                    error=f"健康检查失败: {response.status_code}"
                )
                
        except Exception as e:
            return TestResult(
                name="API连接性测试",
                passed=False,
                duration=time.time() - start_time,
                error=f"无法连接到API服务: {str(e)}"
            )
    
    def test_response_format_consistency(self) -> TestResult:
        """测试响应格式一致性"""
        start_time = time.time()
        
        try:
            endpoints = [
                "/tickets",
                "/notifications", 
                "/admin/users"
            ]
            
            consistent = True
            inconsistent_endpoints = []
            
            for endpoint in endpoints:
                try:
                    response = self.session.get(f"{self.base_url}{endpoint}", params={"page": 1, "page_size": 5})
                    
                    if response.status_code == 200:
                        data = response.json()
                        
                        # 检查标准字段
                        expected_fields = ["code", "data", "msg"]
                        missing_fields = [field for field in expected_fields if field not in data]
                        
                        if missing_fields:
                            consistent = False
                            inconsistent_endpoints.append(f"{endpoint}: 缺少字段 {missing_fields}")
                        
                        # 检查data字段是否包含列表数据
                        if "data" in data and not isinstance(data.get("data"), list):
                            # 对于列表端点，data应该是数组或包含items数组的对象
                            if not (isinstance(data["data"], dict) and ("items" in data["data"] or "data" in data["data"])):
                                consistent = False 
                                inconsistent_endpoints.append(f"{endpoint}: data字段格式不符合预期 - 期望包含items或data数组")
                                
                except Exception as e:
                    inconsistent_endpoints.append(f"{endpoint}: 请求失败 - {str(e)}")
                    consistent = False
            
            return TestResult(
                name="响应格式一致性",
                passed=consistent,
                duration=time.time() - start_time,
                details="所有端点响应格式一致" if consistent else f"发现不一致: {'; '.join(inconsistent_endpoints)}"
            )
            
        except Exception as e:
            return TestResult(
                name="响应格式一致性",
                passed=False,
                duration=time.time() - start_time,
                error=str(e)
            )
    
    def run_all_tests(self):
        """运行所有测试"""
        print("🚀 开始验证工单管理系统修复效果")
        print("=" * 60)
        
        # 定义测试顺序
        tests = [
            self.test_api_connectivity,
            self.test_ticket_pagination_fix,
            self.test_notification_pagination_fix, 
            self.test_user_list_pagination_fix,
            self.test_email_duplicate_error_handling,
            self.test_response_format_consistency,
        ]
        
        for test in tests:
            self._log_test_start(test.__name__.replace("test_", "").replace("_", " ").title())
            result = test()
            self.results.append(result)
            self._log_test_result(result)
        
        self._generate_summary()
    
    def _generate_summary(self):
        """生成测试总结"""
        print("\n" + "=" * 60)
        print("📊 修复验证测试总结")
        print("=" * 60)
        
        total_tests = len(self.results)
        passed_tests = sum(1 for r in self.results if r.passed)
        failed_tests = total_tests - passed_tests
        total_duration = sum(r.duration for r in self.results)
        
        print(f"总测试数: {total_tests}")
        print(f"通过测试: {passed_tests} ✅")
        print(f"失败测试: {failed_tests} ❌") 
        print(f"成功率: {(passed_tests/total_tests*100):.1f}%")
        print(f"总耗时: {total_duration:.2f}秒")
        
        print(f"\n📋 详细结果:")
        for result in self.results:
            status = "✅" if result.passed else "❌"
            print(f"{status} {result.name} - {result.duration:.2f}s")
        
        if failed_tests > 0:
            print(f"\n⚠️  失败的测试:")
            for result in self.results:
                if not result.passed:
                    print(f"❌ {result.name}: {result.error}")
        else:
            print(f"\n🎉 所有修复验证测试通过！系统功能已恢复正常。")
        
        print("\n" + "=" * 60)


if __name__ == "__main__":
    tester = FixVerificationTester()
    tester.run_all_tests()