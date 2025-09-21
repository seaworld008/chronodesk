# 工单管理系统 Makefile

.PHONY: help dev build test clean docker-up docker-down docker-logs install-deps

# 默认目标
help:
	@echo "Available commands:"
	@echo "  dev          - Start development environment"
	@echo "  build        - Build both server and web"
	@echo "  test         - Run tests"
	@echo "  clean        - Clean build artifacts"
	@echo "  docker-up    - Start services with Docker Compose"
	@echo "  docker-down  - Stop Docker Compose services"
	@echo "  docker-logs  - View Docker Compose logs"
	@echo "  install-deps - Install dependencies"
	@echo "  server-dev   - Start server in development mode"
	@echo "  web-dev      - Start web in development mode"
	@echo "  db-migrate   - Run database migrations"
	@echo "  swagger      - Generate Swagger documentation"

# 开发环境
dev: docker-up

# 构建项目
build: build-server build-web

build-server:
	@echo "Building server..."
	cd server && go build -o bin/server main.go

build-web:
	@echo "Building web..."
	cd web && npm run build

# 运行测试
test: test-server test-web

test-server:
	@echo "Running server tests..."
	cd server && go test ./...

test-web:
	@echo "Skipping web checks (not configured)"

smoke:
	@echo "Running API smoke suites..."
	cd server && mkdir -p reports
	cd server && pytest tests/auth -v --html=reports/auth.html --self-contained-html
	cd server && pytest tests/automation -v --html=reports/automation.html --self-contained-html
	cd server && pytest tests/system -v --html=reports/system.html --self-contained-html

# 清理
clean:
	@echo "Cleaning build artifacts..."
	rm -rf server/bin
	rm -rf web/dist
	rm -rf web/build

# Docker 命令
docker-up:
	@echo "Starting services with Docker Compose..."
	docker-compose up -d
	@echo "Services started. Check status with: make docker-logs"
	@echo "API: http://localhost:8080/healthz"
	@echo "Web: http://localhost:3000"

docker-down:
	@echo "Stopping Docker Compose services..."
	docker-compose down

docker-logs:
	@echo "Viewing Docker Compose logs..."
	docker-compose logs -f

# 安装依赖
install-deps: install-server-deps install-web-deps

install-server-deps:
	@echo "Installing server dependencies..."
	cd server && go mod tidy

install-web-deps:
	@echo "Installing web dependencies..."
	cd web && npm install

# 开发模式启动
server-dev:
	@echo "Starting server in development mode..."
	cd server && go run main.go

web-dev:
	@echo "Starting web in development mode..."
	cd web && npm run dev

# 数据库迁移
db-migrate:
	@echo "Running database migrations..."
	cd server && go run cmd/migrate/main.go

# 生成 Swagger 文档
swagger:
	@echo "Generating Swagger documentation..."
	cd server && swag init -g main.go -o docs

# 初始化项目
init: install-deps
	@echo "Project initialized successfully!"
	@echo "Run 'make dev' to start development environment"
