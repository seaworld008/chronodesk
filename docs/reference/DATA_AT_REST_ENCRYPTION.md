# 数据库敏感字段静态加密

ChronoDesk 对以下长期凭据使用版本化 AES-256-GCM 信封加密：

- A2A Push Notification 的 token 与 authentication；
- SMTP 密码；
- Webhook Config 以及未粉碎、未过期 delivery snapshot 的签名 secret、
  previous secret 与 access token；
- 人类账号的 TOTP seed。

密文使用 `cdsec:v1:<key-id>:<payload>` 格式。随机 nonce 防止相同明文产生相同密文，AAD 将密文绑定到表、记录 ID 和字段，复制到其他记录或字段后将无法解密。

刷新令牌、邮箱验证令牌、密码重置令牌和一次性 OTP 使用域分离 SHA-256 摘要，
备用恢复码逐条使用 bcrypt 哈希；这些值无需恢复明文，因此不使用可逆加密。

## Keyring 配置

密钥来源按以下优先级选择：

1. 专用环境 keyring（推荐长期方案，可独立在线轮换）；
2. 若两个专用 DEK 变量均未配置，则使用
   `NewDerivedKeyring` 从稳定、高熵的 `AGENT_CREDENTIAL_PEPPER`
   经 HKDF-SHA256 和 `database-secrets/v1` 域分离派生。Pepper 绝不直接作为
   AES key。

专用 keyring 从密钥管理系统注入两个环境变量：

```bash
CHRONODESK_DATA_ENCRYPTION_PRIMARY_KEY_ID=dek-2026-07
CHRONODESK_DATA_ENCRYPTION_KEYS='{"dek-2026-07":"<32-byte-base64-key>"}'
```

`CHRONODESK_DATA_ENCRYPTION_KEYS` 是 key ID 到 Base64 密钥的 JSON 对象。每个密钥解码后必须恰好为 32 字节。生产环境应由 Vault、KMS/SSM、1Password 或同类密钥管理系统注入，禁止提交到 Git。

如果专用 keyring 只配置了一半或格式错误，系统立即失败，不会回退到派生 key。派生路径同样要求 Pepper 至少 32 字节；缺失或过短时启动失败。

## 当前格式验证

运行数据库结构迁移后，显式执行只读验证：

```bash
cd server
go run ./cmd/credential-maintain -validate-only
```

命令和正常服务启动都会执行 `security.ValidateDatabaseSecrets` 与
`auth.ValidateAuthCredentialStorage`。系统不会自动接受或转换明文：发现
明文、不支持的密码哈希、密文篡改、错误 AAD、缺失 key ID 或错误密钥时立即
失败。部署前必须从可信备份恢复当前格式，或撤销并重新签发相关凭据。

Webhook delivery snapshot 的 live 定义为
`credential_shredded_at IS NULL AND credential_expires_at > maintenanceNow`。
未粉碎但缺失有效 deadline 的行会 fail closed；已经过期或已经粉碎的行不会被
解密或 rewrap。snapshot 中复制的 envelope 继续使用原 Config ID 的历史 AAD：
`webhook_configs/<config-id>/<field>`，不能改成 snapshot UUID。

runtime 验证按可信 Project inventory 在短 RLS transaction 内逐项目执行；
maintenance rotation 使用一个顶层 transaction，按稳定 Project、Webhook Config、
live snapshot、A2A Push、global email 顺序加锁。任一坏 envelope 或 CAS 冲突会使
整次 rotation 回滚，并返回零成功报告。

不支持的密码哈希不可安全反推，可由管理员显式执行：

```bash
go run ./cmd/credential-maintain -quarantine
```

该操作用随机 bcrypt 材料替换不可验证的哈希、撤销会话并暂停活跃账号；管理员
必须完成正常密码重置，并在审查后显式重新启用账号。

## 轮换

1. 生成新的 32 字节随机密钥并加入 keyring；
2. 将新的 key ID 设为 primary，旧 key 暂时保留；
3. 执行 `go run ./cmd/credential-maintain -rotate`，将已认证的当前格式密文
   重新封装为新 key；
4. 使用 old+new keyring 执行
   `go run ./cmd/credential-maintain -validate-only`；
5. 使用只包含新 key 的隔离维护配置再次执行 `-validate-only`；
6. 确认所有实例均已加载 new-only 配置后再移除旧 key。

轮换事务遇到明文或畸形信封会完整回滚。维护日志只包含轮换和验证数量，不输出
任何凭据或密文。

## Webhook 与 Push 投递

Webhook 和 A2A Push 在真正发起请求前才短暂解密凭据。Webhook 默认仅允许公网
HTTPS，解析出的全部 IP 都必须通过私网/保留地址检查，实际连接固定到已验证的
IP，并禁止代理和重定向，从而避免 DNS rebinding 与 SSRF。

Webhook 审计日志只记录严格白名单内的非敏感请求/响应头；Authorization、签名、
Cookie、回调响应正文、URL path/query 均不落盘。已知 secret/access token 还会在
保存前执行二次脱敏。

Webhook snapshot 凭据具有不可延长的七天绝对 deadline。普通编辑、停用或
soft-delete 只影响未来 fan-out，已经冻结的投递继续使用原 snapshot 至成功或原
deadline；普通 soft-delete 不是凭据撤销。普通 `PUT`/`DELETE` 都要求强
`If-Match`，并在配置变更的同一事务 CAS durable admin resource version。

Webhook claim 把 `dispatch_started_at` 与 `locked_at` 写为同一个 claim 时间，
表示绑定当前 generation 的 prepared。紧接 HTTP client `Do` 前的短事务取得
config 生命周期锁，再以 CAS 把 marker 写为严格晚于 `locked_at` 的时间戳：

- `NULL` 表示 legacy/unknown，保守计为 in-flight；首次 claim 可以完成，崩溃后
  不能自动 reclaim；
- `dispatch_started_at == locked_at` 表示 prepared，可被紧急撤销或 stale
  reclaim 到新的 prepared generation；
- `dispatch_started_at > locked_at` 表示 dispatch authorization 已提交，可能
  已经外部可见或仍在进程内，但不证明请求已经离开进程。

config 锁是 dispatch start 与紧急撤销的线性化边界。revoke-first 会终止
prepared 投递且产生零 HTTP；start-first 会被报告为 in-flight。
dispatch-authorized marker 后的不确定崩溃不自动重发，只在原 deadline 到达后
cleanup。
PostgreSQL/SQLite tuple fence 只允许 prepared → 新 prepared 的 claim generation
变化，并强制 `attempts + 1`、`locked_at` 前进、非空 worker、新 UUIDv7 token 和
marker 重绑。它阻断旧 Worker stale-reclaim `NULL`/dispatch-authorized 行及
marker 降级。旧 Worker 首次 claim 留下的 `NULL` 可以完成；崩溃后必须等待
deadline，且绝不能批量回填或重绑为 prepared。

紧急撤销是独立的 exact `project_admin` 命令。live/tombstone preflight 只返回
配置 ID、状态、删除标记、撤销状态和版本等无秘密最小投影，并要求操作员另行完成
不可逆确认。命令以强 `If-Match` 进入同一 Project transaction，禁用配置并清空
`secret`、`previous_secret`、`previous_secret_expires_at`、`access_token`，
把 `pending/failed/dead` 以及 prepared 投递变为 `expired`，报告无法安全召回的
`NULL`/dispatch-authorized `processing`，并把所有尚未粉碎的 snapshot 凭据以
`revoked` 清空。紧急撤销是不可复活的终态。

claim、replay、cleanup、finalize 和双 HTTP gate 使用相同的
Project/config/delivery/snapshot 锁序，因此撤销事务先提交时不能开始新的外部
请求。撤销或过期不设置父 Domain Event 的 `PublishedAt`，也不清空或改写既有
`PublishedAt`。

应用层 credential shred 是逻辑 crypto-shred，不等于从 PostgreSQL WAL、replica、
PITR 增量、云快照或离线备份中即时物理擦除旧密文。恢复到撤销前时间点必须先在
Webhook egress 关闭的隔离环境重放撤销记录，再运行 new-only keyring 验证和零
HTTP 演练。具体发布、回滚、备份和恢复步骤见
[Webhook 凭据生命周期与紧急撤销运行手册](../operations/webhook-credential-lifecycle.md)，
架构约束见
[ADR-0009](../adr/0009-webhook-delivery-credential-lifecycle.md)。
