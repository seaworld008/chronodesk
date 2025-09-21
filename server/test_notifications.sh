#!/bin/bash

# 通知系统功能测试脚本

API_BASE="http://localhost:8080/api"
AUTH_TOKEN="test-token"

echo "=== 通知系统功能测试 ==="

# 1. 测试获取通知列表
echo "1. 测试获取通知列表..."
curl -s -H "Authorization: Bearer $AUTH_TOKEN" \
     -H "Content-Type: application/json" \
     "$API_BASE/notifications" | jq '.'

echo

# 2. 测试获取未读通知数量
echo "2. 测试获取未读通知数量..."
curl -s -H "Authorization: Bearer $AUTH_TOKEN" \
     -H "Content-Type: application/json" \
     "$API_BASE/notifications/unread-count" | jq '.'

echo

# 3. 测试创建通知（管理员）
echo "3. 测试创建通知（管理员）..."
curl -s -X POST \
     -H "Authorization: Bearer $AUTH_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{
       "type": "system_alert",
       "title": "测试通知",
       "content": "这是一个测试通知消息",
       "priority": "normal",
       "channel": "in_app",
       "recipient_id": 1,
       "action_url": "/test"
     }' \
     "$API_BASE/admin/notifications" | jq '.'

echo

# 4. 再次获取通知列表检查是否创建成功
echo "4. 再次获取通知列表..."
curl -s -H "Authorization: Bearer $AUTH_TOKEN" \
     -H "Content-Type: application/json" \
     "$API_BASE/notifications" | jq '.'

echo

# 5. 测试工单状态变更触发通知
echo "5. 测试工单状态变更触发通知..."
curl -s -X PUT \
     -H "Authorization: Bearer $AUTH_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{
       "status": "in_progress"
     }' \
     "$API_BASE/tickets/1" | jq '.'

echo

# 6. 检查通知是否自动创建
echo "6. 检查是否自动创建了状态变更通知..."
sleep 1
curl -s -H "Authorization: Bearer $AUTH_TOKEN" \
     -H "Content-Type: application/json" \
     "$API_BASE/notifications?limit=5&order_by=created_at&order_dir=desc" | jq '.'

echo
echo "=== 测试完成 ==="