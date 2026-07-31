# Changelog

本文件记录 ChronoDesk 的重要变更。格式参考 [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)，版本号遵循 [Semantic Versioning](https://semver.org/spec/v2.0.0.html)。

## [Unreleased]

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
- 将跨 REST、MCP、A2A 和人类入口的工单业务语义收敛到共享领域服务。
- 将受支持的 Agent 协议基线明确为 MCP `2026-07-28` 与 A2A `1.0`。

### Security

- 将 Agent 内容和协议载荷统一视为不可信输入，并加强服务端授权、最小权限、重放/幂等与审计边界。
- 提供 GitHub Security Advisory 私密漏洞报告流程。

> 本项目从该 `Unreleased` 部分开始维护结构化 changelog；更早的开发历史请查阅 Git commit 记录。
