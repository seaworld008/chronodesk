#!/usr/bin/env python3
"""
项目进度状态综合测试脚本

根据任务文档检查实际完成情况，验证系统功能完整性
"""

import requests
import json
import time
import sys
from datetime import datetime
from typing import Dict, List, Optional

# 测试配置
BASE_URL = "http://localhost:8080"
API_BASE = f"{BASE_URL}/api"

class ProjectStatusTester:
    def __init__(self):
        self.session = requests.Session()
        self.token = "test-token"
        self.admin_token = "admin-test-token"
        self.test_results = {
            "basic_features": {},
            "notification_system": {},
            "user_management": {},
            "system_status": {},
            "api_endpoints": {},
            "enhancement_features": {}
        }
        
        # 设置请求头
        self.session.headers.update({
            "Authorization": f"Bearer {self.token}",
            "Content-Type": "application/json"
        })
    
    def test_system_health(self) -> bool:
        """测试系统基础健康状态"""
        print("\n🔍 测试系统基础健康状态...")
        
        try:
            # 测试健康检查端点
            health_response = self.session.get(f"{BASE_URL}/healthz")
            if health_response.status_code == 200:
                print("  ✅ 健康检查端点正常")
                self.test_results["system_status"]["health_check"] = "✅ 正常"
            else:
                print(f"  ❌ 健康检查失败: {health_response.status_code}")
                self.test_results["system_status"]["health_check"] = f"❌ 失败({health_response.status_code})"
                return False
            
            # 测试用户认证
            auth_response = self.session.get(f"{API_BASE}/auth/me")
            if auth_response.status_code == 200:
                print("  ✅ 用户认证系统正常")
                self.test_results["basic_features"]["authentication"] = "✅ 正常"
            else:
                print(f"  ❌ 用户认证失败: {auth_response.status_code}")
                self.test_results["basic_features"]["authentication"] = f"❌ 失败({auth_response.status_code})"
                return False
            
            return True
            
        except Exception as e:
            print(f"  ❌ 系统健康检查异常: {e}")
            self.test_results["system_status"]["health_check"] = f"❌ 异常: {str(e)}"
            return False
    
    def test_core_apis(self) -> Dict[str, str]:
        """测试核心API端点"""
        print("\n📡 测试核心API端点...")
        
        core_apis = [
            {"name": "工单列表", "method": "GET", "url": f"{API_BASE}/tickets"},
            {"name": "用户资料", "method": "GET", "url": f"{API_BASE}/user/profile"},
            {"name": "通知列表", "method": "GET", "url": f"{API_BASE}/notifications"},
            {"name": "通知偏好", "method": "GET", "url": f"{API_BASE}/notifications/preferences"},
            {"name": "邮件配置", "method": "GET", "url": f"{API_BASE}/admin/email-config"},
            {"name": "用户管理", "method": "GET", "url": f"{API_BASE}/admin/users"},
            {"name": "系统配置", "method": "GET", "url": f"{API_BASE}/admin/system/configs"},
            {"name": "Webhook配置", "method": "GET", "url": f"{API_BASE}/webhooks"},
        ]
        
        api_results = {}
        
        for api in core_apis:
            try:
                if api["method"] == "GET":
                    response = self.session.get(api["url"])
                
                if response.status_code == 200:
                    print(f"  ✅ {api['name']}: {response.status_code}")
                    api_results[api["name"]] = "✅ 正常"
                elif response.status_code == 401:
                    print(f"  🔒 {api['name']}: 需要权限 ({response.status_code})")
                    api_results[api["name"]] = "🔒 需要权限"
                else:
                    print(f"  ❌ {api['name']}: {response.status_code}")
                    api_results[api["name"]] = f"❌ 失败({response.status_code})"
                    
            except Exception as e:
                print(f"  ❌ {api['name']}: 异常 - {str(e)}")
                api_results[api["name"]] = f"❌ 异常: {str(e)}"
        
        self.test_results["api_endpoints"] = api_results
        return api_results
    
    def test_notification_system(self) -> Dict[str, str]:
        """测试通知系统功能"""
        print("\n🔔 测试通知系统功能...")
        
        notification_tests = {}
        
        try:
            # 测试通知列表
            notifications_response = self.session.get(f"{API_BASE}/notifications")
            if notifications_response.status_code == 200:
                notifications = notifications_response.json()
                total_notifications = len(notifications.get("data", []))
                print(f"  ✅ 通知列表查询成功: {total_notifications} 条通知")
                notification_tests["通知列表"] = f"✅ {total_notifications} 条通知"
            else:
                print(f"  ❌ 通知列表查询失败: {notifications_response.status_code}")
                notification_tests["通知列表"] = f"❌ 失败({notifications_response.status_code})"
            
            # 测试未读通知数量
            unread_response = self.session.get(f"{API_BASE}/notifications/unread-count")
            if unread_response.status_code == 200:
                unread_count = unread_response.json().get("count", 0)
                print(f"  ✅ 未读通知统计: {unread_count} 条")
                notification_tests["未读统计"] = f"✅ {unread_count} 条未读"
            else:
                print(f"  ❌ 未读通知统计失败: {unread_response.status_code}")
                notification_tests["未读统计"] = f"❌ 失败({unread_response.status_code})"
            
            # 测试通知偏好设置
            preferences_response = self.session.get(f"{API_BASE}/notifications/preferences")
            if preferences_response.status_code == 200:
                preferences = preferences_response.json().get("data", [])
                print(f"  ✅ 通知偏好设置: {len(preferences)} 项配置")
                notification_tests["偏好设置"] = f"✅ {len(preferences)} 项配置"
            else:
                print(f"  ❌ 通知偏好设置失败: {preferences_response.status_code}")
                notification_tests["偏好设置"] = f"❌ 失败({preferences_response.status_code})"
            
        except Exception as e:
            print(f"  ❌ 通知系统测试异常: {e}")
            notification_tests["系统状态"] = f"❌ 异常: {str(e)}"
        
        self.test_results["notification_system"] = notification_tests
        return notification_tests
    
    def test_email_system(self) -> Dict[str, str]:
        """测试邮件系统功能"""
        print("\n📧 测试邮件系统功能...")
        
        email_tests = {}
        
        try:
            # 测试邮件配置
            email_config_response = self.session.get(f"{API_BASE}/admin/email-config")
            if email_config_response.status_code == 200:
                email_config = email_config_response.json()
                print("  ✅ 邮件配置查询成功")
                email_tests["邮件配置"] = "✅ 配置正常"
            else:
                print(f"  ❌ 邮件配置查询失败: {email_config_response.status_code}")
                email_tests["邮件配置"] = f"❌ 失败({email_config_response.status_code})"
            
            # 测试创建邮件通知
            notification_data = {
                "type": "system_alert",
                "title": "项目进度测试通知",
                "content": "这是项目进度测试的系统通知",
                "priority": "normal",
                "channel": "email",
                "recipient_id": 1,
                "metadata": {
                    "test_source": "project_status_test",
                    "timestamp": datetime.now().isoformat()
                }
            }
            
            create_response = self.session.post(f"{API_BASE}/admin/notifications", 
                                              json=notification_data)
            if create_response.status_code == 201:
                notification = create_response.json()["data"]
                print(f"  ✅ 邮件通知创建成功 (ID: {notification['id']})")
                email_tests["通知创建"] = f"✅ 成功(ID: {notification['id']})"
            else:
                print(f"  ❌ 邮件通知创建失败: {create_response.status_code}")
                email_tests["通知创建"] = f"❌ 失败({create_response.status_code})"
            
            # 测试定时通知
            from datetime import timedelta
            future_time = datetime.now() + timedelta(seconds=10)
            scheduled_data = {
                "type": "system_maintenance",
                "title": "定时通知测试",
                "content": "这是定时通知功能测试",
                "priority": "normal",
                "channel": "email", 
                "recipient_id": 1,
                "scheduled_at": future_time.isoformat() + "Z",
                "metadata": {
                    "test_source": "scheduled_test"
                }
            }
            
            scheduled_response = self.session.post(f"{API_BASE}/admin/notifications",
                                                 json=scheduled_data)
            if scheduled_response.status_code == 201:
                scheduled_notification = scheduled_response.json()["data"]
                print(f"  ✅ 定时通知创建成功 (ID: {scheduled_notification['id']})")
                email_tests["定时通知"] = f"✅ 成功(ID: {scheduled_notification['id']})"
            else:
                print(f"  ❌ 定时通知创建失败: {scheduled_response.status_code}")
                email_tests["定时通知"] = f"❌ 失败({scheduled_response.status_code})"
            
        except Exception as e:
            print(f"  ❌ 邮件系统测试异常: {e}")
            email_tests["系统状态"] = f"❌ 异常: {str(e)}"
        
        self.test_results["notification_system"].update(email_tests)
        return email_tests
    
    def test_enhancement_features(self) -> Dict[str, str]:
        """测试功能增强特性"""
        print("\n🚀 测试功能增强特性...")
        
        enhancement_tests = {}
        
        try:
            # 测试示例数据(FE001)
            tickets_response = self.session.get(f"{API_BASE}/tickets")
            if tickets_response.status_code == 200:
                tickets = tickets_response.json()
                total_tickets = tickets.get("total", 0)
                print(f"  ✅ FE001-示例数据: {total_tickets} 个工单")
                enhancement_tests["FE001-示例数据"] = f"✅ {total_tickets} 个工单"
            else:
                enhancement_tests["FE001-示例数据"] = f"❌ 失败({tickets_response.status_code})"
            
            # 测试Webhook通知(FE002)
            webhooks_response = self.session.get(f"{API_BASE}/webhooks")
            if webhooks_response.status_code == 200:
                print("  ✅ FE002-Webhook通知系统可用")
                enhancement_tests["FE002-Webhook通知"] = "✅ 系统可用"
            else:
                enhancement_tests["FE002-Webhook通知"] = f"❌ 失败({webhooks_response.status_code})"
            
            # 测试用户个人中心(FE003)
            user_profile_response = self.session.get(f"{API_BASE}/user/profile")
            if user_profile_response.status_code == 200:
                print("  ✅ FE003-用户个人中心功能可用")
                enhancement_tests["FE003-用户个人中心"] = "✅ 功能可用"
            else:
                enhancement_tests["FE003-用户个人中心"] = f"❌ 失败({user_profile_response.status_code})"
            
            # 测试管理员用户管理(FE005)
            admin_users_response = self.session.get(f"{API_BASE}/admin/users")
            if admin_users_response.status_code == 200:
                users = admin_users_response.json()
                total_users = users.get("total", 0)
                print(f"  ✅ FE005-管理员用户管理: {total_users} 个用户")
                enhancement_tests["FE005-用户管理"] = f"✅ {total_users} 个用户"
            else:
                enhancement_tests["FE005-用户管理"] = f"❌ 失败({admin_users_response.status_code})"
            
            # 测试系统配置(FE006相关)
            system_configs_response = self.session.get(f"{API_BASE}/admin/system/configs")
            if system_configs_response.status_code == 200:
                print("  ✅ 系统配置管理功能可用")
                enhancement_tests["系统配置管理"] = "✅ 功能可用"
            else:
                enhancement_tests["系统配置管理"] = f"❌ 失败({system_configs_response.status_code})"
                
        except Exception as e:
            print(f"  ❌ 功能增强测试异常: {e}")
            enhancement_tests["系统状态"] = f"❌ 异常: {str(e)}"
        
        self.test_results["enhancement_features"] = enhancement_tests
        return enhancement_tests
    
    def generate_summary_report(self) -> Dict:
        """生成综合测试报告"""
        print("\n📊 生成项目状态综合报告...")
        
        # 计算各模块成功率
        def calculate_success_rate(tests: Dict[str, str]) -> float:
            if not tests:
                return 0.0
            total = len(tests)
            success = len([v for v in tests.values() if v.startswith("✅")])
            return (success / total) * 100
        
        # 统计所有测试结果
        all_tests = {}
        for category, tests in self.test_results.items():
            if isinstance(tests, dict):
                all_tests.update(tests)
        
        total_tests = len(all_tests)
        successful_tests = len([v for v in all_tests.values() if v.startswith("✅")])
        overall_success_rate = (successful_tests / total_tests) * 100 if total_tests > 0 else 0
        
        # 统计功能模块完成情况
        completed_features = []
        if calculate_success_rate(self.test_results.get("basic_features", {})) > 80:
            completed_features.append("✅ 基础功能模块")
        if calculate_success_rate(self.test_results.get("notification_system", {})) > 80:
            completed_features.append("✅ 通知系统模块")
        if calculate_success_rate(self.test_results.get("user_management", {})) > 80:
            completed_features.append("✅ 用户管理模块")
        if calculate_success_rate(self.test_results.get("enhancement_features", {})) > 60:
            completed_features.append("✅ 功能增强模块")
        
        # 生成报告
        report = {
            "测试时间": datetime.now().strftime("%Y-%m-%d %H:%M:%S"),
            "系统状态": "🟢 运行正常" if overall_success_rate > 80 else "🟡 部分功能异常" if overall_success_rate > 60 else "🔴 系统异常",
            "总体成功率": f"{overall_success_rate:.1f}%",
            "测试统计": {
                "总测试项": total_tests,
                "成功项": successful_tests,
                "失败项": total_tests - successful_tests
            },
            "模块成功率": {
                "基础功能": f"{calculate_success_rate(self.test_results.get('basic_features', {})):.1f}%",
                "通知系统": f"{calculate_success_rate(self.test_results.get('notification_system', {})):.1f}%",
                "API端点": f"{calculate_success_rate(self.test_results.get('api_endpoints', {})):.1f}%",
                "功能增强": f"{calculate_success_rate(self.test_results.get('enhancement_features', {})):.1f}%"
            },
            "已完成功能": completed_features,
            "详细结果": self.test_results
        }
        
        return report
    
    def run_comprehensive_test(self) -> Dict:
        """运行完整的项目状态测试"""
        print("🧪 开始项目状态综合测试")
        print("=" * 60)
        print(f"测试目标: {BASE_URL}")
        print(f"测试时间: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
        
        # 执行各项测试
        if not self.test_system_health():
            print("\n❌ 系统基础健康检查失败，停止测试")
            return self.generate_summary_report()
        
        self.test_core_apis()
        self.test_notification_system()
        self.test_email_system()
        self.test_enhancement_features()
        
        # 生成最终报告
        report = self.generate_summary_report()
        
        print("\n" + "=" * 60)
        print("📊 测试结果摘要")
        print("=" * 60)
        print(f"系统状态: {report['系统状态']}")
        print(f"总体成功率: {report['总体成功率']}")
        print(f"测试统计: {report['测试统计']['成功项']}/{report['测试统计']['总测试项']} 通过")
        
        print("\n📈 各模块成功率:")
        for module, rate in report['模块成功率'].items():
            print(f"  {module}: {rate}")
        
        print("\n✅ 已完成功能模块:")
        for feature in report['已完成功能']:
            print(f"  {feature}")
        
        if report['总体成功率'].replace('%', '') == '100.0':
            print("\n🎉 所有功能测试通过！系统状态优秀！")
            return_code = 0
        elif float(report['总体成功率'].replace('%', '')) > 80:
            print("\n✅ 大部分功能正常，系统状态良好")
            return_code = 0
        else:
            print("\n⚠️  系统存在问题，需要进一步调试")
            return_code = 1
        
        print(f"\n测试完成，退出码: {return_code}")
        
        return report

def main():
    """主函数"""
    tester = ProjectStatusTester()
    report = tester.run_comprehensive_test()
    
    # 保存报告到文件
    with open("project_status_report.json", "w", encoding="utf-8") as f:
        json.dump(report, f, ensure_ascii=False, indent=2)
    
    print(f"\n📄 详细报告已保存到: project_status_report.json")
    
    # 根据成功率决定退出码
    success_rate = float(report['总体成功率'].replace('%', ''))
    if success_rate > 80:
        sys.exit(0)
    else:
        sys.exit(1)

if __name__ == "__main__":
    main()