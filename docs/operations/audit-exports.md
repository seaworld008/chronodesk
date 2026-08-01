# 平台审计导出运行手册

平台审计中心使用异步任务生成脱敏 CSV。每个导出最多覆盖 30 天和
100,000 行，文件在 24 小时后过期；创建、状态查询、下载和清理都沿用平台身份
与审计边界。导出文件不是数据库备份，也不能绕过审计中心的字段脱敏规则。

## 存储拓扑

当前版本只提供 `local` 文件适配器。生产部署必须显式选择一种拓扑：

- `single`：一个 ChronoDesk 应用实例和一个内置导出 Worker，文件目录位于该
  实例的持久卷。
- `shared-rwx`：多个应用实例共享同一个支持 ReadWriteMany 的持久卷；所有实例
  的 `AUDIT_EXPORT_STORAGE_DIR` 必须指向同一目录。

`CHRONODESK_REPLICA_COUNT` 大于 1 时只接受 `shared-rwx`。不要为多实例分别挂载
本地盘，否则任务可能在一个实例生成、却由另一个实例处理下载。生产环境缺少明确
模式、目录或 Worker ID 时服务会拒绝启动。

最小单实例配置：

```dotenv
AUDIT_EXPORT_STORAGE_BACKEND=local
AUDIT_EXPORT_STORAGE_DIR=/var/lib/chronodesk/audit-exports
AUDIT_EXPORT_LOCAL_DEPLOYMENT_MODE=single
CHRONODESK_REPLICA_COUNT=1
AUDIT_EXPORT_WORKER_ID=chronodesk-audit-export-1
AUDIT_EXPORT_POLL_INTERVAL=5s
AUDIT_EXPORT_CLEANUP_INTERVAL=15m
```

目录必须位于持久卷，且只授权 ChronoDesk 运行身份读写。不要通过静态文件服务器
公开该目录；下载必须经过受保护的 Human API，以便检查任务所有者、过期时间并
记录下载审计。

## 运维检查

发布前验证：

1. 所有实例看到同一个目录和一致的拓扑参数。
2. 每个 Worker ID 在同一部署中唯一且重启后稳定。
3. 创建小范围导出后，任务从 `pending` 进入 `completed`，仅创建者可以下载。
4. 过期任务返回 `410 Gone`，清理周期后文件已删除。
5. 导出 CSV 中 IP 已脱敏，以 `=`, `+`, `-`, `@` 开头的单元格不会被表格软件当作公式执行。

备份策略应备份数据库中的审计原始记录，而不是这些短期派生文件。扩缩容前如需从
`single` 切换到 `shared-rwx`，先停止导出 Worker，迁移未过期文件和持久卷，再以
一致配置启动全部实例。
