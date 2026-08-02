import { expect, test, type Page } from '@playwright/test'
import {
  authorizedProjectAccess,
  defaultMockIdentity,
  fulfillJSON,
  installMockSession,
  projectA,
  projectB,
} from './helpers/mockHumanSession'
import { monitorBrowserHealth } from './helpers/browserAudit'

type ProjectRole = 'project_admin' | 'manager' | 'observer'

const timestamp = '2026-08-01T08:00:00Z'
const outboxDeliveryID = '00000000-0000-7000-8000-000000000110'

const connection = {
  id: '00000000-0000-7000-8000-000000000101',
  key: 'primary',
  name: '主服务台连接',
  description: '企业服务台入站连接',
  status: 'active',
  replay_window_seconds: 300,
  has_configuration: true,
  has_verification_key: true,
  last_verified_at: timestamp,
  created_at: timestamp,
  updated_at: timestamp,
}

const directory = <T,>(items: T[], page = 1, total = items.length) => ({
  items,
  total,
  page,
  page_size: 25,
  total_pages: total === 0 ? 0 : Math.ceil(total / 25),
})

const installIntegrationBackend = async (
  page: Page,
  role: ProjectRole,
  options: { holdOpsSecondPage?: boolean } = {},
) => {
  const sessionID = `integration-${role}-${Date.now()}`
  await installMockSession(
    page,
    {
      ...defaultMockIdentity,
      sessionID,
    },
    projectA,
  )
  const accesses = [
    authorizedProjectAccess(projectA, role),
    authorizedProjectAccess(projectB, role),
  ]
  const requests: URL[] = []
  const adminOutboxRequests: URL[] = []
  const replayRequests: {
    idempotencyKey: string | null
    ifMatch: string | null
  }[] = []
  let outboxReplayed = false
  let integrationOutboxLoads = 0
  let releaseOpsSecondPage: (() => void) | undefined
  const opsSecondPageGate = new Promise<void>((resolve) => {
    releaseOpsSecondPage = resolve
  })

  await page.route('**/api/**', async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const pathname = url.pathname
    if (pathname === '/api/auth/me') {
      await fulfillJSON(route, {
        code: 0,
        data: {
          id: defaultMockIdentity.id,
          username: 'integration-reviewer',
          email: defaultMockIdentity.email,
          platform_role: 'member',
          status: 'active',
          email_verified: true,
          otp_enabled: false,
        },
      })
      return
    }
    if (pathname === '/api/projects') {
      await fulfillJSON(route, { code: 0, data: accesses })
      return
    }
    const projectMatch = pathname.match(
      /^\/api\/projects\/([^/]+)\/(.+)$/u,
    )
    if (!projectMatch) {
      await fulfillJSON(route, { code: 0, data: [] })
      return
    }
    const projectKey = decodeURIComponent(projectMatch[1])
    const resource = projectMatch[2]
    const access = accesses.find((candidate) =>
      candidate.project.key === projectKey)
    if (resource === 'context') {
      await fulfillJSON(route, { code: 0, data: access })
      return
    }
    if (
      resource === 'admin/agents/outbox'
      && request.method() === 'GET'
    ) {
      adminOutboxRequests.push(url)
      const requestedPage = Number(url.searchParams.get('page') ?? 1)
      await fulfillJSON(route, {
        code: 0,
        data: {
          items: requestedPage === 2 ? [{
            id: outboxDeliveryID,
            created_at: timestamp,
            event_id: '00000000-0000-7000-8000-000000000109',
            destination_type: 'webhook',
            destination_label: 'Webhook',
            status: 'failed',
            attempts: 2,
            next_attempt_at: timestamp,
            last_error: '管理端错误详情不得显示在集成页面',
            updated_at: timestamp,
            resource_version: 7,
          }] : [{
            id: '00000000-0000-7000-8000-000000000999',
            created_at: timestamp,
            event_id: '00000000-0000-7000-8000-000000000998',
            destination_type: 'notification',
            destination_label: '项目通知',
            status: 'succeeded',
            attempts: 1,
            next_attempt_at: timestamp,
            last_error: '',
            updated_at: timestamp,
            resource_version: 2,
          }],
          total: 2,
          page: requestedPage,
          page_size: 100,
          total_pages: 2,
        },
      })
      return
    }
    if (
      resource === `admin/agents/outbox/${outboxDeliveryID}/replay`
      && request.method() === 'POST'
    ) {
      replayRequests.push({
        idempotencyKey: request.headers()['idempotency-key'] ?? null,
        ifMatch: request.headers()['if-match'] ?? null,
      })
      outboxReplayed = true
      await fulfillJSON(route, {
        code: 0,
        data: {
          replayed: true,
        },
      }, 202)
      return
    }
    if (!resource.startsWith('integrations')) {
      await fulfillJSON(route, { code: 0, data: [] })
      return
    }
    requests.push(url)
    if (resource === 'integrations/overview') {
      await fulfillJSON(route, {
        code: 0,
        data: {
          connector_definitions: 1,
          connections: 51,
          active_connections: 50,
          error_connections: 1,
          open_conflicts: 1,
          open_dead_letters: 1,
          running_sync_runs: 1,
          recent_runs: [],
          recent_runs_limit: 20,
          recent_runs_truncated: false,
          connection_health: [],
          connection_health_limit: 100,
          connection_health_truncated: false,
        },
      })
      return
    }
    if (resource === 'integrations/connections') {
      const requestedPage = Number(url.searchParams.get('page') ?? 1)
      if (
        options.holdOpsSecondPage
        && projectKey === projectA.key
        && requestedPage === 2
      ) {
        await opsSecondPageGate
      }
      const item = {
        ...connection,
        id: `${connection.id}-${projectKey}-${requestedPage}`,
        name: projectKey === projectB.key
          ? '财务项目连接'
          : requestedPage === 2
            ? '运营项目第二页连接'
            : connection.name,
      }
      try {
        await fulfillJSON(route, {
          code: 0,
          data: directory([item], requestedPage, 51),
        })
      } catch {
        // 项目切换后旧请求会被 AbortController 主动取消。
      }
      return
    }
    if (resource === 'integrations/connector-definitions') {
      await fulfillJSON(route, {
        code: 0,
        data: directory([{
          id: '00000000-0000-7000-8000-000000000102',
          key: 'service-desk',
          name: '服务台连接器',
          description: '受管连接器',
          kind: 'webhook',
          direction: 'bidirectional',
          status: 'active',
          signature_scheme: 'hmac-sha256',
          default_replay_window_seconds: 300,
          has_configuration_schema: true,
          has_mapping_schema: true,
          created_at: timestamp,
          updated_at: timestamp,
        }]),
      })
      return
    }
    if (
      /^integrations\/connections\/[^/]+\/mappings$/u.test(resource)
    ) {
      await fulfillJSON(route, {
        code: 0,
        data: directory([{
          id: '00000000-0000-7000-8000-000000000103',
          key: 'ticket-import',
          version: 2,
          status: 'published',
          target_command: 'ticket.create',
          definition_digest: 'a'.repeat(64),
          published_at: timestamp,
          created_at: timestamp,
          updated_at: timestamp,
        }]),
      })
      return
    }
    if (resource === 'integrations/inbox') {
      await fulfillJSON(route, {
        code: 0,
        data: directory([{
          id: '00000000-0000-7000-8000-000000000104',
          connection_id: 101,
          external_message_id: 'EXT-MSG-100',
          external_resource_type: 'ticket',
          external_resource_id: 'EXT-100',
          signed_at: timestamp,
          received_at: timestamp,
          content_type: 'application/json',
          payload_digest: 'b'.repeat(64),
          status: 'completed',
          processed_at: timestamp,
          created_at: timestamp,
          updated_at: timestamp,
        }]),
      })
      return
    }
    if (/^integrations\/inbox\/[^/]+\/receipts$/u.test(resource)) {
      await fulfillJSON(route, {
        code: 0,
        data: directory([{
          id: '00000000-0000-7000-8000-000000000105',
          status: 'applied',
          resource_type: 'ticket',
          resource_id: 'OPS-100',
          resource_version: 1,
          event_id: '00000000-0000-7000-8000-000000000205',
          operation_id: '00000000-0000-7000-8000-000000000305',
          actor_type: 'system',
          actor_id: 'connector:primary',
          processed_at: timestamp,
          created_at: timestamp,
        }]),
      })
      return
    }
    if (resource === 'integrations/sync-runs') {
      await fulfillJSON(route, {
        code: 0,
        data: directory([{
          id: '00000000-0000-7000-8000-000000000106',
          connection_id: 101,
          run_key: 'hourly-sync',
          direction: 'inbound',
          status: 'succeeded',
          started_at: timestamp,
          finished_at: timestamp,
          processed_count: 12,
          succeeded_count: 12,
          failed_count: 0,
          conflict_count: 0,
        }]),
      })
      return
    }
    if (resource === 'integrations/conflicts') {
      await fulfillJSON(route, {
        code: 0,
        data: directory([{
          id: '00000000-0000-7000-8000-000000000107',
          connection_id: 101,
          type: 'external_link_mismatch',
          status: 'open',
          external_resource_type: 'ticket',
          external_resource_id: 'EXT-101',
          existing_internal_resource_id: 'OPS-100',
          incoming_internal_resource_id: 'OPS-101',
          created_at: timestamp,
          updated_at: timestamp,
        }]),
      })
      return
    }
    if (resource === 'integrations/dead-letters') {
      await fulfillJSON(route, {
        code: 0,
        data: directory([{
          id: '00000000-0000-7000-8000-000000000108',
          connection_id: 101,
          status: 'open',
          reason_code: 'upstream_unavailable',
          attempt_count: 3,
          next_attempt_at: timestamp,
          created_at: timestamp,
          updated_at: timestamp,
        }]),
      })
      return
    }
    if (resource === 'integrations/domain-events') {
      await fulfillJSON(route, {
        code: 0,
        data: {
          items: [{
            id: '00000000-0000-7000-8000-000000000109',
            created_at: timestamp,
            type: 'io.chronodesk.ticket.created.v1',
            subject: 'ticket/OPS-100',
            actor_type: 'human',
            actor_id: '42',
            resource_version: 1,
            time: timestamp,
          }],
          next_cursor: '',
          has_more: false,
        },
      })
      return
    }
    if (resource === 'integrations/outbox') {
      integrationOutboxLoads += 1
      await fulfillJSON(route, {
        code: 0,
        data: directory([{
          id: outboxDeliveryID,
          event_id: '00000000-0000-7000-8000-000000000109',
          destination_type: 'webhook',
          destination_label: 'Webhook',
          status: outboxReplayed ? 'pending' : 'failed',
          attempts: outboxReplayed ? 0 : 2,
          max_attempts: 8,
          next_attempt_at: timestamp,
          last_error: outboxReplayed ? '' : '上游暂不可用',
          created_at: timestamp,
          updated_at: timestamp,
        }]),
      })
      return
    }
    if (request.method() === 'POST') {
      await fulfillJSON(route, { code: 0, data: { accepted: true } })
      return
    }
    await fulfillJSON(route, { code: 0, data: directory([]) })
  })

  return {
    adminOutboxRequests,
    integrationOutboxLoads: () => integrationOutboxLoads,
    replayRequests,
    requests,
    sessionID,
    releaseOpsSecondPage: () => releaseOpsSecondPage?.(),
  }
}

test.describe('企业集成中心', () => {
  test('INT-001：七个运行视图使用有界请求、脱敏详情与键盘交互', async ({
    page,
  }) => {
    const health = monitorBrowserHealth(page)
    const backend = await installIntegrationBackend(page, 'manager')
    await page.goto('/#/integration-runtime')
    const main = page.getByRole('main')
    await expect(
      main.getByRole('heading', { name: '集成中心', exact: true }),
    ).toBeVisible()
    await expect(main.getByText('当前项目：OPS', { exact: true })).toBeVisible()
    const navigation = page.getByRole('menu', { name: '主导航' })
    const integrationGroup = navigation.getByRole('menuitem', {
      name: /^集成中心/u,
    })
    if ((await integrationGroup.getAttribute('aria-expanded')) !== 'true') {
      await integrationGroup.click()
    }
    await expect(
      navigation.getByRole('menuitem', { name: 'Webhook', exact: true }),
    ).toBeVisible()
    await expect(
      navigation.getByRole('menuitem', { name: '集成运行', exact: true }),
    ).toBeVisible()
    await expect(
      main.getByRole('table', { name: '连接实例列表', exact: true }),
    ).toBeVisible()

    await main.getByRole('button', { name: '连接器定义' }).click()
    await expect(
      main.getByRole('table', { name: '连接器定义列表', exact: true }),
    ).toBeVisible()

    const expectedTabs = [
      ['映射', '映射版本列表'],
      ['Inbox 与同步', 'Inbox 消息列表'],
      ['冲突', '集成冲突列表'],
      ['死信', '集成死信列表'],
      ['领域事件', '领域事件列表'],
      ['Outbox', 'Outbox 投递列表'],
    ] as const
    for (const [tab, table] of expectedTabs) {
      await main.getByRole('tab', { name: tab, exact: true }).click()
      await expect(
        main.getByRole('table', { name: table, exact: true }),
      ).toBeVisible()
    }
    await expect(
      main.getByRole('button', { name: /重新投递/u }),
    ).toHaveCount(0)

    await main.getByRole('tab', { name: 'Inbox 与同步', exact: true }).click()
    await main.getByRole('button', { name: '同步运行', exact: true }).click()
    await expect(
      main.getByRole('table', { name: '同步运行列表', exact: true }),
    ).toBeVisible()
    await main.getByRole('button', { name: 'Inbox 消息', exact: true }).click()
    const inboxTable = main.getByRole('table', {
      name: 'Inbox 消息列表',
      exact: true,
    })
    await inboxTable.getByRole('row').nth(1).focus()
    await page.keyboard.press('Enter')
    await expect(page.getByRole('heading', {
      name: 'Inbox 消息详情',
      exact: true,
    })).toBeVisible()
    await expect(page.getByText('payload', { exact: true })).toHaveCount(0)
    await page.getByRole('button', { name: '关闭详情', exact: true }).click()
    await main.getByRole('button', {
      name: '查看消息 EXT-MSG-100 的处理回执',
      exact: true,
    }).click()
    await expect(
      page.getByRole('table', { name: 'Inbox 处理回执列表', exact: true }),
    ).toBeVisible()
    await page.getByRole('button', { name: '关闭', exact: true }).click()
    await main.getByRole('tab', { name: '冲突', exact: true }).click()
    await expect(
      main.getByRole('button', { name: /解决冲突/u }),
    ).toBeVisible()

    const listRequests = backend.requests.filter((url) =>
      !url.pathname.endsWith('/overview'))
    expect(listRequests.length).toBeGreaterThanOrEqual(8)
    for (const url of listRequests) {
      if (url.pathname.endsWith('/domain-events')) {
        expect(url.searchParams.get('limit')).toBe('25')
        expect(url.searchParams.has('page')).toBe(false)
      } else {
        expect(url.searchParams.get('page')).toBe('1')
        expect(url.searchParams.get('page_size')).toBe('25')
      }
    }
    health.assertClean()
  })

  test('INT-002：观察员可读但没有管理动作', async ({ page }) => {
    await installIntegrationBackend(page, 'observer')
    await page.goto('/#/integration-runtime')
    const main = page.getByRole('main')
    await expect(
      main.getByRole('heading', { name: '集成中心', exact: true }),
    ).toBeVisible()
    const navigation = page.getByRole('menu', { name: '主导航' })
    await expect(
      navigation.getByRole('menuitem', { name: '集成中心', exact: true }),
    ).toBeVisible()
    await expect(
      navigation.getByRole('menuitem', { name: 'Webhook', exact: true }),
    ).toHaveCount(0)
    await main.getByRole('tab', { name: '冲突', exact: true }).click()
    await expect(
      main.getByRole('button', { name: /解决冲突/u }),
    ).toHaveCount(0)
    await main.getByRole('tab', { name: '死信', exact: true }).click()
    await expect(
      main.getByRole('button', { name: /重新处理死信/u }),
    ).toHaveCount(0)
    await main.getByRole('tab', { name: 'Outbox', exact: true }).click()
    await expect(
      main.getByRole('button', { name: /重新投递/u }),
    ).toHaveCount(0)
  })

  test('INT-003：翻页发起服务端请求，项目切换取消旧页并清除陈旧数据', async ({
    page,
  }) => {
    const backend = await installIntegrationBackend(
      page,
      'manager',
      { holdOpsSecondPage: true },
    )
    await page.goto('/#/integration-runtime')
    const main = page.getByRole('main')
    await expect(main.getByText('主服务台连接', { exact: true })).toBeVisible()
    await main.getByRole('button', { name: /下一页|next page/iu }).click()
    await expect.poll(() => backend.requests.some((url) =>
      url.pathname.includes('/OPS/integrations/connections')
      && url.searchParams.get('page') === '2')).toBe(true)

    await page.evaluate(({ projectKey, subject, sessionID }) => {
      localStorage.setItem(
        'chronodesk.activeProject',
        JSON.stringify({
          subject,
          session_id: sessionID,
          project_key: projectKey,
        }),
      )
      window.dispatchEvent(new CustomEvent(
        'chronodesk:project-scope-changed',
        { detail: { project_key: projectKey } },
      ))
    }, {
      projectKey: projectB.key,
      subject: String(defaultMockIdentity.id),
      sessionID: backend.sessionID,
    })
    await expect(main.getByText('当前项目：FIN', { exact: true })).toBeVisible()
    await expect(main.getByText('财务项目连接', { exact: true })).toBeVisible()
    backend.releaseOpsSecondPage()
    await expect(main.getByText(
      '运营项目第二页连接',
      { exact: true },
    )).toHaveCount(0)
  })

  test('INT-004：390 像素视口仍可访问树形导航、标签和横向表格', async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await installIntegrationBackend(page, 'manager')
    await page.goto('/#/integration-runtime')
    const main = page.getByRole('main')
    await expect(
      main.getByRole('heading', { name: '集成中心', exact: true }),
    ).toBeVisible()
    await main.getByRole('tab', { name: 'Outbox', exact: true }).click()
    const table = main.getByRole('table', {
      name: 'Outbox 投递列表',
      exact: true,
    })
    await expect(table).toBeVisible()
    const bodyWidth = await page.evaluate(() => document.body.scrollWidth)
    const viewportWidth = await page.evaluate(() => window.innerWidth)
    expect(bodyWidth).toBeLessThanOrEqual(viewportWidth)
  })

  test('INT-005：仅项目管理员可确认并按最新版本重新投递失败的 Outbox', async ({
    page,
  }) => {
    const backend = await installIntegrationBackend(page, 'project_admin')
    await page.goto('/#/integration-runtime')
    const main = page.getByRole('main')
    await main.getByRole('tab', { name: 'Outbox', exact: true }).click()
    const replayButton = main.getByRole('button', {
      name: `重新投递 ${outboxDeliveryID}`,
      exact: true,
    })
    await expect(replayButton).toBeVisible()
    await expect(main.getByText('上游暂不可用', { exact: true })).toBeVisible()
    await expect(
      main.getByText('管理端错误详情不得显示在集成页面', { exact: true }),
    ).toHaveCount(0)

    await replayButton.focus()
    await page.keyboard.press('Enter')
    const dialog = page.getByRole('dialog', { name: '确认重新投递' })
    await expect(dialog).toBeVisible()
    await expect(dialog.getByText(/按事件 ID 去重/u)).toBeVisible()
    expect(backend.replayRequests).toHaveLength(0)

    await dialog.getByRole('button', {
      name: '确认重新投递',
      exact: true,
    }).click()
    await expect(
      page.getByText('事件投递已重新排队', { exact: true }),
    ).toBeVisible()
    await expect.poll(() => backend.integrationOutboxLoads()).toBeGreaterThan(1)
    await expect(replayButton).toHaveCount(0)

    expect(backend.adminOutboxRequests).toHaveLength(2)
    expect(
      backend.adminOutboxRequests.map((url) =>
        url.searchParams.get('page')),
    ).toEqual(['1', '2'])
    for (const url of backend.adminOutboxRequests) {
      expect(url.searchParams.get('page_size')).toBe('100')
      expect(url.searchParams.get('sort_by')).toBe('created_at')
      expect(url.searchParams.get('sort_order')).toBe('desc')
    }
    expect(backend.replayRequests).toHaveLength(1)
    expect(backend.replayRequests[0].ifMatch).toBe('"v7"')
    expect(backend.replayRequests[0].idempotencyKey).toBeTruthy()
  })
})
