import { expect, test } from '@playwright/test'
import { assertDestructiveE2EAllowed } from './helpers/safety'
import type { AuthorizedProject } from '../src/lib/generated/human-api'

const encode = (value: unknown) =>
  Buffer.from(JSON.stringify(value)).toString('base64url')

test.describe('跨项目运营大屏', () => {
  test.beforeAll(() => {
    assertDestructiveE2EAllowed('跨项目运营大屏只读 E2E')
  })

  test('恢复 URL 多选并以单一后端请求刷新桌面与移动视图', async ({ page }) => {
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
        name: '财务服务',
        description: '',
        status: 'active',
      },
    ] satisfies AuthorizedProject[]
    const dashboardRequests: string[] = []

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
    await page.route('**/api/auth/me', async (route) => {
      await route.fulfill({
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
      })
    })
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
          data: projects.map((project) => ({
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
    await page.route('**/api/workbench/dashboard?*', async (route) => {
      const url = new URL(route.request().url())
      dashboardRequests.push(url.search)
      const selected = url.searchParams.getAll('project_keys')
      const selectedProjects = (
        selected.length === 0
          ? projects
          : projects.filter((project) => selected.includes(project.key))
      )
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          code: 0,
          msg: 'ok',
          data: {
            generated_at: '2026-07-31T12:00:00Z',
            days: Number(url.searchParams.get('days') ?? 30),
            selected_projects: selectedProjects.map(({ key, name }) => ({
              key,
              name,
            })),
            summary: {
              total: 12,
              status: {
                open: 4,
                in_progress: 3,
                pending: 1,
                resolved: 2,
                closed: 1,
                cancelled: 1,
              },
              priority: {
                low: 1,
                normal: 4,
                high: 3,
                urgent: 2,
                critical: 2,
              },
              sla_breached: 2,
              overdue: 1,
              assignment: {
                assigned: 10,
                unassigned: 2,
                human: 8,
                service_principal: 2,
              },
            },
            daily_trend: [
              { date: '2026-07-30', created: 5 },
              { date: '2026-07-31', created: 7 },
            ],
            project_breakdown: selectedProjects.map((project, index) => ({
              project_key: project.key,
              project_name: project.name,
              total: index === 0 ? 8 : 4,
              sla_breached: index === 0 ? 2 : 0,
              overdue: index === 0 ? 1 : 0,
            })),
          },
        }),
      })
    })

    await page.goto('/#/workbench/dashboard?project_keys=OPS&project_keys=FIN&days=7')
    await expect(page.getByText('运营大屏', { exact: true }).first()).toBeVisible()
    await expect(page.getByLabel('当前范围：已选 2 个项目')).toBeVisible()
    await expect(page.getByRole('table', { name: '项目贡献列表' })).toBeVisible()
    await expect.poll(() => dashboardRequests.at(-1)).toContain(
      'project_keys=OPS&project_keys=FIN&days=7',
    )

    await page.getByLabel('选择项目 财务服务').click()
    await expect(page).toHaveURL(/project_keys=OPS&days=7/u)
    await expect(page.getByLabel('当前范围：已选 1 个项目')).toBeVisible()
    await expect.poll(() => dashboardRequests.at(-1)).toContain(
      'project_keys=OPS&days=7',
    )

    await page.getByRole('button', { name: '清除筛选' }).click()
    await expect(page.getByLabel('当前范围：全部授权项目')).toBeVisible()
    await expect.poll(() => dashboardRequests.at(-1)).toBe('?days=7')

    const accountMenu = page.getByTestId('account-menu')
    const scopeBadge = page.getByTestId('scope-badge')
    await expect(accountMenu).toBeVisible()
    await expect(scopeBadge).toBeVisible()
    const desktopHeader = await Promise.all([
      scopeBadge.boundingBox(),
      accountMenu.boundingBox(),
    ])
    expect(desktopHeader[0]!.x).toBeLessThan(desktopHeader[1]!.x)

    await page.setViewportSize({ width: 390, height: 844 })
    await expect(page.getByText('运营大屏', { exact: true }).first()).toBeVisible()
    await expect(accountMenu).toBeVisible()
    await expect(scopeBadge).toBeVisible()
  })
})
