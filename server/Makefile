# 工单系统 Makefile

.PHONY: help build run run-migrate migrate migrate-seed migrate-sample migrate-drop migrate-verbose clean test test-race deps fmt vet dev-setup build-prod credential-validate credential-rotate credential-quarantine

# 默认目标
help:
	@echo "工单系统构建和管理工具"
	@echo ""
	@echo "可用命令:"
	@echo "  build          构建服务器"
	@echo "  run            运行服务器（不自动迁移）"
	@echo "  run-migrate    运行服务器（启用自动迁移）"
	@echo "  migrate        执行唯一的标准数据库迁移"
	@echo "  migrate-seed   迁移并显式初始化种子数据"
	@echo "  migrate-sample 初始化开发种子数据与演示数据"
	@echo "  migrate-drop   确认后删除自有表并重建（仅一次性开发环境）"
	@echo "  migrate-verbose 输出详细迁移日志"
	@echo "  credential-validate    只读验证当前凭据存储"
	@echo "  credential-rotate      将当前密文轮换到主密钥"
	@echo "  credential-quarantine  隔离不支持的密码哈希"
	@echo "  clean          清理构建文件"
	@echo "  test           运行测试"
	@echo "  test-race      运行竞态检测"
	@echo "  vet            执行 Go 静态检查"
	@echo "  help           显示此帮助信息"

# 构建服务器
build:
	@echo "构建服务器..."
	go build -trimpath -o bin/chronodesk ./cmd/chronodesk
	go build -trimpath -o bin/chronodesk-credential-maintain ./cmd/credential-maintain
	@echo "构建完成: bin/chronodesk"

# 运行服务器（不自动迁移）
run:
	@echo "启动服务器（跳过自动迁移）..."
	go run ./cmd/chronodesk

# 运行服务器（启用自动迁移）
run-migrate:
	@echo "启动服务器（启用自动迁移）..."
	AUTO_MIGRATE=true go run ./cmd/chronodesk

# 数据库迁移命令（新版 - 基于GORM AutoMigrate，高效快速）
migrate:
	@echo "🚀 执行数据库迁移..."
	@go run cmd/migrate/main.go

migrate-seed:
	@echo "🌱 执行数据库迁移并填充种子数据..."
	@go run cmd/migrate/main.go -seed

migrate-sample:
	@echo "🌱 执行数据库迁移并填充开发演示数据..."
	@ENVIRONMENT=development go run cmd/migrate/main.go -seed -sample-data

migrate-drop:
	@echo "⚠️  删除所有表并重新迁移（危险操作）..."
	@printf "该操作会永久删除 ChronoDesk 数据。请输入 DROP 确认：" && read answer && [ "$$answer" = "DROP" ]
	@ALLOW_DESTRUCTIVE_MIGRATION=true go run cmd/migrate/main.go -drop -seed

migrate-verbose:
	@echo "📝 执行详细数据库迁移..."
	@go run cmd/migrate/main.go -v

credential-validate:
	@go run ./cmd/credential-maintain -validate-only

credential-rotate:
	@go run ./cmd/credential-maintain -rotate

credential-quarantine:
	@go run ./cmd/credential-maintain -quarantine

# 清理构建文件
clean:
	@echo "清理构建文件..."
	rm -rf bin/
	@echo "清理完成"

# 运行测试
test:
	@echo "运行测试..."
	go test ./... -count=1

test-race:
	@echo "运行 Go 竞态检测..."
	go test -race ./... -count=1

# 安装依赖
deps:
	@echo "安装依赖..."
	go mod download

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
	CGO_ENABLED=0 GOOS=linux go build -trimpath -o bin/chronodesk ./cmd/chronodesk
	CGO_ENABLED=0 GOOS=linux go build -trimpath -o bin/chronodesk-credential-maintain ./cmd/credential-maintain
	@echo "生产环境构建完成: bin/chronodesk"
