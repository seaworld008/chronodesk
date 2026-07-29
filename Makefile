# 工单管理系统 Makefile

.PHONY: help dev build build-server build-web test test-server test-web clean docker-up docker-down docker-logs install-deps install-server-deps install-web-deps openapi-lint smoke server-dev web-dev db-migrate init

# 隔离一次性规范校验工具的缓存，避免用户级 npm 缓存的权限或版本污染。
NPM_TOOL_CACHE ?= $(CURDIR)/.cache/npm-tools

# 默认目标
help:
	@echo "可用命令："
	@echo "  dev          - 启动 Docker 开发环境"
	@echo "  build        - 构建后端与前端"
	@echo "  test         - 执行后端与前端质量门禁"
	@echo "  clean        - 清理构建产物"
	@echo "  docker-up    - 启动 Docker Compose"
	@echo "  docker-down  - 停止 Docker Compose"
	@echo "  docker-logs  - 查看 Docker Compose 日志"
	@echo "  install-deps - 安装依赖"
	@echo "  server-dev   - 启动后端开发服务"
	@echo "  web-dev      - 启动前端开发服务"
	@echo "  db-migrate   - 执行标准数据库迁移"
	@echo "  openapi-lint - 校验 OpenAPI 3.2 机器契约"

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
test: test-server test-web openapi-lint

test-server:
	@echo "Running server tests..."
	cd server && go test ./...

test-web:
	@echo "Running web quality gates..."
	cd web && npm run typecheck
	cd web && npm run lint
	cd web && npm run audit:security

openapi-lint:
	@echo "Validating OpenAPI 3.2 contract..."
	NPM_CONFIG_CACHE=$(NPM_TOOL_CACHE) npx --yes @redocly/cli@2.41.1 lint server/internal/openapi/openapi.yaml --format=stylish
	NPM_CONFIG_CACHE=$(NPM_TOOL_CACHE) npx --yes @stoplight/spectral-cli@6.16.2 lint \
		-r server/internal/openapi/.spectral.yaml \
		server/internal/openapi/openapi.yaml \
		--fail-severity=warn

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
	@echo "API: http://localhost:8081/healthz"
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

# 初始化项目
init: install-deps
	@echo "Project initialized successfully!"
	@echo "Run 'make dev' to start development environment"
