# 工单管理系统 API 接口文档

**版本:** v1.0.0  
**基础URL:** `http://localhost:8081/api`  
**认证方式:** Bearer Token (JWT)

## 目录
- [认证接口](#认证接口)
- [工单管理接口](#工单管理接口)
- [工单工作流接口](#工单工作流接口)
- [用户管理接口](#用户管理接口)
- [分类管理接口](#分类管理接口)
- [通知管理接口](#通知管理接口)
- [系统管理接口](#系统管理接口)
- [审计日志接口](#审计日志接口)
- [文件上传接口](#文件上传接口)
- [WebSocket接口](#websocket接口)
- [数据模型](#数据模型)
- [错误码](#错误码)

---

## 认证接口

### 用户注册
```
POST /api/auth/register
Content-Type: application/json

Request Body:
{
  "username": "user123",
  "email": "user@example.com", 
  "password": "Password123!",
  "first_name": "张",
  "last_name": "三",
  "phone": "13800138000"
}

Response:
{
  "success": true,
  "data": {
    "user": {
      "id": 1,
      "username": "user123", 
      "email": "user@example.com",
      "first_name": "张",
      "last_name": "三",
      "role": "user",
      "status": "active",
      "created_at": "2025-01-01T12:00:00Z"
    },
    "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "expires_at": "2025-01-02T12:00:00Z"
  },
  "message": "用户注册成功"
}
```

### 用户登录
```
POST /api/auth/login
Content-Type: application/json

Request Body:
{
  "email": "user@example.com",
  "password": "Password123!",
  "otp_code": "123456",
  "device_token": "previous-trusted-device-token",
  "remember_device": true,
  "device_name": "Personal Mac"
}

说明：
- `otp_code` 可选，启用双因子认证时必须提供。
- `device_token` 可选，携带上一次返回的 `trusted_device_token` 可跳过 OTP。
- `remember_device` 为 true 时会生成新的 `trusted_device_token` 并返回给客户端。

Response:
{
  "code": 0,
  "msg": "Login successful",
  "data": {
    "user": {
      "id": 1,
      "username": "user123",
      "email": "user@example.com",
      "role": "user"
    },
    "access_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "refresh_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "expires_in": 900,
    "token_type": "Bearer",
    "trusted_device_token": "new-trusted-device-token"
  }
}

备注：`trusted_device_token` 仅在 `remember_device=true` 且登录成功时返回，客户端需妥善保存并在下次登录放入 `device_token` 字段。

```

### 刷新Token
```
POST /api/auth/refresh
Content-Type: application/json
Authorization: Bearer {refresh_token}

Response:
{
  "code": 0,
  "msg": "Refresh token successful",
  "data": {
    "access_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "refresh_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "expires_in": 900,
    "token_type": "Bearer"
  }
}
```

### 查询可信设备
```
GET /api/user/trusted-devices
Authorization: Bearer {access_token}

Response:
{
  "code": 0,
  "msg": "获取可信设备成功",
  "data": [
    {
      "id": 12,
      "device_name": "Personal Mac",
      "last_used_at": "2025-01-02T08:12:30Z",
      "last_ip": "203.0.113.5",
      "user_agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5)...",
      "expires_at": "2025-02-01T08:12:30Z",
      "revoked": false,
      "created_at": "2025-01-01T07:45:12Z",
      "updated_at": "2025-01-02T08:12:30Z"
    }
  ]
}
```

### 撤销可信设备
```
DELETE /api/user/trusted-devices/{id}
Authorization: Bearer {access_token}

Response:
{
  "code": 0,
  "msg": "已撤销可信设备",
  "data": null
}
```

备注：撤销后，原设备缓存的 `trusted_device_token` 会失效，需要重新通过 OTP 登录。

### 查询登录历史
```
GET /api/user/login-history?device_type=desktop&is_active=true
Authorization: Bearer {access_token}

Response:
{
  "code": 0,
  "msg": "获取登录历史成功",
  "data": {
    "items": [
      {
        "id": 21,
        "login_time": "2025-01-02T08:00:00Z",
        "ip_address": "203.0.113.5",
        "device_info": "desktop - macOS",
        "login_method": "password+trusted",
        "is_active": true
      }
    ],
    "pagination": {
      "page": 1,
      "page_size": 20,
      "total": 1
    }
  }
}
```

支持过滤参数：`ip_address`、`device_type`、`login_method`、`session_id`、`is_active`、`status`、`start_date`、`end_date`。

### 获取用户信息
```
GET /api/auth/me
Authorization: Bearer {token}

Response:
{
  "success": true,
  "data": {
    "id": 1,
    "username": "user123",
    "email": "user@example.com",
    "first_name": "张",
    "last_name": "三",
    "role": "user",
    "status": "active",
    "created_at": "2025-01-01T12:00:00Z",
    "updated_at": "2025-01-01T12:00:00Z"
  }
}
```

### 用户登出
```
POST /api/auth/logout
Authorization: Bearer {token}

Response:
{
  "success": true,
  "message": "登出成功"
}
```

---

## 工单管理接口

### 获取工单列表
```
GET /api/tickets
Authorization: Bearer {token}
Query Parameters:
- page: 页码 (默认: 1)
- limit: 每页数量 (默认: 20)
- status: 状态筛选 (open,in_progress,pending,resolved,closed)
- priority: 优先级筛选 (low,normal,high,urgent,critical) 
- type: 类型筛选 (incident,request,problem,change,complaint,consultation)
- assigned_to_id: 分配人ID
- created_by_id: 创建人ID
- category_id: 分类ID
- search: 搜索关键词
- sort: 排序字段 (created_at,updated_at,due_date,priority)
- order: 排序方向 (asc,desc)

Response:
{
  "success": true,
  "data": [
    {
      "id": 1,
      "ticket_number": "TK-2025-001",
      "title": "登录问题",
      "description": "无法登录系统",
      "status": "open",
      "priority": "high",
      "type": "incident",
      "source": "web",
      "created_by": {
        "id": 2,
        "username": "customer1",
        "first_name": "李",
        "last_name": "四"
      },
      "assigned_to": {
        "id": 3,
        "username": "agent1",
        "first_name": "王",
        "last_name": "五"
      },
      "category": {
        "id": 1,
        "name": "技术支持"
      },
      "customer_email": "customer@example.com",
      "customer_phone": "13900139000",
      "customer_name": "李四",
      "created_at": "2025-01-01T10:00:00Z",
      "updated_at": "2025-01-01T10:30:00Z",
      "due_date": "2025-01-02T10:00:00Z",
      "sla_due_date": "2025-01-01T14:00:00Z",
      "is_overdue": false,
      "is_escalated": false,
      "sla_breached": false
    }
  ],
  "pagination": {
    "page": 1,
    "limit": 20,
    "total": 150,
    "pages": 8
  }
}
```

### 获取单个工单
```
GET /api/tickets/{id}
Authorization: Bearer {token}

Response:
{
  "success": true,
  "data": {
    "id": 1,
    "ticket_number": "TK-2025-001",
    "title": "登录问题",
    "description": "用户反馈无法登录系统，提示用户名或密码错误",
    "status": "in_progress",
    "priority": "high",
    "type": "incident",
    "source": "web",
    "created_by": {
      "id": 2,
      "username": "customer1",
      "first_name": "李",
      "last_name": "四",
      "email": "customer@example.com"
    },
    "assigned_to": {
      "id": 3,
      "username": "agent1", 
      "first_name": "王",
      "last_name": "五",
      "email": "agent@example.com"
    },
    "category": {
      "id": 1,
      "name": "技术支持",
      "description": "技术相关问题"
    },
    "tags": ["登录", "认证", "紧急"],
    "custom_fields": {
      "affected_systems": ["主站", "移动端"],
      "reproduction_steps": "1. 打开登录页面\n2. 输入正确用户名密码\n3. 点击登录按钮"
    },
    "customer_email": "customer@example.com",
    "customer_phone": "13900139000",
    "customer_name": "李四",
    "created_at": "2025-01-01T10:00:00Z",
    "updated_at": "2025-01-01T10:30:00Z",
    "due_date": "2025-01-02T10:00:00Z",
    "sla_due_date": "2025-01-01T14:00:00Z",
    "resolved_at": null,
    "closed_at": null,
    "first_reply_at": "2025-01-01T10:15:00Z",
    "response_time": 15,
    "resolution_time": null,
    "view_count": 5,
    "comment_count": 3,
    "rating": null,
    "is_overdue": false,
    "is_escalated": false,
    "sla_breached": false,
    "comments": [
      {
        "id": 1,
        "content": "已开始调查该问题",
        "user": {
          "id": 3,
          "username": "agent1",
          "first_name": "王",
          "last_name": "五"
        },
        "created_at": "2025-01-01T10:15:00Z",
        "is_internal": false
      }
    ]
  }
}
```

### 创建工单
```
POST /api/tickets
Authorization: Bearer {token}
Content-Type: application/json

Request Body:
{
  "title": "系统无法访问",
  "description": "网站显示500错误，无法正常访问",
  "type": "incident",
  "priority": "urgent", 
  "source": "web",
  "category_id": 1,
  "assigned_to_id": 3,
  "customer_email": "user@example.com",
  "customer_phone": "13800138000",
  "customer_name": "张三",
  "tags": ["系统故障", "紧急"],
  "custom_fields": {
    "affected_url": "https://example.com/dashboard",
    "browser": "Chrome 91.0",
    "error_code": "500"
  },
  "due_date": "2025-01-02T18:00:00Z"
}

Response:
{
  "success": true,
  "data": {
    "id": 2,
    "ticket_number": "TK-2025-002", 
    "title": "系统无法访问",
    "status": "open",
    "priority": "urgent",
    "type": "incident",
    "created_by": {
      "id": 1,
      "username": "user123"
    },
    "assigned_to": {
      "id": 3,
      "username": "agent1"
    },
    "created_at": "2025-01-01T12:00:00Z"
  },
  "message": "工单创建成功"
}
```

### 更新工单
```
PUT /api/tickets/{id}
Authorization: Bearer {token}
Content-Type: application/json

Request Body:
{
  "title": "登录问题已修复",
  "status": "resolved",
  "priority": "normal",
  "assigned_to_id": 4,
  "internal_notes": "已重置用户密码并测试登录功能",
  "rating": 5,
  "rating_comment": "问题解决及时，服务态度良好"
}

Response:
{
  "success": true,
  "data": {
    "id": 1,
    "ticket_number": "TK-2025-001",
    "title": "登录问题已修复",
    "status": "resolved",
    "priority": "normal",
    "resolved_at": "2025-01-01T15:30:00Z",
    "resolution_time": 330,
    "updated_at": "2025-01-01T15:30:00Z"
  },
  "message": "工单更新成功"
}
```

### 删除工单
```
DELETE /api/tickets/{id}
Authorization: Bearer {token}

Response:
{
  "success": true,
  "message": "工单删除成功"
}
```

---

## 工单工作流接口

### 分配工单
```
POST /api/tickets/{id}/assign
Authorization: Bearer {token}
Content-Type: application/json

Request Body:
{
  "assigned_to_id": 123,
  "comment": "分配给技术支持处理"
}

Response:
{
  "success": true,
  "data": {
    "id": 1,
    "assigned_to_id": 123,
    "assigned_to": {
      "id": 123,
      "username": "tech_support",
      "first_name": "张",
      "last_name": "三"
    },
    "updated_at": "2025-01-01T14:30:00Z"
  },
  "message": "工单分配成功"
}
```

### 转移工单
```
POST /api/tickets/{id}/transfer
Authorization: Bearer {token}
Content-Type: application/json

Request Body:
{
  "assigned_to_id": 456,
  "department": "technical",
  "comment": "转移到技术部门处理",
  "transfer_reason": "需要技术专家支持"
}

Response:
{
  "success": true,
  "data": {
    "id": 1,
    "assigned_to_id": 456,
    "previous_assignee_id": 123,
    "updated_at": "2025-01-01T15:00:00Z"
  },
  "message": "工单转移成功"
}
```

### 升级工单
```
POST /api/tickets/{id}/escalate
Authorization: Bearer {token}
Content-Type: application/json

Request Body:
{
  "reason": "客户VIP，需要优先处理",
  "escalate_to_id": 789,
  "comment": "紧急处理"
}

Response:
{
  "success": true,
  "data": {
    "id": 1,
    "priority": "urgent",
    "is_escalated": true,
    "assigned_to": {
      "id": 789,
      "username": "supervisor",
      "role": "supervisor"
    },
    "updated_at": "2025-01-01T15:30:00Z"
  },
  "message": "工单升级成功"
}
```

### 更新工单状态
```
POST /api/tickets/{id}/status
Authorization: Bearer {token}
Content-Type: application/json

Request Body:
{
  "status": "resolved",
  "comment": "问题已解决",
  "resolution_notes": "重启服务器解决了问题"
}

Response:
{
  "success": true,
  "data": {
    "id": 1,
    "status": "resolved",
    "resolved_at": "2025-01-01T16:00:00Z",
    "resolution_time": 360,
    "updated_at": "2025-01-01T16:00:00Z"
  },
  "message": "状态更新成功"
}
```

### 获取工单历史
```
GET /api/tickets/{id}/history
Authorization: Bearer {token}

Response:
{
  "success": true,
  "data": [
    {
      "id": 1,
      "action": "assign",
      "description": "工单分配给 tech_support",
      "user": {
        "id": 456,
        "username": "supervisor"
      },
      "created_at": "2025-01-01T10:30:00Z",
      "field_changes": {
        "assigned_to_id": {
          "old": null,
          "new": 123
        }
      }
    }
  ],
  "total": 15
}
```

---

## 统计和仪表板接口

### 获取工单统计
```
GET /api/tickets/stats
Authorization: Bearer {token}

Response:
{
  "success": true,
  "data": {
    "total": 1500,
    "open": 45,
    "in_progress": 23,
    "pending": 12,
    "resolved": 890,
    "closed": 530,
    "overdue": 8,
    "sla_breached": 3,
    "my_tickets": 15,
    "unassigned": 12,
    "high_priority": 5,
    "escalated": 2,
    "by_priority": {
      "low": 200,
      "normal": 800,
      "high": 400,
      "urgent": 80,
      "critical": 20
    },
    "by_category": {
      "technical": 600,
      "billing": 300,
      "general": 400,
      "complaint": 200
    }
  }
}
```

### 获取我的工单
```
GET /api/tickets/my-tickets
Authorization: Bearer {token}
Query Parameters:
- limit: 数量限制 (默认: 10)
- status: 状态筛选 (open,in_progress)
- priority: 优先级筛选 (high,urgent)

Response:
{
  "success": true,
  "data": [
    {
      "id": 1,
      "ticket_number": "TK-2025-001",
      "title": "登录问题",
      "status": "in_progress", 
      "priority": "high",
      "customer_name": "张三",
      "created_at": "2025-01-01T10:00:00Z",
      "due_date": "2025-01-02T10:00:00Z",
      "is_overdue": false,
      "sla_breached": false
    }
  ],
  "total": 15
}
```

### 获取未分配工单
```
GET /api/tickets/unassigned
Authorization: Bearer {token}
Query Parameters:
- limit: 数量限制 (默认: 20)
- priority: 优先级筛选 (high,urgent)
- category_id: 分类ID

Response:
{
  "success": true,
  "data": [
    {
      "id": 2,
      "ticket_number": "TK-2025-002",
      "title": "系统故障",
      "priority": "urgent",
      "customer_name": "李四",
      "category": {
        "id": 1,
        "name": "技术支持"
      },
      "created_at": "2025-01-01T11:00:00Z",
      "auto_assign_user_id": 123
    }
  ],
  "total": 12
}
```

### 获取逾期工单
```
GET /api/tickets/overdue
Authorization: Bearer {token}

Response:
{
  "success": true,
  "data": [
    {
      "id": 3,
      "ticket_number": "TK-2025-003",
      "title": "网络连接问题",
      "status": "in_progress",
      "priority": "high",
      "assigned_to": {
        "id": 123,
        "username": "agent1"
      },
      "due_date": "2024-12-31T23:59:59Z",
      "overdue_hours": 12,
      "is_overdue": true
    }
  ],
  "total": 8
}
```

### 获取SLA违约工单
```
GET /api/tickets/sla-breach
Authorization: Bearer {token}

Response:
{
  "success": true,
  "data": [
    {
      "id": 4,
      "ticket_number": "TK-2025-004",
      "title": "支付问题",
      "status": "pending",
      "priority": "urgent",
      "sla_due_date": "2024-12-31T18:00:00Z",
      "sla_breached": true,
      "breach_hours": 6,
      "category": {
        "id": 2,
        "name": "账单问题",
        "sla_hours": 4
      }
    }
  ],
  "total": 3
}
```

---

## 批量操作接口

### 批量分配工单
```
POST /api/tickets/bulk-assign
Authorization: Bearer {token}
Content-Type: application/json

Request Body:
{
  "ticket_ids": [1, 2, 3, 4],
  "assigned_to_id": 123,
  "comment": "批量分配给技术支持"
}

Response:
{
  "success": true,
  "data": {
    "assigned_count": 4,
    "failed_count": 0,
    "assigned_tickets": [1, 2, 3, 4],
    "failed_tickets": []
  },
  "message": "批量分配完成"
}
```

### 批量状态更新
```
POST /api/tickets/bulk-status
Authorization: Bearer {token}
Content-Type: application/json

Request Body:
{
  "ticket_ids": [5, 6, 7],
  "status": "resolved",
  "comment": "批量解决"
}

Response:
{
  "success": true,
  "data": {
    "updated_count": 3,
    "failed_count": 0,
    "updated_tickets": [5, 6, 7],
    "failed_tickets": []
  },
  "message": "批量状态更新完成"
}
```

---

## 用户管理接口

### 获取用户列表
```
GET /api/admin/users
Authorization: Bearer {token}
Query Parameters:
- page: 页码 (默认: 1)
- limit: 每页数量 (默认: 20)
- role: 角色筛选 (user,agent,admin)
- status: 状态筛选 (active,inactive,suspended)
- search: 搜索关键词

Response:
{
  "success": true,
  "data": [
    {
      "id": 1,
      "username": "user123",
      "email": "user@example.com",
      "first_name": "张",
      "last_name": "三",
      "role": "user",
      "status": "active",
      "created_at": "2025-01-01T12:00:00Z",
      "last_login_at": "2025-01-01T15:30:00Z",
      "ticket_count": 5
    }
  ],
  "pagination": {
    "page": 1,
    "limit": 20,
    "total": 100,
    "pages": 5
  }
}
```

### 获取单个用户
```
GET /api/admin/users/{id}
Authorization: Bearer {token}

Response:
{
  "success": true,
  "data": {
    "id": 1,
    "username": "user123",
    "email": "user@example.com",
    "first_name": "张",
    "last_name": "三",
    "phone": "13800138000",
    "role": "user",
    "status": "active",
    "avatar_url": "https://example.com/avatars/user123.jpg",
    "created_at": "2025-01-01T12:00:00Z",
    "updated_at": "2025-01-01T12:00:00Z",
    "last_login_at": "2025-01-01T15:30:00Z",
    "login_count": 15,
    "ticket_stats": {
      "created": 10,
      "assigned": 5,
      "resolved": 8
    }
  }
}
```

### 创建用户
```
POST /api/admin/users
Authorization: Bearer {token}
Content-Type: application/json

Request Body:
{
  "username": "newuser",
  "email": "newuser@example.com",
  "password": "Password123!",
  "first_name": "新",
  "last_name": "用户",
  "phone": "13900139000",
  "role": "agent",
  "status": "active"
}

Response:
{
  "success": true,
  "data": {
    "id": 15,
    "username": "newuser",
    "email": "newuser@example.com",
    "role": "agent",
    "status": "active",
    "created_at": "2025-01-01T16:00:00Z"
  },
  "message": "用户创建成功"
}
```

---

## 分类管理接口

### 获取分类列表
```
GET /api/categories
Authorization: Bearer {token}

Response:
{
  "success": true,
  "data": [
    {
      "id": 1,
      "name": "技术支持",
      "description": "技术相关问题",
      "color": "#3b82f6",
      "icon": "tech-support",
      "sla_hours": 4,
      "auto_assign_user_id": 123,
      "is_active": true,
      "ticket_count": 150,
      "subcategories": [
        {
          "id": 10,
          "name": "系统故障",
          "description": "系统相关故障",
          "ticket_count": 80
        }
      ]
    }
  ]
}
```

### 创建分类
```
POST /api/categories
Authorization: Bearer {token}
Content-Type: application/json

Request Body:
{
  "name": "新分类",
  "description": "分类描述",
  "color": "#10b981",
  "icon": "category",
  "sla_hours": 2,
  "auto_assign_user_id": 456,
  "parent_id": null
}

Response:
{
  "success": true,
  "data": {
    "id": 5,
    "name": "新分类",
    "description": "分类描述",
    "color": "#10b981",
    "created_at": "2025-01-01T16:30:00Z"
  },
  "message": "分类创建成功"
}
```

---

## 通知管理接口

### 获取通知列表
```
GET /api/notifications
Authorization: Bearer {token}
Query Parameters:
- page: 页码 (默认: 1)
- limit: 每页数量 (默认: 20)
- unread: 仅未读 (true/false)

Response:
{
  "success": true,
  "data": [
    {
      "id": 1,
      "type": "ticket_assigned",
      "title": "工单已分配",
      "message": "工单 TK-2025-001 已分配给您",
      "data": {
        "ticket_id": 1,
        "ticket_number": "TK-2025-001"
      },
      "is_read": false,
      "created_at": "2025-01-01T14:00:00Z"
    }
  ],
  "pagination": {
    "page": 1,
    "limit": 20,
    "total": 50,
    "pages": 3
  }
}
```

### 标记通知为已读
```
PUT /api/notifications/{id}/read
Authorization: Bearer {token}

Response:
{
  "success": true,
  "message": "通知已标记为已读"
}
```

### 标记所有通知为已读
```
PUT /api/notifications/read-all
Authorization: Bearer {token}

Response:
{
  "success": true,
  "message": "所有通知已标记为已读"
}
```

### 获取未读通知数量
```
GET /api/notifications/unread-count
Authorization: Bearer {token}

Response:
{
  "success": true,
  "data": {
    "count": 5
  }
}
```

---

## 系统管理接口

### 获取系统配置
```
GET /api/admin/configs
Authorization: Bearer {token}

Response:
{
  "success": true,
  "data": [
    {
      "key": "site_name",
      "value": "工单管理系统",
      "description": "网站名称",
      "type": "string",
      "is_public": true
    },
    {
      "key": "max_file_size",
      "value": "10485760",
      "description": "文件上传最大大小（字节）",
      "type": "integer",
      "is_public": false
    }
  ]
}
```

### 更新系统配置
```
PUT /api/admin/configs/{key}
Authorization: Bearer {token}
Content-Type: application/json

Request Body:
{
  "value": "新的工单管理系统",
  "description": "网站名称"
}

Response:
{
  "success": true,
  "data": {
    "key": "site_name",
    "value": "新的工单管理系统",
    "updated_at": "2025-01-01T17:00:00Z"
  },
  "message": "配置更新成功"
}
```

### 获取邮件配置
```
GET /api/admin/email-config
Authorization: Bearer {token}

Response:
{
  "success": true,
  "data": {
    "smtp_host": "smtp.gmail.com",
    "smtp_port": 587,
    "smtp_username": "system@example.com",
    "smtp_from": "noreply@example.com",
    "smtp_from_name": "工单系统",
    "is_enabled": true,
    "last_test_at": "2025-01-01T10:00:00Z",
    "test_status": "success"
  }
}
```

### 测试邮件连接
```
POST /api/admin/email-config/test
Authorization: Bearer {token}
Content-Type: application/json

Request Body:
{
  "test_email": "test@example.com"
}

Response:
{
  "success": true,
  "data": {
    "status": "success",
    "message": "邮件发送成功",
    "test_time": "2025-01-01T17:30:00Z"
  },
  "message": "邮件测试完成"
}
```

---

## 审计日志接口

### 获取管理员操作日志
```
GET /api/admin/audit-logs
Authorization: Bearer {token}

Query Parameters (optional):
- page: 页码，默认 1
- limit: 每页条数，默认 20，最大 200
- user_id: 按执行人 ID 过滤
- role: 按执行人角色过滤
- method: HTTP Method，例：POST
- status: 响应状态码
- keyword: 模糊搜索用户名 / 路径 / 动作
- start_time / end_time: 时间范围，支持 RFC3339 或 2006-01-02 格式

Response:
{
  "code": 0,
  "msg": "获取审计日志成功",
  "data": {
    "items": [
      {
        "id": 42,
        "created_at": "2025-09-25T12:34:56Z",
        "user_id": 1,
        "username": "superadmin",
        "role": "admin",
        "action": "POST /api/admin/users",
        "method": "POST",
        "path": "/api/admin/users",
        "status_code": 201,
        "client_ip": "127.0.0.1",
        "latency_ms": 45,
        "result": "success"
      }
    ],
    "total": 58,
    "page": 1,
    "limit": 20
  }
}
```

## 文件上传接口

### 上传文件
```
POST /api/upload
Authorization: Bearer {token}
Content-Type: multipart/form-data

Form Data:
- file: 文件内容
- type: 文件类型 (avatar,attachment,document)
- ticket_id: 关联工单ID (可选)

Response:
{
  "success": true,
  "data": {
    "id": 1,
    "filename": "document.pdf",
    "original_name": "用户手册.pdf",
    "file_size": 1024000,
    "mime_type": "application/pdf",
    "file_path": "/uploads/2025/01/document.pdf",
    "url": "http://localhost:8081/uploads/2025/01/document.pdf",
    "uploaded_at": "2025-01-01T18:00:00Z"
  },
  "message": "文件上传成功"
}
```

---

## WebSocket接口

### 连接WebSocket
```
WebSocket URL: ws://localhost:8081/api/ws
Authorization: Bearer {token} (通过查询参数或头部传递)

连接成功后会收到:
{
  "type": "connected",
  "data": {
    "user_id": 1,
    "connection_id": "conn_123456"
  }
}
```

### 实时通知消息
```json
{
  "type": "notification",
  "data": {
    "id": 1,
    "type": "ticket_assigned",
    "title": "工单已分配",
    "message": "工单 TK-2025-001 已分配给您",
    "ticket_id": 1,
    "created_at": "2025-01-01T18:30:00Z"
  }
}
```

### 工单状态更新通知
```json
{
  "type": "ticket_updated",
  "data": {
    "ticket_id": 1,
    "ticket_number": "TK-2025-001",
    "status": "resolved",
    "updated_by": {
      "id": 3,
      "username": "agent1"
    },
    "updated_at": "2025-01-01T18:45:00Z"
  }
}
```

---

## 数据模型

### 工单模型 (Ticket)
```json
{
  "id": "integer, 工单ID",
  "ticket_number": "string, 工单编号",
  "title": "string, 工单标题",
  "description": "string, 工单描述",
  "status": "string, 状态 (open|in_progress|pending|resolved|closed|cancelled)",
  "priority": "string, 优先级 (low|normal|high|urgent|critical)",
  "type": "string, 类型 (incident|request|problem|change|complaint|consultation)",
  "source": "string, 来源 (web|email|phone|chat|api|mobile)",
  "created_by_id": "integer, 创建人ID",
  "assigned_to_id": "integer, 分配人ID",
  "category_id": "integer, 分类ID",
  "customer_email": "string, 客户邮箱",
  "customer_phone": "string, 客户电话",
  "customer_name": "string, 客户姓名",
  "tags": "array, 标签列表",
  "custom_fields": "object, 自定义字段",
  "due_date": "datetime, 截止时间",
  "sla_due_date": "datetime, SLA截止时间",
  "resolved_at": "datetime, 解决时间",
  "closed_at": "datetime, 关闭时间",
  "first_reply_at": "datetime, 首次回复时间",
  "response_time": "integer, 响应时间(分钟)",
  "resolution_time": "integer, 解决时间(分钟)",
  "view_count": "integer, 查看次数",
  "comment_count": "integer, 评论数量",
  "rating": "integer, 客户评分(1-5)",
  "rating_comment": "string, 评分备注",
  "is_overdue": "boolean, 是否逾期",
  "is_escalated": "boolean, 是否已升级",
  "sla_breached": "boolean, SLA是否违约",
  "created_at": "datetime, 创建时间",
  "updated_at": "datetime, 更新时间"
}
```

### 用户模型 (User)
```json
{
  "id": "integer, 用户ID",
  "username": "string, 用户名",
  "email": "string, 邮箱",
  "first_name": "string, 名",
  "last_name": "string, 姓",
  "phone": "string, 电话",
  "avatar_url": "string, 头像URL",
  "role": "string, 角色 (user|agent|supervisor|admin)",
  "status": "string, 状态 (active|inactive|suspended)",
  "last_login_at": "datetime, 最后登录时间",
  "created_at": "datetime, 创建时间",
  "updated_at": "datetime, 更新时间"
}
```

### 分类模型 (Category)
```json
{
  "id": "integer, 分类ID",
  "name": "string, 分类名称",
  "description": "string, 分类描述",
  "color": "string, 颜色代码",
  "icon": "string, 图标名称",
  "parent_id": "integer, 父分类ID",
  "sla_hours": "integer, SLA时长(小时)",
  "auto_assign_user_id": "integer, 自动分配用户ID",
  "is_active": "boolean, 是否激活",
  "sort_order": "integer, 排序",
  "created_at": "datetime, 创建时间",
  "updated_at": "datetime, 更新时间"
}
```

---

## 错误码

### HTTP状态码
- `200` - 请求成功
- `201` - 创建成功
- `400` - 请求参数错误
- `401` - 未认证/Token无效
- `403` - 无权限
- `404` - 资源不存在
- `409` - 资源冲突
- `422` - 数据验证失败
- `429` - 请求频率限制
- `500` - 服务器内部错误

### 业务错误码
```json
{
  "success": false,
  "error_code": "TICKET_NOT_FOUND",
  "message": "工单不存在",
  "details": {
    "ticket_id": 999
  }
}
```

常见错误码:
- `INVALID_CREDENTIALS` - 用户名或密码错误
- `TOKEN_EXPIRED` - Token已过期
- `INSUFFICIENT_PERMISSIONS` - 权限不足
- `TICKET_NOT_FOUND` - 工单不存在
- `USER_NOT_FOUND` - 用户不存在
- `CATEGORY_NOT_FOUND` - 分类不存在
- `INVALID_STATUS_TRANSITION` - 无效的状态转换
- `ASSIGNMENT_FAILED` - 分配失败
- `FILE_TOO_LARGE` - 文件过大
- `UNSUPPORTED_FILE_TYPE` - 不支持的文件类型
- `RATE_LIMIT_EXCEEDED` - 请求频率超限
- `VALIDATION_FAILED` - 数据验证失败

---

## 认证说明

所有需要认证的接口都需要在请求头中包含JWT Token:

```
Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...
```

Token有效期为24小时，过期后需要使用refresh_token刷新或重新登录。

## 权限说明

系统支持基于角色的权限控制 (RBAC):

- **user** - 普通用户，可创建和查看自己的工单
- **agent** - 客服代理，可处理分配的工单
- **supervisor** - 主管，可管理团队工单和用户
- **admin** - 管理员，拥有所有权限

权限验证在每个接口中进行，无权限时返回403错误。

## 分页说明

列表接口统一使用分页参数:
- `page` - 页码，从1开始
- `limit` - 每页数量，默认20，最大100

响应格式:
```json
{
  "data": [...],
  "pagination": {
    "page": 1,
    "limit": 20,
    "total": 100,
    "pages": 5
  }
}
```

## 排序说明

支持排序的接口可使用以下参数:
- `sort` - 排序字段
- `order` - 排序方向 (asc/desc)

例: `GET /api/tickets?sort=created_at&order=desc`

## 搜索说明

搜索功能支持多字段模糊匹配，一般搜索工单标题、描述、客户信息等相关字段。

## 版本说明

当前API版本为 v1.0.0，所有接口URL以 `/api` 开头。未来版本更新时会保持向后兼容。
