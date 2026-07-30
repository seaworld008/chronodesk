import { expect, test } from '@playwright/test'
import { authenticatePage } from './helpers/testData'
import { assertDestructiveE2EAllowed } from './helpers/safety'

test.describe('我的跨项目工作台', () => {
  test.beforeAll(() => {
    assertDestructiveE2EAllowed('跨项目工作台只读 E2E')
  })

  test('按成员项目展示来源、切换视图并在跳转前切换项目', async ({
    page,
  }) => {
    const project = {
      id: 73,
      public_id: '00000000-0000-7000-8000-000000000073',
      organization_id: 1,
      business_unit_id: 1,
      key: 'OPS',
      name: '运营支持',
      status: 'active',
    }

    const requestedViews: string[] = []
    // authenticatePage first installs the global mutation guard. Feature mocks
    // are registered afterwards so Playwright evaluates these narrower routes
    // before that broad safety route.
    await authenticatePage(page)
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
          msg: '获取授权项目成功',
          data: [{
            project,
            role: 'agent',
            scope: {
              organization_id: project.organization_id,
              project_id: project.id,
            },
          }],
        }),
      })
    })
    await page.route('**/api/workbench/tickets?*', async (route) => {
      const url = new URL(route.request().url())
      const view = url.searchParams.get('view') ?? 'todo'
      requestedViews.push(view)
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          code: 0,
          msg: '获取我的跨项目工作台成功',
          data: {
            items: view === 'created'
              ? []
              : [{
                  id: 987654,
                  public_id: '00000000-0000-7000-8000-000000987654',
                  project_id: project.id,
                  project_key: project.key,
                  project_name: project.name,
                  ticket_number: `${project.key}-987654`,
                  title: '跨项目来源可见性验证',
                  type: 'request',
                  priority: 'high',
                  status: 'open',
                  assigned_to_name: '测试处理人',
                  sla_breached: false,
                  version: 1,
                  created_at: '2026-07-30T08:00:00Z',
                  updated_at: '2026-07-30T09:00:00Z',
                }],
            total: view === 'created' ? 0 : 1,
            page: 1,
            page_size: 20,
            total_pages: view === 'created' ? 0 : 1,
            view,
          },
        }),
      })
    })

    await page.getByRole('menuitem', {
      name: '我的跨项目工作台',
      exact: true,
    }).click()

    await expect(page).toHaveURL(/#\/workbench$/u)
    const table = page.getByRole('table', {
      name: '我的待办工单列表',
      exact: true,
    })
    await expect(table).toBeVisible()
    const projectCell = table.getByRole('cell', {
      name: `${project.name}（${project.key}）`,
      exact: true,
    })
    await expect(projectCell).toContainText(project.name)
    await expect(projectCell).toContainText(project.key)
    await expect(
      table.getByText('跨项目来源可见性验证', { exact: true }),
    ).toBeVisible()

    await page.getByRole('tab', { name: '我创建的', exact: true }).click()
    await expect.poll(() => requestedViews).toContain('created')
    await expect(
      page.getByRole('table', {
        name: '我创建的工单列表',
        exact: true,
      }).getByText('当前视图暂无工单', { exact: true }),
    ).toBeVisible()

    await page.getByRole('tab', { name: '分派给我的', exact: true }).click()
    const assignedTable = page.getByRole('table', {
      name: '分派给我的工单列表',
      exact: true,
    })
    const openButton = assignedTable.getByRole('button', {
      name: `进入 ${project.name} 的工单 ${project.key}-987654`,
      exact: true,
    })
    await expect(openButton).toBeVisible()
    await openButton.click()

    await expect(page).toHaveURL(/#\/tickets\/987654\/show$/u)
    await expect.poll(
      () => page.evaluate(
        () => localStorage.getItem('chronodesk.activeProjectKey'),
      ),
    ).toBe(project.key)
  })
})
