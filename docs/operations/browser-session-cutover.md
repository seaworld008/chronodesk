# 浏览器会话 Cookie 硬切换

ChronoDesk 浏览器会话采用协调式硬切换。后端与 Web 必须在同一发布窗口升级，
不保留 JSON 或自定义 Header 中传递 refresh bearer 的兼容路径。

## 运行时契约

- 登录、无需邮箱验证的注册和刷新成功后，服务端写入
  `chronodesk_refresh_token` Cookie。
- Cookie 固定使用 `HttpOnly`、`SameSite=Strict` 和 `Path=/api/auth`；
  生产环境或 TLS 直连时同时使用 `Secure`。Cookie 不设置 `Domain`，保持为
  host-only。
- 登录、注册和刷新响应只返回内存使用的短期 `access_token`，JSON 中不得出现
  `refresh_token`。
- `POST /api/auth/refresh` 和 `POST /api/auth/logout` 必须携带 Cookie 和与
  `WEB_URL` 完全匹配的 `Origin`，且请求体必须为空。`X-Refresh-Token` 和 JSON
  或 query refresh bearer 一律返回 `400`。
- refresh 成功才覆盖 Cookie。refresh 失败不写 `Set-Cookie`，避免一个并发失败
  响应清除另一个标签页刚轮换成功的新 Cookie。
- 当前会话 logout 成功后只清 refresh Cookie；logout-all 的数据库事务成功后才
  同时清 refresh Cookie 和可信设备 Cookie。失败响应不清 Cookie。
- refresh 的并发请求复用既有的确定性短时重放机制：同一旧 Cookie 在恢复窗口内
  得到同一替代 Cookie，不会分叉出多个有效会话。

`WEB_URL` 是 refresh/logout Origin 门禁的唯一信任来源。不要从 `Host`、
`X-Forwarded-Host` 或 `X-Forwarded-Proto` 推导允许来源。部署时还必须将相同
Origin 显式列入 `CORS_ALLOWED_ORIGINS`；凭据型 CORS 不接受 `*`。

## 发布前检查

1. 确认 Web 已使用 `credentials: "include"`，刷新和 logout 发送空 body，且不再
   读取、存储或广播 refresh bearer。
2. 确认 `WEB_URL` 是无凭据、无 query/fragment 的绝对 HTTP(S) URL；生产必须为
   HTTPS。
3. 确认 `CORS_ALLOWED_ORIGINS` 包含 Web 的精确 Origin，且不包含 `*`。
4. 运行认证包、Human OpenAPI、race 和浏览器多标签页用例。
5. 发布后检查浏览器存储中没有 bearer，refresh Cookie 对 JavaScript 不可见。

## 旧会话受控撤销

代码提供了显式、可重复测试的
`AuthService.RevokeAllSessionsIssuedBefore(ctx, cutoff)` 维护边界。它按原始登录
会话时间选择目标，因此即使旧会话已在 cutoff 后轮换，新 refresh 记录也会随
原始会话一起撤销；访问令牌对应的登录历史同时失效。普通服务启动不会调用该
能力，本次变更也不会自动修改生产会话。

在上线执行全局旧会话撤销前，需要单独增加受审计的一次性 CLI：

```text
chronodesk-session-maintain revoke-before \
  --cutoff <RFC3339> \
  --change-id <受控变更编号> \
  --dry-run

chronodesk-session-maintain revoke-before \
  --cutoff <同一RFC3339> \
  --change-id <同一受控变更编号> \
  --execute \
  --confirm-browser-session-cutover
```

该 CLI 必须先输出有界计数和 cutoff，再以运行时数据库角色执行一次事务，并写入
不含令牌或 session ID 的持久化安全审计。没有 `--execute`、确认短语或变更编号
时不得写库。发布失败时可以回滚代码，但已撤销的旧会话不会复活，用户需要重新
登录。
