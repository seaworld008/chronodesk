import { expect, test, type Page } from '@playwright/test'
import {
  authorizedProjectAccess,
  defaultMockIdentity,
  fulfillJSON,
  installMockSession,
  projectA,
  projectB,
} from './helpers/mockHumanSession'

type AutomationMockOptions = {
  role?: 'project_admin' | 'manager' | 'agent'
  failFirstSLA?: boolean
}

const installAutomationConfigurationBackend = async (
  page: Page,
  options: AutomationMockOptions = {},
) => {
  const role = options.role ?? 'manager'
  const requests: Array<{ path: string; search: string }> = []
  const creates: Array<Record<string, unknown>> = []
  let slaCalls = 0

  await page.route('**/api/**', async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    if (url.pathname === '/api/projects') {
      await fulfillJSON(route, {
        code: 0,
        msg: 'ok',
        data: [
          authorizedProjectAccess(projectA, role),
          authorizedProjectAccess(projectB, role),
        ],
      })
      return
    }

    const match = url.pathname.match(
      /^\/api\/projects\/(OPS|FIN)\/admin\/automation\/(sla|templates|quick-replies)$/u,
    )
    if (match && request.method() === 'GET') {
      const [, projectKey, endpoint] = match
      requests.push({ path: url.pathname, search: url.search })
      if (
        endpoint === 'sla' &&
        options.failFirstSLA &&
        slaCalls++ === 0
      ) {
        await fulfillJSON(
          route,
          {
            success: false,
            message: '列表查询参数无效',
            error: 'invalid_request',
          },
          400,
        )
        return
      }
      const pageNumber = Number(url.searchParams.get('page') ?? '1')
      const pageSize = Number(url.searchParams.get('page_size') ?? '25')
      const id = projectKey === 'OPS' ? pageNumber : 900 + pageNumber
      const common = {
        total: endpoint === 'sla' ? 151 : 1,
        page: pageNumber,
        page_size: pageSize,
        total_pages: endpoint === 'sla'
          ? Math.ceil(151 / pageSize)
          : 1,
      }
      const items =
        endpoint === 'sla'
          ? [{
              id,
              name: `${projectKey} SLA 第 ${pageNumber} 页`,
              description: '分页 SLA',
              is_active:
                url.searchParams.get('is_active') !== 'false',
              is_default: pageNumber === 1,
              response_time: 30,
              resolution_time: 240,
              applied_count: 12,
              violation_count: 1,
              compliance_rate: 91.7,
              created_at: '2026-08-01T01:00:00Z',
              updated_at: '2026-08-01T02:00:00Z',
            }]
          : endpoint === 'templates'
            ? [{
                id,
                name: `${projectKey} 标准模板`,
                description: '标准模板',
                category: 'incident',
                is_active: true,
                title_template: '标题',
                content_template: '内容',
                default_type: 'incident',
                default_priority: 'normal',
                default_status: 'open',
                usage_count: 4,
                created_at: '2026-08-01T01:00:00Z',
                updated_at: '2026-08-01T02:00:00Z',
              }]
            : [{
                id,
                name: `${projectKey} 快捷回复`,
                category: 'network',
                content: '请重新连接网络后重试',
                tags: '网络,连接,排障,客户',
                is_public: true,
                usage_count: 8,
                created_by: 42,
                created_at: '2026-08-01T01:00:00Z',
                updated_at: '2026-08-01T02:00:00Z',
              }]
      await fulfillJSON(route, {
        success: true,
        data: { items, ...common },
      })
      return
    }
    if (match && request.method() === 'POST') {
      creates.push(request.postDataJSON() as Record<string, unknown>)
      await fulfillJSON(route, {
        success: true,
        data: {
          id: 999,
          ...(request.postDataJSON() as Record<string, unknown>),
        },
      }, 201)
      return
    }
    if (
      /\/admin\/automation\/quick-replies\/\d+\/use$/u.test(url.pathname)
      && request.method() === 'POST'
    ) {
      await fulfillJSON(route, { success: true, data: null })
      return
    }
    await fulfillJSON(route, { code: 0, msg: 'ok', data: [] })
  })

  return { requests, creates }
}

test.describe('自动化配置目录（mock）', () => {
  test('SLA 错误可重试、服务端分页筛选并在项目切换后清空旧行', async ({
    page,
  }) => {
    await installMockSession(
      page,
      {
        ...defaultMockIdentity,
        sessionID: 'automation-config-pagination',
      },
      projectA,
    )
    const backend = await installAutomationConfigurationBackend(page, {
      failFirstSLA: true,
    })

    await page.goto('/#/project-settings/sla')
    const alert = page.getByRole('alert')
    await expect(alert).toContainText('列表查询参数无效')
    await alert.getByRole('button', { name: '重试' }).click()
    await expect(page.getByText('OPS SLA 第 1 页')).toBeVisible()
    expect(backend.requests.at(-1)?.search).toBe('?page=1&page_size=25')

    await page.getByRole('button', { name: /下一页|next page/iu }).click()
    await expect(page.getByText('OPS SLA 第 2 页')).toBeVisible()
    expect(backend.requests.at(-1)?.search).toContain('page=2')

    await page.getByRole('combobox', {
      name: '每页数量',
      exact: true,
    }).click()
    await page.getByRole('option', { name: '100' }).click()
    await expect(page.getByText('OPS SLA 第 1 页')).toBeVisible()
    expect(backend.requests.at(-1)?.search).toContain('page_size=100')

    await page.getByLabel('启用状态').click()
    await page.getByRole('option', { name: '停用' }).click()
    await page.getByRole('button', { name: '应用筛选' }).click()
    await expect.poll(() => backend.requests.at(-1)?.search)
      .toContain('is_active=false')

    await page.getByTestId('active-project-switcher').click()
    await page.getByRole('option', {
      name: /财务服务.*项目经理/u,
    }).click()
    await expect(page).toHaveURL(/#\/$/u)
    await page.goto('/#/project-settings/sla')
    await expect(page.getByText('FIN SLA 第 1 页')).toBeVisible()
    await expect(page.getByText(/OPS SLA/u)).toHaveCount(0)
    expect(backend.requests.at(-1)?.path).toContain('/projects/FIN/')
  })

  test('项目设置入口可达、标签多选清晰且创建请求使用规范化标签', async ({
    page,
  }) => {
    await installMockSession(
      page,
      {
        ...defaultMockIdentity,
        sessionID: 'automation-config-tabs',
      },
      projectA,
    )
    const backend = await installAutomationConfigurationBackend(page)

    await page.goto('/#/project-settings/templates')
    await expect(
      page.getByRole('heading', { name: '工单模板', level: 1 }),
    ).toBeVisible()
    await page.getByRole('menuitem', { name: '快捷回复', exact: true }).click()
    await expect(page).toHaveURL(/#\/project-settings\/quick-replies$/u)
    await expect(page.getByText('已选择 4 项')).toBeVisible()

    await page.getByRole('button', { name: '新建快捷回复' }).click()
    await page.getByRole('textbox', {
      name: '名称',
      exact: true,
    }).fill('网络排障回复')
    await page.getByRole('textbox', {
      name: '回复内容',
      exact: true,
    }).fill('请检查网络连接。')
    const tagInput = page.getByRole('combobox', {
      name: '标签',
      exact: true,
    })
    await tagInput.fill('Network')
    await tagInput.press('Enter')
    await tagInput.fill('NETWORK')
    await tagInput.press('Enter')
    await tagInput.fill('客户')
    await tagInput.press('Enter')
    await expect(page.getByText(/已选择 2 项/)).toContainText('已选择 2 项')
    await page.getByRole('button', { name: '确认创建' }).click()
    await expect.poll(() => backend.creates.length).toBe(1)
    expect(backend.creates[0]).toMatchObject({
      name: '网络排障回复',
      content: '请检查网络连接。',
      tags: 'Network,客户',
      is_public: false,
    })
  })

  test('处理人看不到管理入口且直接访问管理子路由会被统一守卫拦截', async ({
    page,
  }) => {
    await installMockSession(
      page,
      {
        ...defaultMockIdentity,
        sessionID: 'automation-config-agent-role',
      },
      projectA,
    )
    await installAutomationConfigurationBackend(page, { role: 'agent' })

    await page.goto('/#/')
    await expect(
      page.getByRole('menuitem', { name: '自动化', exact: true }),
    ).toHaveCount(0)
    await page.goto('/#/automation-quick-replies')
    await expect(page).toHaveURL(/#\/$/u)
    await expect(
      page.getByRole('heading', { name: '快捷回复', level: 1 }),
    ).toHaveCount(0)
  })
})
