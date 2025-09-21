# 工单系统 API 接口文档

## 概述

本文档详细描述了工单系统的所有API接口，包括认证、工单管理、用户管理等功能模块。

**基础信息：**
- 基础URL: `http://localhost:8080`
- API版本: v1
- 认证方式: Bearer Token (JWT)
- 数据格式: JSON
- 字符编码: UTF-8

## 认证机制

### JWT Token 说明
- Access Token: 用于API访问，有效期较短
- Refresh Token: 用于刷新Access Token，有效期较长
- 在请求头中添加: `Authorization: Bearer <access_token>`

## 通用响应格式

### 成功响应
```json
{
  "success": true,
  "message": "操作成功",
  "data": {}
}
```

### 错误响应
```json
{
  "error": "error_code",
  "message": "错误描述",
  "code": "HTTP_STATUS_CODE"
}
```

### 分页响应
```json
{
  "data": [],
  "total": 100,
  "page": 1,
  "page_size": 20,
  "total_pages": 5
}
```

## 数据模型

### 用户模型 (User)
```json
{
  "id": 1,
  "username": "john_doe",
  "email": "john@example.com",
  "role": "user",
  "status": "active",
  "email_verified": true,
  "otp_enabled": false,
  "last_login_at": "2024-01-15T10:30:00Z",
  "created_at": "2024-01-01T00:00:00Z",
  "updated_at": "2024-01-15T10:30:00Z",
  "profile": {
    "first_name": "John",
    "last_name": "Doe",
    "phone_number": "+1234567890",
    "avatar": "https://example.com/avatar.jpg",
    "timezone": "UTC",
    "language": "en"
  }
}
```

### 工单模型 (Ticket)
```json
{
  "id": 1,
  "ticket_number": "TK-2024-001",
  "title": "系统登录问题",
  "description": "用户无法正常登录系统",
  "type": "incident",
  "priority": "high",
  "status": "open",
  "source": "web",
  "created_by_id": 1,
  "created_by": {
    "id": 1,
    "username": "reporter",
    "email": "reporter@example.com"
  },
  "assigned_to_id": 2,
  "assigned_to": {
    "id": 2,
    "username": "assignee",
    "email": "assignee@example.com"
  },
  "category_id": 1,
  "category": {
    "id": 1,
    "name": "技术支持",
    "description": "技术相关问题"
  },
  "subcategory_id": 1,
  "subcategory": {
    "id": 1,
    "name": "登录问题",
    "description": "用户登录相关问题"
  },
  "tags": ["login", "urgent"],
  "due_date": "2024-01-20T23:59:59Z",
  "resolved_at": null,
  "closed_at": null,
  "first_reply_at": null,
  "sla_breached": false,
  "sla_due_date": "2024-01-18T10:00:00Z",
  "response_time": null,
  "resolution_time": null,
  "customer_email": "customer@example.com",
  "customer_phone": "+1234567890",
  "customer_name": "Customer Name",
  "attachments": ["file1.pdf", "image1.jpg"],
  "custom_fields": {
    "department": "IT",
    "location": "Building A"
  },
  "view_count": 5,
  "comment_count": 3,
  "rating": 4,
  "rating_comment": "服务很好",
  "created_at": "2024-01-15T10:00:00Z",
  "updated_at": "2024-01-15T15:30:00Z"
}
```

### 评论模型 (Comment)
```json
{
  "id": 1,
  "ticket_id": 1,
  "user_id": 2,
  "user": {
    "id": 2,
    "username": "support_agent",
    "email": "agent@example.com"
  },
  "content": "我们正在调查这个问题",
  "content_type": "text",
  "comment_type": "public",
  "attachments": ["screenshot.png"],
  "is_internal": false,
  "time_spent": 30,
  "work_type": "investigation",
  "created_at": "2024-01-15T11:00:00Z",
  "updated_at": "2024-01-15T11:00:00Z"
}
```

## 认证接口

### 用户注册
**POST** `/api/auth/register`

**请求体：**
```json
{
  "username": "john_doe",
  "email": "john@example.com",
  "password": "password123",
  "confirm_password": "password123"
}
```

**响应：**
```json
{
  "success": true,
  "message": "注册成功，请检查邮箱验证OTP"
}
```

### 用户登录
**POST** `/api/auth/login`

**请求体：**
```json
{
  "email": "john@example.com",
  "password": "password123",
  "otp_code": "123456"
}
```

**响应：**
```json
{
  "user": {
    "id": 1,
    "username": "john_doe",
    "email": "john@example.com",
    "role": "user",
    "status": "active",
    "email_verified": true,
    "otp_enabled": false
  },
  "access_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "refresh_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "expires_in": 3600,
  "token_type": "Bearer"
}
```

### 刷新Token
**POST** `/api/auth/refresh`

**请求体：**
```json
{
  "refresh_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."
}
```

**响应：**
```json
{
  "user": {
    "id": 1,
    "username": "john_doe",
    "email": "john@example.com",
    "role": "user"
  },
  "access_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "refresh_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "expires_in": 3600,
  "token_type": "Bearer"
}
```

### 用户登出
**POST** `/api/auth/logout`

**请求头：** `Authorization: Bearer <access_token>`

**响应：**
```json
{
  "success": true,
  "message": "登出成功"
}
```

### 获取当前用户信息
**GET** `/api/auth/me`

**请求头：** `Authorization: Bearer <access_token>`

**响应：**
```json
{
  "success": true,
  "data": {
    "id": 1,
    "username": "john_doe",
    "email": "john@example.com",
    "role": "user",
    "status": "active",
    "email_verified": true,
    "profile": {
      "first_name": "John",
      "last_name": "Doe"
    }
  }
}
```

### 忘记密码
**POST** `/api/auth/forgot-password`

**请求体：**
```json
{
  "email": "john@example.com"
}
```

**响应：**
```json
{
  "success": true,
  "message": "密码重置邮件已发送"
}
```

### 重置密码
**POST** `/api/auth/reset-password`

**请求体：**
```json
{
  "token": "reset_token_here",
  "new_password": "newpassword123"
}
```

**响应：**
```json
{
  "success": true,
  "message": "密码重置成功"
}
```

### 验证邮箱
**POST** `/api/auth/verify-email`

**请求体：**
```json
{
  "token": "verification_token_here"
}
```

**响应：**
```json
{
  "success": true,
  "message": "邮箱验证成功"
}
```

### 重发验证邮件
**POST** `/api/auth/resend-verification`

**请求体：**
```json
{
  "email": "john@example.com"
}
```

**响应：**
```json
{
  "success": true,
  "message": "验证邮件已重新发送"
}
```

## 工单接口

### 获取工单列表
**GET** `/api/tickets`

**查询参数：**
- `page`: 页码 (默认: 1)
- `page_size`: 每页数量 (默认: 20)
- `status`: 工单状态 (open, in_progress, pending, resolved, closed, cancelled)
- `priority`: 优先级 (low, normal, high, urgent, critical)
- `assigned_to`: 分配给用户ID
- `created_by`: 创建者用户ID
- `search`: 搜索关键词
- `sort_by`: 排序字段 (默认: created_at)
- `sort_order`: 排序方向 (asc, desc, 默认: desc)

**示例请求：**
```
GET /api/tickets?page=1&page_size=10&status=open&priority=high&search=登录
```

**响应：**
```json
{
  "data": [
    {
      "id": 1,
      "ticket_number": "TK-2024-001",
      "title": "系统登录问题",
      "description": "用户无法正常登录系统",
      "type": "incident",
      "priority": "high",
      "status": "open",
      "created_by": {
        "id": 1,
        "username": "reporter",
        "email": "reporter@example.com"
      },
      "assigned_to": {
        "id": 2,
        "username": "assignee",
        "email": "assignee@example.com"
      },
      "created_at": "2024-01-15T10:00:00Z",
      "updated_at": "2024-01-15T15:30:00Z"
    }
  ],
  "total": 1,
  "page": 1,
  "page_size": 10,
  "total_pages": 1
}
```

### 获取工单详情
**GET** `/api/tickets/{id}`

**路径参数：**
- `id`: 工单ID

**响应：**
```json
{
  "success": true,
  "data": {
    "id": 1,
    "ticket_number": "TK-2024-001",
    "title": "系统登录问题",
    "description": "用户无法正常登录系统，尝试多次登录都失败",
    "type": "incident",
    "priority": "high",
    "status": "open",
    "source": "web",
    "created_by": {
      "id": 1,
      "username": "reporter",
      "email": "reporter@example.com"
    },
    "assigned_to": {
      "id": 2,
      "username": "assignee",
      "email": "assignee@example.com"
    },
    "category": {
      "id": 1,
      "name": "技术支持"
    },
    "tags": ["login", "urgent"],
    "due_date": "2024-01-20T23:59:59Z",
    "customer_email": "customer@example.com",
    "customer_name": "Customer Name",
    "attachments": ["file1.pdf"],
    "custom_fields": {
      "department": "IT"
    },
    "view_count": 5,
    "comment_count": 3,
    "created_at": "2024-01-15T10:00:00Z",
    "updated_at": "2024-01-15T15:30:00Z"
  }
}
```

### 创建工单
**POST** `/api/tickets`

**请求头：** `Authorization: Bearer <access_token>`

**请求体：**
```json
{
  "title": "系统登录问题",
  "description": "用户无法正常登录系统",
  "type": "incident",
  "priority": "high",
  "source": "web",
  "assigned_to_id": 2,
  "category_id": 1,
  "subcategory_id": 1,
  "tags": ["login", "urgent"],
  "due_date": "2024-01-20T23:59:59Z",
  "customer_email": "customer@example.com",
  "customer_phone": "+1234567890",
  "customer_name": "Customer Name",
  "attachments": ["file1.pdf"],
  "custom_fields": {
    "department": "IT",
    "location": "Building A"
  }
}
```

**响应：**
```json
{
  "success": true,
  "message": "工单创建成功",
  "data": {
    "id": 1,
    "ticket_number": "TK-2024-001",
    "title": "系统登录问题",
    "description": "用户无法正常登录系统",
    "type": "incident",
    "priority": "high",
    "status": "open",
    "source": "web",
    "created_by_id": 1,
    "assigned_to_id": 2,
    "created_at": "2024-01-15T10:00:00Z",
    "updated_at": "2024-01-15T10:00:00Z"
  }
}
```

### 更新工单
**PUT** `/api/tickets/{id}`

**请求头：** `Authorization: Bearer <access_token>`

**路径参数：**
- `id`: 工单ID

**请求体：**
```json
{
  "title": "系统登录问题 - 已更新",
  "description": "用户无法正常登录系统，已确认是服务器问题",
  "priority": "urgent",
  "status": "in_progress",
  "assigned_to_id": 3,
  "tags": ["login", "urgent", "server-issue"],
  "internal_notes": "已联系运维团队",
  "custom_fields": {
    "department": "IT",
    "escalated": true
  }
}
```

**响应：**
```json
{
  "success": true,
  "message": "工单更新成功",
  "data": {
    "id": 1,
    "ticket_number": "TK-2024-001",
    "title": "系统登录问题 - 已更新",
    "description": "用户无法正常登录系统，已确认是服务器问题",
    "priority": "urgent",
    "status": "in_progress",
    "assigned_to_id": 3,
    "updated_at": "2024-01-15T16:00:00Z"
  }
}
```

### 删除工单
**DELETE** `/api/tickets/{id}`

**请求头：** `Authorization: Bearer <access_token>`

**路径参数：**
- `id`: 工单ID

**响应：**
```json
{
  "success": true,
  "message": "工单删除成功"
}
```

### 分配工单
**POST** `/api/tickets/{id}/assign`

**请求头：** `Authorization: Bearer <access_token>`

**路径参数：**
- `id`: 工单ID

**请求体：**
```json
{
  "assigned_to_id": 3,
  "comment": "分配给技术专家处理"
}
```

**响应：**
```json
{
  "success": true,
  "message": "工单分配成功",
  "data": {
    "id": 1,
    "assigned_to_id": 3,
    "assigned_to": {
      "id": 3,
      "username": "expert",
      "email": "expert@example.com"
    },
    "updated_at": "2024-01-15T16:30:00Z"
  }
}
```

### 获取工单统计
**GET** `/api/tickets/stats`

**请求头：** `Authorization: Bearer <access_token>`

**查询参数：**
- `period`: 统计周期 (day, week, month, year)
- `start_date`: 开始日期 (YYYY-MM-DD)
- `end_date`: 结束日期 (YYYY-MM-DD)

**响应：**
```json
{
  "success": true,
  "data": {
    "total_tickets": 150,
    "open_tickets": 25,
    "in_progress_tickets": 30,
    "resolved_tickets": 80,
    "closed_tickets": 15,
    "overdue_tickets": 5,
    "avg_resolution_time": 24.5,
    "avg_response_time": 2.3,
    "satisfaction_rating": 4.2,
    "by_priority": {
      "low": 20,
      "normal": 60,
      "high": 50,
      "urgent": 15,
      "critical": 5
    },
    "by_category": {
      "技术支持": 80,
      "功能请求": 40,
      "Bug报告": 30
    }
  }
}
```

### 批量更新工单
**POST** `/api/tickets/bulk-update`

**请求头：** `Authorization: Bearer <access_token>`

**请求体：**
```json
{
  "ticket_ids": [1, 2, 3],
  "updates": {
    "status": "resolved",
    "assigned_to_id": 2,
    "priority": "normal"
  }
}
```

**响应：**
```json
{
  "success": true,
  "message": "批量更新成功",
  "data": {
    "updated_count": 3,
    "failed_count": 0,
    "updated_tickets": [1, 2, 3]
  }
}
```

## 评论接口

### 获取工单评论
**GET** `/api/tickets/{ticket_id}/comments`

**路径参数：**
- `ticket_id`: 工单ID

**查询参数：**
- `page`: 页码 (默认: 1)
- `page_size`: 每页数量 (默认: 20)
- `include_internal`: 是否包含内部评论 (true/false, 默认: false)

**响应：**
```json
{
  "data": [
    {
      "id": 1,
      "ticket_id": 1,
      "user": {
        "id": 2,
        "username": "support_agent",
        "email": "agent@example.com"
      },
      "content": "我们正在调查这个问题",
      "content_type": "text",
      "comment_type": "public",
      "attachments": [],
      "is_internal": false,
      "time_spent": 30,
      "created_at": "2024-01-15T11:00:00Z",
      "updated_at": "2024-01-15T11:00:00Z"
    }
  ],
  "total": 1,
  "page": 1,
  "page_size": 20,
  "total_pages": 1
}
```

### 添加工单评论
**POST** `/api/tickets/{ticket_id}/comments`

**请求头：** `Authorization: Bearer <access_token>`

**路径参数：**
- `ticket_id`: 工单ID

**请求体：**
```json
{
  "content": "问题已经解决，请确认",
  "content_type": "text",
  "comment_type": "public",
  "is_internal": false,
  "time_spent": 60,
  "work_type": "resolution",
  "attachments": ["solution.pdf"]
}
```

**响应：**
```json
{
  "success": true,
  "message": "评论添加成功",
  "data": {
    "id": 2,
    "ticket_id": 1,
    "user_id": 2,
    "content": "问题已经解决，请确认",
    "content_type": "text",
    "comment_type": "public",
    "is_internal": false,
    "time_spent": 60,
    "attachments": ["solution.pdf"],
    "created_at": "2024-01-15T12:00:00Z",
    "updated_at": "2024-01-15T12:00:00Z"
  }
}
```

## 用户管理接口

### 获取用户列表
**GET** `/api/users`

**请求头：** `Authorization: Bearer <access_token>`

**查询参数：**
- `page`: 页码 (默认: 1)
- `page_size`: 每页数量 (默认: 20)
- `role`: 用户角色 (admin, agent, user)
- `status`: 用户状态 (active, inactive, suspended)
- `search`: 搜索关键词

**响应：**
```json
{
  "data": [
    {
      "id": 1,
      "username": "john_doe",
      "email": "john@example.com",
      "role": "user",
      "status": "active",
      "email_verified": true,
      "last_login_at": "2024-01-15T10:30:00Z",
      "created_at": "2024-01-01T00:00:00Z"
    }
  ],
  "total": 1,
  "page": 1,
  "page_size": 20,
  "total_pages": 1
}
```

### 更新用户资料
**PUT** `/api/users/profile`

**请求头：** `Authorization: Bearer <access_token>`

**请求体：**
```json
{
  "first_name": "John",
  "last_name": "Doe",
  "phone_number": "+1234567890",
  "avatar": "https://example.com/avatar.jpg",
  "timezone": "UTC",
  "language": "en"
}
```

**响应：**
```json
{
  "success": true,
  "message": "用户资料更新成功",
  "data": {
    "id": 1,
    "username": "john_doe",
    "email": "john@example.com",
    "profile": {
      "first_name": "John",
      "last_name": "Doe",
      "phone_number": "+1234567890",
      "avatar": "https://example.com/avatar.jpg",
      "timezone": "UTC",
      "language": "en"
    }
  }
}
```

### 修改密码
**POST** `/api/users/change-password`

**请求头：** `Authorization: Bearer <access_token>`

**请求体：**
```json
{
  "current_password": "oldpassword123",
  "new_password": "newpassword123"
}
```

**响应：**
```json
{
  "success": true,
  "message": "密码修改成功"
}
```

## 枚举值说明

### 工单状态 (TicketStatus)
- `open`: 开放
- `in_progress`: 处理中
- `pending`: 等待中
- `resolved`: 已解决
- `closed`: 已关闭
- `cancelled`: 已取消

### 工单优先级 (TicketPriority)
- `low`: 低
- `normal`: 普通
- `high`: 高
- `urgent`: 紧急
- `critical`: 严重

### 工单类型 (TicketType)
- `incident`: 事件
- `request`: 请求
- `problem`: 问题
- `change`: 变更
- `complaint`: 投诉
- `consultation`: 咨询

### 工单来源 (TicketSource)
- `web`: 网页
- `email`: 邮件
- `phone`: 电话
- `chat`: 聊天
- `api`: API
- `mobile`: 移动端

### 用户角色 (UserRole)
- `admin`: 管理员
- `agent`: 客服代理
- `user`: 普通用户

### 用户状态 (UserStatus)
- `active`: 活跃
- `inactive`: 非活跃
- `suspended`: 暂停
- `locked`: 锁定

## 错误代码说明

### 认证相关错误
- `unauthorized`: 未授权访问
- `invalid_credentials`: 无效凭据
- `token_expired`: Token已过期
- `invalid_token`: 无效Token
- `account_locked`: 账户被锁定
- `email_not_verified`: 邮箱未验证
- `otp_required`: 需要OTP验证
- `invalid_otp`: 无效OTP

### 工单相关错误
- `ticket_not_found`: 工单不存在
- `invalid_ticket_id`: 无效工单ID
- `create_ticket_failed`: 工单创建失败
- `update_ticket_failed`: 工单更新失败
- `delete_ticket_failed`: 工单删除失败
- `assign_ticket_failed`: 工单分配失败
- `permission_denied`: 权限不足

### 通用错误
- `invalid_request`: 无效请求
- `validation_failed`: 验证失败
- `internal_server_error`: 服务器内部错误
- `rate_limit_exceeded`: 请求频率超限
- `resource_not_found`: 资源不存在

## 请求限制

### 频率限制
- 认证接口: 每分钟最多10次请求
- 工单接口: 每分钟最多100次请求
- 其他接口: 每分钟最多60次请求

### 数据限制
- 工单标题: 最大255字符
- 工单描述: 最大10000字符
- 评论内容: 最大5000字符
- 附件大小: 单个文件最大10MB
- 批量操作: 最多100个项目

## 示例代码

### JavaScript/TypeScript
```javascript
// 登录示例
const login = async (email, password) => {
  const response = await fetch('http://localhost:8080/api/auth/login', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ email, password }),
  });
  
  const data = await response.json();
  
  if (response.ok) {
    localStorage.setItem('token', data.access_token);
    localStorage.setItem('refreshToken', data.refresh_token);
    return data;
  } else {
    throw new Error(data.message);
  }
};

// 获取工单列表示例
const getTickets = async (params = {}) => {
  const token = localStorage.getItem('token');
  const queryString = new URLSearchParams(params).toString();
  
  const response = await fetch(`http://localhost:8080/api/tickets?${queryString}`, {
    headers: {
      'Authorization': `Bearer ${token}`,
      'Content-Type': 'application/json',
    },
  });
  
  if (response.ok) {
    return await response.json();
  } else {
    throw new Error('获取工单列表失败');
  }
};

// 创建工单示例
const createTicket = async (ticketData) => {
  const token = localStorage.getItem('token');
  
  const response = await fetch('http://localhost:8080/api/tickets', {
    method: 'POST',
    headers: {
      'Authorization': `Bearer ${token}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(ticketData),
  });
  
  if (response.ok) {
    return await response.json();
  } else {
    const error = await response.json();
    throw new Error(error.message);
  }
};
```

### Python
```python
import requests
import json

class TicketSystemAPI:
    def __init__(self, base_url="http://localhost:8080"):
        self.base_url = base_url
        self.token = None
    
    def login(self, email, password):
        """用户登录"""
        url = f"{self.base_url}/api/auth/login"
        data = {"email": email, "password": password}
        
        response = requests.post(url, json=data)
        
        if response.status_code == 200:
            result = response.json()
            self.token = result["access_token"]
            return result
        else:
            raise Exception(f"登录失败: {response.json()['message']}")
    
    def get_headers(self):
        """获取请求头"""
        return {
            "Authorization": f"Bearer {self.token}",
            "Content-Type": "application/json"
        }
    
    def get_tickets(self, **params):
        """获取工单列表"""
        url = f"{self.base_url}/api/tickets"
        
        response = requests.get(url, params=params, headers=self.get_headers())
        
        if response.status_code == 200:
            return response.json()
        else:
            raise Exception(f"获取工单失败: {response.json()['message']}")
    
    def create_ticket(self, ticket_data):
        """创建工单"""
        url = f"{self.base_url}/api/tickets"
        
        response = requests.post(url, json=ticket_data, headers=self.get_headers())
        
        if response.status_code == 201:
            return response.json()
        else:
            raise Exception(f"创建工单失败: {response.json()['message']}")

# 使用示例
api = TicketSystemAPI()
api.login("user@example.com", "password123")

# 获取工单列表
tickets = api.get_tickets(status="open", priority="high")
print(f"找到 {tickets['total']} 个工单")

# 创建新工单
new_ticket = {
    "title": "系统问题",
    "description": "系统运行缓慢",
    "type": "incident",
    "priority": "high",
    "source": "api"
}

result = api.create_ticket(new_ticket)
print(f"工单创建成功，ID: {result['data']['id']}")
```

## 更新日志

### v1.0.0 (2024-01-15)
- 初始版本发布
- 实现基础认证功能
- 实现工单CRUD操作
- 实现评论系统
- 实现用户管理

---

**注意事项：**
1. 所有时间字段均使用ISO 8601格式 (YYYY-MM-DDTHH:mm:ssZ)
2. 所有ID字段均为正整数
3. 分页从1开始计数
4. 请求体大小限制为1MB
5. API响应时间通常在100ms以内
6. 建议使用HTTPS协议进行生产环境部署
7. Token过期时间为1小时，请及时刷新
8. 所有敏感信息都不会在响应中返回

如有疑问或需要技术支持，请联系开发团队。