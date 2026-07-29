# ChronoDesk 黑盒 API 测试

本目录只包含通过真实 HTTP Interface 验证运行中 ChronoDesk 的 Pytest 套件。
测试不会导入 Go Implementation，因此不生成虚假的 Python 源码覆盖率。

## 目录

```text
tests/
├── auth/          注册、登录、刷新、登出、OTP、可信设备与备用码
├── human/         四角色 RBAC、对象权限、边界、评论、附件、通知与错误契约
├── tickets/       Ticket 生命周期、Assignment、通知隔离与清理
├── automation/    规则、SLA、模板与快速回复
├── system/        配置、健康与维护操作
├── utils/         fail-closed HTTP 客户端、断言与精确 E2E 数据管理
├── CASE_MAPPING.md Human REST 用例 ID 与精确证据边界
├── validate_case_evidence_manifest.py 236 Case ID/locator 静态门禁
└── conftest.py    健康检查、管理员登录和共享 fixture
```

## 前置条件

先启动完整环境并初始化开发数据：

```bash
make dev
docker compose exec server chronodesk-migrate -seed
```

测试默认访问 `http://localhost:8081/api`，并要求 `/healthz` 同时报告
`dependencies.postgresql=ok` 与 `dependencies.redis=ok`。依赖不可用、管理员登录失败
或响应契约异常都会直接失败，不会跳过后制造假绿。

可通过环境变量覆盖受控测试环境：

```bash
export TEST_API_BASE_URL="http://localhost:8081/api"
export TEST_HEALTHCHECK_URL="http://localhost:8081/healthz"
export TEST_ADMIN_EMAIL="admin@example.com"
export TEST_ADMIN_PASSWORD="development-password"
```

非回环目标默认硬拒绝。只有明确确认目标是独立测试环境时，才可同时提供远端
放行标志与本轮唯一所有权前缀：

```bash
export CHRONODESK_ALLOW_REMOTE_E2E=1
export CHRONODESK_E2E_OWNERSHIP_PREFIX="e2e-team-run-1234"
```

仅设置其中一个不会放行。全局 cleanup/config round-trip 即使面对远端隔离环境
也会拒绝；它们只允许在回环、一次性环境中显式设置
`CHRONODESK_EPHEMERAL_E2E=1` 后运行，并在 `finally` 中恢复及复核原快照。

不要把真实凭据写入本文件、测试源码、报告或命令历史。

## 执行

从仓库根目录运行全部黑盒套件：

```bash
make smoke
```

运行单个切片：

```bash
cd server
python3 -m pytest tests/tickets -v
python3 -m pytest tests/human -v
```

`make smoke` 生成忽略跟踪的 `server/reports/smoke.html`。每个写入用例必须使用
唯一测试标识并在 `finally`/fixture teardown 中删除或恢复自己创建的数据。
请求/响应诊断及 Pytest console/HTML report 会统一移除 token、OTP/备用码、
Cookie、Authorization、client secret、password 与 DSN。

Human REST 套件统一使用 `E2E-<run-id>-` 前缀，并在删除前重新校验资源所有权。
429 用例会耗尽一个本轮专用用户的单路由限流桶；默认最多发出 1000 次请求，
如测试环境采用更高限额，需显式设置 `TEST_RATE_LIMIT_EXHAUSTION_CEILING`。
完整映射与证据边界见 [CASE_MAPPING.md](CASE_MAPPING.md)。

证据清单静态校验不要求启动 API，也不声称测试已经执行：

```bash
python3 tests/validate_case_evidence_manifest.py
```
