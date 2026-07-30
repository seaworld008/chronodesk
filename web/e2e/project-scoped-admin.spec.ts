import { expect, test, type Page } from '@playwright/test';

type ProjectRole =
    | 'project_admin'
    | 'manager'
    | 'agent'
    | 'requester'
    | 'observer';

const project = {
    id: 73,
    public_id: '00000000-0000-7000-8000-000000000073',
    organization_id: 1,
    business_unit_id: 1,
    key: 'OPS',
    name: '运营支持',
    status: 'active',
};

const financeProject = {
    ...project,
    id: 74,
    public_id: '00000000-0000-7000-8000-000000000074',
    key: 'FIN',
    name: '财务服务',
};

const installSession = async (
    page: Page,
    role: 'supervisor' | 'agent',
) => {
    await page.addInitScript(({ userRole, activeProjectKey }) => {
        localStorage.setItem('token', 'project-scope-test-token');
        localStorage.setItem(
            'user',
            JSON.stringify({
                id: 42,
                username: 'project-scope-tester',
                email: 'project-scope@example.test',
                role: userRole,
            }),
        );
        localStorage.setItem('permissions', '[]');
        if (!localStorage.getItem('chronodesk.activeProjectKey')) {
            localStorage.setItem(
                'chronodesk.activeProjectKey',
                activeProjectKey,
            );
        }
    }, { userRole: role, activeProjectKey: project.key });
};

const projectAccessResponse = (role: ProjectRole) => ({
    code: 0,
    data: [{
        project,
        role,
        scope: {
            organization_id: project.organization_id,
            project_id: project.id,
        },
    }],
});

const fulfillJSON = (
    route: Parameters<Parameters<Page['route']>[1]>[0],
    body: unknown,
) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify(body),
});

const mockProjectData = async (
    page: Page,
    projectRole: ProjectRole,
    requestedPaths: string[] = [],
) => {
    await page.route('**/api/projects', (route) =>
        fulfillJSON(route, projectAccessResponse(projectRole)));
    await page.route('**/api/projects/OPS/**', (route) => {
        const url = new URL(route.request().url());
        requestedPaths.push(`${url.pathname}${url.search}`);
        if (url.pathname.endsWith('/tickets/stats')) {
            return fulfillJSON(route, {
                code: 0,
                data: {
                    total: 0,
                    open: 0,
                    in_progress: 0,
                    pending: 0,
                    resolved: 0,
                    overdue: 0,
                    sla_breached: 0,
                    my_tickets: 0,
                    unassigned: 0,
                    high_priority: 0,
                    escalated: 0,
                },
            });
        }
        if (url.pathname.includes('/tickets')) {
            return fulfillJSON(route, {
                code: 0,
                data: { items: [], total: 0 },
            });
        }
        if (url.pathname.endsWith('/webhooks')) {
            return fulfillJSON(route, {
                code: 0,
                data: { items: [], total: 0, page: 1, size: 100 },
            });
        }
        if (url.pathname.endsWith('/admin/automation/rules')) {
            return fulfillJSON(route, {
                success: true,
                data: { rules: [], total: 0 },
            });
        }
        return fulfillJSON(route, { code: 0, data: [] });
    });
};

test.describe('项目级管理入口', () => {
    test('manager 可访问自动化与 Webhook，但不可进入平台管理', async ({
        page,
    }) => {
        await installSession(page, 'agent');
        const requestedPaths: string[] = [];
        await mockProjectData(page, 'manager', requestedPaths);

        await page.goto('/#/');
        await expect(
            page.getByRole('heading', { name: '工单运营总览' }),
        ).toBeVisible();
        await expect(
            page.getByRole('menuitem', { name: '自动化规则' }),
        ).toBeVisible();
        await expect(
            page.getByRole('menuitem', { name: '自动化日志' }),
        ).toBeVisible();
        await expect(
            page.getByRole('menuitem', { name: 'Webhook 集成' }),
        ).toBeVisible();
        await expect(
            page.getByRole('menuitem', { name: '用户管理' }),
        ).toHaveCount(0);
        await expect(
            page.getByRole('menuitem', { name: '系统设置' }),
        ).toHaveCount(0);

        await page.getByRole('menuitem', { name: 'Webhook 集成' }).click();
        await expect(
            page.getByRole('heading', { name: 'Webhook 集成' }),
        ).toBeVisible();
        await expect.poll(() => requestedPaths).toContain(
            '/api/projects/OPS/webhooks?page=1&page_size=100',
        );

        await page.getByRole('menuitem', { name: '自动化规则' }).click();
        await expect(page.getByRole('link', { name: '新建' })).toBeVisible();
        await expect.poll(() =>
            requestedPaths.some((path) =>
                path.startsWith('/api/projects/OPS/admin/automation/rules?'),
            ),
        ).toBe(true);

        await page.goto('/#/users');
        await expect(page).toHaveURL(/#\/$/u);
    });

    test('Agent 仪表盘的四个请求全部绑定当前项目', async ({ page }) => {
        await installSession(page, 'agent');
        const requestedPaths: string[] = [];
        await mockProjectData(page, 'agent', requestedPaths);

        await page.goto('/#/');
        await expect(
            page.getByRole('heading', { name: '工单运营总览' }),
        ).toBeVisible();
        await expect.poll(() =>
            requestedPaths.filter((path) =>
                path.startsWith('/api/projects/OPS/tickets'),
            ).length,
        ).toBe(4);
        expect(requestedPaths).toEqual(expect.arrayContaining([
            '/api/projects/OPS/tickets/stats',
            '/api/projects/OPS/tickets?priority=urgent,critical&status=open,in_progress&page_size=10',
            '/api/projects/OPS/tickets?page_size=10&sort_by=created_at&sort_order=desc',
            '/api/projects/OPS/tickets/my-tickets?limit=10',
        ]));
    });

    test('切换项目后重新加载并刷新当前项目权限与请求范围', async ({
        page,
    }) => {
        await installSession(page, 'agent');
        const requestedPaths: string[] = [];
        await page.route('**/api/projects', (route) =>
            fulfillJSON(route, {
                code: 0,
                data: [
                    projectAccessResponse('agent').data[0],
                    {
                        project: financeProject,
                        role: 'project_admin',
                        scope: {
                            organization_id: financeProject.organization_id,
                            project_id: financeProject.id,
                        },
                    },
                ],
            }));
        await page.route('**/api/projects/**', (route) => {
            const url = new URL(route.request().url());
            requestedPaths.push(`${url.pathname}${url.search}`);
            if (url.pathname.endsWith('/tickets/stats')) {
                return fulfillJSON(route, {
                    code: 0,
                    data: {
                        total: 0,
                        open: 0,
                        in_progress: 0,
                        pending: 0,
                        resolved: 0,
                        overdue: 0,
                        sla_breached: 0,
                        my_tickets: 0,
                        unassigned: 0,
                        high_priority: 0,
                        escalated: 0,
                    },
                });
            }
            return fulfillJSON(route, {
                code: 0,
                data: { items: [], total: 0 },
            });
        });

        await page.goto('/#/');
        await page.getByRole('combobox', { name: '当前项目' }).click();
        await page
            .getByRole('option', { name: '财务服务 · project_admin' })
            .click();

        await expect.poll(() =>
            page.evaluate(() =>
                localStorage.getItem('chronodesk.activeProjectKey')),
        ).toBe('FIN');
        await expect(
            page.getByRole('menuitem', { name: 'Webhook 集成' }),
        ).toBeVisible();
        await expect.poll(() =>
            requestedPaths.some((path) =>
                path.startsWith('/api/projects/FIN/tickets'),
            ),
        ).toBe(true);
    });

    test('项目角色未解析时保持加载态，解析为普通成员后拒绝管理页', async ({
        page,
    }) => {
        await installSession(page, 'supervisor');
        let releaseProjects: (() => void) | undefined;
        const projectRelease = new Promise<void>((resolve) => {
            releaseProjects = resolve;
        });
        await page.route('**/api/projects', async (route) => {
            await projectRelease;
            await fulfillJSON(route, projectAccessResponse('agent'));
        });
        await page.route('**/api/projects/OPS/**', (route) =>
            fulfillJSON(route, { code: 0, data: { items: [] } }));

        await page.goto('/#/webhook-settings');
        await expect(page.getByLabel('正在加载页面')).toBeVisible();
        await expect(
            page.getByRole('heading', { name: 'Webhook 集成' }),
        ).toHaveCount(0);

        releaseProjects?.();
        await expect(page).toHaveURL(/#\/$/u);
        await expect(
            page.getByRole('menuitem', { name: 'Webhook 集成' }),
        ).toHaveCount(0);
    });
});
