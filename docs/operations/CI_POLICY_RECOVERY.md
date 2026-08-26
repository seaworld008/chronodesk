# CI policy 安全恢复

本文用于 required `ci-policy` 缺失、失败或与 PR 当前 head 不一致时的受控恢复。
目标是恢复默认分支上的可信判定，不是绕过分支保护。任何时候都不得 checkout、
执行或上传 `pull_request_target` 中的 PR 内容，也不得手工创建一个冒充 policy 的
成功 commit status。

## 先确认当前事实

所有诊断都绑定 PR 当前 head。先记录 PR 编号、head SHA、head 仓库、作者、目标分支
和 workflow run；不要复用旧审批记录：

```bash
repo=seaworld008/chronodesk
pr=<PR-编号>

gh pr view "$pr" --repo "$repo" \
  --json number,state,author,baseRefName,headRefName,headRefOid,isCrossRepository,url

head_sha="$(
  gh pr view "$pr" --repo "$repo" \
    --json headRefOid --jq .headRefOid
)"
gh api "repos/$repo/commits/$head_sha/status" \
  --jq '.statuses[] |
    select(.context == "ci-policy") |
    [.state, .sha, .description, .target_url] | @tsv'
```

从 `target_url` 打开对应运行并确认事件类型。可信自动判定只能来自
`pull_request_target`；Dependabot GitHub Actions 人工批准只能来自
`workflow_dispatch`。出现 `workflow_run`、未知 workflow 或 SHA 不一致时，将其
视为不可接受证据。

## 常见失败及处置

### 普通外部 PR 修改控制面

外部贡献者可以修改业务代码和文档，但不能让 fork 中的 CI 控制面自动获得可信
状态。维护者完成逐行审阅后，应在本仓库新建自己的分支，按最小范围重新实现或
挑选已理解的变更，再由该维护者创建新 PR。不要为了通过 policy 临时授予贡献者
仓库写权限。

### 作者或 sender 权限不足

受保护变更要求 PR 作者和触发当前事件的 sender 同时具有 `write`、`maintain` 或
`admin` 权限。如果合法内部 PR 因临时 GitHub API 故障失败，可在故障恢复后从原
运行执行 failed-job rerun：

```bash
gh run rerun <run-id> --repo "$repo" --failed
```

不要通过普通 `workflow_dispatch` 重评任意 PR；该入口只批准 Dependabot GitHub
Actions 精确 SHA。

### head 在判定期间更新

policy 会拒绝在竞态期间写状态。等待 `synchronize` 为新 head 启动新运行，重新
读取 `headRefOid`，并只检查新 SHA。旧 SHA 的成功或失败都不能用于当前 head。

### 文件清单不完整或超过 3000

GitHub Pull Request Files API 最多可可靠枚举 3000 个文件。计数不一致、无效计数
或超过上限时必须拆分 PR；不得添加白名单、忽略剩余文件或手工补成功状态。

### Dependabot GitHub Actions

机器人 PR 默认失败。只有当前仓库写入者可按
[CI 流水线文档](../testing/CI_PIPELINE.md#dependabot-github-actions-批准)批准本仓库
`dependabot/github_actions/` PR 的当前精确 SHA。批准前至少检查：

1. PR 作者确为 `dependabot[bot]`，head 仓库为本仓库；
2. 所有变更都位于 `.github/workflows/` 或 `.github/actions/`；
3. Action 固定到已审阅的不可变提交，权限没有扩大；
4. 输入 SHA 等于刚从 GitHub API 读取的 `headRefOid`；
5. 原因可供后续审计，且不包含凭据或其他敏感信息。

错误批准运行不会给当前 head 写 success。Dependabot 更新 head 后必须重新审阅并
以新 SHA 批准。

## policy 自身损坏时的最小恢复

优先 re-run 暂时失败的 workflow。只有默认分支上的 policy 语法或逻辑已经损坏、
导致任何修复 PR 都无法生成 required context 时，仓库管理员才可启动紧急恢复：

1. 保存 GitHub API 回读的完整 `main` protection、失败运行 URL、当前 main SHA 和
   修复 PR SHA，并取得另一名维护者或安全责任人的明确批准。
2. 准备一个只回滚或修复 `.github/workflows/ci-policy.yml` 的最小 PR；其余 required
   checks 必须全部通过，PR conversation 必须解决。
3. 临时从 required checks 中只移除 `ci-policy`。不得关闭管理员约束、strict
   checks、线性历史、conversation resolution，也不得允许 force push、删除 main
   或直接推送。
4. 合并最小修复 PR 后，立即用仓库中的
   `.github/main-branch-protection.json` 恢复完整保护并从 API 回读比较。
5. 执行本文的 live canary；在所有场景通过前保持发布冻结。

恢复完整保护的命令如下，执行前应确认当前 checkout 是已审阅的修复后 `main`：

```bash
repo=seaworld008/chronodesk
gh api --method PUT \
  "repos/$repo/branches/main/protection" \
  --input .github/main-branch-protection.json

gh api "repos/$repo/branches/main/protection"
```

紧急变更的开始时间、批准人、API 回读、修复 PR、恢复时间和 canary 结果应进入私有
安全事件记录。不得在公开 Issue 中粘贴可能暴露仓库治理细节或凭据的运行日志。

## 恢复后 live canary

在一次性测试分支/PR 中逐项验证，并以每个 PR 的当前 head SHA 回读状态：

1. 外部 fork 只修改普通文档：`ci-policy` success；
2. 外部 fork 修改受保护控制面：failure；
3. 本仓库作者与 sender 都有写权限并修改控制面：success；
4. Dependabot GitHub Actions 新 head：默认 failure；
5. 错误 SHA 或无权限 actor 的人工批准：运行失败且不产生 success；
6. 正确 SHA 的人工批准：只给该 SHA success；
7. Dependabot 再次更新 head：新 SHA 默认 failure，旧批准不继承；
8. 直接推送、force push 与删除 `main` 继续被分支保护拒绝。

完成 canary 后删除一次性分支；保留运行 URL、SHA 与 API 回读作为验收证据。
