import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { parse } from 'yaml'

const SHA_A = 'a'.repeat(40)
const SHA_B = 'b'.repeat(40)
const REPOSITORY = 'seaworld008/chronodesk'
const workflow = parse(
  await readFile(
    new URL('../../.github/workflows/ci-policy.yml', import.meta.url),
    'utf8',
  ),
)
const policyScript = workflow.jobs.policy.steps[0].with.script
const AsyncFunction = Object.getPrototypeOf(
  async function policyFunction() {},
).constructor
const executePolicy = new AsyncFunction(
  'github',
  'context',
  'core',
  policyScript,
)

const createPull = ({
  author = 'external-contributor',
  authorType = 'User',
  changedFiles = 1,
  headRef = 'feature/ci-policy-test',
  headRepo = REPOSITORY,
  number = 42,
  sha = SHA_A,
} = {}) => ({
  number,
  state: 'open',
  changed_files: changedFiles,
  user: {
    login: author,
    type: authorType,
  },
  base: {
    ref: 'main',
    repo: {
      full_name: REPOSITORY,
    },
  },
  head: {
    ref: headRef,
    sha,
    repo: {
      full_name: headRepo,
    },
  },
})

const apiError = () =>
  Object.assign(new Error('simulated GitHub API failure'), {
    status: 503,
  })

const runPolicy = async ({
  action = 'opened',
  actor = 'maintainer',
  eventName = 'pull_request_target',
  failApi,
  files = [{ filename: 'docs/README.md' }],
  inputs,
  permissions = {},
  pull = createPull({ changedFiles: files.length }),
  pullSnapshots = [pull, pull],
  sender = 'external-contributor',
} = {}) => {
  let pullReadIndex = 0
  const calls = {
    permissions: [],
    pullReads: [],
    fileReads: [],
    notices: [],
    statuses: [],
  }
  const failures = []

  const pullsGet = async (parameters) => {
    calls.pullReads.push(parameters)
    if (failApi === 'pulls.get') {
      throw apiError()
    }
    const snapshot =
      pullSnapshots[
        Math.min(pullReadIndex, pullSnapshots.length - 1)
      ]
    pullReadIndex += 1
    return { data: structuredClone(snapshot) }
  }
  const listFiles = async (parameters) => {
    calls.fileReads.push(parameters)
    if (failApi === 'pulls.listFiles') {
      throw apiError()
    }
    return { data: structuredClone(files) }
  }
  const getCollaboratorPermissionLevel = async ({ username }) => {
    calls.permissions.push(username)
    if (failApi === 'repos.getCollaboratorPermissionLevel') {
      throw apiError()
    }
    return {
      data: {
        permission: permissions[username] ?? 'none',
      },
    }
  }
  const createCommitStatus = async (status) => {
    if (failApi === 'repos.createCommitStatus') {
      throw apiError()
    }
    calls.statuses.push(structuredClone(status))
    return { data: status }
  }

  const github = {
    paginate: async (endpoint, parameters) => {
      const response = await endpoint(parameters)
      return response.data
    },
    rest: {
      pulls: {
        get: pullsGet,
        listFiles,
      },
      repos: {
        createCommitStatus,
        getCollaboratorPermissionLevel,
      },
    },
  }
  const context = {
    actor,
    eventName,
    payload:
      eventName === 'workflow_dispatch'
        ? { inputs }
        : {
            action,
            pull_request: {
              number: pull.number,
              head: structuredClone(pull.head),
            },
            sender: { login: sender },
          },
    repo: {
      owner: 'seaworld008',
      repo: 'chronodesk',
    },
    runId: 123456,
    serverUrl: 'https://github.com',
  }
  const core = {
    notice: (message) => calls.notices.push(message),
    setFailed: (message) => failures.push(message),
  }

  await executePolicy(github, context, core)
  return { calls, failures }
}

const assertStatus = (result, state, sha = SHA_A) => {
  assert.equal(result.calls.statuses.length, 1)
  assert.deepEqual(
    {
      context: result.calls.statuses[0].context,
      sha: result.calls.statuses[0].sha,
      state: result.calls.statuses[0].state,
    },
    {
      context: 'ci-policy',
      sha,
      state,
    },
  )
  assert.equal(result.failures.length, state === 'success' ? 0 : 1)
}

test('外部贡献者只修改普通文件时通过且不查询协作者权限', async () => {
  const pull = createPull({
    changedFiles: 1,
    headRepo: 'external-fork/chronodesk',
  })
  const result = await runPolicy({
    files: [{ filename: 'docs/README.md' }],
    pull,
    pullSnapshots: [pull, pull],
  })

  assertStatus(result, 'success')
  assert.deepEqual(result.calls.permissions, [])
})

test('外部贡献者修改 CI 控制面时失败', async () => {
  const result = await runPolicy({
    files: [{ filename: '.github/workflows/smoke.yml' }],
    permissions: {
      'external-contributor': 'read',
    },
  })

  assertStatus(result, 'failure')
})

test('所有声明的 CI 控制面入口都受保护', async (t) => {
  const protectedPaths = [
    '.github/workflows/smoke.yml',
    '.github/actions/bootstrap/action.yml',
    '.github/dependabot.yml',
    '.github/CODEOWNERS',
    'CODEOWNERS',
    '.github/main-branch-protection.json',
    '.gitleaks.toml',
    '.gitleaksignore',
    '.env',
    'GNUmakefile',
    'Makefile',
    'makefile',
    'go.work',
    'go.work.sum',
    'compose.override.yaml',
    'docker-compose.override.yml',
    'docker-compose.yml',
    'server/.dockerignore',
    'server/Dockerfile',
    'server/tests/conftest.py',
    'server/tests/ruff.toml',
    'server/tests/utils/safety.py',
    'server/vendor/modules.txt',
    'sdk/go/vendor/example/x.go',
    'sdk/python/.ruff.toml',
    'web/Dockerfile.dev',
    'web/.env.production',
    'web/.npmrc',
    'web/.postcssrc.yaml',
    'web/e2e/helpers/safety.ts',
    'web/postcss.config.cjs',
    'web/package.json',
    'web/package-lock.json',
    'web/playwright.config.ts',
    'web/src/eslint.config.mjs',
    'web/vite.config.js',
    'server/pytest.ini',
    'server/tests/test_python_toolchain.sh',
    'server/tests/validate_case_evidence_manifest.py',
    'web/patches/ra-ui-materialui+5.15.1.patch',
    'web/scripts/audit-security.mjs',
    'web/scripts/ci-runtime.test.mjs',
    'web/scripts/ci-policy.test.mjs',
  ]

  for (const pathname of protectedPaths) {
    await t.test(pathname, async () => {
      const result = await runPolicy({
        files: [{ filename: pathname }],
        permissions: {
          'external-contributor': 'read',
        },
      })
      assertStatus(result, 'failure')
    })
  }
})

test('名称相似但不会被工具自动加载的普通文档仍可通过', async () => {
  const pull = createPull({
    changedFiles: 1,
    headRepo: 'external-fork/chronodesk',
  })
  const result = await runPolicy({
    files: [{ filename: 'docs/compose-guide.yml' }],
    pull,
    pullSnapshots: [pull, pull],
  })

  assertStatus(result, 'success')
  assert.deepEqual(result.calls.permissions, [])
})

test('本仓库 PR 的作者和事件 sender 都有写权限时可修改控制面', async () => {
  const pull = createPull({ author: 'author-writer' })
  const result = await runPolicy({
    files: [{ filename: 'web/package-lock.json' }],
    permissions: {
      'author-writer': 'push',
      'release-maintainer': 'push',
    },
    pull,
    pullSnapshots: [pull, pull],
    sender: 'release-maintainer',
  })

  assertStatus(result, 'success')
  assert.deepEqual(
    result.calls.permissions.sort(),
    ['author-writer', 'release-maintainer'],
  )
})

test('作者有写权限但 head 来自 fork 时仍失败', async () => {
  const pull = createPull({
    author: 'author-writer',
    headRepo: 'author-writer/chronodesk',
  })
  const result = await runPolicy({
    files: [{ filename: 'Makefile' }],
    permissions: {
      'author-writer': 'write',
      'release-maintainer': 'admin',
    },
    pull,
    pullSnapshots: [pull, pull],
    sender: 'release-maintainer',
  })

  assertStatus(result, 'failure')
})

test('synchronize 后只给新 SHA 写判定，旧 SHA 的成功不能继承', async () => {
  const firstPull = createPull({
    author: 'author-writer',
    sha: SHA_A,
  })
  const first = await runPolicy({
    files: [{ filename: 'Makefile' }],
    permissions: {
      'author-writer': 'write',
      'release-maintainer': 'write',
    },
    pull: firstPull,
    pullSnapshots: [firstPull, firstPull],
    sender: 'release-maintainer',
  })
  assertStatus(first, 'success', SHA_A)

  const synchronizedPull = createPull({
    author: 'author-writer',
    sha: SHA_B,
  })
  const synchronized = await runPolicy({
    files: [{ filename: 'Makefile' }],
    permissions: {
      'author-writer': 'write',
      'external-contributor': 'read',
    },
    pull: synchronizedPull,
    pullSnapshots: [synchronizedPull, synchronizedPull],
    sender: 'external-contributor',
  })
  assertStatus(synchronized, 'failure', SHA_B)
})

test('旧 pull_request_target 事件重跑不能判定更新后的 head', async () => {
  const eventPull = createPull({
    author: 'author-writer',
    sha: SHA_A,
  })
  const currentPull = structuredClone(eventPull)
  currentPull.head.sha = SHA_B
  const result = await runPolicy({
    files: [{ filename: 'Makefile' }],
    permissions: {
      'author-writer': 'write',
      'release-maintainer': 'write',
    },
    pull: eventPull,
    pullSnapshots: [currentPull, currentPull],
    sender: 'release-maintainer',
  })

  assert.equal(result.calls.statuses.length, 0)
  assert.equal(result.failures.length, 1)
  assert.match(result.failures[0], /事件/u)
})

test('Dependabot pull_request_target 默认写 failure', async () => {
  const pull = createPull({
    author: 'dependabot[bot]',
    authorType: 'Bot',
    headRef: 'dependabot/github_actions/actions/checkout-7',
  })
  const result = await runPolicy({
    files: [{ filename: '.github/workflows/smoke.yml' }],
    pull,
    pullSnapshots: [pull, pull],
    sender: 'dependabot[bot]',
  })

  assertStatus(result, 'failure')
  assert.deepEqual(result.calls.permissions, [])
})

const dependabotDispatch = ({
  actor = 'release-maintainer',
  expectedHeadSha = SHA_A,
  files = [{ filename: '.github/workflows/smoke.yml' }],
  headRef = 'dependabot/github_actions/actions/checkout-7',
  headRepo = REPOSITORY,
  permissions = { 'release-maintainer': 'write' },
  pullSnapshots,
  userLogin = 'dependabot[bot]',
  userType = 'Bot',
} = {}) => {
  const pull = createPull({
    author: userLogin,
    authorType: userType,
    changedFiles: files.length,
    headRef,
    headRepo,
  })
  return runPolicy({
    actor,
    eventName: 'workflow_dispatch',
    files,
    inputs: {
      approve_dependabot_update: true,
      expected_head_sha: expectedHeadSha,
      pull_request: pull.number,
      reason: '批准固定版本的 Dependabot 依赖更新',
    },
    permissions,
    pull,
    pullSnapshots: pullSnapshots ?? [pull, pull],
  })
}

test('GitHub API 返回 push 权限的维护者可批准 Dependabot 精确 SHA', async () => {
  const result = await dependabotDispatch({
    permissions: { 'release-maintainer': 'push' },
  })

  assertStatus(result, 'success')
  assert.deepEqual(result.calls.permissions, ['release-maintainer'])
})

test('Dependabot 元数据编辑不覆盖当前 head 的精确 SHA 判定', async () => {
  const approved = await dependabotDispatch()
  assertStatus(approved, 'success')

  const pull = createPull({
    author: 'dependabot[bot]',
    authorType: 'Bot',
    headRef: 'dependabot/github_actions/actions/checkout-7',
  })
  const edited = await runPolicy({
    action: 'edited',
    files: [{ filename: '.github/workflows/smoke.yml' }],
    pull,
    pullSnapshots: [pull],
    sender: 'dependabot[bot]',
  })

  assert.equal(edited.calls.statuses.length, 0)
  assert.equal(edited.failures.length, 0)
  assert.equal(edited.calls.notices.length, 1)
  assert.match(edited.calls.notices[0], /不覆盖/u)
})

test('仓库写入者可批准已配置生态的当前 Dependabot 精确 SHA', async (t) => {
  const ecosystems = [
    {
      files: [{ filename: '.github/workflows/smoke.yml' }],
      headRef: 'dependabot/github_actions/actions/checkout-7',
      name: 'GitHub Actions',
    },
    {
      files: [
        { filename: 'server/go.mod' },
        { filename: 'server/go.sum' },
      ],
      headRef: 'dependabot/go_modules/go-dependencies-2026-08-27',
      name: 'Go modules',
    },
    {
      files: [
        { filename: 'web/package.json' },
        { filename: 'web/package-lock.json' },
      ],
      headRef: 'dependabot/npm_and_yarn/web-dependencies-2026-08-27',
      name: 'npm',
    },
  ]

  for (const { files, headRef, name } of ecosystems) {
    await t.test(name, async () => {
      const result = await dependabotDispatch({ files, headRef })
      assertStatus(result, 'success')
      assert.deepEqual(result.calls.permissions, ['release-maintainer'])
    })
  }
})

test('Dependabot 人工批准对错误 SHA 和无权限 actor 失败关闭', async (t) => {
  await t.test('错误 SHA', async () => {
    const result = await dependabotDispatch({
      expectedHeadSha: SHA_B,
    })
    assert.equal(result.calls.statuses.length, 0)
    assert.equal(result.failures.length, 1)
  })

  await t.test('无权限 actor', async () => {
    const result = await dependabotDispatch({
      actor: 'read-only-reviewer',
      permissions: { 'read-only-reviewer': 'read' },
    })
    assert.equal(result.calls.statuses.length, 0)
    assert.equal(result.failures.length, 1)
  })
})

test('Dependabot 身份、同仓库和已配置生态任一不匹配都失败关闭', async (t) => {
  const invalidCases = [
    {
      name: '伪装身份',
      options: { userLogin: 'dependency-helper[bot]' },
    },
    {
      name: 'fork head',
      options: { headRepo: 'external-fork/chronodesk' },
    },
    {
      name: '未配置 ecosystem',
      options: {
        headRef: 'dependabot/pip/server/pytest-10',
      },
    },
  ]

  for (const { name, options } of invalidCases) {
    await t.test(name, async () => {
      const result = await dependabotDispatch(options)
      assert.equal(result.calls.statuses.length, 0)
      assert.equal(result.failures.length, 1)
    })
  }
})

test('Dependabot 生态与文件路径不匹配或包含越界文件时失败关闭', async (t) => {
  const invalidCases = [
    {
      name: 'Go ref 修改 npm 清单',
      options: {
        files: [{ filename: 'web/package.json' }],
        headRef: 'dependabot/go_modules/go-dependencies-2026-08-27',
      },
    },
    {
      name: 'npm ref 修改 Go 清单',
      options: {
        files: [{ filename: 'server/go.mod' }],
        headRef: 'dependabot/npm_and_yarn/web-dependencies-2026-08-27',
      },
    },
    {
      name: 'Actions ref 修改 Go 清单',
      options: {
        files: [{ filename: 'server/go.sum' }],
        headRef: 'dependabot/github_actions/actions/checkout-7',
      },
    },
    {
      name: '允许文件混入越界文档',
      options: {
        files: [
          { filename: 'web/package-lock.json' },
          { filename: 'docs/README.md' },
        ],
        headRef: 'dependabot/npm_and_yarn/web-dependencies-2026-08-27',
      },
    },
  ]

  for (const { name, options } of invalidCases) {
    await t.test(name, async () => {
      const result = await dependabotDispatch(options)
      assert.equal(result.calls.statuses.length, 0)
      assert.equal(result.failures.length, 1)
      assert.match(result.failures[0], /范围外/u)
    })
  }
})

test('写 status 前 head 变化时不向旧 SHA 或新 SHA 写状态', async () => {
  const initialPull = createPull({
    author: 'dependabot[bot]',
    authorType: 'Bot',
    headRef: 'dependabot/github_actions/actions/checkout-7',
    sha: SHA_A,
  })
  const changedPull = structuredClone(initialPull)
  changedPull.head.sha = SHA_B

  const result = await runPolicy({
    actor: 'release-maintainer',
    eventName: 'workflow_dispatch',
    files: [{ filename: '.github/workflows/smoke.yml' }],
    inputs: {
      approve_dependabot_update: true,
      expected_head_sha: SHA_A,
      pull_request: initialPull.number,
      reason: '批准固定版本的 GitHub Actions 更新',
    },
    permissions: { 'release-maintainer': 'write' },
    pull: initialPull,
    pullSnapshots: [initialPull, changedPull],
  })

  assert.equal(result.calls.statuses.length, 0)
  assert.equal(result.failures.length, 1)
  assert.match(result.failures[0], /head/u)
})

test('重命名同时检查 filename 和 previous_filename', async () => {
  const result = await runPolicy({
    files: [
      {
        filename: 'docs/retired-smoke.yml',
        previous_filename: '.github/workflows/smoke.yml',
      },
    ],
    permissions: {
      'external-contributor': 'read',
    },
  })

  assertStatus(result, 'failure')
})

test('文件数超过 3000 或清单计数不一致时均失败关闭', async (t) => {
  await t.test('超过 3000', async () => {
    const pull = createPull({ changedFiles: 3001 })
    const result = await runPolicy({
      files: [{ filename: 'docs/README.md' }],
      pull,
      pullSnapshots: [pull, pull],
    })
    assert.equal(result.calls.statuses.length, 0)
    assert.equal(result.failures.length, 1)
    assert.match(result.failures[0], /3000/u)
  })

  await t.test('计数不一致', async () => {
    const pull = createPull({ changedFiles: 2 })
    const result = await runPolicy({
      files: [{ filename: 'docs/README.md' }],
      pull,
      pullSnapshots: [pull, pull],
    })
    assert.equal(result.calls.statuses.length, 0)
    assert.equal(result.failures.length, 1)
    assert.match(result.failures[0], /不一致/u)
  })
})

test('任一 GitHub API 错误都不能产生 success 状态', async (t) => {
  const failures = [
    {
      endpoint: 'pulls.get',
      options: {},
    },
    {
      endpoint: 'pulls.listFiles',
      options: {},
    },
    {
      endpoint: 'repos.getCollaboratorPermissionLevel',
      options: {
        files: [{ filename: 'Makefile' }],
      },
    },
    {
      endpoint: 'repos.createCommitStatus',
      options: {},
    },
  ]

  for (const { endpoint, options } of failures) {
    await t.test(endpoint, async () => {
      const result = await runPolicy({
        ...options,
        failApi: endpoint,
      })
      assert.equal(
        result.calls.statuses.some(({ state }) => state === 'success'),
        false,
      )
      assert.equal(result.failures.length, 1)
    })
  }
})
