# ChronoDesk 黑盒 API 测试

本目录只包含通过真实 HTTP Interface 验证运行中 ChronoDesk 的 Pytest 套件。
测试不会导入 Go Implementation，因此不生成虚假的 Python 源码覆盖率。

## 目录

```text
tests/
├── auth/          注册、登录、刷新、登出、OTP、可信设备与备用码
├── tickets/       Ticket 生命周期、Assignment、通知隔离与清理
├── automation/    规则、SLA、模板与快速回复
├── system/        配置、健康与维护操作
├── utils/         fail-closed HTTP 客户端
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

不要把真实凭据写入本文件、测试源码、报告或命令历史。

## 执行

从仓库根目录运行全部黑盒套件：

```bash
make smoke
```

运行单个切片：

```bash
cd server
pytest tests/tickets -v
```

`make smoke` 生成忽略跟踪的 `server/reports/smoke.html`。每个写入用例必须使用
唯一测试标识并在 `finally`/fixture teardown 中删除或恢复自己创建的数据。
