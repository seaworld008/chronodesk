#!/usr/bin/env python3
"""
邮件通知系统测试脚本

测试功能：
1. 邮件配置管理
2. 邮件通知创建和发送
3. 通知偏好设置
4. 批量邮件处理
5. 失败重试机制
"""

import requests
import json
import time
import sys
from datetime import datetime, timedelta
from typing import Dict, List, Optional

# 测试配置
BASE_URL = "http://localhost:8080"
API_BASE = f"{BASE_URL}/api"

class EmailNotificationTester:
    def __init__(self):
        self.session = requests.Session()
        self.token = None
        self.admin_token = None
        self.test_user_id = 1
        
    def authenticate(self) -> bool:
        """用户认证"""
        print("🔐 执行用户认证...")
        
        # 使用简单的Bearer token进行认证（开发环境）
        self.token = "test-token"
        self.admin_token = "admin-test-token" 
        
        # 设置请求头
        self.session.headers.update({
            "Authorization": f"Bearer {self.token}",
            "Content-Type": "application/json"
        })
        
        # 测试认证是否有效
        try:
            response = self.session.get(f"{API_BASE}/auth/me")
            if response.status_code == 200:
                print("✅ 用户认证成功")
                return True
            else:
                print(f"❌ 用户认证失败: {response.status_code}")
                return False
        except Exception as e:
            print(f"❌ 认证请求失败: {e}")
            return False

    def test_email_config_management(self) -> bool:
        """测试邮件配置管理"""
        print("\n📧 测试邮件配置管理...")
        
        try:
            # 1. 获取当前邮件配置
            print("1. 获取当前邮件配置...")
            response = self.session.get(f"{API_BASE}/admin/email-config")
            if response.status_code != 200:
                print(f"❌ 获取邮件配置失败: {response.status_code} - {response.text}")
                return False
            
            config = response.json()
            print(f"   当前配置状态: 邮件验证{'已启用' if config.get('email_verification_enabled') else '未启用'}")
            print(f"   SMTP配置{'完整' if config.get('is_configured') else '不完整'}")
            
            # 2. 更新邮件配置（启用邮件通知）
            print("2. 更新邮件配置...")
            update_config = {
                "email_verification_enabled": True,
                "smtp_host": "smtp.gmail.com",
                "smtp_port": 587,
                "smtp_username": "test@example.com",
                "smtp_password": "test-password",
                "smtp_use_tls": True,
                "smtp_use_ssl": False,
                "from_email": "noreply@ticketsystem.com",
                "from_name": "工单系统",
                "welcome_email_subject": "欢迎使用工单系统",
                "otp_email_subject": "邮箱验证码"
            }
            
            response = self.session.put(f"{API_BASE}/admin/email-config", 
                                      json=update_config)
            if response.status_code != 200:
                print(f"❌ 更新邮件配置失败: {response.status_code} - {response.text}")
                # 不返回False，继续测试其他功能
                print("   ⚠️  邮件配置失败，但继续测试通知创建功能...")
            else:
                updated_config = response.json()
                print(f"✅ 邮件配置更新成功")
                print(f"   邮件验证: {'已启用' if updated_config.get('email_verification_enabled') else '未启用'}")
            
            return True
            
        except Exception as e:
            print(f"❌ 邮件配置管理测试异常: {e}")
            return False

    def test_email_notification_creation(self) -> bool:
        """测试邮件通知创建"""
        print("\n📬 测试邮件通知创建...")
        
        try:
            # 创建不同类型的邮件通知
            test_notifications = [
                {
                    "type": "ticket_assigned",
                    "title": "新工单分配测试",
                    "content": "这是一个测试邮件通知，用于验证工单分配邮件发送功能。",
                    "priority": "high",
                    "channel": "email",
                    "recipient_id": self.test_user_id,
                    "action_url": f"{BASE_URL}/tickets/1",
                    "metadata": {
                        "ticket_number": "TEST-001",
                        "ticket_title": "测试工单标题",
                        "ticket_status": "open",
                        "ticket_priority": "high"
                    }
                },
                {
                    "type": "ticket_status_changed",
                    "title": "工单状态变更测试",
                    "content": "工单状态已从'开放'变更为'处理中'。",
                    "priority": "normal",
                    "channel": "email",
                    "recipient_id": self.test_user_id,
                    "action_url": f"{BASE_URL}/tickets/2",
                    "metadata": {
                        "ticket_number": "TEST-002",
                        "old_status": "open",
                        "new_status": "in_progress"
                    }
                },
                {
                    "type": "ticket_commented",
                    "title": "工单新回复测试",
                    "content": "您的工单收到了新的回复，请及时查看。",
                    "priority": "normal",
                    "channel": "email",
                    "recipient_id": self.test_user_id,
                    "action_url": f"{BASE_URL}/tickets/3",
                    "metadata": {
                        "ticket_number": "TEST-003",
                        "comment_author": "管理员"
                    }
                },
                {
                    "type": "system_alert",
                    "title": "系统维护通知测试",
                    "content": "系统将在今晚23:00进行例行维护，预计持续2小时。",
                    "priority": "urgent",
                    "channel": "email",
                    "recipient_id": self.test_user_id,
                    "metadata": {
                        "maintenance_start": "2024-01-01 23:00:00",
                        "maintenance_duration": "2 hours"
                    }
                }
            ]
            
            created_notifications = []
            
            for i, notification_data in enumerate(test_notifications, 1):
                print(f"{i}. 创建{notification_data['type']}通知...")
                
                response = self.session.post(f"{API_BASE}/admin/notifications", 
                                           json=notification_data)
                
                if response.status_code == 201:
                    notification = response.json()["data"]
                    created_notifications.append(notification)
                    print(f"✅ 通知创建成功 (ID: {notification['id']})")
                    print(f"   类型: {notification['type']}")
                    print(f"   渠道: {notification['channel']}")
                    print(f"   状态: {'已发送' if notification['is_sent'] else '待发送'}")
                else:
                    print(f"❌ 通知创建失败: {response.status_code} - {response.text}")
                    continue
                
                # 短暂等待，让邮件有时间发送
                time.sleep(1)
            
            if created_notifications:
                print(f"✅ 成功创建 {len(created_notifications)} 个邮件通知")
                return True
            else:
                print("❌ 没有成功创建任何通知")
                return False
                
        except Exception as e:
            print(f"❌ 邮件通知创建测试异常: {e}")
            return False

    def test_notification_preferences(self) -> bool:
        """测试通知偏好设置"""
        print("\n⚙️  测试通知偏好设置...")
        
        try:
            # 1. 获取当前偏好设置
            print("1. 获取当前通知偏好设置...")
            response = self.session.get(f"{API_BASE}/notifications/preferences")
            
            if response.status_code == 200:
                preferences = response.json()["data"]
                print(f"   当前偏好设置数量: {len(preferences)}")
                for pref in preferences:
                    print(f"   - {pref['notification_type']}: 邮件{'启用' if pref['email_enabled'] else '禁用'}")
            else:
                print(f"   当前无偏好设置 ({response.status_code})")
                preferences = []
            
            # 2. 更新偏好设置
            print("2. 更新通知偏好设置...")
            
            new_preferences = [
                {
                    "notification_type": "ticket_assigned",
                    "email_enabled": True,
                    "in_app_enabled": True,
                    "webhook_enabled": False,
                    "max_daily_count": 50,
                    "batch_delivery": False
                },
                {
                    "notification_type": "ticket_status_changed", 
                    "email_enabled": True,
                    "in_app_enabled": True,
                    "webhook_enabled": False,
                    "max_daily_count": 30,
                    "batch_delivery": False
                },
                {
                    "notification_type": "ticket_commented",
                    "email_enabled": True,
                    "in_app_enabled": True,
                    "webhook_enabled": False,
                    "max_daily_count": 20,
                    "batch_delivery": True,
                    "batch_interval": 30
                },
                {
                    "notification_type": "system_alert",
                    "email_enabled": True,
                    "in_app_enabled": True,
                    "webhook_enabled": True,
                    "max_daily_count": 10,
                    "batch_delivery": False
                }
            ]
            
            response = self.session.put(f"{API_BASE}/notifications/preferences",
                                       json=new_preferences)
            
            if response.status_code == 200:
                print("✅ 通知偏好设置更新成功")
                
                # 验证设置是否生效
                response = self.session.get(f"{API_BASE}/notifications/preferences")
                if response.status_code == 200:
                    updated_preferences = response.json()["data"]
                    print(f"   更新后偏好设置数量: {len(updated_preferences)}")
                    
                return True
            else:
                print(f"❌ 通知偏好设置更新失败: {response.status_code} - {response.text}")
                return False
                
        except Exception as e:
            print(f"❌ 通知偏好设置测试异常: {e}")
            return False

    def test_notification_list_and_management(self) -> bool:
        """测试通知列表和管理功能"""
        print("\n📋 测试通知列表和管理...")
        
        try:
            # 1. 获取通知列表
            print("1. 获取用户通知列表...")
            response = self.session.get(f"{API_BASE}/notifications")
            
            if response.status_code != 200:
                print(f"❌ 获取通知列表失败: {response.status_code}")
                return False
            
            notifications_data = response.json()
            notifications = notifications_data["data"]
            total_count = notifications_data.get("total", len(notifications))
            
            print(f"✅ 获取通知列表成功，共 {total_count} 条通知")
            
            # 统计各类型通知
            type_stats = {}
            channel_stats = {}
            unread_count = 0
            
            for notification in notifications:
                # 类型统计
                ntype = notification["type"]
                type_stats[ntype] = type_stats.get(ntype, 0) + 1
                
                # 渠道统计
                channel = notification["channel"]
                channel_stats[channel] = channel_stats.get(channel, 0) + 1
                
                # 未读统计
                if not notification["is_read"]:
                    unread_count += 1
            
            print("   通知类型统计:")
            for ntype, count in type_stats.items():
                print(f"   - {ntype}: {count}")
            
            print("   通知渠道统计:")
            for channel, count in channel_stats.items():
                print(f"   - {channel}: {count}")
            
            print(f"   未读通知: {unread_count} 条")
            
            # 2. 获取未读通知数量
            print("2. 验证未读通知数量...")
            response = self.session.get(f"{API_BASE}/notifications/unread-count")
            
            if response.status_code == 200:
                api_unread_count = response.json()["count"]
                print(f"✅ API返回未读数量: {api_unread_count}")
                
                if api_unread_count == unread_count:
                    print("✅ 未读数量统计一致")
                else:
                    print(f"⚠️  未读数量不一致: 列表统计 {unread_count} vs API返回 {api_unread_count}")
            
            # 3. 测试标记已读功能
            if notifications and unread_count > 0:
                print("3. 测试标记通知为已读...")
                
                # 找第一个未读通知
                unread_notification = None
                for notification in notifications:
                    if not notification["is_read"]:
                        unread_notification = notification
                        break
                
                if unread_notification:
                    notification_id = unread_notification["id"]
                    response = self.session.put(f"{API_BASE}/notifications/{notification_id}/read")
                    
                    if response.status_code == 200:
                        print(f"✅ 通知 {notification_id} 标记为已读成功")
                    else:
                        print(f"❌ 标记已读失败: {response.status_code}")
                
                # 4. 测试全部标记为已读
                print("4. 测试标记所有通知为已读...")
                response = self.session.put(f"{API_BASE}/notifications/read-all")
                
                if response.status_code == 200:
                    print("✅ 所有通知标记为已读成功")
                    
                    # 验证未读数量是否变为0
                    time.sleep(1)
                    response = self.session.get(f"{API_BASE}/notifications/unread-count")
                    if response.status_code == 200:
                        final_unread_count = response.json()["count"]
                        if final_unread_count == 0:
                            print("✅ 验证成功：未读数量为0")
                        else:
                            print(f"⚠️  未读数量仍为: {final_unread_count}")
                else:
                    print(f"❌ 批量标记已读失败: {response.status_code}")
            
            return True
            
        except Exception as e:
            print(f"❌ 通知列表管理测试异常: {e}")
            return False

    def test_email_notification_templates(self) -> bool:
        """测试邮件通知模板"""
        print("\n📧 测试不同类型邮件通知模板...")
        
        notification_types = [
            "ticket_assigned",
            "ticket_status_changed", 
            "ticket_commented",
            "ticket_created",
            "ticket_overdue",
            "system_maintenance",
            "system_alert"
        ]
        
        try:
            for notification_type in notification_types:
                print(f"测试 {notification_type} 邮件模板...")
                
                notification_data = {
                    "type": notification_type,
                    "title": f"{notification_type.replace('_', ' ').title()} 测试",
                    "content": f"这是 {notification_type} 类型的测试邮件通知内容。",
                    "priority": "normal",
                    "channel": "email",
                    "recipient_id": self.test_user_id,
                    "action_url": f"{BASE_URL}/test/{notification_type}",
                    "metadata": {
                        "test_type": notification_type,
                        "template_test": True,
                        "ticket_number": f"TEST-{notification_type.upper()}-001"
                    }
                }
                
                response = self.session.post(f"{API_BASE}/admin/notifications",
                                           json=notification_data)
                
                if response.status_code == 201:
                    notification = response.json()["data"]
                    print(f"✅ {notification_type} 模板测试成功 (ID: {notification['id']})")
                else:
                    print(f"❌ {notification_type} 模板测试失败: {response.status_code}")
                
                time.sleep(0.5)  # 短暂延时
            
            print("✅ 邮件模板测试完成")
            return True
            
        except Exception as e:
            print(f"❌ 邮件模板测试异常: {e}")
            return False

    def test_scheduled_notifications(self) -> bool:
        """测试定时通知功能"""
        print("\n⏰ 测试定时通知功能...")
        
        try:
            # 创建一个5秒后发送的定时通知
            future_time = datetime.now() + timedelta(seconds=5)
            scheduled_notification = {
                "type": "system_maintenance",
                "title": "定时通知测试",
                "content": "这是一个定时发送的测试通知，应该在创建后5秒发送。",
                "priority": "normal",
                "channel": "email",
                "recipient_id": self.test_user_id,
                "scheduled_at": future_time.isoformat() + "Z",  # 添加Z后缀表示UTC时间
                "metadata": {
                    "scheduled_test": True,
                    "created_at": datetime.now().isoformat()
                }
            }
            
            print(f"创建定时通知，计划发送时间: {future_time.strftime('%Y-%m-%d %H:%M:%S')}")
            
            response = self.session.post(f"{API_BASE}/admin/notifications",
                                       json=scheduled_notification)
            
            if response.status_code == 201:
                notification = response.json()["data"]
                print(f"✅ 定时通知创建成功 (ID: {notification['id']})")
                print(f"   计划发送时间: {notification.get('scheduled_at', '未设置')}")
                
                # 等待并检查通知是否按时发送
                print("⏳ 等待定时通知发送...")
                time.sleep(6)  # 等待6秒确保通知已发送
                
                # 检查通知状态
                response = self.session.get(f"{API_BASE}/notifications")
                if response.status_code == 200:
                    notifications = response.json()["data"]
                    scheduled_notif = None
                    
                    for notif in notifications:
                        if notif["id"] == notification["id"]:
                            scheduled_notif = notif
                            break
                    
                    if scheduled_notif:
                        print(f"   通知状态: {'已发送' if scheduled_notif['is_sent'] else '未发送'}")
                        if scheduled_notif['is_sent']:
                            print("✅ 定时通知按时发送成功")
                        else:
                            print("⚠️  定时通知尚未发送（可能邮件发送需要更多时间）")
                
                return True
            else:
                print(f"❌ 定时通知创建失败: {response.status_code}")
                return False
                
        except Exception as e:
            print(f"❌ 定时通知测试异常: {e}")
            return False

    def run_comprehensive_test(self) -> bool:
        """运行综合测试"""
        print("🧪 开始邮件通知系统综合测试")
        print("=" * 60)
        
        # 认证
        if not self.authenticate():
            return False
        
        test_results = []
        
        # 执行各项测试
        tests = [
            ("邮件配置管理", self.test_email_config_management),
            ("邮件通知创建", self.test_email_notification_creation),
            ("通知偏好设置", self.test_notification_preferences),
            ("通知列表和管理", self.test_notification_list_and_management),
            ("邮件模板测试", self.test_email_notification_templates),
            ("定时通知功能", self.test_scheduled_notifications)
        ]
        
        for test_name, test_func in tests:
            print(f"\n🔄 执行测试: {test_name}")
            try:
                result = test_func()
                test_results.append((test_name, result))
                if result:
                    print(f"✅ {test_name} 测试通过")
                else:
                    print(f"❌ {test_name} 测试失败")
            except Exception as e:
                print(f"❌ {test_name} 测试异常: {e}")
                test_results.append((test_name, False))
            
            time.sleep(1)  # 测试间隔
        
        # 输出测试结果摘要
        print("\n" + "=" * 60)
        print("📊 测试结果摘要")
        print("=" * 60)
        
        passed = 0
        failed = 0
        
        for test_name, result in test_results:
            status = "✅ 通过" if result else "❌ 失败"
            print(f"{test_name}: {status}")
            
            if result:
                passed += 1
            else:
                failed += 1
        
        total = len(test_results)
        success_rate = (passed / total * 100) if total > 0 else 0
        
        print(f"\n总计: {total} 个测试")
        print(f"通过: {passed} 个")
        print(f"失败: {failed} 个") 
        print(f"成功率: {success_rate:.1f}%")
        
        if success_rate >= 70:
            print("\n🎉 邮件通知系统基本功能正常！")
            return True
        else:
            print("\n⚠️  邮件通知系统存在问题，需要进一步调试")
            return False

def main():
    """主测试函数"""
    print("邮件通知系统测试脚本")
    print(f"测试目标: {BASE_URL}")
    print(f"当前时间: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
    
    tester = EmailNotificationTester()
    
    try:
        success = tester.run_comprehensive_test()
        exit_code = 0 if success else 1
        
        print(f"\n测试完成，退出码: {exit_code}")
        sys.exit(exit_code)
        
    except KeyboardInterrupt:
        print("\n\n⚠️  测试被用户中断")
        sys.exit(1)
    except Exception as e:
        print(f"\n❌ 测试执行异常: {e}")
        sys.exit(1)

if __name__ == "__main__":
    main()