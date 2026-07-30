# ChronoDesk 数据库迁移

ChronoDesk 只保留一个结构迁移入口：`cmd/migrate`。该命令基于当前领域模型执行可重复迁移、索引校验和运行时 Schema 门禁，不再提供旧版快速 DDL、JSONB 修复或手工填充命令。

## 连接配置

迁移程序会加载 `server/.env`，并按以下顺序选择 PostgreSQL 连接：

1. `DATABASE_MIGRATION_URL`
2. `DATABASE_URL_UNPOOLED`
3. `POSTGRES_URL_NON_POOLING`
4. `DATABASE_URL`

也可以通过 `-dsn` 显式传入连接字符串。生产环境应使用密钥管理系统注入连接信息，不要把凭据写入仓库或命令历史。
非回环 PostgreSQL 必须使用不会降级为明文的 TLS 模式（`sslmode=require`、
`verify-ca` 或 `verify-full`）。`POSTGRES_ALLOW_INSECURE=true` 只允许用于隔离的
一次性开发网络，禁止在共享或生产环境设置。

运行中的 ChronoDesk 不读取上述迁移连接。应用只接受
`DATABASE_RUNTIME_URL`，该 URL 必须使用可登录、非表 owner、
`NOSUPERUSER`、`NOBYPASSRLS` 且不是 owner 角色成员的独立 PostgreSQL
角色。应用启动会验证全部项目表已经同时启用 `ENABLE ROW LEVEL SECURITY`
和 `FORCE ROW LEVEL SECURITY`，验证失败时不接收流量。

当开发环境显式使用 `AUTO_MIGRATE=true` 时，还必须设置
`DATABASE_MIGRATION_URL`。应用以这个短生命周期 owner 连接执行迁移并原子
启用/强制 RLS，关闭迁移连接后才建立 runtime 连接。生产实例应保持
`AUTO_MIGRATE=false`，由独立发布任务运行 `cmd/migrate` 和 RLS cutover，
不要向长期运行的应用容器注入迁移凭据。

## 常用命令

在仓库根目录执行：

```bash
# 仅执行结构迁移
make db-migrate

# 输出详细数据库日志
cd server && go run ./cmd/migrate -v

# 显式初始化业务种子数据
cd server && go run ./cmd/migrate -seed

# 仅在开发环境显式增加演示账号与工单
cd server && ENVIRONMENT=development go run ./cmd/migrate -seed -sample-data

# 为高延迟云数据库提高总超时
cd server && go run ./cmd/migrate -timeout 10m

# 网络超时后从指定的一基模型序号继续
cd server && go run ./cmd/migrate -timeout 10m -resume-from-model 15
```

等价的 Make 目标：

```bash
make -C server migrate
make -C server migrate-seed
make -C server migrate-sample
make -C server migrate-verbose
```

`-seed` 初始化管理员、默认分类、必要系统配置，以及该管理员在迁移所创建的
active 默认 Organization/Project 下的明确 `project_admin` Membership。全部写入
在一个事务内完成；默认项目缺失、已归档或已有冲突 Membership 时会拒绝覆盖并
整体回滚。该高权限 Membership 必须通过共享领域服务，以
`chronodesk-bootstrap` system Actor 同事务写入 CloudEvent、Outbox 和审计哈希链；
命令缺少该领域写入器时 fail closed。重复执行不会创建重复管理员、Membership
或事件。演示数据必须额外传入 `-sample-data`，且仅接受
`ENVIRONMENT=development`。

旧库已有的 active 用户在项目 scope 迁移中按原角色映射到默认项目。迁移命令和
应用启动组合层必须注入共享 Membership 领域写入器；缺失时迁移整体回滚。新建
授权以 `chronodesk-project-scope-migration` system Actor 同事务写入
Membership、CloudEvent、Outbox 与审计哈希链。停用或软删除用户不会获得新的
active Membership。

该破坏性数据切换只允许执行一次。迁移在同一事务的最后写入
`schema_migration_checkpoints`，固定记录 key、版本、编译期 checksum 和完成时间；
PostgreSQL 使用事务级 advisory lock 串行化首次切换。任一 Ticket、Membership、
事件、Outbox 或审计写入失败时，checkpoint 与全部回填一起回滚。后续迁移验证
checkpoint 后会跳过全部数据与授权回填，因此不会重置项目编号、改写其他项目
工单，也不会把后来创建的 Human 或 Service Principal 静默授予 DEFAULT 项目。
结构门禁仍会检查并按需恢复项目列的 `NOT NULL`；健康数据库不会重复执行
`ALTER TABLE`。checkpoint 内容不匹配会 fail closed；在项目 RLS 已启用后发现
checkpoint 缺失同样拒绝自动认领，必须先人工审计数据库。

对于真正的旧库，迁移在 GORM 应用最终模型契约前，仅给原本不存在的
`organization_id/project_id` 加入非空 `0` 哨兵，并移除列默认值；Ticket 的
公共 ID 使用唯一、可识别的临时哨兵。一次性回填会把这些值替换为可信项目范围
与 UUIDv7。上一次迁移中断后留下的“六列已存在但全部为 `NULL/0`”形态会被
规范回相同的 legacy 哨兵，以便安全重试；任何既有项目控制数据或非零项目范围
都会 fail closed，绝不会在 checkpoint 缺失时被自动归入 DEFAULT。

模型、迁移与运行时门禁共同要求所有项目业务表的
`organization_id/project_id`，以及 Ticket 的 `public_id/queue_id/`
`request_type_version_id/workflow_version_id` 为 `NOT NULL`。运行时会从
PostgreSQL catalog 验证该契约；六列幂等唯一索引不能在可空项目范围上启动。

项目 scope 回填后，迁移会在事务内锁定 `idempotency_records`，将旧四列
`idx_idempotency_actor_operation_key` 原子重建为
`organization_id, project_id, actor_type, actor_id, operation, key` 六列唯一
索引。运行时启动校验会核对唯一性和精确列顺序；旧索引、表达式索引、部分索引
或无效索引都会拒绝启动。

`-resume-from-model` 只用于一次迁移在模型扫描阶段因网络超时中断的情况。已完成的单模型迁移会独立提交；续跑仍会执行全部索引与 Schema 校验。

## 破坏性重建

开发环境需要重建 ChronoDesk 自有表时，使用：

```bash
make -C server migrate-drop
```

该目标要求交互输入 `DROP`，并在子进程中设置 `ALLOW_DESTRUCTIVE_MIGRATION=true`。不要在共享、测试、预发布或生产数据库执行。

## 应用启动

默认启动不会迁移：

```bash
make server-dev
```

只有显式设置迁移 URL 和以下变量，才会在启动时运行相同的标准迁移并
完成 FORCE RLS cutover：

```bash
cd server && \
  AUTO_MIGRATE=true \
  DATABASE_MIGRATION_URL='postgres://migration-role:...@localhost/chronodesk?sslmode=disable' \
  DATABASE_RUNTIME_URL='postgres://runtime-role:...@localhost/chronodesk?sslmode=disable' \
  go run ./cmd/chronodesk
```

生产发布建议把迁移作为独立部署步骤，并在应用实例启动时保持 `AUTO_MIGRATE=false`，避免多个实例同时执行 DDL。

项目请求在一个带 `SET LOCAL` 范围的短事务内执行，响应只有在 COMMIT
成功后才发给客户端。普通 JSON 响应在内存缓冲；较大的附件响应使用有界
临时文件，项目路由不允许 WebSocket、实时 flush 或 HTTP/2 push。后台
Worker 必须按项目把 claim 与 finalize 分成两个短事务，网络投递、模型调用
和文件处理不得占用数据库事务。

启动时的数据库密钥信封校验同样使用 runtime 角色：服务先从可信项目目录
枚举全部项目，再逐项目进入短 RLS 事务校验 Webhook 与 A2A Push 凭据；
全局 SMTP 配置单独校验。禁止用无范围扫描的“零行结果”判定密钥健康。

## 事件契约迁移门禁

标准迁移会同步升级自动化规则和 Webhook 中保存的历史事件名称。该数据迁移与
结构迁移使用同一事务边界，重复执行不会产生额外变化。已知旧名称会转换为当前
完整 CloudEvent 类型，并在需要时补充状态筛选；未知名称、无法解析的 JSON 或
无法映射为真实当前事件的历史 Webhook 日志会令迁移失败并完整回滚。

发生这类失败时，应先备份并审查报错记录。不要直接改成宽泛事件或删除审计
日志；确认业务语义后再进行一次性数据修复，然后重跑标准迁移。

## 凭据存储维护

数据库结构迁移不会改写凭据。ChronoDesk 只接受当前 `cdsec` 信封、bcrypt
密码/备用码哈希和域分离 token 摘要；维护命令不会摄入历史明文。
凭据维护需要与结构迁移相同的短生命周期特权连接，优先读取
`DATABASE_MIGRATION_URL`，且不会使用 `DATABASE_RUNTIME_URL` 做可能被 RLS
过滤成空结果的全局校验。

```bash
make credential-validate
make credential-rotate
make credential-validate
```

`credential-rotate` 只重新封装已认证的当前格式密文，遇到明文或畸形信封会
回滚并失败。若验证发现不支持的密码哈希，执行
`make credential-quarantine` 隔离账号，完成正常密码重置并审查后再重新启用。详情见
[数据库静态加密](../reference/DATA_AT_REST_ENCRYPTION.md)。
