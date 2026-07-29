SHELL := /bin/bash
.DEFAULT_GOAL := help

COMPOSE ?= docker compose
PYTHON ?= python3
NPM_TOOL_CACHE ?= $(CURDIR)/.cache/npm-tools
VERSION ?= 0.1.0
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
GO_LDFLAGS := -s -w \
	-X github.com/seaworld008/chronodesk/server/internal/version.Version=$(VERSION) \
	-X github.com/seaworld008/chronodesk/server/internal/version.Commit=$(COMMIT) \
	-X github.com/seaworld008/chronodesk/server/internal/version.BuildDate=$(BUILD_DATE)

.PHONY: \
	help doctor install-deps install-server-deps install-web-deps install-test-deps \
	dev server-dev web-dev docker-up docker-down docker-logs \
	build build-server build-web clean \
	fmt fmt-check test test-server test-race test-web test-python-static security verify \
	openapi-lint smoke e2e db-migrate db-migrate-seed db-migrate-sample \
	credential-validate credential-rotate credential-quarantine

help:
	@echo "ChronoDesk 开发命令"
	@echo ""
	@echo "环境"
	@echo "  doctor          检查本机工具链"
	@echo "  install-deps    可重复安装 Go、Web 和 Python 测试依赖"
	@echo "  dev             构建并启动 Docker Compose 开发环境"
	@echo "  server-dev      在本机启动 Go API"
	@echo "  web-dev         在本机启动 Vite 管理端"
	@echo ""
	@echo "质量"
	@echo "  fmt             格式化 Go 源码"
	@echo "  test            执行 Go、Web 与 OpenAPI 标准门禁"
	@echo "  test-race       执行 Go 竞态检测"
	@echo "  security        执行 Go 与 Web 依赖安全检查"
	@echo "  verify          执行格式、测试、安全与生产构建门禁"
	@echo "  smoke           对运行中的 API 执行全部 Python 黑盒测试"
	@echo "  e2e             对运行中的完整环境执行 Playwright 测试"
	@echo ""
	@echo "数据与容器"
	@echo "  db-migrate      执行幂等数据库迁移"
	@echo "  db-migrate-seed 执行迁移并写入开发种子数据"
	@echo "  db-migrate-sample 执行迁移并写入开发演示数据"
	@echo "  credential-validate    只读验证当前凭据存储"
	@echo "  credential-rotate      将当前密文轮换到主密钥"
	@echo "  credential-quarantine  隔离不支持的密码哈希"
	@echo "  docker-down     停止 Docker Compose"
	@echo "  docker-logs     查看 Docker Compose 日志"

doctor:
	@command -v go >/dev/null && go version
	@command -v node >/dev/null && node --version
	@command -v npm >/dev/null && npm --version
	@command -v $(PYTHON) >/dev/null && $(PYTHON) --version
	@$(COMPOSE) version

install-deps: install-server-deps install-web-deps install-test-deps

install-server-deps:
	cd server && go mod download

install-web-deps:
	cd web && npm ci

install-test-deps:
	$(PYTHON) -m pip install -r server/requirements-test.txt

dev: docker-up

server-dev:
	cd server && go run ./cmd/chronodesk

web-dev:
	cd web && npm run dev

docker-up:
	VERSION=$(VERSION) COMMIT=$(COMMIT) BUILD_DATE=$(BUILD_DATE) \
		$(COMPOSE) up --build -d
	@echo "管理后台：http://localhost:3000"
	@echo "健康检查：http://localhost:8081/healthz"

docker-down:
	$(COMPOSE) down

docker-logs:
	$(COMPOSE) logs -f

build: build-server build-web

build-server:
	cd server && mkdir -p bin
	cd server && go build -trimpath -ldflags="$(GO_LDFLAGS)" -o bin/chronodesk ./cmd/chronodesk
	cd server && go build -trimpath -ldflags="$(GO_LDFLAGS)" -o bin/chronodesk-migrate ./cmd/migrate
	cd server && go build -trimpath -ldflags="$(GO_LDFLAGS)" -o bin/chronodesk-credential-maintain ./cmd/credential-maintain

build-web:
	cd web && npm run build

clean:
	rm -rf server/bin server/reports server/htmlcov
	rm -rf web/dist web/build web/playwright-report web/test-results

fmt:
	cd server && gofmt -w .

fmt-check:
	@test -z "$$(cd server && gofmt -l .)" || \
		{ echo "存在未格式化的 Go 文件："; cd server && gofmt -l .; exit 1; }

test: test-server test-web test-python-static openapi-lint

test-server:
	cd server && go test ./... -count=1
	cd server && go vet ./...

test-race:
	cd server && go test -race ./... -count=1

test-web:
	cd web && npm run typecheck
	cd web && npm run lint
	cd web && npm run audit:security

test-python-static:
	$(PYTHON) -m ruff format --check server/tests
	$(PYTHON) -m ruff check server/tests
	$(PYTHON) -m compileall -q server/tests
	$(PYTHON) server/tests/validate_case_evidence_manifest.py
	$(PYTHON) server/tests/validate_case_evidence_manifest.py --self-test
	$(PYTHON) -m pytest -c server/pytest.ini --collect-only -q server/tests

security:
	cd server && go run golang.org/x/vuln/cmd/govulncheck@latest ./...
	cd web && npm run audit:security

verify: fmt-check test security build

openapi-lint:
	NPM_CONFIG_CACHE=$(NPM_TOOL_CACHE) npx --yes @redocly/cli@2.41.1 \
		lint server/internal/openapi/openapi.yaml --format=stylish
	NPM_CONFIG_CACHE=$(NPM_TOOL_CACHE) npx --yes @stoplight/spectral-cli@6.16.2 \
		lint -r server/internal/openapi/.spectral.yaml \
		server/internal/openapi/openapi.yaml \
		--fail-severity=warn

smoke:
	mkdir -p server/reports
	$(PYTHON) -m pytest -c server/pytest.ini server/tests -v \
		--html=server/reports/smoke.html \
		--self-contained-html

e2e:
	cd web && npm run test:e2e

db-migrate:
	cd server && go run ./cmd/migrate

db-migrate-seed:
	cd server && go run ./cmd/migrate -seed

db-migrate-sample:
	cd server && ENVIRONMENT=development go run ./cmd/migrate -seed -sample-data

credential-validate:
	cd server && go run ./cmd/credential-maintain -validate-only

credential-rotate:
	cd server && go run ./cmd/credential-maintain -rotate

credential-quarantine:
	cd server && go run ./cmd/credential-maintain -quarantine
