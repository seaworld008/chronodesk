## 变更摘要 / Summary

<!-- 说明解决了什么问题、为什么需要，以及方案的核心。 -->

## 关联工作 / Related work

<!-- 使用 Closes #123，或链接对应的 Issue / Discussion。 -->

## 影响范围 / Impact

- [ ] Go server / 领域服务
- [ ] React web
- [ ] 数据库迁移
- [ ] REST / OpenAPI
- [ ] MCP `2026-07-28`
- [ ] A2A `1.0`
- [ ] 安全、权限或隐私
- [ ] 文档或社区文件

### Agent、协议与安全影响

<!--
说明 Agent 输入、服务主体/scope、授权、幂等、租约、审计、回调或协议兼容性影响。
如无影响，请写“无 / None”并说明判断依据。
-->

## 验证 / Verification

<!-- 只列出实际执行的命令和结果；未执行的检查请说明原因。 -->

```text
command — result
```

## 截图或示例 / Screenshots or examples

<!-- UI/API 行为变更请附截图、短视频或脱敏的示例载荷；不适用可删除。 -->

## 提交前检查 / Checklist

- [ ] PR 聚焦一个逻辑变更，不包含无关重构或格式化。
- [ ] 我已阅读 `AGENTS.md`、`CONTEXT.md`、`ARCHITECTURE.md` 和 `CONTRIBUTING.md`。
- [ ] 领域服务仍是业务语义的唯一来源；Adapter 未复制业务规则。
- [ ] 所有 Agent/模型/协议内容均按不可信输入处理，并保持服务端授权与最小权限。
- [ ] 新增或改变的行为有相称的测试，契约、迁移、文档和 changelog 已同步。
- [ ] 未提交秘密、令牌、私钥、真实客户数据、个人信息或未经脱敏的日志。
- [ ] Commit subject 遵循 Conventional Commits。
