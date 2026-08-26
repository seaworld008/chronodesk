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
    'Makefile',
    'docker-compose.yml',
    'server/Dockerfile',
    'web/Dockerfile.dev',
    'web/package.json',
    'web/package-lock.json',
    'web/playwright.config.ts',
    'server/pytest.ini',
    'server/tests/test_python_toolchain.sh',
    'server/tests/validate_case_evidence_manifest.py',
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

test('本仓库 PR 的作者和事件 sender 都有写权限时可修改控制面', async () => {
  const pull = createPull({ author: 'author-writer' })
  const result = await runPolicy({
    files: [{ filename: 'web/package-lock.json' }],
    permissions: {
      'author-writer': 'write',
      'release-maintainer': 'maintain',
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
    headRef,
    headRepo,
  })
  return runPolicy({
    actor,
    eventName: 'workflow_dispatch',
    files: [{ filename: '.github/workflows/smoke.yml' }],
    inputs: {
      approve_dependabot_update: true,
      expected_head_sha: expectedHeadSha,
      pull_request: pull.number,
      reason: '批准固定版本的 GitHub Actions 更新',
    },
    permissions,
    pull,
    pullSnapshots: pullSnapshots ?? [pull, pull],
  })
}

test('仓库写入者可批准当前 Dependabot Actions 精确 SHA', async () => {
  const result = await dependabotDispatch()

  assertStatus(result, 'success')
  assert.deepEqual(result.calls.permissions, ['release-maintainer'])
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

test('Dependabot 身份、同仓库和 head ref 前缀任一不匹配都失败关闭', async (t) => {
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
      name: '错误 ecosystem',
      options: {
        headRef: 'dependabot/npm_and_yarn/web/react-20',
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
