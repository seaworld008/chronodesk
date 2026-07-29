# 为 ChronoDesk 贡献 / Contributing to ChronoDesk

感谢你帮助改进 ChronoDesk。无论是缺陷报告、设计讨论、文档还是代码贡献，都请遵守本项目的 [行为准则](CODE_OF_CONDUCT.md)。

## 开始之前

- 搜索现有 Issue 和 Discussions，避免重复工作。
- 对较大的功能、协议或架构变更，先在 Discussions 提案并确认方向。
- 每个 Issue 和 Pull Request 聚焦一个可审查的目标；不要顺带重构无关代码。
- 安全漏洞不得提交公开 Issue。请按 [安全策略](SECURITY.md) 使用 GitHub Security Advisory 私密报告。
- 不要提交凭据、令牌、私钥、真实客户数据或未经脱敏的日志。若秘密曾进入提交历史，仅删除文件并不足够，请立即轮换秘密并联系维护者。

提交贡献即表示你同意按照仓库的 [Apache License 2.0](LICENSE) 提供该贡献。

## 仓库结构

- `server/` 是独立的 Go 模块，API、领域服务、协议 Adapter 和数据库迁移均位于其中。
- `web/` 是 Vite + React 管理端，业务页面位于 `web/src/admin/`，共享能力位于
  `web/src/lib/`、`web/src/components/` 与 `web/src/i18n/`。
- 根目录的 `Makefile` 与 `docker-compose.yml` 是本地工作流的稳定 Interface；
  不再维护散落的一次性脚本。

开始修改前请阅读 `AGENTS.md`、`CONTEXT.md` 和 `ARCHITECTURE.md`。若文档与实现不一致，请在 PR 中明确指出，不要悄悄建立第二套约定。

## 架构与协议约束

领域服务是业务语义的唯一来源。REST、MCP 和 A2A Adapter 只负责认证上下文绑定、协议校验、输入输出转换与调用领域服务，不得复制状态迁移、权限、幂等、审计或副作用规则。一个业务动作经不同入口调用时必须得到一致的授权与结果。

当前 Agent 协议基线仅包括：

- MCP specification revision `2026-07-28`
- A2A 官方发布 `v1.0.1`（wire `1.0`）

协议贡献必须对齐上述版本并附上相应契约或互操作测试。不要添加旧版、草案版或自定义兼容分支，除非维护者已在设计讨论中明确接受。

所有 Agent 提供的内容都必须视为不可信输入，包括工具参数、任务载荷、回调地址、资源内容和模型生成文本。变更应保持最小权限、服务端授权、边界校验、幂等保护和可审计性；不得把提示词、客户端声明或 Adapter 侧判断当作安全边界。

## 本地开发

需要 Go、Node.js/npm、Python、Docker 与 Docker Compose。先检查本机工具链，再安装可重复依赖：

```bash
make doctor
make install-deps
```

复制 `server/.env.example` 为 `server/.env`，仅填写本地开发值，切勿提交该文件。启动完整开发环境：

```bash
make dev
docker compose exec server chronodesk-migrate -seed
```

也可以分别运行：

```bash
make server-dev
make web-dev
```

默认 API 地址为 `http://localhost:8081`，Web 开发地址为 `http://localhost:3000`。

数据库结构变更应在 `server/cmd/migrate/` 中提供可审查的迁移，并在本地执行：

```bash
make db-migrate
```

不要在共享或生产环境运行破坏性迁移命令。

## 代码规范

Go 代码保持 `gofmt` 格式、包名小写、导出标识符使用 CamelCase。提交前运行：

```bash
make fmt
make fmt-check
make test-server
```

React 文件使用 PascalCase，Hook 使用 `useX` 命名，并使用 `@/*` alias 避免深层
相对导入。优先复用已有 MUI/react-admin 与企业表格 Module。

新增或修改行为时：

- Go handler 与 service 使用表驱动测试覆盖成功、拒绝和边界路径。
- 协议 Adapter 同时验证协议契约与领域语义一致性。
- 安全敏感路径覆盖越权、重放、重复提交和不可信输入。
- UI 变更包含类型检查、lint，并在 PR 中附截图或短视频。

## 测试

选择与改动范围相称的最小测试集，并在 PR 中准确列出执行结果。

```bash
# Go 测试与 vet
make test-server

# Go 竞态检测
make test-race

# Web 类型、lint 与依赖安全门禁
make test-web

# OpenAPI 契约
make openapi-lint

# Go 与 Web 依赖安全检查
make security

# API smoke suites（需要测试依赖和运行中的 API）
make smoke

# Playwright 端到端测试（需要运行中的完整环境）
make e2e

# 完整格式、测试、安全与生产构建门禁
make verify
```

根目录 `make test` 会运行 Go、Web 和 OpenAPI 质量门禁。若某项检查因环境限制未运行，请在 PR 中说明原因；不要将其写成已通过。

## Commit 与 Pull Request

Commit 使用简短、祈使语气的 [Conventional Commits](https://www.conventionalcommits.org/) subject，例如：

```text
feat: add agent lease renewal
fix: reject replayed a2a task updates
docs: clarify mcp compatibility policy
```

Pull Request 应：

- 说明问题、方案、影响范围和明确不在范围内的内容。
- 关联 Issue 或 Discussion，并保持为一个逻辑变更。
- 标明对 `server`、`web`、数据迁移、API、MCP、A2A 和安全边界的影响。
- 列出实际执行的测试及结果；UI/API 改动附截图或示例载荷。
- 同步必要的契约、迁移、文档和 changelog。
- 确认没有秘密、个人数据、生成产物或无关格式化改动。

维护者可能要求拆分过大的 PR。Review 意见应围绕实现和影响，遵守 [行为准则](CODE_OF_CONDUCT.md)。

## 获取帮助

使用方式和排障问题请见 [支持说明](SUPPORT.md)。路线建议优先在 [GitHub Discussions](https://github.com/seaworld008/chronodesk/discussions) 讨论；已确认、可复现的缺陷再使用 Issue Form。
