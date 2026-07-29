# 支持 / Support

本项目由社区维护，不提供保证响应时间的商业支持。为了让问题尽快到达正确位置，请按下表选择渠道。

| 需求 | 渠道 |
| --- | --- |
| 使用问题、部署排障、方案讨论 | [GitHub Discussions](https://github.com/seaworld008/chronodesk/discussions) |
| 已确认且可复现的缺陷 | [GitHub Issues](https://github.com/seaworld008/chronodesk/issues/new/choose) |
| 功能建议 | Feature request Issue Form 或 Discussions |
| 未修复的安全漏洞 | [私密 Security Advisory](https://github.com/seaworld008/chronodesk/security/advisories/new) |
| 行为准则事件 | 按 [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) 私下联系维护者 |

请勿用公开 Issue 报告安全漏洞、秘密泄露、真实个人数据或行为准则事件。

## 提问前

1. 阅读仓库文档、现有 Discussions 和 Issues。
2. 使用 `main` 最新版本或最新正式标签复现。
3. 确认协议版本属于当前支持基线：MCP `2026-07-28` 或 A2A `1.0`。
4. 将问题缩减为最小复现，并移除无关自定义代码。
5. 脱敏日志和配置。请使用占位符，不要仅遮住秘密的一部分。

## 提供诊断信息

请包含 ChronoDesk commit SHA/版本、操作系统、Go/Node/Docker 版本、部署方式、受影响模块、预期与实际结果、复现步骤，以及已经尝试过的排查方式。协议问题还应包含入口、协议版本、请求 ID/任务 ID、返回状态和经过脱敏的最小载荷。

Agent 相关问题请说明：

- 使用的服务主体类型与 scope（不要提供令牌）。
- 入口是 REST、MCP 还是 A2A。
- 是否涉及策略拒绝、租约、幂等、重试、回调或紧急只读模式。
- 相同行为从其他协议入口调用时是否一致。

日志中不得包含访问令牌、Cookie、密码、私钥、连接串、真实客户内容或个人身份信息。如果秘密可能已泄露，请先轮换/撤销，然后按 [SECURITY.md](SECURITY.md) 私密联系维护者。

## 支持边界

维护者优先处理可复现缺陷、安全问题和当前路线图内的工作。旧 commit、修改后的 fork、非标准协议扩展、MCP 非 `2026-07-28` 版本和 A2A 非 `1.0` 版本仅尽力协助。维护者可能将不完整的 Issue 转为 Discussion，或在长期无反馈后关闭。
