import { expect, test, type Page, type Route } from '@playwright/test'
import { assertDestructiveE2EAllowed } from './helpers/safety'
import type { AuthorizedProject } from '../src/lib/generated/human-api'

const encode = (value: unknown) =>
  Buffer.from(JSON.stringify(value)).toString('base64url')

const projects = [
  {
    id: 73,
    public_id: '00000000-0000-7000-8000-000000000073',
    created_at: '2026-07-30T07:00:00Z',
    updated_at: '2026-07-30T07:00:00Z',
    organization_id: 1,
    business_unit_id: 1,
    key: 'OPS',
    name: '运营支持',
    description: '',
    status: 'active',
  },
  {
    id: 74,
    public_id: '00000000-0000-7000-8000-000000000074',
    created_at: '2026-07-30T07:00:00Z',
    updated_at: '2026-07-30T07:00:00Z',
    organization_id: 1,
    business_unit_id: 1,
    key: 'FIN',
    name: '财务服务与跨区域协同项目（移动端长名称）',
    description: '',
    status: 'active',
  },
  {
    id: 75,
    public_id: '00000000-0000-7000-8000-000000000075',
    created_at: '2026-07-30T07:00:00Z',
    updated_at: '2026-07-30T07:00:00Z',
    organization_id: 1,
    business_unit_id: 1,
    key: 'EMPTY',
    name: '零工单项目',
    description: '',
    status: 'active',
  },
] satisfies AuthorizedProject[]

const installSession = async (
  page: Page,
  authorizedProjects: AuthorizedProject[] = projects,
) => {
  const expiresAt = Math.floor(Date.now() / 1000) + 3600
  const token = `${encode({ alg: 'none', typ: 'JWT' })}.${encode({
    sub: '7',
    sid: 'workbench-dashboard-session',
    platform_role: 'platform_admin',
    exp: expiresAt,
  })}.signature`
  await page.addInitScript(({ accessToken, exp }) => {
    localStorage.setItem('token', accessToken)
    localStorage.setItem('refreshToken', 'workbench-dashboard-refresh')
    localStorage.setItem('tokenExpiresAt', String(exp * 1000))
    localStorage.setItem('user', JSON.stringify({
      id: 7,
      username: 'dashboard-user',
      email: 'dashboard@example.invalid',
      platform_role: 'platform_admin',
      status: 'active',
      email_verified: true,
      otp_enabled: false,
    }))
    localStorage.setItem('chronodesk.activeProject', JSON.stringify({
      subject: '7',
      session_id: 'workbench-dashboard-session',
      project_key: 'OPS',
    }))
  }, { accessToken: token, exp: expiresAt })
  await page.route('**/api/auth/me', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      code: 0,
      msg: 'ok',
      data: {
        id: 7,
        username: 'dashboard-user',
        email: 'dashboard@example.invalid',
        platform_role: 'platform_admin',
        status: 'active',
        email_verified: true,
        otp_enabled: false,
      },
    }),
  }))
  await page.route('**/api/projects', async (route) => {
    if (route.request().method() !== 'GET') {
      await route.fallback()
      return
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        code: 0,
        msg: 'ok',
        data: authorizedProjects.map((project) => ({
          project,
          project_role: 'observer',
          scope: {
            organization_id: project.organization_id,
            project_id: project.id,
          },
        })),
      }),
    })
  })
}

const selectedProjectsFor = (url: URL) => {
  const selected = url.searchParams.getAll('project_keys')
  return selected.length === 0
    ? projects
    : projects.filter((project) => selected.includes(project.key))
}

const dashboardEnvelope = (
  url: URL,
  selectedProjects = selectedProjectsFor(url),
  total = 12,
) => ({
  code: 0,
  msg: 'ok',
  data: {
    generated_at: '2026-07-31T12:00:00Z',
    days: Number(url.searchParams.get('days') ?? 30),
    selected_projects: selectedProjects.map(({ key, name }) => ({ key, name })),
    summary: {
      total,
      status: {
        open: total,
        in_progress: 0,
        pending: 0,
        resolved: 0,
        closed: 0,
        cancelled: 0,
      },
      priority: {
        low: 0,
        normal: total,
        high: 0,
        urgent: 0,
        critical: 0,
      },
      sla_breached: 0,
      overdue: 0,
      assignment: {
        assigned: 0,
        unassigned: total,
        human: 0,
        service_principal: 0,
      },
    },
    daily_trend: [
      { date: '2026-07-30', created: 0 },
      { date: '2026-07-31', created: total },
    ],
    project_breakdown: selectedProjects.map((project, index) => ({
      project_key: project.key,
      project_name: project.name,
      total: project.key === 'EMPTY' ? 0 : Math.max(0, total - index),
      sla_breached: 0,
      overdue: 0,
    })),
  },
})

const fulfillDashboard = (
  route: Route,
  body: ReturnType<typeof dashboardEnvelope>,
) => route.fulfill({
  status: 200,
  contentType: 'application/json',
  body: JSON.stringify(body),
})

test.describe('跨项目运营大屏', () => {
  test.beforeAll(() => {
    assertDestructiveE2EAllowed('跨项目运营大屏只读 E2E')
  })

  test('默认全部项目、URL 多选、零工单和移动端无页面级溢出', async ({
    page,
  }) => {
    const dashboardRequests: string[] = []
    await installSession(page)
    await page.route('**/api/workbench/dashboard?*', async (route) => {
      const url = new URL(route.request().url())
      dashboardRequests.push(url.search)
      await fulfillDashboard(route, dashboardEnvelope(url))
    })

    await page.goto('/#/workbench/dashboard')
    await expect(page.getByLabel('当前范围：全部授权项目')).toBeVisible()
    await expect.poll(() => dashboardRequests.at(-1)).toBe('?days=30')
    const table = page.getByRole('table', { name: '项目贡献列表' })
    await expect(table).toBeVisible()
    await expect(
      table.getByRole('row').filter({ hasText: '零工单项目' }),
    ).toContainText('0')

    await page.goto(
      '/#/workbench/dashboard?project_keys=OPS&project_keys=FIN&days=7',
    )
    await expect(page.getByLabel('当前范围：已选 2 个项目')).toBeVisible()
    await expect.poll(() => dashboardRequests.at(-1)).toContain(
      'project_keys=FIN&project_keys=OPS&days=7',
    )

    await page.setViewportSize({ width: 390, height: 844 })
    await expect(page.getByTestId('workbench-dashboard-page')).toBeVisible()
    await expect(page.getByTestId('account-menu')).toBeVisible()
    await expect(page.getByTestId('scope-badge')).toBeVisible()
    const viewport = await page.evaluate(() => ({
      width: window.innerWidth,
      documentWidth: document.documentElement.scrollWidth,
      bodyWidth: document.body.scrollWidth,
    }))
    expect(viewport.documentWidth).toBeLessThanOrEqual(viewport.width)
    expect(viewport.bodyWidth).toBeLessThanOrEqual(viewport.width)
  })

  test('延迟、403 和 500 请求不会在新 scope 标签下显示旧数据', async ({
    page,
  }) => {
    await installSession(page)
    let resolveFinanceRoute: (route: Route) => void = () => undefined
    const pendingFinanceRoute = new Promise<Route>((resolve) => {
      resolveFinanceRoute = resolve
    })
    let operationsMode: 'success' | 'forbidden' = 'success'
    let financeMode: 'delayed' | 'error' = 'delayed'
    await page.route('**/api/workbench/dashboard?*', async (route) => {
      const url = new URL(route.request().url())
      const keys = url.searchParams.getAll('project_keys')
      if (keys.length === 1 && keys[0] === 'OPS') {
        if (operationsMode === 'forbidden') {
          await route.fulfill({
            status: 403,
            contentType: 'application/json',
            body: JSON.stringify({ code: 403, msg: '项目授权已变化' }),
          })
          return
        }
        await fulfillDashboard(route, dashboardEnvelope(url, [projects[0]], 111))
        return
      }
      if (keys.length === 1 && keys[0] === 'FIN') {
        if (financeMode === 'error') {
          await route.fulfill({
            status: 500,
            contentType: 'application/json',
            body: JSON.stringify({ code: 500, msg: '统计服务暂不可用' }),
          })
          return
        }
        resolveFinanceRoute(route)
        return
      }
      await fulfillDashboard(route, dashboardEnvelope(url))
    })

    await page.goto('/#/workbench/dashboard?project_keys=OPS&days=30')
    await expect(page.getByLabel('工单总量：111')).toBeVisible()

    await page.goto('/#/workbench/dashboard?project_keys=FIN&days=30')
    await expect(page.getByLabel('当前范围：已选 1 个项目')).toBeVisible()
    await expect(page.getByLabel('正在加载运营大屏')).toBeVisible()
    await expect(page.getByLabel('工单总量：111')).toHaveCount(0)
    const financeRoute = await pendingFinanceRoute
    const financeURL = new URL(financeRoute.request().url())
    await fulfillDashboard(
      financeRoute,
      dashboardEnvelope(financeURL, [projects[1]], 222),
    )
    await expect(page.getByLabel('工单总量：222')).toBeVisible()

    operationsMode = 'forbidden'
    await page.goto('/#/workbench/dashboard?project_keys=OPS&days=30')
    await expect(page.getByRole('alert')).toContainText('项目授权已变化')
    await expect(page.getByLabel('工单总量：222')).toHaveCount(0)
    await expect(page.getByRole('table', { name: '项目贡献列表' }))
      .toHaveCount(0)

    financeMode = 'error'
    await page.goto('/#/workbench/dashboard?project_keys=FIN&days=30')
    await expect(page.getByRole('alert')).toContainText('统计服务暂不可用')
    await expect(page.getByLabel('工单总量：222')).toHaveCount(0)
  })

  test('无成员返回明确空态，非法 days 显式报错且不发统计请求', async ({
    page,
  }) => {
    const dashboardRequests: string[] = []
    await installSession(page, [])
    await page.route('**/api/workbench/dashboard?*', async (route) => {
      const url = new URL(route.request().url())
      dashboardRequests.push(url.search)
      await fulfillDashboard(route, dashboardEnvelope(url, [], 0))
    })

    await page.goto('/#/workbench/dashboard?days=101')
    await expect(page.getByRole('alert')).toContainText('统计周期链接无效')
    await expect(page.getByRole('button', { name: '近 30 天' }))
      .toHaveAttribute('aria-pressed', 'false')
    expect(dashboardRequests).toEqual([])

    await page.getByRole('button', { name: '重置筛选' }).click()
    await expect(page).toHaveURL(/days=30/u)
    await expect.poll(() => dashboardRequests).toEqual(['?days=30'])
    await expect(page.getByRole('alert')).toContainText(
      '当前账号没有有效项目成员关系',
    )
    await expect(page.getByRole('table', { name: '项目贡献列表' }))
      .toHaveCount(0)
  })
})
