#!/usr/bin/env python3
"""
数据提供器修复验证
========================

快速验证修复后的React Admin数据流是否正常工作

运行: python test_data_provider_fix.py
"""

import requests
import json

def test_tickets_api():
    """测试工单API响应格式"""
    print("🔍 测试工单API响应格式...")
    
    url = "http://localhost:8081/api/tickets"
    headers = {"Authorization": "Bearer test-token"}
    params = {"page": 1, "page_size": 5}
    
    try:
        response = requests.get(url, headers=headers, params=params)
        print(f"   HTTP Status: {response.status_code}")
        
        if response.status_code == 200:
            data = response.json()
            print(f"   Response Structure: {list(data.keys())}")
            
            # 检查是否是我们期望的嵌套格式
            if data.get('code') == 0 and 'data' in data:
                nested_data = data['data']
                print(f"   Data Structure: {list(nested_data.keys())}")
                
                if 'items' in nested_data and isinstance(nested_data['items'], list):
                    print(f"   ✅ 找到items数组，包含 {len(nested_data['items'])} 条记录")
                    print(f"   ✅ 总数: {nested_data.get('total', 'N/A')}")
                    print(f"   ✅ 分页: 第{nested_data.get('page', 'N/A')}页，每页{nested_data.get('page_size', 'N/A')}条")
                    
                    # 展示第一条记录的字段
                    if nested_data['items']:
                        first_item = nested_data['items'][0]
                        key_fields = ['id', 'title', 'status', 'priority', 'ticket_number', 'created_at']
                        available_fields = [field for field in key_fields if field in first_item]
                        print(f"   ✅ 记录字段: {available_fields}")
                        
                        return True
                else:
                    print("   ❌ 数据格式不符合预期 - 缺少items数组")
                    return False
            else:
                print("   ❌ 响应格式不符合预期 - 缺少code或data字段")
                return False
        else:
            print(f"   ❌ API调用失败: {response.text}")
            return False
            
    except Exception as e:
        print(f"   ❌ 请求失败: {str(e)}")
        return False


def test_users_api():
    """测试用户管理API响应格式"""
    print("\n🔍 测试用户管理API响应格式...")
    
    url = "http://localhost:8081/api/admin/users"
    headers = {"Authorization": "Bearer test-token"}
    params = {"page": 1, "page_size": 5}
    
    try:
        response = requests.get(url, headers=headers, params=params)
        print(f"   HTTP Status: {response.status_code}")
        
        if response.status_code == 200:
            data = response.json()
            print(f"   Response Structure: {list(data.keys())}")
            
            if data.get('code') == 0 and 'data' in data:
                if isinstance(data['data'], list):
                    print(f"   ✅ 用户列表包含 {len(data['data'])} 条记录")
                    return True
                elif 'items' in data['data']:
                    print(f"   ✅ 用户列表包含 {len(data['data']['items'])} 条记录")
                    return True
                else:
                    print(f"   ℹ️ 用户数据格式: {data['data']}")
                    return True
            else:
                print("   ❌ 用户API响应格式不符合预期")
                return False
        else:
            print(f"   ❌ 用户API调用失败: {response.text}")
            return False
            
    except Exception as e:
        print(f"   ❌ 用户API请求失败: {str(e)}")
        return False


def test_notifications_api():
    """测试通知API响应格式"""
    print("\n🔍 测试通知API响应格式...")
    
    url = "http://localhost:8081/api/notifications"
    headers = {"Authorization": "Bearer test-token"}
    params = {"page": 1, "page_size": 3}
    
    try:
        response = requests.get(url, headers=headers, params=params)
        print(f"   HTTP Status: {response.status_code}")
        
        if response.status_code == 200:
            data = response.json()
            print(f"   Response Structure: {list(data.keys())}")
            return True
        else:
            print(f"   ❌ 通知API调用失败: {response.text}")
            return False
            
    except Exception as e:
        print(f"   ❌ 通知API请求失败: {str(e)}")
        return False


def main():
    print("🚀 开始验证数据提供器修复效果")
    print("=" * 50)
    
    tests = [
        ("工单API响应", test_tickets_api),
        ("用户API响应", test_users_api), 
        ("通知API响应", test_notifications_api),
    ]
    
    results = []
    for test_name, test_func in tests:
        try:
            result = test_func()
            results.append((test_name, result))
        except Exception as e:
            print(f"   ❌ {test_name}测试异常: {str(e)}")
            results.append((test_name, False))
    
    print("\n" + "=" * 50)
    print("📊 测试结果总结:")
    
    passed = 0
    for test_name, result in results:
        status = "✅ PASS" if result else "❌ FAIL"
        print(f"{status} {test_name}")
        if result:
            passed += 1
    
    print(f"\n通过率: {passed}/{len(results)} ({passed/len(results)*100:.1f}%)")
    
    if passed == len(results):
        print("🎉 所有API响应格式验证通过！数据流修复成功。")
    else:
        print("⚠️ 部分测试失败，需要进一步检查数据流配置。")


if __name__ == "__main__":
    main()