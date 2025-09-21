#!/usr/bin/env python3
"""
测试定时通知API的详细错误信息
"""

import requests
import json
from datetime import datetime, timedelta

# API配置
API_BASE = "http://localhost:8080/api"

def test_scheduled_notification():
    # 使用开发环境的测试token
    headers = {
        "Authorization": "Bearer test-token",
        "Content-Type": "application/json"
    }
    
    # 测试不同的时间格式
    future_time = datetime.now() + timedelta(seconds=5)
    
    test_formats = [
        future_time.isoformat(),  # 2025-08-22T00:11:00
        future_time.isoformat() + "Z",  # 2025-08-22T00:11:00Z
        future_time.strftime("%Y-%m-%dT%H:%M:%S.%fZ"),  # 2025-08-22T00:11:00.123456Z
        future_time.strftime("%Y-%m-%d %H:%M:%S"),  # 2025-08-22 00:11:00
    ]
    
    for i, time_format in enumerate(test_formats):
        print(f"\n🔄 测试格式 {i+1}: {time_format}")
        
        notification = {
            "type": "system_maintenance",
            "title": f"定时通知测试 - 格式 {i+1}",
            "content": f"测试时间格式: {time_format}",
            "priority": "normal",
            "channel": "email",
            "recipient_id": 1,
            "scheduled_at": time_format,
            "metadata": {
                "test_format": i+1,
                "original_format": time_format
            }
        }
        
        response = requests.post(f"{API_BASE}/admin/notifications", 
                               json=notification, headers=headers)
        
        print(f"   状态码: {response.status_code}")
        print(f"   响应: {response.text}")
        
        if response.status_code == 201:
            print("   ✅ 成功!")
            break
        else:
            print(f"   ❌ 失败")
    
    # 测试没有scheduled_at的情况
    print(f"\n🔄 测试无定时发送:")
    
    notification = {
        "type": "system_maintenance", 
        "title": "普通通知测试",
        "content": "这是一个普通通知，不设置定时发送",
        "priority": "normal",
        "channel": "email", 
        "recipient_id": 1,
        "metadata": {"test_type": "immediate"}
    }
    
    response = requests.post(f"{API_BASE}/admin/notifications",
                           json=notification, headers=headers)
    
    print(f"   状态码: {response.status_code}")
    print(f"   响应: {response.text}")
    
    if response.status_code == 201:
        print("   ✅ 普通通知创建成功!")
    else:
        print(f"   ❌ 普通通知创建失败")

if __name__ == "__main__":
    test_scheduled_notification()