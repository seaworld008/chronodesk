# Changelog

本文件记录 ChronoDesk 的重要变更。格式参考 [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)，版本号遵循 [Semantic Versioning](https://semver.org/spec/v2.0.0.html)。

## [Unreleased]

### Added

- Agent-native 服务主体、策略、工单租约、幂等、审计、领域事件与 Outbox 基础能力。
- 共享领域 Module 之上的 Agent REST、MCP `2026-07-28` 与 A2A v1.0.1 /
  wire `1.0` Adapter。
- Agent 管理控制面，用于凭据、策略、只读/紧急停止和运行状态管理。
- Apache License 2.0、贡献、安全、支持、行为准则、路线图和 GitHub 社区模板。

### Changed

- 将跨 REST、MCP、A2A 和人类入口的工单业务语义收敛到共享领域服务。
- 将受支持的 Agent 协议基线明确为 MCP `2026-07-28` 与 A2A `1.0`。

### Security

- 将 Agent 内容和协议载荷统一视为不可信输入，并加强服务端授权、最小权限、重放/幂等与审计边界。
- 提供 GitHub Security Advisory 私密漏洞报告流程。

> 本项目从该 `Unreleased` 部分开始维护结构化 changelog；更早的开发历史请查阅 Git commit 记录。
