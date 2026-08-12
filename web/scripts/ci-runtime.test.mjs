import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import { parse } from 'yaml'

const webRoot = fileURLToPath(new URL('..', import.meta.url))
const repositoryRoot = fileURLToPath(new URL('../..', import.meta.url))

const readRepositoryFile = (path) =>
  readFile(new URL(path, new URL(`file://${repositoryRoot}/`)), 'utf8')

const loadWorkflow = async (name) =>
  parse(await readRepositoryFile(`.github/workflows/${name}.yml`))

const workflowEvents = (workflow) => workflow.on ?? workflow.true

test('完整 Smoke 只验证 PR 合并结果并取消同 PR 的旧运行', async () => {
  const workflow = await loadWorkflow('smoke')
  const events = workflowEvents(workflow)

  assert.deepEqual(events.pull_request.branches, ['main'])
  assert.equal(events.push, undefined)
  assert.equal(events.workflow_dispatch, undefined)
  assert.equal(
    workflow.concurrency.group,
    'smoke-${{ github.event.pull_request.number || github.ref }}',
  )
  assert.equal(
    workflow.concurrency['cancel-in-progress'],
    "${{ github.event_name == 'pull_request' }}",
  )
  assert.equal(workflow.jobs.smoke['timeout-minutes'], 35)
  assert.equal(
    workflow.jobs.smoke.env.CHRONODESK_EPHEMERAL_E2E,
    '1',
  )
  assert.match(
    workflow.jobs.smoke.env.CHRONODESK_E2E_RUN_ID,
    /github\.run_id/u,
  )

  const checkout = workflow.jobs.smoke.steps.find(
    (step) => step.name === 'Checkout repository',
  )
  assert.equal(checkout.uses, 'actions/checkout@v7')
  assert.equal(checkout.with.ref, undefined)
  assert.equal(checkout.with['persist-credentials'], false)
})

test('成功 Smoke 不上传报告，失败仍保留脱敏诊断', async () => {
  const workflow = await loadWorkflow('smoke')
  const steps = workflow.jobs.smoke.steps
  const prepare = steps.find(
    (step) => step.id === 'prepare-safe-reports',
  )
  const upload = steps.find(
    (step) => step.name === 'Upload smoke reports',
  )

  assert.equal(prepare.if, 'failure()')
  assert.equal(
    upload.if,
    "failure() && steps.prepare-safe-reports.outputs.has_reports == 'true'",
  )
  assert.equal(upload.uses, 'actions/upload-artifact@v7')
})

test('短时安全检查保留 main 状态并取消同 PR 的旧运行', async () => {
  for (const [name, group] of [
    ['codeql', 'codeql'],
    ['security', 'dependency-security'],
  ]) {
    const workflow = await loadWorkflow(name)
    const events = workflowEvents(workflow)

    assert.deepEqual(events.push.branches, ['main'])
    assert.ok(events.pull_request !== undefined)
    assert.equal(
      workflow.concurrency.group,
      `${group}-\${{ github.event.pull_request.number || github.ref }}`,
    )
    assert.equal(
      workflow.concurrency['cancel-in-progress'],
      "${{ github.event_name == 'pull_request' }}",
    )

    if (name === 'security') {
      assert.ok(
        workflow.jobs.web.steps.some(
          (step) => step.run === 'npm run test:ci-runtime',
        ),
      )
    }
  }
})

test('可信 CI policy 不执行 PR 代码并显式写入唯一状态', async () => {
  const workflow = await loadWorkflow('ci-policy')
  const events = workflowEvents(workflow)
  const policy = workflow.jobs.policy
  const serialized = JSON.stringify(workflow)
  const script = policy.steps[0].with.script

  assert.deepEqual(events.pull_request_target.branches, ['main'])
  assert.deepEqual(events.pull_request_target.types, [
    'opened',
    'reopened',
    'synchronize',
    'ready_for_review',
    'edited',
  ])
  assert.deepEqual(events.workflow_run.workflows, ['Smoke Tests'])
  assert.deepEqual(events.workflow_run.types, ['completed'])
  assert.equal(
    events.workflow_dispatch.inputs.pull_request.type,
    'number',
  )
  assert.deepEqual(workflow.permissions, {
    contents: 'read',
    'pull-requests': 'read',
    statuses: 'write',
  })
  assert.equal(policy.steps.length, 1)
  assert.equal(
    policy.steps[0].uses,
    'actions/github-script@ed597411d8f924073f98dfc5c65a23a2325f34cd',
  )
  assert.doesNotMatch(serialized, /actions\/checkout/u)
  assert.doesNotMatch(serialized, /download-artifact/u)
  assert.match(
    script,
    /context:\s*'ci-policy'/u,
  )
  assert.match(
    script,
    /sha:\s*pull\.head\.sha/u,
  )
  assert.match(
    script,
    /previous_filename:\s*previous/u,
  )
  assert.match(
    script,
    /Number\.isSafeInteger\(pull\.changed_files\)/u,
  )
  assert.match(script, /files\.length === pull\.changed_files/u)
  assert.match(
    script,
    /getCollaboratorPermissionLevel/u,
  )
  assert.match(
    script,
    /candidate\.head\.sha === run\.head_sha/u,
  )
  assert.match(
    script,
    /candidate\.base\.ref === 'main'/u,
  )
  assert.match(
    policy.if,
    /pull_request\.user\.login != 'dependabot\[bot\]'/u,
  )
  assert.match(
    policy.if,
    /github\.event_name == 'workflow_dispatch'/u,
  )
})

test('仓库声明的 main 保护严格绑定 GitHub Actions 门禁', async () => {
  const protection = JSON.parse(
    await readRepositoryFile('.github/main-branch-protection.json'),
  )
  const checks = protection.required_status_checks.checks

  assert.equal(protection.required_status_checks.strict, true)
  assert.equal(protection.enforce_admins, true)
  assert.equal(protection.required_conversation_resolution, true)
  assert.equal(protection.required_linear_history, true)
  assert.equal(protection.allow_force_pushes, false)
  assert.equal(protection.allow_deletions, false)
  assert.equal(protection.allow_fork_syncing, false)
  assert.equal(
    protection.required_pull_request_reviews
      .required_approving_review_count,
    0,
  )
  assert.deepEqual(
    checks.map(({ context }) => context).sort(),
    [
      'analyze (go)',
      'analyze (javascript-typescript)',
      'ci-policy',
      'go',
      'secrets',
      'smoke',
      'web',
    ],
  )
  assert.ok(checks.every(({ app_id: appID }) => appID === 15368))
})

test('Playwright 只在一次性回环 CI 使用文件级并行', async () => {
  const config = await readFile(
    new URL('playwright.config.ts', new URL(`file://${webRoot}/`)),
    'utf8',
  )

  assert.match(config, /fullyParallel:\s*false/u)
  assert.match(config, /workers:\s*isEphemeralPublishingCI\s*\?\s*3\s*:\s*1/u)
  assert.match(config, /process\.env\.CHRONODESK_EPHEMERAL_E2E === '1'/u)
  assert.match(config, /isLoopbackE2E\(\)/u)
})
