# 工单系统 Makefile

.PHONY: help build run migrate migrate-all migrate-tables migrate-indexes migrate-seed clean test

# 默认目标
help:
	@echo "工单系统构建和管理工具"
	@echo ""
	@echo "可用命令:"
	@echo "  build          构建服务器"
	@echo "  run            运行服务器（不自动迁移）"
	@echo "  run-migrate    运行服务器（启用自动迁移）"
	@echo "  migrate-fast   快速数据库迁移（推荐）"
	@echo "  build-migrate  构建快速迁移工具"
	@echo "  migrate-all    执行完整数据库迁移（旧版，较慢）"
	@echo "  migrate-tables 只迁移数据库表结构（旧版）"
	@echo "  migrate-indexes 只创建数据库索引（旧版）"
	@echo "  migrate-seed   只初始化种子数据（旧版）"
	@echo "  clean          清理构建文件"
	@echo "  test           运行测试"
	@echo "  help           显示此帮助信息"

# 构建服务器
build:
	@echo "构建服务器..."
	go build -o bin/server main.go
	@echo "构建完成: bin/server"

# 运行服务器（不自动迁移）
run:
	@echo "启动服务器（跳过自动迁移）..."
	go run main.go

# 运行服务器（启用自动迁移）
run-migrate:
	@echo "启动服务器（启用自动迁移）..."
	AUTO_MIGRATE=true go run main.go

# 数据库迁移命令（新版 - 基于GORM AutoMigrate，高效快速）
migrate:
	@echo "🚀 执行数据库迁移..."
	@go run cmd/migrate/main.go

migrate-seed:
	@echo "🌱 执行数据库迁移并填充种子数据..."
	@go run cmd/migrate/main.go -seed

migrate-drop:
	@echo "⚠️  删除所有表并重新迁移（危险操作）..."
	@go run cmd/migrate/main.go -drop -seed

migrate-verbose:
	@echo "📝 执行详细数据库迁移..."
	@go run cmd/migrate/main.go -v -seed

# 清理构建文件
clean:
	@echo "清理构建文件..."
	rm -rf bin/
	@echo "清理完成"

# 运行测试
test:
	@echo "运行测试..."
	go test ./...

# 安装依赖
deps:
	@echo "安装依赖..."
	go mod download
	go mod tidy

# 格式化代码
fmt:
	@echo "格式化代码..."
	go fmt ./...

# 代码检查
vet:
	@echo "代码检查..."
	go vet ./...

# 开发环境设置
dev-setup: deps
	@echo "设置开发环境..."
	@if [ ! -f .env ]; then cp .env.example .env; echo "已创建 .env 文件，请根据需要修改配置"; fi
	@echo "开发环境设置完成"

# 生产环境构建
build-prod:
	@echo "生产环境构建..."
	CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o bin/server main.go
	@echo "生产环境构建完成: bin/server"