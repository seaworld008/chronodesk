# 数据库迁移指南

本文档介绍如何使用工单系统的数据库迁移工具。

## 概述

为了提高服务器启动速度和灵活性，我们将数据库迁移从服务器启动过程中分离出来，创建了独立的迁移工具。

## 迁移工具

### 独立迁移程序

位置：`cmd/migrate/main.go`

这是一个独立的Go程序，可以按需执行数据库迁移操作。

#### 使用方法

```bash
# 显示帮助信息
go run cmd/migrate/main.go -help

# 执行完整迁移（推荐）
go run cmd/migrate/main.go -all

# 只迁移表结构
go run cmd/migrate/main.go -migrate

# 只创建索引
go run cmd/migrate/main.go -indexes

# 只初始化种子数据
go run cmd/migrate/main.go -seed

# 组合操作
go run cmd/migrate/main.go -migrate -indexes
```

### Makefile 快捷命令

我们提供了Makefile来简化常用操作：

```bash
# 显示所有可用命令
make help

# 执行完整数据库迁移
make migrate-all

# 只迁移表结构
make migrate-tables

# 只创建索引
make migrate-indexes

# 只初始化种子数据
make migrate-seed
```

## 服务器启动

### 默认模式（推荐）

```bash
# 启动服务器，跳过自动迁移
make run
# 或
go run main.go
```

服务器将快速启动，不执行数据库迁移。

### 自动迁移模式

```bash
# 启动服务器并自动执行迁移
make run-migrate
# 或
AUTO_MIGRATE=true go run main.go
```

服务器启动时会自动执行完整的数据库迁移。

## 推荐工作流程

### 首次部署

1. 配置数据库连接（.env文件）
2. 执行完整迁移：`make migrate-all`
3. 启动服务器：`make run`

### 开发环境

1. 修改数据库模型后
2. 执行表迁移：`make migrate-tables`
3. 如需要，创建新索引：`make migrate-indexes`
4. 重启服务器：`make run`

### 生产环境

1. 停止服务器
2. 备份数据库
3. 执行迁移：`make migrate-all`
4. 启动服务器：`make run`

## 迁移内容

### 表结构迁移 (`-migrate`)

- 用户表 (users)
- 用户资料表 (user_profiles)
- 刷新令牌表 (refresh_tokens)
- 登录尝试表 (login_attempts)
- 分类表 (categories)
- 工单表 (tickets)
- 工单评论表 (ticket_comments)
- 工单历史表 (ticket_histories)
- OTP验证码表 (otp_codes)

### 索引创建 (`-indexes`)

- 用户表索引（邮箱、用户名、角色等）
- 工单表索引（状态、优先级、分类等）
- 评论表索引（工单ID、用户ID等）
- 历史表索引（工单ID、操作类型等）
- 其他性能优化索引

### 种子数据 (`-seed`)

- 默认管理员用户
- 基础分类数据
- 系统配置数据

## 故障排除

### 常见问题

1. **数据库连接失败**
   - 检查 .env 文件中的数据库配置
   - 确保数据库服务正在运行
   - 验证数据库用户权限

2. **迁移失败**
   - 检查数据库用户是否有创建表的权限
   - 查看详细错误信息
   - 确保数据库版本兼容

3. **索引创建失败**
   - 通常是警告，不会中断迁移
   - 检查是否有重复的索引
   - 验证表结构是否正确

### 日志信息

迁移工具会输出详细的日志信息：

```
数据库连接成功
开始执行完整数据库迁移...
Starting database migration...
Database migration completed successfully
开始创建数据库索引...
Additional indexes created successfully
开始初始化种子数据...
Seed data initialized successfully
完整迁移完成
所有操作完成
```

## 环境变量

### 服务器启动控制

- `AUTO_MIGRATE=true`: 启用服务器启动时自动迁移
- `AUTO_MIGRATE=false` 或未设置: 跳过自动迁移（默认）

### 数据库配置

确保以下环境变量正确配置：

```env
DATABASE_URL=postgres://username:password@localhost:5432/dbname?sslmode=disable
DB_HOST=localhost
DB_PORT=5432
DB_USER=username
DB_PASSWORD=password
DB_NAME=dbname
```

## 最佳实践

1. **生产环境**：始终使用独立迁移工具，不要依赖自动迁移
2. **开发环境**：可以使用自动迁移提高开发效率
3. **备份**：生产环境迁移前务必备份数据库
4. **测试**：在测试环境先验证迁移脚本
5. **监控**：关注迁移过程中的日志和性能指标

## 版本控制

- 数据库模型变更应该通过代码版本控制
- 迁移脚本随代码一起版本化
- 生产环境部署时确保代码和数据库版本一致