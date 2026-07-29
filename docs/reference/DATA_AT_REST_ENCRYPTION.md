# 数据库敏感字段静态加密

ChronoDesk 对以下长期凭据使用版本化 AES-256-GCM 信封加密：

- A2A Push Notification 的 token 与 authentication；
- SMTP 密码；
- Webhook 签名 secret 与 access token；
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

## 首次迁移

运行数据库结构迁移后，先备份数据库，再显式执行：

```bash
cd server
go run ./cmd/secret-migrate
go run ./cmd/secret-migrate -validate-only
```

同一命令会在一个外层事务中迁移长期数据库凭据与认证凭据，并在完成后同时执行
`security.ValidateDatabaseSecrets` 和 `auth.ValidateAuthCredentialStorage`。
正常服务启动只执行这两类验证，不会自动接受或迁移旧明文：发现明文、旧
SHA-256 密码哈希、密文篡改、错误 AAD、缺失 key ID 或错误密钥时会立即失败。
旧 SHA-256 密码不可安全反推，必须由管理员先重置为 bcrypt。

## 轮换

1. 生成新的 32 字节随机密钥并加入 keyring；
2. 将新的 key ID 设为 primary，旧 key 暂时保留；
3. 执行 `go run ./cmd/secret-migrate`，将旧密文重新封装为新 key；
4. 执行 `-validate-only` 并重启所有实例；
5. 确认所有实例均已加载新配置后再移除旧 key。

迁移日志只包含加密、轮换和验证的记录数量，不输出任何凭据或密文。

## Webhook 与 Push 投递

Webhook 和 A2A Push 在真正发起请求前才短暂解密凭据。Webhook 默认仅允许公网
HTTPS，解析出的全部 IP 都必须通过私网/保留地址检查，实际连接固定到已验证的
IP，并禁止代理和重定向，从而避免 DNS rebinding 与 SSRF。

Webhook 审计日志只记录严格白名单内的非敏感请求/响应头；Authorization、签名、
Cookie、回调响应正文、URL path/query 均不落盘。已知 secret/access token 还会在
保存前执行二次脱敏。
