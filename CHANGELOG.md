# Changelog

本文件记录 ChronoDesk 的重要变更。格式参考 [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)，版本号遵循 [Semantic Versioning](https://semver.org/spec/v2.0.0.html)。

## [Unreleased]

### Added

### Changed

### Fixed

### Security

## [0.2.0] - 2026-08-11

### Added

- 提供面向企业运营的分组导航、项目生命周期管理、Membership 范围工作台、可分页目录与
  可持久化列宽，并补全登录、工作台、工单、知识库、设置和个人中心的员工旅程。
- 建立项目知识库的文章、版本、ACL、来源、对象存储与摄取任务生命周期；普通成员可通过
  显式 `knowledge_contributor` capability 提交个人管理的草稿，发布仍由项目管理角色控制。
- 提供项目级 Webhook 凭据有限生命周期：投递快照使用加密凭据和有限重放，项目管理员可在
  版本与幂等前置条件下执行紧急撤销，并配套 ADR 与运维手册。
- 提供有界管理员审计导出、目录分页索引以及附件/知识对象存储身份迁移与运行时门禁。

### Changed

- 统一企业列表、项目切换、设置范围和 Human Web 契约；跨项目聚合只来源于 active
  Membership，平台角色仍不构造 ProjectScope。
- 工作流允许多个状态映射到同一 lifecycle category，以兼容合法的重复阶段配置，同时继续
  通过状态标识符执行精确转换。
- 注册流程在一个数据库事务内提交用户、Profile、验证记录与登录会话，任何后续写入失败都会
  回滚整个注册。
- 统一构建、容器、Web、SDK 与 Agent OpenAPI 的产品版本为 `0.2.0`；运行时构建身份保持
  权威，显式 `APP_VERSION` 不一致时启动失败，迁移幂等维护只读的 `system.version` 投影。

### Fixed

- 修复 MUI 9 Autocomplete slot 合并导致的原生输入引用丢失，并稳定工单创建、编辑和分类选择
  的浏览器交互契约。
- 修复 same-status 请求伪造成功转换的问题；未发生状态变化时不再产生虚假的历史、事件或
  版本递增。
- 修复注册失败后仍可能保留用户、Profile、验证记录或会话关联的问题，并拒绝不完整的事务
  仓储实现。
- 修复 Webhook 紧急撤销在并发、过期凭据、终态投递、资源版本与锁顺序上的边界；已提交的
  普通投递仍遵循有限生命周期，撤销后的快照不可再次重放。
- 收紧附件 smoke 契约、迁移恢复点、PostgreSQL/RLS、凭据维护和运行时依赖健康门禁。

### Security

- 强化项目角色、active Membership、对象 ACL 与平台紧急职责的独立授权，防止平台身份被
  解释为项目访问。
- Webhook 紧急撤销在单一事务内禁用配置、清除当前与快照密文、终止未完成投递并写入脱敏
  审计事实；日志、响应与事件不包含 URL、凭据或密文。
- 将 React Router 固定到包含官方安全回移的 `7.18.2`，并让依赖策略对新增生产漏洞与不兼容
  的 Router 主版本升级保持 fail-closed。
- 加强浏览器健康、认证原子性、秘密扫描、凭据验证和数据库契约检查，避免配置、依赖或迁移
  漂移绕过发布门禁。

## [0.1.0] - 2026-07-31

### Added

- 单 Organization、多 Project 的角色边界：`PlatformRole` 与
  `ProjectRole` 使用独立的封闭枚举，平台角色不再隐式授予项目访问。
- `/api/platform/*` 平台治理、`/api/projects/{projectKey}/*` 项目工作和
  `/api/workbench/*` Membership 范围工作台，以及独立的
  `/human-openapi.json` Human Web P1 契约。
- 角色切换 checkpoint `20260730_platform_roles_v1_cutover` 与
  ADR-0008，记录历史角色的 fail-closed 迁移和实时授权边界。
- Agent-native 服务主体、策略、工单租约、幂等、审计、领域事件与 Outbox 基础能力。
- 共享领域 Module 之上的 Agent REST、MCP `2026-07-28` 与 A2A v1.0.1 /
  wire `1.0` Adapter。
- Agent 管理控制面，用于凭据、策略、只读/紧急停止和运行状态管理。
- Apache License 2.0、贡献、安全、支持、行为准则、路线图和 GitHub 社区模板。

### Changed

- Human JWT 只携带并实时复核平台角色；项目角色由每个项目请求的 active
  Membership 实时解析，角色或身份失配按 `stale_token` 拒绝。
- Human Web 仅在匹配当前 Project Key 的 `project_access_revoked` 响应后清理
  项目选择；角色不足、对象 ACL 等普通 `403` 不再误触发项目切换。
- 将跨 REST、MCP、A2A 和人类入口的工单业务语义收敛到共享领域服务。
- 将受支持的 Agent 协议基线明确为 MCP `2026-07-28` 与 A2A `1.0`。

### Security

- 将 Agent 内容和协议载荷统一视为不可信输入，并加强服务端授权、最小权限、重放/幂等与审计边界。
- WebSocket 最终授权、帧写入与 Membership 撤权使用同一进程内线性化边界，
  撤权完成后不会再投递已排队通知。
- 提供 GitHub Security Advisory 私密漏洞报告流程。

> 本项目从该 `Unreleased` 部分开始维护结构化 changelog；更早的开发历史请查阅 Git commit 记录。

[Unreleased]: https://github.com/seaworld008/chronodesk/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/seaworld008/chronodesk/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/seaworld008/chronodesk/releases/tag/v0.1.0
