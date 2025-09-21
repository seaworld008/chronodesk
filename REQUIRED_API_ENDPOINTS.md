# 工单系统所需的后端 API 接口

本文档列出了完整工单管理系统所需的后端 API 接口，包括工单流转、指派、升级等核心功能。

## 🔄 工单工作流接口

### 1. 工单指派
```
POST /api/tickets/{id}/assign
Content-Type: application/json
Authorization: Bearer {token}

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
        }
    },
    "message": "工单分配成功"
}
```

### 2. 工单转移
```
POST /api/tickets/{id}/transfer
Content-Type: application/json
Authorization: Bearer {token}

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
        "transfer_history": [...]
    },
    "message": "工单转移成功"
}
```

### 3. 工单升级
```
POST /api/tickets/{id}/escalate
Content-Type: application/json
Authorization: Bearer {token}

Request Body:
{
    "reason": "客户VIP，需要优先处理",
    "escalate_to_id": 789, // 升级到的上级ID
    "comment": "紧急处理"
}

Response:
{
    "success": true,
    "data": {
        "id": 1,
        "priority": "urgent", // 自动提升优先级
        "escalated": true,
        "escalated_to": {
            "id": 789,
            "username": "supervisor",
            "role": "supervisor"
        },
        "escalation_history": [...]
    },
    "message": "工单升级成功"
}
```

### 4. 状态变更
```
POST /api/tickets/{id}/status
Content-Type: application/json
Authorization: Bearer {token}

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
        "resolved_at": "2025-01-01T12:00:00Z",
        "resolution_time": 120 // 分钟
    },
    "message": "状态更新成功"
}
```

## 📊 仪表板统计接口

### 1. 工单统计
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
        "my_tickets": 15, // 当前用户的工单（如果是代理商）
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

### 2. 我的工单（代理商视图）
```
GET /api/tickets/my-tickets
Authorization: Bearer {token}
Query Parameters:
- limit: 10 (可选)
- status: open,in_progress (可选)
- priority: high,urgent (可选)

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

### 3. 未分配工单
```
GET /api/tickets/unassigned
Authorization: Bearer {token}
Query Parameters:
- limit: 20 (可选)
- priority: high,urgent (可选)
- category_id: 1 (可选)

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
            "auto_assign_user_id": 123 // 来自分类的自动分配规则
        }
    ],
    "total": 12
}
```

### 4. 逾期工单
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

### 5. SLA违约工单
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

## 🔄 批量操作接口

### 1. 批量分配
```
POST /api/tickets/bulk-assign
Content-Type: application/json
Authorization: Bearer {token}

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

### 2. 批量状态更新
```
POST /api/tickets/bulk-status
Content-Type: application/json
Authorization: Bearer {token}

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
        "failed_count": 0
    },
    "message": "批量状态更新完成"
}
```

## 📈 自动化和规则接口

### 1. 自动分配规则
```
POST /api/automation/auto-assign
Content-Type: application/json
Authorization: Bearer {token}

Request Body:
{
    "category_id": 1,
    "conditions": {
        "priority": ["high", "urgent"],
        "keywords": ["服务器", "网络", "系统"]
    },
    "actions": {
        "assign_to_id": 123,
        "set_priority": "high",
        "add_tags": ["technical", "urgent"]
    }
}
```

### 2. SLA配置接口
```
GET /api/categories/{id}/sla
Authorization: Bearer {token}

Response:
{
    "success": true,
    "data": {
        "category_id": 1,
        "sla_hours": 4,
        "response_time_hours": 1,
        "escalation_rules": [
            {
                "condition": "overdue_1h",
                "action": "notify_supervisor"
            },
            {
                "condition": "overdue_2h", 
                "action": "escalate_priority"
            }
        ]
    }
}
```

## 📝 工单历史和审计接口

### 1. 工单历史
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

### 2. 操作审计日志
```
GET /api/audit/tickets
Authorization: Bearer {token}
Query Parameters:
- user_id: 123 (可选)
- action: assign,transfer,escalate (可选)
- date_from: 2025-01-01 (可选)
- date_to: 2025-01-31 (可选)

Response:
{
    "success": true,
    "data": [
        {
            "id": 1,
            "ticket_id": 10,
            "user_id": 123,
            "action": "escalate",
            "details": {
                "reason": "VIP客户需要优先处理",
                "old_priority": "normal",
                "new_priority": "urgent"
            },
            "ip_address": "192.168.1.100",
            "user_agent": "Mozilla/5.0...",
            "created_at": "2025-01-01T14:20:00Z"
        }
    ],
    "total": 50
}
```

## 🛠 实现建议

### 1. 后端控制器结构
```go
// internal/handlers/ticket_workflow_handler.go
type TicketWorkflowHandler struct {
    ticketService services.TicketWorkflowServiceInterface
    response      *middleware.ResponseHelper
}

func (h *TicketWorkflowHandler) AssignTicket(c *gin.Context)
func (h *TicketWorkflowHandler) TransferTicket(c *gin.Context)  
func (h *TicketWorkflowHandler) EscalateTicket(c *gin.Context)
func (h *TicketWorkflowHandler) UpdateTicketStatus(c *gin.Context)
func (h *TicketWorkflowHandler) GetTicketStats(c *gin.Context)
func (h *TicketWorkflowHandler) GetUnassignedTickets(c *gin.Context)
func (h *TicketWorkflowHandler) GetOverdueTickets(c *gin.Context)
func (h *TicketWorkflowHandler) BulkAssignTickets(c *gin.Context)
```

### 2. 服务层接口
```go
// internal/services/ticket_workflow_service.go
type TicketWorkflowServiceInterface interface {
    AssignTicket(ctx context.Context, ticketID uint, assigneeID uint, comment string) error
    TransferTicket(ctx context.Context, ticketID uint, req TransferRequest) error
    EscalateTicket(ctx context.Context, ticketID uint, req EscalationRequest) error
    UpdateStatus(ctx context.Context, ticketID uint, status models.TicketStatus, comment string) error
    GetStatistics(ctx context.Context, userID uint, role string) (*TicketStatistics, error)
    GetUnassignedTickets(ctx context.Context, filters TicketFilters) ([]models.Ticket, int64, error)
    BulkAssign(ctx context.Context, ticketIDs []uint, assigneeID uint) error
}
```

### 3. 路由注册
```go
// main.go 或路由文件中
workflowGroup := api.Group("/tickets")
{
    workflowGroup.POST("/:id/assign", workflowHandler.AssignTicket)
    workflowGroup.POST("/:id/transfer", workflowHandler.TransferTicket)
    workflowGroup.POST("/:id/escalate", workflowHandler.EscalateTicket)
    workflowGroup.POST("/:id/status", workflowHandler.UpdateTicketStatus)
    workflowGroup.GET("/stats", workflowHandler.GetTicketStats)
    workflowGroup.GET("/unassigned", workflowHandler.GetUnassignedTickets)
    workflowGroup.GET("/overdue", workflowHandler.GetOverdueTickets)
    workflowGroup.GET("/sla-breach", workflowHandler.GetSLABreachedTickets)
    workflowGroup.POST("/bulk-assign", workflowHandler.BulkAssignTickets)
}
```

### 4. 权限验证建议
- 代理商只能查看和操作分配给自己的工单
- 主管可以查看团队的工单并进行分配操作
- 管理员拥有所有权限
- 工单创建者始终可以查看自己创建的工单

这些接口实现后，React Admin 前端的工作流功能将完全可用，为用户提供完整的工单管理体验。