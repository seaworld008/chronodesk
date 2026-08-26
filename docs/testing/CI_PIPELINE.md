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
- 默认分支上的 policy 只通过 GitHub API 读取 PR、当前权限与完整文件清单，不
  checkout、下载 artifact 或执行 PR 内容。普通 PR 由 `pull_request_target`
  立即判定；`workflow_run` 没有写 `ci-policy` 的入口，不能用 Smoke 结果覆盖
  普通 PR 的策略裁定。
- 外部贡献者只修改普通业务或文档文件时可以通过。涉及 CI 控制面时，PR 必须保持
  开放、目标为本仓库 `main`、head 来自本仓库，而且 PR 作者与本次
  `pull_request_target` 的 sender 在判定当时都必须具有 `write`、`maintain`
  或 `admin` 权限。fork、机器人、只读作者或只读 sender 均不能自动获得可信状态。
- 受保护控制面包括 workflows、复合 Actions、Dependabot、CODEOWNERS、分支保护
  声明、Makefile、Compose、Dockerfile、各生态 package/lock、Playwright/pytest
  与由 CI 直接执行的脚本。policy 同时检查重命名前后的路径，并要求
  `changed_files` 为不超过 3000 的完整清单；截断、计数不一致和 API 异常均失败
  关闭。
- 每次判定开始时固定 PR head SHA，写 status 前重新读取 PR。SHA 在评估期间发生
  变化时不向旧 SHA 或新 SHA 写入结果，等待 `synchronize` 对新 head 重新判定。
- 所有机器人 PR 默认写 `ci-policy: failure`。唯一人工例外是同仓库
  `dependabot/github_actions/` PR：仓库写入者通过 `workflow_dispatch` 提交 PR
  编号、当前 40 位小写 head SHA、显式批准布尔值与原因。流程会再次验证 actor
  权限、Dependabot 身份、同仓库、head ref、完整文件清单和 SHA；成功只写给输入的
  精确 SHA。PR 更新后旧批准不会继承，新 head 会恢复为默认 failure。

Playwright 在 CI、本地、共享和远端环境均使用单 worker，`fullyParallel` 固定为
`false`，避免长链路导航、视觉截图、全局配置与账号会话在并发负载下产生假阴性。
测试数据仍通过进程 ID 和本轮 run ID 隔离。

## Dependabot GitHub Actions 批准

先从 GitHub 读取当前 SHA，不得使用通知邮件、旧日志或本地分支记录中的 SHA：

```bash
repo=seaworld008/chronodesk
pr=<Dependabot-PR-编号>
head_sha="$(
  gh pr view "$pr" --repo "$repo" \
    --json headRefOid --jq .headRefOid
)"
test "${#head_sha}" -eq 40

gh workflow run ci-policy.yml --repo "$repo" --ref main \
  -f pull_request="$pr" \
  -f expected_head_sha="$head_sha" \
  -f approve_dependabot_update=true \
  -f reason='已审阅固定 Action 版本、上游发布说明与权限差异'
```

批准运行成功后必须回读精确提交状态，并确认 `ci-policy` 的最新状态来自该运行：

```bash
gh api "repos/$repo/commits/$head_sha/status" \
  --jq '.statuses[] |
    select(.context == "ci-policy") |
    [.state, .sha, .target_url] | @tsv'
```

错误 SHA、非 GitHub Actions ecosystem、fork、非 Dependabot 身份或无写权限 actor
不得通过。不要用 GitHub API 手工伪造 commit status。异常与受控恢复步骤见
[CI policy 安全恢复](../operations/CI_POLICY_RECOVERY.md)。

## 变更验证

修改 workflow、分支保护或 Playwright 并行策略时至少执行：

```bash
cd web
npm run test:ci-policy
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
5. `ci-policy` live canary 覆盖外部普通文件成功、外部控制面失败、同仓库双写权限
   成功、Dependabot 默认失败、错误 SHA 批准失败，以及正确 SHA 批准成功后更新
   head 再次失败。
