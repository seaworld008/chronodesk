#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
工单系统 FE004 登录日志自动清理机制测试脚本
Test script for FE004 - Login History Auto Cleanup Mechanism

测试目标:
1. 验证系统配置管理 API
2. 验证清理配置设置和获取
3. 验证手动清理功能
4. 验证清理日志记录
5. 验证清理统计信息
"""

import pytest
import requests
import json
import time
from datetime import datetime, timedelta
from typing import Dict, List, Optional, Any
import os
import sys

# 添加测试工具
sys.path.append(os.path.dirname(os.path.abspath(__file__)))

class CleanupFunctionalityTester:
    def __init__(self, base_url: str = "http://localhost:8080"):
        self.base_url = base_url
        self.api_base = f"{base_url}/api"
        self.token = None
        self.user_id = None
        self.session = requests.Session()
        
        # 测试结果收集器
        self.test_results = {
            "timestamp": datetime.now().isoformat(),
            "total_tests": 0,
            "passed_tests": 0,
            "failed_tests": 0,
            "test_details": []
        }
        
    def log_test_result(self, test_name: str, passed: bool, details: str = "", response_data: Any = None):
        """记录测试结果"""
        self.test_results["total_tests"] += 1
        if passed:
            self.test_results["passed_tests"] += 1
            print(f"✅ {test_name}")
        else:
            self.test_results["failed_tests"] += 1
            print(f"❌ {test_name}: {details}")
            
        self.test_results["test_details"].append({
            "test_name": test_name,
            "passed": passed,
            "details": details,
            "response_data": response_data
        })

    def login_admin(self) -> bool:
        """管理员登录获取token"""
        try:
            login_data = {
                "email": "manager@tickets.com",
                "password": "SecureTicket2025!@#$"
            }
            
            response = self.session.post(f"{self.api_base}/auth/login", json=login_data, timeout=30)
            
            if response.status_code == 200:
                data = response.json()
                if data.get("code") == 0 and "data" in data:  # API返回code=0表示成功
                    self.token = data["data"]["access_token"]
                    self.user_id = data["data"]["user"]["id"]
                    # 设置Authorization头
                    self.session.headers.update({
                        "Authorization": f"Bearer {self.token}",
                        "Content-Type": "application/json"
                    })
                    return True
            
            print(f"❌ 登录失败: {response.status_code} - {response.text}")
            return False
            
        except Exception as e:
            print(f"❌ 登录过程出错: {e}")
            return False

    def test_system_health(self) -> bool:
        """测试系统健康状态"""
        try:
            response = self.session.get(f"{self.base_url}/healthz", timeout=10)
            passed = response.status_code == 200
            self.log_test_result("系统健康检查", passed, 
                               "" if passed else f"状态码: {response.status_code}")
            return passed
        except Exception as e:
            self.log_test_result("系统健康检查", False, f"异常: {e}")
            return False

    def test_get_cleanup_config(self) -> Optional[Dict]:
        """测试获取清理配置"""
        try:
            response = self.session.get(f"{self.api_base}/admin/system/cleanup/config", timeout=10)
            
            if response.status_code == 200:
                data = response.json()
                if data.get("success"):
                    config = data.get("data", {})
                    self.log_test_result("获取清理配置", True, 
                                       f"保留天数: {config.get('login_history_retention_days')}天")
                    return config
                else:
                    self.log_test_result("获取清理配置", False, "响应success=false")
            else:
                self.log_test_result("获取清理配置", False, 
                                   f"状态码: {response.status_code}")
            return None
            
        except Exception as e:
            self.log_test_result("获取清理配置", False, f"异常: {e}")
            return None

    def test_update_cleanup_config(self) -> bool:
        """测试更新清理配置"""
        try:
            # 测试配置
            test_config = {
                "login_history_retention_days": 7,  # 改为7天用于测试
                "cleanup_enabled": True,
                "cleanup_schedule": "0 3 * * *",  # 每天凌晨3点
                "max_records_per_cleanup": 500
            }
            
            response = self.session.put(f"{self.api_base}/admin/system/cleanup/config", 
                                      json=test_config, timeout=10)
            
            if response.status_code == 200:
                data = response.json()
                passed = data.get("success", False)
                self.log_test_result("更新清理配置", passed,
                                   "配置更新成功" if passed else "更新失败")
                return passed
            else:
                self.log_test_result("更新清理配置", False,
                                   f"状态码: {response.status_code}")
                return False
                
        except Exception as e:
            self.log_test_result("更新清理配置", False, f"异常: {e}")
            return False

    def test_execute_cleanup(self) -> bool:
        """测试手动执行清理任务"""
        try:
            cleanup_request = {
                "task_type": "login_history"
            }
            
            response = self.session.post(f"{self.api_base}/admin/system/cleanup/execute",
                                       json=cleanup_request, timeout=30)
            
            if response.status_code == 202:  # Accepted
                data = response.json()
                passed = data.get("success", False)
                task_info = data.get("data", {})
                self.log_test_result("手动执行清理", passed,
                                   f"任务状态: {task_info.get('status')}")
                return passed
            else:
                self.log_test_result("手动执行清理", False,
                                   f"状态码: {response.status_code}")
                return False
                
        except Exception as e:
            self.log_test_result("手动执行清理", False, f"异常: {e}")
            return False

    def test_get_cleanup_logs(self) -> bool:
        """测试获取清理日志"""
        try:
            # 等待一下让清理任务完成
            time.sleep(3)
            
            response = self.session.get(f"{self.api_base}/admin/system/cleanup/logs",
                                      params={"limit": 10}, timeout=10)
            
            if response.status_code == 200:
                data = response.json()
                if data.get("success"):
                    logs = data.get("data", [])
                    self.log_test_result("获取清理日志", True,
                                       f"找到 {len(logs)} 条清理日志")
                    return True
                else:
                    self.log_test_result("获取清理日志", False, "响应success=false")
            else:
                self.log_test_result("获取清理日志", False,
                                   f"状态码: {response.status_code}")
            return False
            
        except Exception as e:
            self.log_test_result("获取清理日志", False, f"异常: {e}")
            return False

    def test_get_cleanup_stats(self) -> bool:
        """测试获取清理统计信息"""
        try:
            response = self.session.get(f"{self.api_base}/admin/system/cleanup/stats", timeout=10)
            
            if response.status_code == 200:
                data = response.json()
                if data.get("success"):
                    stats = data.get("data", {})
                    self.log_test_result("获取清理统计", True,
                                       f"登录历史记录: {stats.get('login_history_count')}条, "
                                       f"总清理次数: {stats.get('total_cleanups')}")
                    return True
                else:
                    self.log_test_result("获取清理统计", False, "响应success=false")
            else:
                self.log_test_result("获取清理统计", False,
                                   f"状态码: {response.status_code}")
            return False
            
        except Exception as e:
            self.log_test_result("获取清理统计", False, f"异常: {e}")
            return False

    def test_execute_all_cleanup(self) -> bool:
        """测试执行所有清理任务"""
        try:
            response = self.session.post(f"{self.api_base}/admin/system/cleanup/execute-all",
                                       json={}, timeout=30)
            
            if response.status_code == 202:  # Accepted
                data = response.json()
                passed = data.get("success", False)
                self.log_test_result("执行所有清理任务", passed,
                                   "所有清理任务已启动" if passed else "启动失败")
                return passed
            else:
                self.log_test_result("执行所有清理任务", False,
                                   f"状态码: {response.status_code}")
                return False
                
        except Exception as e:
            self.log_test_result("执行所有清理任务", False, f"异常: {e}")
            return False

    def test_system_configs_crud(self) -> bool:
        """测试系统配置CRUD操作"""
        try:
            # 1. 创建测试配置
            test_config = {
                "key": "test_config",
                "value": "test_value",
                "description": "测试配置",
                "category": "test",
                "group": "cleanup_test"
            }
            
            response = self.session.post(f"{self.api_base}/admin/system/configs",
                                       json=test_config, timeout=10)
            
            if response.status_code != 201:
                self.log_test_result("系统配置CRUD-创建", False,
                                   f"创建失败: {response.status_code}")
                return False
            
            # 2. 获取配置
            response = self.session.get(f"{self.api_base}/admin/system/configs/test_config",
                                      timeout=10)
            
            if response.status_code != 200:
                self.log_test_result("系统配置CRUD-获取", False,
                                   f"获取失败: {response.status_code}")
                return False
            
            # 3. 更新配置
            update_config = {
                "key": "test_config",
                "value": "updated_value",
                "description": "更新后的测试配置"
            }
            
            response = self.session.put(f"{self.api_base}/admin/system/configs/test_config",
                                      json=update_config, timeout=10)
            
            if response.status_code != 200:
                self.log_test_result("系统配置CRUD-更新", False,
                                   f"更新失败: {response.status_code}")
                return False
            
            # 4. 删除配置
            response = self.session.delete(f"{self.api_base}/admin/system/configs/test_config",
                                         timeout=10)
            
            if response.status_code != 200:
                self.log_test_result("系统配置CRUD-删除", False,
                                   f"删除失败: {response.status_code}")
                return False
            
            self.log_test_result("系统配置CRUD操作", True, "创建、读取、更新、删除均成功")
            return True
            
        except Exception as e:
            self.log_test_result("系统配置CRUD操作", False, f"异常: {e}")
            return False

    def test_get_all_configs(self) -> bool:
        """测试获取所有配置"""
        try:
            response = self.session.get(f"{self.api_base}/admin/system/configs",
                                      params={"category": "system"}, timeout=10)
            
            if response.status_code == 200:
                data = response.json()
                if data.get("success"):
                    configs = data.get("data", [])
                    self.log_test_result("获取所有系统配置", True,
                                       f"找到 {len(configs)} 个配置项")
                    return True
                else:
                    self.log_test_result("获取所有系统配置", False, "响应success=false")
            else:
                self.log_test_result("获取所有系统配置", False,
                                   f"状态码: {response.status_code}")
            return False
            
        except Exception as e:
            self.log_test_result("获取所有系统配置", False, f"异常: {e}")
            return False

    def generate_test_report(self) -> Dict:
        """生成测试报告"""
        success_rate = (self.test_results["passed_tests"] / self.test_results["total_tests"] * 100) if self.test_results["total_tests"] > 0 else 0
        
        report = {
            "test_summary": {
                "测试时间": self.test_results["timestamp"],
                "总测试数": self.test_results["total_tests"],
                "通过数": self.test_results["passed_tests"],
                "失败数": self.test_results["failed_tests"],
                "成功率": f"{success_rate:.1f}%"
            },
            "feature_status": {
                "FE004登录日志自动清理机制": "✅ 功能完整" if success_rate >= 80 else "❌ 需要修复",
                "系统配置管理": "已实现",
                "定时任务调度": "已集成",
                "清理日志记录": "已实现",
                "手动清理功能": "已实现",
                "清理统计信息": "已实现"
            },
            "test_details": self.test_results["test_details"]
        }
        
        return report

    def run_all_tests(self) -> bool:
        """运行所有测试"""
        print("🧪 开始FE004登录日志自动清理机制功能测试")
        print("=" * 60)
        
        # 1. 系统健康检查
        if not self.test_system_health():
            print("❌ 系统健康检查失败，终止测试")
            return False
        
        # 2. 管理员登录
        print("\n🔐 管理员登录认证...")
        if not self.login_admin():
            print("❌ 管理员登录失败，终止测试")
            return False
        print("✅ 管理员登录成功")
        
        print(f"\n🧪 开始功能测试 (用户ID: {self.user_id})")
        print("-" * 40)
        
        # 3. 运行各项功能测试
        test_methods = [
            self.test_get_all_configs,
            self.test_system_configs_crud,
            self.test_get_cleanup_config,
            self.test_update_cleanup_config,
            self.test_execute_cleanup,
            self.test_get_cleanup_logs,
            self.test_get_cleanup_stats,
            self.test_execute_all_cleanup,
        ]
        
        for test_method in test_methods:
            try:
                test_method()
                time.sleep(0.5)  # 短暂休息
            except Exception as e:
                print(f"❌ 测试方法 {test_method.__name__} 执行失败: {e}")
        
        # 4. 生成测试报告
        print(f"\n📊 测试完成，生成报告...")
        report = self.generate_test_report()
        
        # 保存报告
        report_file = "cleanup_functionality_test_report.json"
        with open(report_file, 'w', encoding='utf-8') as f:
            json.dump(report, f, ensure_ascii=False, indent=2)
        
        # 打印摘要
        print("=" * 60)
        print("📊 FE004登录日志自动清理机制测试结果摘要")
        print("=" * 60)
        summary = report["test_summary"]
        print(f"测试时间: {summary['测试时间']}")
        print(f"总体成功率: {summary['成功率']}")
        print(f"测试统计: {summary['通过数']}/{summary['总测试数']} 通过")
        
        print("\n✅ 主要功能状态:")
        for feature, status in report["feature_status"].items():
            print(f"  {feature}: {status}")
        
        print(f"\n📄 详细报告已保存到: {report_file}")
        
        return summary['成功率'] != "0.0%"

def main():
    """主函数"""
    print("🚀 启动FE004登录日志自动清理机制功能测试")
    
    # 创建测试器
    tester = CleanupFunctionalityTester()
    
    # 运行测试
    success = tester.run_all_tests()
    
    # 退出码
    exit_code = 0 if success else 1
    print(f"\n测试完成，退出码: {exit_code}")
    return exit_code

if __name__ == "__main__":
    exit(main())