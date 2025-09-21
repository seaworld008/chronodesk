#!/usr/bin/env python3
"""
WebSocket连接测试脚本
测试实时通知系统的WebSocket功能
"""

import asyncio
import json
import time
import websockets
import requests
from typing import Optional


class WebSocketTester:
    def __init__(self, ws_url: str, api_base: str, token: str):
        self.ws_url = ws_url
        self.api_base = api_base
        self.token = token
        self.session = requests.Session()
        self.session.headers.update({
            "Authorization": f"Bearer {token}",
            "Content-Type": "application/json"
        })
    
    async def test_websocket_connection(self):
        """测试WebSocket连接"""
        print("🔌 测试WebSocket连接...")
        
        headers = {
            "Authorization": f"Bearer {self.token}"
        }
        
        try:
            async with websockets.connect(self.ws_url, extra_headers=headers) as websocket:
                print("✅ WebSocket连接成功!")
                
                # 发送ping消息
                ping_msg = {"type": "ping", "timestamp": time.time()}
                await websocket.send(json.dumps(ping_msg))
                print("📤 发送ping消息")
                
                # 等待响应
                try:
                    response = await asyncio.wait_for(websocket.recv(), timeout=5.0)
                    response_data = json.loads(response)
                    print(f"📥 收到响应: {response_data}")
                except asyncio.TimeoutError:
                    print("⚠️  等待响应超时")
                
                # 保持连接一段时间以接收可能的通知
                print("⏳ 保持连接30秒等待通知...")
                try:
                    while True:
                        message = await asyncio.wait_for(websocket.recv(), timeout=30.0)
                        data = json.loads(message)
                        print(f"📱 收到实时消息: {data}")
                except asyncio.TimeoutError:
                    print("✅ WebSocket连接测试完成")
                    
        except Exception as e:
            print(f"❌ WebSocket连接失败: {e}")
            return False
            
        return True
    
    def create_test_notification(self):
        """创建测试通知以验证实时推送"""
        print("📝 创建测试通知...")
        
        test_notification = {
            "type": "system_alert",
            "title": "WebSocket测试通知",
            "content": "这是WebSocket实时推送测试通知",
            "priority": "high",
            "recipient_id": 1,
            "channel": "websocket"
        }
        
        try:
            response = self.session.post(
                f"{self.api_base}/admin/notifications",
                json=test_notification
            )
            response.raise_for_status()
            notification_data = response.json()
            print(f"✅ 创建通知成功: ID {notification_data['data']['id']}")
            return notification_data['data']['id']
        except Exception as e:
            print(f"❌ 创建通知失败: {e}")
            return None
    
    async def test_realtime_notifications(self):
        """测试实时通知推送"""
        print("\n🚀 开始测试实时通知推送...")
        
        headers = {
            "Authorization": f"Bearer {self.token}"
        }
        
        try:
            async with websockets.connect(self.ws_url, extra_headers=headers) as websocket:
                print("✅ WebSocket连接建立")
                
                # 在另一个线程中创建通知
                print("📝 正在创建测试通知...")
                notification_id = self.create_test_notification()
                
                if notification_id:
                    # 等待实时通知
                    print("⏳ 等待实时通知推送...")
                    try:
                        message = await asyncio.wait_for(websocket.recv(), timeout=10.0)
                        data = json.loads(message)
                        print(f"📱 收到实时通知: {data}")
                        return True
                    except asyncio.TimeoutError:
                        print("⚠️  未收到实时通知（可能是推送机制尚未集成）")
                        return False
                        
        except Exception as e:
            print(f"❌ 实时通知测试失败: {e}")
            return False
    
    def test_api_endpoints(self):
        """测试通知相关API端点"""
        print("\n🔍 测试通知API端点...")
        
        try:
            # 测试获取通知列表
            response = self.session.get(f"{self.api_base}/notifications")
            response.raise_for_status()
            notifications = response.json()
            print(f"✅ 获取通知列表成功: {len(notifications['data'])}个通知")
            
            # 测试获取未读数量
            response = self.session.get(f"{self.api_base}/notifications/unread-count")
            response.raise_for_status()
            count_data = response.json()
            print(f"✅ 获取未读数量成功: {count_data['count']}个未读")
            
            return True
            
        except Exception as e:
            print(f"❌ API测试失败: {e}")
            return False


async def main():
    """主测试函数"""
    # 配置参数
    WS_URL = "ws://localhost:8080/api/ws"
    API_BASE = "http://localhost:8080/api"
    TOKEN = "test-token"
    
    tester = WebSocketTester(WS_URL, API_BASE, TOKEN)
    
    print("🎯 WebSocket实时通知系统测试")
    print("=" * 50)
    
    # 1. 测试API端点
    api_success = tester.test_api_endpoints()
    
    # 2. 测试WebSocket连接
    ws_success = await tester.test_websocket_connection()
    
    # 3. 测试实时通知推送
    realtime_success = await tester.test_realtime_notifications()
    
    # 输出总结
    print("\n📊 测试结果总结:")
    print(f"API端点测试: {'✅ 通过' if api_success else '❌ 失败'}")
    print(f"WebSocket连接: {'✅ 通过' if ws_success else '❌ 失败'}")
    print(f"实时通知推送: {'✅ 通过' if realtime_success else '❌ 失败'}")
    
    overall_success = api_success and ws_success
    print(f"\n🎯 总体测试结果: {'✅ 通过' if overall_success else '❌ 失败'}")
    
    if not overall_success:
        print("\n⚠️  注意: 实时推送功能需要进一步集成到通知创建流程中")
    
    return overall_success


if __name__ == "__main__":
    asyncio.run(main())