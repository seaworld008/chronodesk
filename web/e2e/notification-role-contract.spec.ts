import { expect, test, type Page } from '@playwright/test';
import type { ProjectRole } from '../src/lib/generated/human-api';
import {
    authorizedProjectAccess,
    defaultMockIdentity,
    fulfillJSON,
    installMockSession,
    projectA,
} from './helpers/mockHumanSession';

type NotificationMockState = {
    creates: Record<string, unknown>[];
};

const installNotificationBackend = async (
    page: Page,
    projectRole: ProjectRole,
): Promise<NotificationMockState> => {
    const state: NotificationMockState = { creates: [] };
    const access = authorizedProjectAccess(projectA, projectRole);
    const notificationsPath =
        `/api/projects/${projectA.key}/notifications`;

    await page.route('**/api/**', async (route) => {
        const request = route.request();
        const url = new URL(request.url());

        if (url.pathname === '/api/projects') {
            await fulfillJSON(route, { code: 0, data: [access] });
            return;
        }
        if (
            url.pathname === notificationsPath &&
            request.method() === 'GET'
        ) {
            await fulfillJSON(route, {
                code: 0,
                data: {
                    items: [],
                    total: 0,
                    page: 1,
                    page_size: 25,
                    total_pages: 0,
                },
            });
            return;
        }
        if (
            url.pathname === notificationsPath &&
            request.method() === 'POST'
        ) {
            const body =
                request.postDataJSON() as Record<string, unknown>;
            state.creates.push(body);
            await fulfillJSON(
                route,
                {
                    code: 0,
                    data: {
                        id: 901,
                        ...body,
                        created_at: '2026-07-30T08:00:00Z',
                        updated_at: '2026-07-30T08:00:00Z',
                        is_read: false,
                        is_sent: false,
                        is_delivered: false,
                    },
                },
                201,
            );
            return;
        }
        if (
            url.pathname ===
                `/api/projects/${projectA.key}/assignees`
        ) {
            await fulfillJSON(route, {
                code: 0,
                data: [
                    {
                        id: 88,
                        username: 'notification-recipient',
                        first_name: 'Notification',
                        last_name: 'Recipient',
                    },
                ],
            });
            return;
        }
        if (
            url.pathname ===
                `/api/projects/${projectA.key}/tickets/stats`
        ) {
            await fulfillJSON(route, {
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
            return;
        }

        await fulfillJSON(route, { code: 0, data: [] });
    });

    return state;
};

test.describe('通知创建项目职责边界', () => {
    for (const role of ['project_admin', 'manager'] as const) {
        test(`${role} 可从通知列表创建且只提交严格六字段`, async ({
            page,
        }) => {
            await installMockSession(
                page,
                {
                    ...defaultMockIdentity,
                    sessionID: `e2e-notification-create-${role}`,
                },
                projectA,
            );
            const state = await installNotificationBackend(page, role);
            await page.goto('/#/notifications');

            await page
                .getByRole('link', {
                    name: '创建通知',
                    exact: true,
                })
                .click();
            await expect(page).toHaveURL(/#\/notifications\/create$/u);

            await page.getByLabel('标题').fill(`${role} 创建通知`);
            await page
                .getByRole('textbox', {
                    name: '内容',
                    exact: true,
                })
                .fill('严格项目作用域通知内容');
            await page
                .getByRole('combobox', { name: '接收者' })
                .click();
            await page
                .getByRole('option', {
                    name: /notification-recipient/u,
                })
                .click();

            const createResponse = page.waitForResponse(
                (response) =>
                    response.request().method() === 'POST' &&
                    new URL(response.url()).pathname ===
                        `/api/projects/${projectA.key}/notifications`,
            );
            await page
                .getByRole('button', { name: '保存', exact: true })
                .click();
            expect((await createResponse).status()).toBe(201);

            expect(state.creates).toEqual([
                {
                    type: 'system_alert',
                    title: `${role} 创建通知`,
                    content: '严格项目作用域通知内容',
                    priority: 'normal',
                    channel: 'in_app',
                    recipient_id: 88,
                },
            ]);
        });
    }

    for (const role of [
        'agent',
        'requester',
        'observer',
    ] as const satisfies readonly ProjectRole[]) {
        test(`${role} 无创建按钮且直接 create 路由在请求前被拒绝`, async ({
            page,
        }) => {
            await installMockSession(
                page,
                {
                    ...defaultMockIdentity,
                    sessionID: `e2e-notification-denied-${role}`,
                },
                projectA,
            );
            const state = await installNotificationBackend(page, role);
            await page.goto('/#/notifications');

            await expect(
                page.getByRole('link', {
                    name: '创建通知',
                    exact: true,
                }),
            ).toHaveCount(0);
            await page.goto('/#/notifications/create');
            await expect(page).toHaveURL(/#\/?$/u);
            await expect(page.getByLabel('通知类型')).toHaveCount(0);
            expect(state.creates).toEqual([]);
        });
    }
});
