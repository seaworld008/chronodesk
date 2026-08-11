import { expect, test, type Page } from '@playwright/test';
import {
    authorizedProjectAccess,
    defaultMockIdentity,
    fulfillJSON,
    installMockSession,
    projectA,
} from './helpers/mockHumanSession';
import { monitorBrowserHealth } from './helpers/browserAudit';
import type {
    TicketSource,
    TicketStatus,
} from '@/lib/generated/human-api';

const ticketID = 9013;
const projectAdmin = {
    ...defaultMockIdentity,
    sessionID: 'e2e-ticket-edit-enum-contract',
};

const installTicketEditBackend = async (
    page: Page,
    overrides: Partial<{
        status: TicketStatus;
        source: TicketSource;
    }>,
) => {
    const updates: Record<string, unknown>[] = [];
    const ticket = {
        id: ticketID,
        public_id: '00000000-0000-7000-8000-000000009013',
        project_id: projectA.id,
        ticket_number: `OPS-${ticketID}`,
        title: '工单编辑枚举契约回归',
        description: '验证后端合法枚举值始终能由编辑表单正确呈现。',
        type: 'request',
        priority: 'normal',
        status: 'open',
        source: 'web',
        created_by_id: projectAdmin.id,
        assigned_to_id: null,
        category_id: null,
        version: 3,
        tags: [],
        internal_notes: '',
        sla_breached: false,
        created_at: '2026-07-30T08:00:00Z',
        updated_at: '2026-07-30T09:00:00Z',
        ...overrides,
    };
    const projectAccess = authorizedProjectAccess(projectA, 'project_admin');

    await page.route('**/api/**', async (route) => {
        const request = route.request();
        const url = new URL(request.url());

        if (
            url.pathname === '/api/projects' &&
            request.method() === 'GET'
        ) {
            await fulfillJSON(route, {
                code: 0,
                data: {
                    items: [projectAccess],
                    page: 1,
                    page_size: 100,
                    total: 1,
                    total_pages: 1,
                },
            });
            return;
        }

        if (
            url.pathname ===
                `/api/projects/${projectA.key}/tickets/${ticketID}` &&
            request.method() === 'GET'
        ) {
            await fulfillJSON(route, { code: 0, data: ticket });
            return;
        }

        if (
            url.pathname ===
                `/api/projects/${projectA.key}/tickets/${ticketID}/transitions` &&
            request.method() === 'GET'
        ) {
            await fulfillJSON(route, {
                success: true,
                data: {
                    allowed_next_statuses: [],
                },
            });
            return;
        }

        if (
            url.pathname ===
                `/api/projects/${projectA.key}/tickets/${ticketID}` &&
            request.method() === 'PUT'
        ) {
            const body =
                request.postDataJSON() as Record<string, unknown>;
            updates.push(body);
            await fulfillJSON(route, {
                code: 0,
                data: {
                    ...ticket,
                    ...body,
                    id: ticketID,
                    version: ticket.version + 1,
                },
            });
            return;
        }

        if (
            url.pathname === `/api/projects/${projectA.key}/assignees` &&
            request.method() === 'GET'
        ) {
            await fulfillJSON(route, {
                code: 0,
                data: {
                    items: [],
                    page: 1,
                    page_size: 25,
                    total: 0,
                    total_pages: 0,
                },
            });
            return;
        }

        if (
            url.pathname === `/api/projects/${projectA.key}/categories` &&
            request.method() === 'GET'
        ) {
            await fulfillJSON(route, {
                code: 0,
                data: {
                    items: [],
                    page: 1,
                    page_size: 25,
                    total: 0,
                    total_pages: 0,
                },
            });
            return;
        }

        await route.fulfill({
            status: 404,
            contentType: 'application/problem+json',
            body: JSON.stringify({
                code: 'unexpected_test_request',
                detail: `${request.method()} ${url.pathname}`,
            }),
        });
    });

    return { updates };
};

test.describe('工单编辑枚举契约', () => {
    test.beforeEach(async ({ page }) => {
        await installMockSession(page, projectAdmin, projectA);
    });

    test('已取消状态只读展示且普通保存不提交状态', async ({ page }) => {
        const browserHealth = monitorBrowserHealth(page);
        const backend = await installTicketEditBackend(page, {
            status: 'cancelled',
        });

        await page.goto(`/#/tickets/${ticketID}`);

        const status = page.getByRole('combobox', {
            name: '状态',
            exact: true,
        });
        await expect(status).toBeVisible({ timeout: 15_000 });
        await expect(status).toContainText('已取消');
        await expect(status).toBeDisabled();

        await page.getByLabel('工单标题').fill('工单编辑枚举契约安全回归');
        await page.getByRole('button', { name: '保存更改' }).click();
        await expect.poll(() => backend.updates).toHaveLength(1);
        expect(backend.updates[0]).not.toHaveProperty('status');
        browserHealth.assertClean();
    });

    test('智能体来源只读展示且可保存其他字段', async ({ page }) => {
        const browserHealth = monitorBrowserHealth(page);
        const backend = await installTicketEditBackend(page, {
            source: 'agent',
        });

        await page.goto(`/#/tickets/${ticketID}`);

        const source = page.getByRole('combobox', {
            name: '来源',
            exact: true,
        });
        await expect(source).toBeVisible({ timeout: 15_000 });
        await expect(source).toContainText('智能体');
        await expect(source).toBeDisabled();

        await page.getByLabel('工单标题').fill('工单来源可信边界回归');
        await page.getByRole('button', { name: '保存更改' }).click();
        await expect.poll(() => backend.updates).toHaveLength(1);
        expect(backend.updates[0]).not.toHaveProperty('source');
        expect(backend.updates[0]).not.toHaveProperty('status');
        browserHealth.assertClean();
    });

    test('详情页正确展示智能体来源', async ({ page }) => {
        const browserHealth = monitorBrowserHealth(page);
        await installTicketEditBackend(page, { source: 'agent' });

        await page.goto(`/#/tickets/${ticketID}/show`);

        await expect(
            page.getByRole('main').getByText('智能体', {
                exact: true,
            }),
        ).toBeVisible({ timeout: 15_000 });
        browserHealth.assertClean();
    });
});
