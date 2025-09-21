# 工单管理系统 · 待办任务拆解

> 依据 2025-09-20 的巡检结果整理，可随开发进度更新状态。

## 1. 前端代码质量
- [x] **FE-LINT-01**：清理 `web/src/admin/automation/**/*` 中的 `any` 与未使用变量，保证 `npm run lint` 通过。
- [x] **FE-LINT-02**：为 `AutomationLogList` 等组件添加类型定义与错误处理，替换占位 `any`。
- [x] **FE-LINT-03**：处理 `settings/EmailSettings.tsx`、`settings/SystemSettings.tsx`、`settings/WebhookSettings.tsx` 中的依赖警告（缺少 deps、未使用变量）。
- [ ] **FE-LINT-04**：将 `make test-web` 更新为执行 `npm run lint`，并在 README 中同步说明。
- [x] **FE-LINT-05**：清理 `web/src/admin/users/**/*` 中的 `any` 与未使用导入。
- [x] **FE-LINT-06**：类型化 `web/src/layout/CustomLayout.tsx` 及其 props。
- [x] **FE-LINT-07**：为 `web/src/lib/apiClient.ts` 补充请求/响应泛型，移除 `any`。
- [x] **FE-LINT-08**：重构 `web/src/lib/dataProvider.ts`（查询/过滤/响应类型），解决 `any` 与 `no-case-declarations`。
- [x] **FE-LINT-09**：整理 `web/src/lib/retry.ts`，补充泛型和错误类型。
- [x] **FE-LINT-10**：补齐 `web/src/types/index.ts` 剩余 `any`（如 `ApiResponse`、`TicketHistory` 等）。

## 2. 认证与安全
- [x] **SEC-AUTH-01**：完善 OTP 发送与验证流程（当前 handler 为 stub）。
- [x] **SEC-AUTH-02**：替换 `main.go` 中的 JWT `TODO`，实现 token 解析与用户校验。
- [x] **SEC-AUTH-03**：在 `services/ticket_service.go` 等敏感操作中补充管理员角色校验。
- [x] **SEC-AUTH-04**：将管理员操作审计日志写入持久化存储，并提供查询接口。

## 3. WebSocket 与通知
- [ ] **WS-NOTICE-01**：实现未读消息数量计算（`websocket/integration.go`、`client.go` 中的 `TODO`）。
- [ ] **WS-NOTICE-02**：支持消息确认/已读回执，并回写到数据库。
- [ ] **WS-NOTICE-03**：完善通知模板环境变量读取（`notification_service.go` 中的默认值 `TODO`）。

## 4. 自动化体系
- [ ] **AUTO-LOG-01**：将自动化执行的动作 (`actions_executed`) 与变更 (`changes`) 正确写入日志表。
- [ ] **AUTO-LOG-02**：补全 SLA 工作时间计算逻辑（`automation_service.go` `TODO`）。
- [ ] **AUTO-LOG-03**：为自动化日志前端增加详情抽屉，展示完整动作/错误上下文。

## 5. 数据模型与序列化
- [ ] **MODEL-JSON-01**：为 TicketHistory、TicketComment、Notification 等 JSON 字段引入自定义类型或辅助函数完成反序列化。
- [ ] **MODEL-JSON-02**：提供统一的 JSON 序列化/反序列化工具，避免直接字符串拼装。

## 6. 文档与流程
- [ ] **DOC-01**：将 Swagger (`make swagger`) 纳入标准流程，并在 README 中描述。
- [ ] **DOC-02**：为上面各任务提供完成后的更新记录（项目总览/README）。

---
最后更新时间：2025-09-20
