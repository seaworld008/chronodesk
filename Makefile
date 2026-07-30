SHELL := /bin/bash
.DEFAULT_GOAL := help

COMPOSE ?= docker compose
# PYTHON remains a backwards-compatible alias for the interpreter that creates
# the repository virtual environment. Python tooling itself always runs inside
# VENV so Homebrew's externally managed interpreter is never modified.
PYTHON ?= python3
BOOTSTRAP_PYTHON ?= $(PYTHON)
VENV ?= $(CURDIR)/.venv
PYTHON_REQUIREMENTS ?= server/requirements-test.txt
VENV_PYTHON := $(VENV)/bin/python
PYTHON_REQUIREMENTS_STAMP := $(VENV)/.requirements-test.txt
NPM_TOOL_CACHE ?= $(CURDIR)/.cache/npm-tools
VERSION ?= 0.1.0
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
GO_LDFLAGS := -s -w \
	-X github.com/seaworld008/chronodesk/server/internal/version.Version=$(VERSION) \
	-X github.com/seaworld008/chronodesk/server/internal/version.Commit=$(COMMIT) \
	-X github.com/seaworld008/chronodesk/server/internal/version.BuildDate=$(BUILD_DATE)

.PHONY: \
	help doctor install-deps install-server-deps install-web-deps install-test-deps install-sdk-deps \
	dev server-dev web-dev docker-up docker-down docker-logs \
	build build-server build-web build-sdk clean \
	fmt fmt-check test test-server test-race test-web test-sdk test-python-static test-python-toolchain security verify \
	openapi-lint asyncapi-lint smoke e2e db-migrate db-migrate-seed db-migrate-sample \
	credential-validate credential-rotate credential-quarantine

.PHONY: FORCE
FORCE:

help:
	@echo "ChronoDesk 开发命令"
	@echo ""
	@echo "环境"
	@echo "  doctor          检查本机工具链"
	@echo "  install-deps    可重复安装 Go、Web 和 Python 测试依赖"
	@echo "  install-sdk-deps 安装 TypeScript SDK 的锁定依赖"
	@echo "  dev             构建并启动 Docker Compose 开发环境"
	@echo "  server-dev      在本机启动 Go API"
	@echo "  web-dev         在本机启动 Vite 管理端"
	@echo ""
	@echo "质量"
	@echo "  fmt             格式化 Go 源码"
	@echo "  test-python-toolchain 验证仓库 Python 虚拟环境与依赖刷新"
	@echo "  test            执行 Go、Web、OpenAPI 与 AsyncAPI 标准门禁"
	@echo "  test-race       执行 Go 竞态检测"
	@echo "  test-sdk        编译并测试 Go、Python、TypeScript 项目绑定 SDK"
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
	@command -v $(BOOTSTRAP_PYTHON) >/dev/null && $(BOOTSTRAP_PYTHON) --version
	@$(COMPOSE) version

install-deps: install-server-deps install-web-deps install-test-deps install-sdk-deps

install-server-deps:
	cd server && go mod download

install-web-deps:
	cd web && npm ci

install-test-deps: $(PYTHON_REQUIREMENTS_STAMP)
	"$(VENV_PYTHON)" -m pip check

$(VENV_PYTHON):
	"$(BOOTSTRAP_PYTHON)" -m venv "$(VENV)"

$(PYTHON_REQUIREMENTS_STAMP): FORCE | $(VENV_PYTHON)
	@if ! cmp -s "$(PYTHON_REQUIREMENTS)" "$@"; then \
		"$(VENV_PYTHON)" -m pip install -r "$(PYTHON_REQUIREMENTS)" && \
		cp "$(PYTHON_REQUIREMENTS)" "$@"; \
	fi

install-sdk-deps:
	cd sdk/typescript && npm ci

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

build: build-server build-web build-sdk

build-server:
	cd server && mkdir -p bin
	cd server && go build -trimpath -ldflags="$(GO_LDFLAGS)" -o bin/chronodesk ./cmd/chronodesk
	cd server && go build -trimpath -ldflags="$(GO_LDFLAGS)" -o bin/chronodesk-migrate ./cmd/migrate
	cd server && go build -trimpath -ldflags="$(GO_LDFLAGS)" -o bin/chronodesk-credential-maintain ./cmd/credential-maintain
	cd server && go build -trimpath -o bin/chronodeskctl ./cmd/chronodeskctl

build-web:
	cd web && npm run build

build-sdk: $(PYTHON_REQUIREMENTS_STAMP)
	cd sdk/go && go build ./...
	"$(VENV_PYTHON)" -m compileall -q sdk/python/chronodesk sdk/python/examples
	cd sdk/typescript && npm run build

clean:
	rm -rf server/bin server/reports server/htmlcov
	rm -rf web/dist web/build web/playwright-report web/test-results
	rm -rf sdk/typescript/dist
	rm -rf sdk/python/build sdk/python/*.egg-info
	find sdk/python -type d -name __pycache__ -prune -exec rm -rf {} +

fmt: $(PYTHON_REQUIREMENTS_STAMP)
	cd server && gofmt -w .
	gofmt -w $$(rg --files sdk/go -g '*.go')
	"$(VENV_PYTHON)" -m ruff format sdk/python

fmt-check: $(PYTHON_REQUIREMENTS_STAMP)
	@test -z "$$(cd server && gofmt -l .)" || \
		{ echo "存在未格式化的 Go 文件："; cd server && gofmt -l .; exit 1; }
	@test -z "$$(gofmt -l $$(rg --files sdk/go -g '*.go'))" || \
		{ echo "SDK 存在未格式化的 Go 文件："; \
		  gofmt -l $$(rg --files sdk/go -g '*.go'); exit 1; }
	"$(VENV_PYTHON)" -m ruff format --check sdk/python

test: test-server test-web test-sdk test-python-static openapi-lint asyncapi-lint

test-server:
	cd server && go test ./... -count=1
	cd server && go vet ./...

test-race:
	cd server && go test -race ./... -count=1

test-web:
	cd web && npm run typecheck
	cd web && npm run lint
	cd web && npm run audit:security

test-sdk: $(PYTHON_REQUIREMENTS_STAMP)
	cd sdk/go && go test ./... -count=1
	"$(VENV_PYTHON)" -m ruff check sdk/python
	PYTHONPATH=$(CURDIR)/sdk/python "$(VENV_PYTHON)" -m unittest discover \
		-s sdk/python/tests -p 'test_*.py'
	cd sdk/typescript && npm test

test-python-static: $(PYTHON_REQUIREMENTS_STAMP) test-python-toolchain
	"$(VENV_PYTHON)" -m ruff format --check server/tests
	"$(VENV_PYTHON)" -m ruff check server/tests
	"$(VENV_PYTHON)" -m compileall -q server/tests
	"$(VENV_PYTHON)" server/tests/validate_case_evidence_manifest.py
	"$(VENV_PYTHON)" server/tests/validate_case_evidence_manifest.py --self-test
	"$(VENV_PYTHON)" -m pytest -c server/pytest.ini --collect-only -q server/tests

test-python-toolchain:
	bash server/tests/test_python_toolchain.sh

security:
	cd server && go run golang.org/x/vuln/cmd/govulncheck@latest ./...
	cd sdk/go && go run golang.org/x/vuln/cmd/govulncheck@latest ./...
	cd web && npm run audit:security
	cd sdk/typescript && npm audit --audit-level=high

verify: fmt-check test security build

openapi-lint:
	NPM_CONFIG_CACHE=$(NPM_TOOL_CACHE) npx --yes @redocly/cli@2.41.1 \
		lint server/internal/openapi/openapi.yaml --format=stylish
	NPM_CONFIG_CACHE=$(NPM_TOOL_CACHE) npx --yes @stoplight/spectral-cli@6.16.2 \
		lint -r server/internal/openapi/.spectral.yaml \
		server/internal/openapi/openapi.yaml \
		--fail-severity=warn

asyncapi-lint:
	NPM_CONFIG_CACHE=$(NPM_TOOL_CACHE) npx --yes @asyncapi/cli@6.0.2 \
		validate server/internal/asyncapi/asyncapi.yaml

smoke: $(PYTHON_REQUIREMENTS_STAMP)
	mkdir -p server/reports
	"$(VENV_PYTHON)" -m pytest -c server/pytest.ini server/tests -v \
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
