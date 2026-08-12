# CI 流水线与分支保护

ChronoDesk 的完整 Smoke 在 Pull Request 的最新合并结果上执行一次。GitHub 的
`pull_request` 事件会检出 `refs/pull/<number>/merge`；`main` 使用严格 required
checks，要求 PR 基于最新目标分支后重新通过门禁。因此 squash merge 不再对相同源码
树重复运行约 24 分钟的 Docker、API 和浏览器套件。

## 强制门禁

`main` 的权威配置记录在 [`.github/main-branch-protection.json`](../../.github/main-branch-protection.json)。
仓库管理员应用该文件后必须从 GitHub API 回读并比较。关键约束如下：

- `smoke`、`secrets`、`go`、`web`、`analyze (go)` 和
  `analyze (javascript-typescript)` 以及可信 `ci-policy` 状态均绑定
  GitHub Actions App `15368`；
- required checks 使用 strict 模式，PR 必须基于最新 `main`；
- 管理员同样受规则约束，所有变更必须经 PR；
- 禁止 force push 和删除 `main`，要求线性历史与 review conversation 全部解决；
- 个人仓库只有一个维护者，审批数为 `0`，避免要求作者无法给自己的 PR 审批。

如保护规则缺失或漂移，不能以“PR 已通过”为由关闭 `main` 的完整回归；应先恢复
保护规则。需要重跑时使用 PR 上既有 Smoke 的 re-run，或在一次性本地环境执行
`make smoke` 与 `make e2e`；required `smoke` 不暴露 `workflow_dispatch`，避免手动
分支运行冒充 PR 合并结果。

## 运行模型

- required Smoke 只由面向 `main` 的 PR 触发。
- 同一 PR 推送新提交时，旧 Smoke 自动取消，只保留最新合并结果。
- CodeQL 与 Dependency Security 仍在 PR 和 `main` push 上运行；它们约 2–3 分钟，
  用于更新默认分支安全状态，不重复 Docker/E2E。
- 成功 Smoke 不上传报告；失败时才生成并扫描脱敏 Pytest HTML。浏览器
  trace、video、截图、HAR 和 ZIP 仍禁止上传。
- 默认分支上的 policy 只通过 GitHub API 读取 PR 与文件清单，不 checkout、
  下载 artifact 或执行 PR 内容。普通 PR 由 `pull_request_target` 立即判定；
  Dependabot 由 Smoke 完成后的 `workflow_run` 使用默认分支写令牌判定。外部
  贡献者修改 workflow 或保护声明时，它会在 PR head 写入失败的 `ci-policy`
  状态；维护者提交仍可更新 CI。`workflow_dispatch` 只重新评估指定开放 PR 的
  同一策略，不执行测试、不能改变判定逻辑，也不会产生 required `smoke`。
- policy 同时检查重命名前后的路径、PR 文件总数与实际仓库写权限。GitHub 文件
  清单截断、权限查询异常或 PR/Smoke 关联不唯一时不会签发成功状态。

Playwright 在 CI、本地、共享和远端环境均使用单 worker，`fullyParallel` 固定为
`false`，避免长链路导航、视觉截图、全局配置与账号会话在并发负载下产生假阴性。
测试数据仍通过进程 ID 和本轮 run ID 隔离。

## 变更验证

修改 workflow、分支保护或 Playwright 并行策略时至少执行：

```bash
cd web
npm run test:ci-runtime
npm run test:e2e:list
npm run typecheck
npm run lint
```

并在一次性 Compose 环境中运行完整 E2E：

```bash
CI=1 \
CHRONODESK_E2E_OWNERSHIP_PREFIX=e2e-<唯一所有者> \
CHRONODESK_E2E_RUN_ID=<唯一运行标识> \
TEST_BASE_URL=http://127.0.0.1:3000 \
npx playwright test --workers=1
```

必须保持完整用例数、零 `429`、零清理冲突、零残留本轮 marker。正式 PR Smoke
使用仓库配置的 retry 策略。

合并后核对：

1. `main` 没有启动第二次完整 Smoke；
2. CodeQL 与 Dependency Security 正常更新默认分支状态；
3. 分支保护 API 与仓库声明一致；
4. 直接推送、force push 与删除 `main` 均被拒绝。
