# ChronoDesk 数据库迁移

ChronoDesk 只保留一个结构迁移入口：`cmd/migrate`。该命令基于当前领域模型执行可重复迁移、索引校验和运行时 Schema 门禁，不再提供旧版快速 DDL、JSONB 修复或手工填充命令。

## 连接配置

迁移程序会加载 `server/.env`，并按以下顺序选择 PostgreSQL 连接：

1. `DATABASE_URL_UNPOOLED`
2. `POSTGRES_URL_NON_POOLING`
3. `DATABASE_URL`

也可以通过 `-dsn` 显式传入连接字符串。生产环境应使用密钥管理系统注入连接信息，不要把凭据写入仓库或命令历史。

## 常用命令

在 `server/` 目录执行：

```bash
# 仅执行结构迁移
go run ./cmd/migrate

# 输出详细数据库日志
go run ./cmd/migrate -v

# 显式初始化业务种子数据
go run ./cmd/migrate -seed

# 为高延迟云数据库提高总超时
go run ./cmd/migrate -timeout 10m

# 网络超时后从指定的一基模型序号继续
go run ./cmd/migrate -timeout 10m -resume-from-model 15
```

等价的 Make 目标：

```bash
make migrate
make migrate-seed
make migrate-verbose
```

`-resume-from-model` 只用于一次迁移在模型扫描阶段因网络超时中断的情况。已完成的单模型迁移会独立提交；续跑仍会执行全部索引与 Schema 校验。

## 破坏性重建

开发环境需要重建 ChronoDesk 自有表时，使用：

```bash
make migrate-drop
```

该目标要求交互输入 `DROP`，并在子进程中设置 `ALLOW_DESTRUCTIVE_MIGRATION=true`。不要在共享、测试、预发布或生产数据库执行。

## 应用启动

默认启动不会迁移：

```bash
go run ./main.go
```

只有显式设置以下变量才会在启动时运行相同的标准迁移：

```bash
AUTO_MIGRATE=true go run ./main.go
```

生产发布建议把迁移作为独立部署步骤，并在应用实例启动时保持 `AUTO_MIGRATE=false`，避免多个实例同时执行 DDL。

## 凭据加密迁移

数据库结构迁移不会自动改写历史明文凭据。凭据加密使用独立、显式的一次性命令：

```bash
go run ./cmd/secret-migrate -validate-only
go run ./cmd/secret-migrate
go run ./cmd/secret-migrate -validate-only
```

先验证、再迁移、最后再次验证。详情见 `docs/reference/DATA_AT_REST_ENCRYPTION.md`。
